package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/bcrypt"
)

type Config struct{ Listen, DataDir, WebDir, OpenIMURL, OpenIMSecret, OpenIMAdmin, AgentID, AdminName, AdminPassword string }
type User struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	Disabled   bool   `json:"disabled"`
	Unread     int    `json:"unread"`
	LastActive int64  `json:"lastActive"`
}
type session struct {
	UserID  string
	Expires time.Time
}
type rateEntry struct {
	Count int
	Until time.Time
}
type App struct {
	cfg Config
	db  *sql.DB
	im  *OpenIM
	// Single-instance gateway: serialize mutations with OpenIM calls, so account revocation and sends have an explicit order.
	mutation sync.Mutex
	mu       sync.Mutex
	sessions map[string]session
	limits   map[string]rateEntry
	revision uint64
}

func randomID() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func main() {
	syscall.Umask(0077)
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := healthcheck(); err != nil {
			os.Exit(1)
		}
		return
	}
	cfg := Config{Listen: env("LISTEN", "127.0.0.1:8090"), DataDir: env("DATA_DIR", "./data"), WebDir: env("WEB_DIR", "../web/dist"), OpenIMURL: env("OPENIM_URL", "http://127.0.0.1:10002"), OpenIMSecret: os.Getenv("OPENIM_SECRET"), OpenIMAdmin: env("OPENIM_ADMIN", "imAdmin"), AgentID: env("AGENT_ID", "support_agent"), AdminName: env("ADMIN_USERNAME", "admin"), AdminPassword: os.Getenv("ADMIN_PASSWORD")}
	if cfg.OpenIMSecret == "" {
		log.Fatal("OPENIM_SECRET is required")
	}
	app, err := NewApp(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer app.db.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go app.maintenance(ctx)
	go app.cleanupOpenIM(ctx)
	srv := &http.Server{Addr: cfg.Listen, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	log.Printf("Support chat listening on http://%s (OpenIM %s)", cfg.Listen, cfg.OpenIMURL)
	if err = srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
func NewApp(cfg Config) (*App, error) {
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "attachments"), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", filepath.Join(cfg.DataDir, "support.db")+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000&_secure_delete=on")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	a := &App{cfg: cfg, db: db, im: NewOpenIM(cfg), sessions: map[string]session{}, limits: map[string]rateEntry{}}
	if err = migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	var n int
	if err = db.QueryRow("SELECT count(*) FROM users WHERE role='agent'").Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 {
		if err = validPassword(cfg.AdminPassword); err != nil {
			return nil, fmt.Errorf("first startup needs ADMIN_PASSWORD: %w", err)
		}
		hash, _ := bcrypt.GenerateFromPassword([]byte(cfg.AdminPassword), bcrypt.DefaultCost)
		_, err = db.Exec("INSERT INTO users(id,username,name,role,hash,created) VALUES(?,?,?,'agent',?,?)", cfg.AgentID, cfg.AdminName, "专属客服", hash, time.Now().UnixMilli())
		if err != nil {
			return nil, err
		}
	}
	return a, nil
}
func (a *App) maintenance(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.mu.Lock()
			for k, s := range a.sessions {
				if time.Now().After(s.Expires) {
					delete(a.sessions, k)
				}
			}
			for k, v := range a.limits {
				if time.Now().After(v.Until) {
					delete(a.limits, k)
				}
			}
			a.mu.Unlock()
			a.mutation.Lock()
			rows, err := a.db.Query("SELECT id FROM attachments WHERE deleted=1 OR (send_id='' AND created<?)", time.Now().Add(-24*time.Hour).UnixMilli())
			if err == nil {
				var ids []string
				for rows.Next() {
					var id string
					_ = rows.Scan(&id)
					ids = append(ids, id)
				}
				rows.Close()
				for _, id := range ids {
					if e := os.Remove(filepath.Join(a.cfg.DataDir, "attachments", id)); e == nil || os.IsNotExist(e) {
						_, _ = a.db.Exec("DELETE FROM attachments WHERE id=?", id)
					}
				}
			}
			a.mutation.Unlock()
		}
	}
}
func validPassword(p string) error {
	if len(p) < 10 || len(p) > 72 {
		return errors.New("密码须为 10–72 字节")
	}
	return nil
}
func (a *App) publish() { a.mu.Lock(); a.revision++; a.mu.Unlock() }
func (a *App) revoke(id string) {
	a.mu.Lock()
	for k, s := range a.sessions {
		if s.UserID == id {
			delete(a.sessions, k)
		}
	}
	a.revision++
	a.mu.Unlock()
}
func (a *App) user(id string) (User, error) {
	var u User
	err := a.db.QueryRow("SELECT id,username,name,role,disabled FROM users WHERE id=?", id).Scan(&u.ID, &u.Username, &u.Name, &u.Role, &u.Disabled)
	return u, err
}
func token(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}
func (a *App) authenticate(r *http.Request) (User, error) {
	a.mu.Lock()
	s, ok := a.sessions[token(r)]
	a.mu.Unlock()
	if !ok || time.Now().After(s.Expires) {
		return User{}, errors.New("请重新登录")
	}
	u, err := a.user(s.UserID)
	if err != nil || u.Disabled {
		return User{}, errors.New("账号已停用，请联系客户服务")
	}
	return u, nil
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		problem(w, 400, "请求格式不正确")
		return false
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		problem(w, 400, "请求格式不正确")
		return false
	}
	return true
}
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", a.login)
	mux.HandleFunc("GET /api/ready", a.ready)
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"version": buildVersion, "schemaVersion": schemaVersion})
	})
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok", "messageService": "OpenIM"})
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		u, err := a.authenticate(r)
		if err != nil {
			problem(w, 401, err.Error())
			return
		}
		switch {
		case r.URL.Path == "/api/me" && r.Method == "GET":
			writeJSON(w, 200, u)
		case r.URL.Path == "/api/logout" && r.Method == "POST":
			a.mu.Lock()
			delete(a.sessions, token(r))
			a.mu.Unlock()
			writeJSON(w, 200, map[string]bool{"ok": true})
		case r.URL.Path == "/api/events" && r.Method == "GET":
			a.events(w, r)
		case r.URL.Path == "/api/customers":
			a.customers(w, r, u)
		case strings.HasPrefix(r.URL.Path, "/api/customers/"):
			a.manageUser(w, r, u)
		case r.URL.Path == "/api/messages":
			a.messages(w, r, u)
		case r.URL.Path == "/api/read" && r.Method == "POST":
			a.markRead(w, r, u)
		case r.URL.Path == "/api/attachments" && r.Method == "POST":
			a.upload(w, r, u)
		case strings.HasPrefix(r.URL.Path, "/api/attachments/") && r.Method == "GET":
			a.attachment(w, r, u)
		default:
			problem(w, 404, "接口不存在")
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/" || r.URL.Path == "/agent" || r.URL.Path == "/customer" {
			http.ServeFile(w, r, filepath.Join(a.cfg.WebDir, "index.html"))
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/assets/") {
			http.NotFound(w, r)
			return
		}
		http.FileServer(http.Dir(a.cfg.WebDir)).ServeHTTP(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' blob: data:; media-src 'self' blob:; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		mux.ServeHTTP(w, r)
	})
}
func (a *App) events(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	f, ok := w.(http.Flusher)
	if !ok {
		return
	}
	last := uint64(^uint64(0))
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		if _, err := a.authenticate(r); err != nil {
			fmt.Fprint(w, "event: logout\ndata: {}\n\n")
			f.Flush()
			return
		}
		a.mu.Lock()
		rev := a.revision
		a.mu.Unlock()
		if rev != last {
			fmt.Fprintf(w, "event: change\ndata: %d\n\n", rev)
			last = rev
		} else {
			fmt.Fprint(w, ": heartbeat\n\n")
		}
		f.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
		}
	}
}
