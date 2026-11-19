package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func editFixture(t *testing.T) *App {
	conv := conversationID("alice", "support_agent")
	remote := []IMMessage{}
	for i, p := range []Payload{{Version: 1, ID: "editable-message-1", Kind: "text", Text: "original customer text"}, {Version: 1, ID: "editable-message-2", Kind: "image", Attachment: &Attachment{ID: "old-image", Kind: "image"}}} {
		payload, _ := json.Marshal(p)
		content, _ := json.Marshal(map[string]string{"data": string(payload)})
		sender, receiver := "alice", "support_agent"
		if i == 1 {
			sender, receiver = receiver, sender
		}
		remote = append(remote, IMMessage{Seq: int64(i + 1), SendID: sender, RecvID: receiver, ContentType: 110, Content: string(content), SendTime: 12345})
	}
	a := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		var data any
		switch r.URL.Path {
		case "/msg/newest_seq":
			data = map[string]any{"maxSeqs": map[string]int64{conv: 2}}
		case "/msg/pull_msg_by_seq":
			data = map[string]any{"msgs": map[string]any{conv: map[string]any{"Msgs": remote}}}
		default:
			t.Error("unexpected upstream write", r.URL.Path)
		}
		writeJSON(w, 200, map[string]any{"errCode": 0, "data": data})
	})
	for _, m := range remote {
		p, _ := decodeMessage(m)
		if _, e := a.db.Exec("INSERT INTO sends(id,customer,sender,fingerprint,state,seq,created) VALUES(?,'alice',?,'test','accepted',?,1)", p.ID, m.SendID, m.Seq); e != nil {
			t.Fatal(e)
		}
	}
	editAttachment(t, a, "old-image", "alice", "support_agent", "image", "editable-message-2")
	return a
}
func editAttachment(t *testing.T, a *App, id, customer, owner, kind, send string) {
	t.Helper()
	if _, e := a.db.Exec("INSERT INTO attachments(id,customer,owner,name,mime,kind,size,send_id,created) VALUES(?,?,?,'fixture','image/png',?,4,?,1)", id, customer, owner, kind, send); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(a.cfg.DataDir, "attachments", id), []byte("test"), 0600); e != nil {
		t.Fatal(e)
	}
}
func editBody(seq, version int, text, attachment string) string {
	return libraryJSON(map[string]any{"seq": seq, "id": "editable-message-" + map[int]string{1: "1", 2: "2"}[seq], "expectedVersion": version, "text": text, "attachmentId": attachment})
}
func editedHistory(t *testing.T, a *App) []Message {
	w := callTest(a, "GET", "/api/messages?customer=alice", "alice", "")
	requireStatus(t, w, 200)
	var out struct {
		Messages []Message `json:"messages"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	return out.Messages
}
func TestEditAuthorizationVersionAndPersistence(t *testing.T) {
	a := editFixture(t)
	body := editBody(1, 0, "corrected text", "")
	requireStatus(t, callTest(a, "PATCH", "/api/messages?customer=alice", "alice", body), 403)
	requireStatus(t, callTest(a, "PATCH", "/api/messages?customer=alice", "bob", body), 403)
	requireStatus(t, callTest(a, "PATCH", "/api/messages?customer=bob", "agent", body), 404)
	requireStatus(t, callTest(a, "PATCH", "/api/messages?customer=alice", "", body), 401)
	requireStatus(t, callTest(a, "PATCH", "/api/messages?customer=alice", "agent", body), 200)
	requireStatus(t, callTest(a, "PATCH", "/api/messages?customer=alice", "agent", body), 200)
	requireStatus(t, callTest(a, "PATCH", "/api/messages?customer=alice", "agent", editBody(1, 0, "stale overwrite", "")), 409)
	list := editedHistory(t, a)
	if list[0].Text != "corrected text" || list[0].Sender != "alice" || list[0].Seq != 1 || list[0].SentAt != 12345 || list[0].EditVersion != 1 || list[0].EditedAt == 0 {
		t.Fatal("edit lost message identity or metadata")
	}
	fresh, e := NewApp(a.cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer fresh.db.Close()
	m := Message{Payload: Payload{ID: "editable-message-1"}, Seq: 1}
	if e = fresh.applyEdit("alice", &m); e != nil || m.Text != "corrected text" {
		t.Fatal("edit not persisted")
	}
}
func TestReplaceEveryMediaKindAndRevokeOldAttachment(t *testing.T) {
	a := editFixture(t)
	previous := "old-image"
	for i, kind := range []string{"image", "voice", "file"} {
		id := "replace-" + kind
		editAttachment(t, a, id, "alice", "support_agent", kind, "")
		body := editBody(2, i, "", id)
		requireStatus(t, callTest(a, "PATCH", "/api/messages?customer=alice", "agent", body), 200)
		requireStatus(t, callTest(a, "PATCH", "/api/messages?customer=alice", "agent", body), 200)
		requireStatus(t, callTest(a, "GET", "/api/attachments/"+previous, "alice", ""), 404)
		requireStatus(t, callTest(a, "GET", "/api/attachments/"+id, "alice", ""), 200)
		list := editedHistory(t, a)
		if len(list) != 2 || list[1].Kind != kind || list[1].Attachment.ID != id || list[1].Sender != "support_agent" {
			t.Fatal("replacement not authoritative")
		}
		previous = id
	}
	requireStatus(t, callTest(a, "PATCH", "/api/messages?customer=alice", "agent", editBody(2, 3, "now text", "")), 200)
	requireStatus(t, callTest(a, "GET", "/api/attachments/"+previous, "alice", ""), 404)
	if m := editedHistory(t, a)[1]; m.Text != "now text" || m.Attachment != nil {
		t.Fatal("media to text replacement failed")
	}
}
func TestReplacementIsolationAndAtomicFailure(t *testing.T) {
	a := editFixture(t)
	editAttachment(t, a, "other-customer", "bob", "support_agent", "image", "")
	editAttachment(t, a, "customer-owned", "alice", "alice", "image", "")
	for _, id := range []string{"other-customer", "customer-owned", "old-image"} {
		requireStatus(t, callTest(a, "PATCH", "/api/messages?customer=alice", "agent", editBody(2, 0, "", id)), 400)
	}
	editAttachment(t, a, "valid-replacement", "alice", "support_agent", "image", "")
	if _, err := a.db.Exec("CREATE TRIGGER fail_edit BEFORE UPDATE ON sends BEGIN SELECT RAISE(ABORT,'forced failure'); END;"); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, callTest(a, "PATCH", "/api/messages?customer=alice", "agent", editBody(2, 0, "", "valid-replacement")), 500)
	old, _ := a.getAttachment("old-image")
	next, _ := a.getAttachment("valid-replacement")
	var n int
	a.db.QueryRow("SELECT count(*) FROM message_edits").Scan(&n)
	if old.Deleted || next.SendID != "" || n != 0 {
		t.Fatal("failed edit changed attachments or revision")
	}
}
func TestDeletedEditsCannotReappearAndCorruptRevisionFailsClosed(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "all"}[all], func(t *testing.T) {
			a := editFixture(t)
			requireStatus(t, callTest(a, "PATCH", "/api/messages?customer=alice", "agent", editBody(1, 0, "replacement", "")), 200)
			_, err := a.db.Exec("UPDATE message_edits SET payload='bad json'")
			if err != nil {
				t.Fatal(err)
			}
			w := callTest(a, "GET", "/api/messages?customer=alice", "alice", "")
			requireStatus(t, w, 503)
			if strings.Contains(w.Body.String(), "original customer text") {
				t.Fatal("original leaked")
			}
			data := `{"seq":1}`
			if all {
				data = `{"all":true}`
			}
			requireStatus(t, callTest(a, "DELETE", "/api/messages?customer=alice", "agent", data), 200)
			requireStatus(t, callTest(a, "PATCH", "/api/messages?customer=alice", "agent", editBody(1, 1, "resurrection", "")), 404)
			var n int
			a.db.QueryRow("SELECT count(*) FROM message_edits").Scan(&n)
			if n != 0 {
				t.Fatal("edit payload retained after deletion")
			}
			for _, m := range editedHistory(t, a) {
				if m.Seq == 1 {
					t.Fatal("deleted edit reappeared")
				}
			}
		})
	}
}
