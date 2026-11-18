package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type Route struct {
	Protocol  int    `json:"protocol"`
	ServiceID string `json:"serviceId"`
	Revision  int64  `json:"revision"`
	ServerURL string `json:"serverUrl"`
}
type Directory struct {
	mu          sync.Mutex
	path, token string
	local       bool
	route       Route
}

func origin(raw string, local bool) (string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\\\r\n\t ") {
		return "", errors.New("请输入不含路径和空格的 HTTPS 地址")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || (u.Path != "" && u.Path != "/") {
		return "", errors.New("请输入完整服务域名，不含路径、账号或参数")
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", errors.New("端口无效")
	}
	if u.Port() != "" {
		port, e := strconv.Atoi(u.Port())
		if e != nil || port < 1 || port > 65535 {
			return "", errors.New("端口无效")
		}
	}
	if u.Scheme != "https" && !(local && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "10.0.2.2")) {
		return "", errors.New("正式服务必须使用 HTTPS")
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}
func newDirectory(path, token string, local bool) (*Directory, error) {
	if len(token) < 32 {
		return nil, errors.New("DIRECTORY_TOKEN must contain at least 32 characters")
	}
	d := &Directory{path: path, token: token, local: local}
	data, err := os.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(data, &d.route); err != nil {
			return nil, err
		}
		if _, err = origin(d.route.ServerURL, local); err != nil {
			return nil, err
		}
		if d.route.Protocol != 1 || d.route.Revision < 1 || len(d.route.ServiceID) < 16 {
			return nil, errors.New("invalid saved directory state")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return d, nil
}
func (d *Directory) persist(route Route) error {
	dir := filepath.Dir(d.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".route-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	err = json.NewEncoder(f).Encode(route)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(tmp, d.path); err != nil {
		return err
	}
	// The atomic rename is the commit point. Keep memory consistent even if directory fsync fails.
	d.route = route
	fd, e := os.Open(dir)
	if e == nil {
		e = fd.Sync()
		fd.Close()
	}
	return e
}
func (d *Directory) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.URL.Path == "/health" && r.Method == "GET" {
			writeJSON(w, 200, map[string]bool{"ok": true})
			return
		}
		if r.URL.Path == "/api/bootstrap" && r.Method == "GET" {
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.route.Revision == 0 {
				problem(w, 503, "入口尚未发布业务地址")
				return
			}
			writeJSON(w, 200, d.route)
			return
		}
		if r.URL.Path != "/admin/route" {
			problem(w, 404, "接口不存在")
			return
		}
		if subtle.ConstantTimeCompare([]byte(token(r)), []byte(d.token)) != 1 {
			problem(w, 401, "入口管理认证失败")
			return
		}
		if r.Method == "GET" {
			d.mu.Lock()
			defer d.mu.Unlock()
			writeJSON(w, 200, d.route)
			return
		}
		if r.Method != "PUT" {
			problem(w, 405, "不支持此操作")
			return
		}
		var in struct {
			ServiceID        string `json:"serviceId"`
			ServerURL        string `json:"serverUrl"`
			ExpectedRevision int64  `json:"expectedRevision"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		canonical, err := origin(in.ServerURL, d.local)
		if err != nil || len(in.ServiceID) < 16 || len(in.ServiceID) > 80 {
			problem(w, 400, "服务标识或 HTTPS 地址无效")
			return
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.route.Revision != in.ExpectedRevision || (d.route.ServiceID != "" && d.route.ServiceID != in.ServiceID) {
			problem(w, 409, "入口版本或服务标识不一致，请重新读取")
			return
		}
		next := Route{Protocol: 1, ServiceID: in.ServiceID, Revision: d.route.Revision + 1, ServerURL: canonical}
		if err = d.persist(next); err != nil {
			problem(w, 500, "入口保存结果待确认，请重新读取后核实")
			return
		}
		writeJSON(w, 200, next)
	})
}
func runDirectory() {
	d, err := newDirectory(filepath.Join(env("DIRECTORY_DATA_DIR", "./directory-data"), "route.json"), os.Getenv("DIRECTORY_TOKEN"), os.Getenv("ALLOW_LOCAL_ROUTING") == "1")
	if err != nil {
		log.Fatal(err)
	}
	listen := env("DIRECTORY_LISTEN", "127.0.0.1:8095")
	log.Printf("Independent directory listening on %s", listen)
	server := &http.Server{Addr: listen, Handler: d.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	log.Fatal(server.ListenAndServe())
}
func routingHTTP() *http.Client {
	return &http.Client{Timeout: 7 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return errors.New("redirects are not allowed for routing")
	}}
}
func routeRequest(ctx context.Context, client *http.Client, method, address, secret string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return err
	}
	req.Header.Set("Cache-Control", "no-cache")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("remote status %d", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return errors.New("invalid remote response")
	}
	return json.Unmarshal(raw, output)
}
func (a *App) adminConnection(w http.ResponseWriter, r *http.Request, u User) {
	if u.Role != "agent" {
		problem(w, 403, "仅客服可管理连接")
		return
	}
	directory, err := origin(a.cfg.DirectoryURL, a.cfg.AllowLocalRouting)
	configured := err == nil && len(a.cfg.DirectoryToken) >= 32
	if !configured {
		if r.Method == "GET" {
			writeJSON(w, 200, map[string]any{"configured": false, "webVersion": buildVersion, "serviceId": a.serviceID()})
			return
		}
		problem(w, 409, "尚未连接固定配置入口，请先完成入口服务初始化")
		return
	}
	if r.Method == "GET" {
		var route Route
		if routeRequest(r.Context(), routingHTTP(), "GET", directory+"/admin/route", a.cfg.DirectoryToken, nil, &route) != nil {
			problem(w, 503, "无法读取固定入口，请检查入口服务")
			return
		}
		writeJSON(w, 200, map[string]any{"configured": true, "directoryUrl": directory, "route": route, "webVersion": buildVersion, "serviceId": a.serviceID()})
		return
	}
	if r.Method != "PUT" {
		problem(w, 405, "不支持此操作")
		return
	}
	var in struct {
		ServerURL        string `json:"serverUrl"`
		ExpectedRevision int64  `json:"expectedRevision"`
		Password         string `json:"password"`
		DataReady        bool   `json:"dataReady"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	target, err := origin(in.ServerURL, a.cfg.AllowLocalRouting)
	if err != nil {
		problem(w, 400, err.Error())
		return
	}
	if !in.DataReady || len(in.Password) > 72 {
		problem(w, 400, "请先完成数据迁移，并输入客服密码确认")
		return
	}
	var hash []byte
	if a.db.QueryRow("SELECT hash FROM users WHERE id=? AND role='agent'", u.ID).Scan(&hash) != nil || bcrypt.CompareHashAndPassword(hash, []byte(in.Password)) != nil {
		problem(w, 403, "客服密码不正确")
		return
	}
	// No credentials accompany the destination probe. Only the configured directory receives its management token.
	var ready struct {
		Status        string `json:"status"`
		ServiceID     string `json:"serviceId"`
		Protocol      int    `json:"protocol"`
		SchemaVersion int    `json:"schemaVersion"`
	}
	if routeRequest(r.Context(), routingHTTP(), "GET", target+"/api/ready", "", nil, &ready) != nil || ready.Status != "ready" {
		problem(w, 409, "新服务尚未就绪，入口保持不变")
		return
	}
	if ready.ServiceID != a.serviceID() || ready.Protocol != 1 || ready.SchemaVersion != schemaVersion {
		problem(w, 409, "新服务身份或数据版本不匹配，请核对完整备份恢复结果")
		return
	}
	a.mutation.Lock()
	defer a.mutation.Unlock()
	if _, err = a.authenticate(r); err != nil {
		problem(w, 401, "请重新登录")
		return
	}
	var route Route
	if err = routeRequest(r.Context(), routingHTTP(), "PUT", directory+"/admin/route", a.cfg.DirectoryToken, map[string]any{"serverUrl": target, "serviceId": a.serviceID(), "expectedRevision": in.ExpectedRevision}, &route); err != nil {
		problem(w, 409, "入口发布未确认，请重新读取当前地址和版本后核实，不要连续重复发布")
		return
	}
	writeJSON(w, 200, map[string]any{"configured": true, "directoryUrl": directory, "route": route, "webVersion": buildVersion, "serviceId": a.serviceID()})
}
