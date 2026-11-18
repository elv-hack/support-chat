package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsAuthorizationConcurrencyAndPersistence(t *testing.T) {
	a := testApp(t, func(w http.ResponseWriter, r *http.Request) {})
	requireStatus(t, callTest(a, "GET", "/api/admin/settings", "alice", ""), 403)
	w := callTest(a, "GET", "/api/admin/settings", "agent", "")
	requireStatus(t, w, 200)
	var s AppSettings
	json.Unmarshal(w.Body.Bytes(), &s)
	s.BrandName = "服务站"
	s.Announcement = "营业公告"
	raw, _ := json.Marshal(s)
	requireStatus(t, callTest(a, "PUT", "/api/admin/settings", "alice", string(raw)), 403)
	requireStatus(t, callTest(a, "PUT", "/api/admin/settings", "agent", string(raw)), 200)
	requireStatus(t, callTest(a, "PUT", "/api/admin/settings", "agent", string(raw)), 409)
	public := callTest(a, "GET", "/api/client/config", "", "")
	requireStatus(t, public, 200)
	if !strings.Contains(public.Body.String(), "服务站") || strings.Contains(public.Body.String(), a.cfg.AdminPassword) {
		t.Fatal("invalid public config")
	}
	fresh, err := NewApp(a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.db.Close()
	saved, err := fresh.settings()
	if err != nil || saved.BrandName != "服务站" || saved.Revision != s.Revision+1 || fresh.serviceID() != a.serviceID() {
		t.Fatal("configuration or identity did not persist")
	}
}
func TestPermissionsEnforcedForEverySendAndUpload(t *testing.T) {
	a := testApp(t, func(w http.ResponseWriter, r *http.Request) { t.Error("denied message reached OpenIM") })
	requireStatus(t, callTest(a, "POST", "/api/customers/alice/permissions", "alice", `{"name":"Alice","permissions":{"text":false}}`), 403)
	requireStatus(t, callTest(a, "POST", "/api/customers/alice/permissions", "agent", `{"name":"Alice","permissions":{"text":false,"image":false,"voice":false,"file":false}}`), 200)
	requireStatus(t, callTest(a, "GET", "/api/me", "alice", ""), 200)
	requireStatus(t, callTest(a, "POST", "/api/messages?customer=alice", "alice", `{"id":"permission-text-test","text":"denied"}`), 403)
	for _, kind := range []string{"image", "voice", "file"} {
		// Previously uploaded content must also be denied after a permission revocation.
		_, err := a.db.Exec("INSERT INTO attachments(id,customer,owner,name,mime,kind,size,created) VALUES(?,'alice','alice','a','text/plain',?,4,1)", kind, kind)
		if err != nil {
			t.Fatal(err)
		}
		requireStatus(t, callTest(a, "POST", "/api/messages?customer=alice", "alice", `{"id":"permission-send-`+kind+`","attachmentId":"`+kind+`"}`), 403)
		var b bytes.Buffer
		form := multipart.NewWriter(&b)
		form.WriteField("kind", kind)
		f, _ := form.CreateFormFile("file", "a.txt")
		f.Write([]byte("denied"))
		form.Close()
		req := httptest.NewRequest("POST", "/api/attachments?customer=alice", &b)
		req.Header.Set("Content-Type", form.FormDataContentType())
		req.Header.Set("Authorization", "Bearer alice")
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		requireStatus(t, w, 403)
	}
	requireStatus(t, callTest(a, "POST", "/api/customers/alice/permissions", "agent", `{"name":"Alice","permissions":{"text":true}}`), 200)
	me := callTest(a, "GET", "/api/me", "alice", "")
	var u User
	json.Unmarshal(me.Body.Bytes(), &u)
	if !u.Permissions.Text || u.Permissions.File {
		t.Fatal("permission update not visible")
	}
}
func TestSchemaTwoMigrationPreservesIdentityAndDefaultPermissions(t *testing.T) {
	a := testApp(t, func(w http.ResponseWriter, r *http.Request) {})
	_, err := a.db.Exec("DROP TABLE app_settings; DROP TABLE service_identity; ALTER TABLE users DROP COLUMN permissions; PRAGMA user_version=2;")
	if err != nil {
		t.Fatal(err)
	}
	if err = migrate(a.db); err != nil {
		t.Fatal(err)
	}
	id := a.serviceID()
	if len(id) < 16 {
		t.Fatal("missing identity")
	}
	if err = migrate(a.db); err != nil {
		t.Fatal(err)
	}
	u, err := a.user("alice")
	if err != nil || u.Permissions.mask() != 15 || a.serviceID() != id {
		t.Fatal("bad migration")
	}
}
func directoryCall(d *Directory, method, path, auth, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+auth)
	w := httptest.NewRecorder()
	d.Handler().ServeHTTP(w, r)
	return w
}
func TestDirectoryAuthenticationCASAndRestart(t *testing.T) {
	key := strings.Repeat("x", 32)
	path := filepath.Join(t.TempDir(), "route.json")
	d, e := newDirectory(path, key, false)
	if e != nil {
		t.Fatal(e)
	}
	requireStatus(t, directoryCall(d, "GET", "/api/bootstrap", "", ""), 503)
	body := `{"serviceId":"0123456789abcdef","serverUrl":"https://chat.example.com","expectedRevision":0}`
	requireStatus(t, directoryCall(d, "PUT", "/admin/route", "wrong", body), 401)
	requireStatus(t, directoryCall(d, "PUT", "/admin/route", key, body), 200)
	requireStatus(t, directoryCall(d, "PUT", "/admin/route", key, body), 409)
	requireStatus(t, directoryCall(d, "PUT", "/admin/route", key, `{"serviceId":"different-service","serverUrl":"https://chat.example.com","expectedRevision":1}`), 409)
	d, e = newDirectory(path, key, false)
	if e != nil {
		t.Fatal(e)
	}
	w := directoryCall(d, "GET", "/api/bootstrap", "", "")
	requireStatus(t, w, 200)
	var route Route
	json.Unmarshal(w.Body.Bytes(), &route)
	if route.Revision != 1 || strings.Contains(w.Body.String(), key) {
		t.Fatal("bad public state")
	}
}
func TestConnectionPublishingRequiresMigratedReadyService(t *testing.T) {
	a := testApp(t, func(w http.ResponseWriter, r *http.Request) {})
	key := strings.Repeat("k", 32)
	d, _ := newDirectory(filepath.Join(t.TempDir(), "route.json"), key, true)
	entry := httptest.NewServer(d.Handler())
	defer entry.Close()
	a.cfg.DirectoryURL = entry.URL
	a.cfg.DirectoryToken = key
	a.cfg.AllowLocalRouting = true
	service := a.serviceID()
	ready := true
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("credentials forwarded to probe")
		}
		status := "ready"
		if !ready {
			status = "unavailable"
		}
		writeJSON(w, 200, map[string]any{"serviceId": service, "status": status, "protocol": 1, "schemaVersion": schemaVersion})
	}))
	defer target.Close()
	body := func(password string) string {
		b, _ := json.Marshal(map[string]any{"serverUrl": target.URL, "expectedRevision": 0, "password": password, "dataReady": true})
		return string(b)
	}
	requireStatus(t, callTest(a, "PUT", "/api/admin/connection", "alice", body(a.cfg.AdminPassword)), 403)
	requireStatus(t, callTest(a, "PUT", "/api/admin/connection", "agent", body("wrong")), 403)
	service = "wrong-service-identity"
	requireStatus(t, callTest(a, "PUT", "/api/admin/connection", "agent", body(a.cfg.AdminPassword)), 409)
	service = a.serviceID()
	ready = false
	requireStatus(t, callTest(a, "PUT", "/api/admin/connection", "agent", body(a.cfg.AdminPassword)), 409)
	if d.route.Revision != 0 {
		t.Fatal("invalid destination published")
	}
	ready = true
	requireStatus(t, callTest(a, "PUT", "/api/admin/connection", "agent", body(a.cfg.AdminPassword)), 200)
	requireStatus(t, callTest(a, "PUT", "/api/admin/connection", "agent", body(a.cfg.AdminPassword)), 409)
	if d.route.Revision != 1 || d.route.ServerURL != target.URL {
		t.Fatal("route not published")
	}
}
func TestOriginRejectsUnsafeURLs(t *testing.T) {
	for _, s := range []string{"http://example.com", "https://a.com/path", "https://user@a.com", "https://a.com?", "https://a.com#", "https://a.com:0", "https://a.com:99999", "https://a.com:", "https://a.com\\@b.com"} {
		if _, e := origin(s, false); e == nil {
			t.Errorf("accepted %s", s)
		}
	}
}
