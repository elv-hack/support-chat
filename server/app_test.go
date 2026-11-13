package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func testApp(t *testing.T, imHandler http.HandlerFunc) *App {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/get_admin_token" {
			writeJSON(w, 200, map[string]any{"errCode": 0, "data": map[string]any{"token": "test-admin-token", "expireTimeSeconds": 3600}})
			return
		}
		imHandler(w, r)
	}))
	t.Cleanup(ts.Close)
	a, err := NewApp(Config{DataDir: t.TempDir(), OpenIMURL: ts.URL, OpenIMSecret: "test-only-secret", AgentID: "support_agent", AdminName: "admin", AdminPassword: "test-only-password"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.db.Close() })
	hash, _ := bcrypt.GenerateFromPassword([]byte("customer-password"), bcrypt.MinCost)
	for _, id := range []string{"alice", "bob"} {
		_, err = a.db.Exec("INSERT INTO users(id,username,name,role,hash,created) VALUES(?,?,?,'customer',?,?)", id, id, id, hash, 1)
		if err != nil {
			t.Fatal(err)
		}
		a.sessions[id] = session{id, time.Now().Add(time.Hour)}
	}
	a.sessions["agent"] = session{"support_agent", time.Now().Add(time.Hour)}
	return a
}
func callTest(a *App, method, path, tok, data string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(data))
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}
func requireStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("want %d got %d: %s", status, w.Code, w.Body)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store")
	}
}
func TestCustomerIsolationRevocationAndFreshStartup(t *testing.T) {
	a := testApp(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected OpenIM request", r.URL.Path) })
	requireStatus(t, callTest(a, "GET", "/api/customers", "alice", ""), 403)
	requireStatus(t, callTest(a, "GET", "/api/messages?customer=bob", "alice", ""), 403)
	requireStatus(t, callTest(a, "DELETE", "/api/messages?customer=alice", "alice", `{"seq":1}`), 403)
	requireStatus(t, callTest(a, "POST", "/api/customers/alice/disable", "agent", ""), 200)
	requireStatus(t, callTest(a, "GET", "/api/me", "alice", ""), 401)
	a.mu.Lock()
	a.sessions["expired"] = session{"bob", time.Now().Add(-time.Second)}
	a.mu.Unlock()
	requireStatus(t, callTest(a, "GET", "/api/me", "expired", ""), 401)
	fresh, err := NewApp(a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.db.Close()
	requireStatus(t, callTest(fresh, "GET", "/api/me", "agent", ""), 401)
}
func TestAmbiguousSendIsNeverAutomaticallyReplayed(t *testing.T) {
	var sends atomic.Int32
	a := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/msg/send_msg" {
			t.Error(r.URL.Path)
		}
		sends.Add(1)
		http.Error(w, "upstream timed out", 502)
	})
	msg := `{"id":"request-unique-001","text":"hello","attachmentId":""}`
	requireStatus(t, callTest(a, "POST", "/api/messages?customer=alice", "alice", msg), 502)
	requireStatus(t, callTest(a, "POST", "/api/messages?customer=alice", "alice", msg), 409)
	requireStatus(t, callTest(a, "POST", "/api/messages?customer=alice", "alice", strings.Replace(msg, "hello", "different", 1)), 409)
	if sends.Load() != 1 {
		t.Fatalf("ambiguous send replayed %d times", sends.Load())
	}
}
func TestSendSuccessDeduplicatesAndForcesReceiver(t *testing.T) {
	var sends atomic.Int32
	a := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["sendID"] != "alice" || in["recvID"] != "support_agent" {
			t.Error("identity not enforced", in)
		}
		sends.Add(1)
		writeJSON(w, 200, map[string]any{"errCode": 0, "data": map[string]string{"clientMsgID": "remote-1"}})
	})
	msg := `{"id":"request-unique-002","text":"hello","attachmentId":""}`
	requireStatus(t, callTest(a, "POST", "/api/messages?customer=alice", "alice", msg), 201)
	requireStatus(t, callTest(a, "POST", "/api/messages?customer=alice", "alice", msg), 200)
	requireStatus(t, callTest(a, "POST", "/api/messages?customer=bob", "bob", msg), 409)
	requireStatus(t, callTest(a, "POST", "/api/messages?customer=alice", "alice", `{"id":"request-unique-003","text":"hi","recvID":"bob"}`), 400)
	if sends.Load() != 1 {
		t.Fatal("duplicate send")
	}
}
func TestPhysicalDeletionRetriesWithoutRevealingTombstones(t *testing.T) {
	var failed atomic.Bool
	failed.Store(true)
	var deletes atomic.Int32
	custom, _ := json.Marshal(map[string]string{"data": `{"v":1,"id":"deletion-request","kind":"text","text":"secret"}`})
	a := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		var data any
		switch r.URL.Path {
		case "/msg/newest_seq":
			data = map[string]any{"maxSeqs": map[string]int64{conversationID("alice", "support_agent"): 1}}
		case "/msg/pull_msg_by_seq":
			data = map[string]any{"msgs": map[string]any{conversationID("alice", "support_agent"): map[string]any{"Msgs": []IMMessage{{Seq: 1, SendID: "alice", RecvID: "support_agent", ClientID: "remote", ContentType: 110, Content: base64.StdEncoding.EncodeToString(custom)}}}}}
		case "/msg/delete_msg_phsical_by_seq":
			deletes.Add(1)
			if failed.Load() {
				http.Error(w, "offline", 503)
				return
			}
			data = map[string]any{}
		default:
			t.Error("unexpected request", r.URL.Path)
		}
		writeJSON(w, 200, map[string]any{"errCode": 0, "data": data})
	})
	_, err := a.db.Exec("INSERT INTO sends(id,customer,sender,fingerprint,state,created) VALUES('deletion-request','alice','alice','hash','delivered',1)")
	if err != nil {
		t.Fatal(err)
	}
	requireStatus(t, callTest(a, "DELETE", "/api/messages?customer=alice", "agent", `{"seq":1}`), 200)
	a.cleanupPass(context.Background())
	w := callTest(a, "GET", "/api/messages?customer=alice", "alice", "")
	requireStatus(t, w, 200)
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("deleted content returned during upstream outage")
	}
	failed.Store(false)
	a.cleanupPass(context.Background())
	if deletes.Load() < 2 {
		t.Fatal("did not retry physical deletion")
	}
}
func TestUnsentAttachmentsAreNotVisibleToOtherParty(t *testing.T) {
	a := testApp(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected upstream call") })
	_, err := a.db.Exec("INSERT INTO attachments(id,customer,owner,name,mime,kind,size,created) VALUES('private','alice','alice','a.txt','text/plain','file',4,1)")
	if err != nil {
		t.Fatal(err)
	}
	requireStatus(t, callTest(a, "GET", "/api/attachments/private", "bob", ""), 404)
	requireStatus(t, callTest(a, "GET", "/api/attachments/private", "agent", ""), 404)
}
func TestCustomerAccountsPersistWithoutPasswordsInPlaintext(t *testing.T) {
	a := testApp(t, func(w http.ResponseWriter, r *http.Request) {})
	var hash []byte
	if err := a.db.QueryRow("SELECT hash FROM users WHERE username='admin'").Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(a.cfg.AdminPassword)) != nil {
		t.Fatal("invalid password hash")
	}
	if string(hash) == a.cfg.AdminPassword {
		t.Fatal("plaintext password persisted")
	}
}

func TestReadinessChecksDependenciesAndAssets(t *testing.T) {
	var available atomic.Bool
	a := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		if !available.Load() {
			http.Error(w, "unavailable", 503)
			return
		}
		writeJSON(w, 200, map[string]any{"errCode": 0, "data": map[string]any{"maxSeqs": map[string]int64{}}})
	})
	a.cfg.WebDir = t.TempDir()
	requireStatus(t, callTest(a, "GET", "/api/ready", "", ""), 503)
	if err := os.WriteFile(filepath.Join(a.cfg.WebDir, "index.html"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, callTest(a, "GET", "/api/ready", "", ""), 503)
	available.Store(true)
	requireStatus(t, callTest(a, "GET", "/api/ready", "", ""), 200)
	a.db.Close()
	requireStatus(t, callTest(a, "GET", "/api/ready", "", ""), 503)
}
func TestMigrationRefusesNewerSchemaWithoutChangingData(t *testing.T) {
	a := testApp(t, func(w http.ResponseWriter, r *http.Request) {})
	if _, err := a.db.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	if err := migrate(a.db); err == nil {
		t.Fatal("newer schema accepted")
	}
	var version, count int
	_ = a.db.QueryRow("PRAGMA user_version").Scan(&version)
	_ = a.db.QueryRow("SELECT count(*) FROM users").Scan(&count)
	if version != 99 || count != 3 {
		t.Fatal("migration damaged existing data")
	}
}
func TestLegacyMigrationIsIdempotent(t *testing.T) {
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("CREATE TABLE sends(id TEXT PRIMARY KEY,customer TEXT,sender TEXT,fingerprint TEXT,remote_id TEXT,state TEXT,created INTEGER); INSERT INTO sends VALUES('id','alice','alice','hash','remote','delivered',1)"); err != nil {
		t.Fatal(err)
	}
	if err = migrate(db); err != nil {
		t.Fatal(err)
	}
	if err = migrate(db); err != nil {
		t.Fatal(err)
	}
	var n, seq, version int
	_ = db.QueryRow("SELECT count(*),seq FROM sends").Scan(&n, &seq)
	_ = db.QueryRow("PRAGMA user_version").Scan(&version)
	if n != 1 || seq != 0 || version != schemaVersion {
		t.Fatal("legacy data migration failed")
	}
}
