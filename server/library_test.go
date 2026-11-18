package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func libraryJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func librarySections(text string) []LibrarySection {
	return []LibrarySection{{Blocks: []LibraryBlock{{Kind: "text", Text: text}}}}
}
func uploadLibraryTest(a *App, tok string, b []byte) *httptest.ResponseRecorder {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, _ := form.CreateFormFile("file", "sample.png")
	file.Write(b)
	form.Close()
	r := httptest.NewRequest("POST", "/api/library/images", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}
func samplePNG() []byte {
	b, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9Z4L8AAAAASUVORK5CYII=")
	return b
}
func TestLibraryPermissionsVersionsAndRestart(t *testing.T) {
	a := testApp(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected OpenIM call") })
	for _, endpoint := range []string{"entries", "images/private", "deliveries?customer=alice"} {
		requireStatus(t, callTest(a, "GET", "/api/library/"+endpoint, "alice", ""), 403)
	}
	requireStatus(t, callTest(a, "POST", "/api/library/send?customer=alice", "alice", `{}`), 403)
	requireStatus(t, uploadLibraryTest(a, "alice", samplePNG()), 403)
	requireStatus(t, callTest(a, "GET", "/api/library/entries", "", ""), 401)
	entry := LibraryEntry{Kind: "reply", Title: "服务时间", Category: "售前/常见问题", Sections: librarySections("工作日 9:00–18:00，其他时间请留言。")}
	w := callTest(a, "POST", "/api/library/entries", "agent", libraryJSON(entry))
	requireStatus(t, w, 201)
	json.Unmarshal(w.Body.Bytes(), &entry)
	stale := entry
	entry.Title = "营业时间"
	w = callTest(a, "PUT", "/api/library/entries/"+entry.ID, "agent", libraryJSON(entry))
	requireStatus(t, w, 200)
	json.Unmarshal(w.Body.Bytes(), &entry)
	requireStatus(t, callTest(a, "PUT", "/api/library/entries/"+entry.ID, "agent", libraryJSON(stale)), 409)
	requireStatus(t, callTest(a, "DELETE", "/api/library/entries/"+entry.ID+"?version=1", "agent", ""), 409)
	fresh, err := NewApp(a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.db.Close()
	var title string
	if fresh.db.QueryRow("SELECT title FROM library_entries WHERE id=?", entry.ID).Scan(&title) != nil || title != "营业时间" {
		t.Fatal("template did not survive restart")
	}
	requireStatus(t, callTest(a, "DELETE", "/api/library/entries/"+entry.ID+"?version=2", "agent", ""), 200)
}
func TestLibraryImageCopiesArePrivateAndIndependent(t *testing.T) {
	var captured []Payload
	a := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Content struct {
				Data string `json:"data"`
			} `json:"content"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		var p Payload
		json.Unmarshal([]byte(in.Content.Data), &p)
		captured = append(captured, p)
		writeJSON(w, 200, map[string]any{"errCode": 0, "data": map[string]string{"clientMsgID": "remote"}})
	})
	requireStatus(t, uploadLibraryTest(a, "agent", []byte(`<svg onload="alert(1)"/>`)), 400)
	w := uploadLibraryTest(a, "agent", samplePNG())
	requireStatus(t, w, 201)
	var image Attachment
	json.Unmarshal(w.Body.Bytes(), &image)
	sections := []LibrarySection{{Title: "方案特点", Blocks: []LibraryBlock{{Kind: "text", Text: "专属方案介绍"}, {Kind: "image", ImageID: image.ID}}}}
	entry := LibraryEntry{Kind: "intro", Title: "标准方案", Category: "企业/服务", Sections: sections}
	w = callTest(a, "POST", "/api/library/entries", "agent", libraryJSON(entry))
	requireStatus(t, w, 201)
	json.Unmarshal(w.Body.Bytes(), &entry)
	for _, customer := range []string{"alice", "bob"} {
		payload := map[string]any{"id": "delivery-image-" + customer, "title": entry.Title, "sections": sections}
		requireStatus(t, callTest(a, "POST", "/api/library/send?customer="+customer, "agent", libraryJSON(payload)), 200)
	}
	if len(captured) != 6 || captured[0].Text != "方案特点" || captured[1].Text != "专属方案介绍" || captured[2].Attachment == nil {
		t.Fatalf("wrong message order: %+v", captured)
	}
	first, second := captured[2].Attachment.ID, captured[5].Attachment.ID
	if first == second || first == image.ID {
		t.Fatal("image identity reused across customers")
	}
	requireStatus(t, callTest(a, "GET", "/api/attachments/"+first, "alice", ""), 200)
	requireStatus(t, callTest(a, "GET", "/api/attachments/"+first, "bob", ""), 404)
	requireStatus(t, callTest(a, "GET", "/api/library/images/"+image.ID, "alice", ""), 403)
	// Editing/deleting the source template and collecting its image cannot remove a chat copy.
	requireStatus(t, callTest(a, "DELETE", "/api/library/entries/"+entry.ID+"?version=1", "agent", ""), 200)
	a.db.Exec("UPDATE library_images SET created=0")
	a.mutation.Lock()
	a.cleanupLibraryImages()
	a.mutation.Unlock()
	requireStatus(t, callTest(a, "GET", "/api/library/images/"+image.ID, "agent", ""), 404)
	w = callTest(a, "GET", "/api/attachments/"+first, "alice", "")
	requireStatus(t, w, 200)
	if !bytes.Equal(w.Body.Bytes(), samplePNG()) {
		t.Fatal("chat image changed")
	}
	var raw string
	a.db.QueryRow("SELECT messages FROM library_deliveries WHERE id='delivery-image-alice'").Scan(&raw)
	if strings.Contains(raw, "专属方案介绍") || strings.Contains(raw, first) {
		t.Fatal("completed receipt retained message content")
	}
}
func TestLibraryResumeNeverReplaysAmbiguousMessages(t *testing.T) {
	var count atomic.Int32
	a := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		n := count.Add(1)
		if n == 2 {
			http.Error(w, "timeout", 502)
			return
		}
		writeJSON(w, 200, map[string]any{"errCode": 0, "data": map[string]string{"clientMsgID": "remote"}})
	})
	sections := []LibrarySection{{Blocks: []LibraryBlock{{Kind: "text", Text: "第一条"}, {Kind: "text", Text: "第二条"}, {Kind: "text", Text: "第三条"}}}}
	input := libraryJSON(map[string]any{"id": "resume-delivery-01", "title": "流程", "sections": sections})
	requireStatus(t, callTest(a, "POST", "/api/library/send?customer=alice", "agent", input), 502)
	requireStatus(t, callTest(a, "POST", "/api/library/send?customer=alice", "agent", input), 409)
	var pendingRaw string
	a.db.QueryRow("SELECT messages FROM library_deliveries WHERE id='resume-delivery-01'").Scan(&pendingRaw)
	if strings.Contains(pendingRaw, "第一条") {
		t.Fatal("partial snapshot retained already submitted text")
	}
	if count.Load() != 2 {
		t.Fatal("replayed ambiguous send")
	}
	requireStatus(t, callTest(a, "POST", "/api/library/send?customer=bob", "agent", input), 409)
	requireStatus(t, callTest(a, "POST", "/api/library/send?customer=alice", "agent", strings.Replace(input, "第一条", "篡改", 1)), 409)
	w := callTest(a, "GET", "/api/library/deliveries?customer=alice", "agent", "")
	requireStatus(t, w, 200)
	var deliveries []LibraryDelivery
	json.Unmarshal(w.Body.Bytes(), &deliveries)
	if len(deliveries) != 1 || deliveries[0].Sent != 1 || !deliveries[0].Uncertain {
		t.Fatal("partial progress missing")
	}
	// Simulate history reconciliation confirming the uncertain message was delivered.
	a.db.Exec("UPDATE sends SET state='delivered' WHERE state='uncertain'")
	requireStatus(t, callTest(a, "POST", "/api/library/send?customer=alice", "agent", `{"id":"resume-delivery-01"}`), 200)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			requireStatus(t, callTest(a, "POST", "/api/library/send?customer=alice", "agent", input), 200)
		}()
	}
	wg.Wait()
	if count.Load() != 3 {
		t.Fatal("retry repeated accepted messages", count.Load())
	}
}
func TestLibraryCancellationAndChatClearPreventResurrection(t *testing.T) {
	var count atomic.Int32
	a := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/msg/newest_seq" {
			writeJSON(w, 200, map[string]any{"errCode": 0, "data": map[string]any{"maxSeqs": map[string]int64{}}})
			return
		}
		count.Add(1)
		http.Error(w, "timeout", 502)
	})
	for _, test := range []struct{ id, action string }{{"cancel-library-01", "cancel"}, {"clear-library-001", "clear"}} {
		input := libraryJSON(map[string]any{"id": test.id, "title": "流程", "sections": librarySections("私密的临时介绍")})
		requireStatus(t, callTest(a, "POST", "/api/library/send?customer=alice", "agent", input), 502)
		if test.action == "cancel" {
			requireStatus(t, callTest(a, "DELETE", "/api/library/deliveries/"+test.id+"?customer=alice", "agent", ""), 200)
		} else {
			requireStatus(t, callTest(a, "DELETE", "/api/messages?customer=alice", "agent", `{"all":true}`), 200)
		}
		before := count.Load()
		requireStatus(t, callTest(a, "POST", "/api/library/send?customer=alice", "agent", input), 409)
		if count.Load() != before {
			t.Fatal("cancelled content resurrected")
		}
		var raw string
		a.db.QueryRow("SELECT messages FROM library_deliveries WHERE id=?", test.id).Scan(&raw)
		if raw != "[]" {
			t.Fatal("cancelled draft content retained")
		}
	}
}
func TestLibraryValidationAndFailedPreparationAreAtomic(t *testing.T) {
	a := testApp(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid preparation contacted OpenIM") })
	w := uploadLibraryTest(a, "agent", samplePNG())
	requireStatus(t, w, 201)
	var image Attachment
	json.Unmarshal(w.Body.Bytes(), &image)
	invalid := LibraryEntry{Kind: "intro", Title: "无效", Sections: []LibrarySection{{Blocks: []LibraryBlock{{Kind: "image", ImageID: "missing"}}}}}
	requireStatus(t, callTest(a, "POST", "/api/library/entries", "agent", libraryJSON(invalid)), 400)
	invalid.Sections = librarySections(strings.Repeat("文", 4001))
	requireStatus(t, callTest(a, "POST", "/api/library/entries", "agent", libraryJSON(invalid)), 400)
	if _, err := a.db.Exec("CREATE TRIGGER fail_delivery BEFORE INSERT ON library_deliveries BEGIN SELECT RAISE(ABORT,'simulated storage failure'); END"); err != nil {
		t.Fatal(err)
	}
	sections := []LibrarySection{{Blocks: []LibraryBlock{{Kind: "image", ImageID: image.ID}}}}
	requireStatus(t, callTest(a, "POST", "/api/library/send?customer=alice", "agent", libraryJSON(map[string]any{"id": "atomic-delivery-01", "title": "图片", "sections": sections})), 500)
	var n int
	a.db.QueryRow("SELECT count(*) FROM attachments").Scan(&n)
	files, _ := os.ReadDir(filepath.Join(a.cfg.DataDir, "attachments"))
	if n != 0 || len(files) != 0 {
		t.Fatal("failed transaction leaked attachment copies")
	}
}
func TestLibraryMigrationFromSchemaOnePreservesAccounts(t *testing.T) {
	a := testApp(t, func(w http.ResponseWriter, r *http.Request) {})
	_, err := a.db.Exec("DROP TABLE library_entries; DROP TABLE library_images; DROP TABLE library_deliveries; DROP TABLE library_delivery_assets; PRAGMA user_version=1;")
	if err != nil {
		t.Fatal(err)
	}
	if err = migrate(a.db); err != nil {
		t.Fatal(err)
	}
	if err = migrate(a.db); err != nil {
		t.Fatal(err)
	}
	var version, count int
	a.db.QueryRow("PRAGMA user_version").Scan(&version)
	a.db.QueryRow("SELECT count(*) FROM users").Scan(&count)
	if version != schemaVersion || count != 3 {
		t.Fatal("schema migration lost existing users")
	}
}
