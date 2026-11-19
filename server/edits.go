package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// OpenIM remains the source of message identity/order. This private server-side revision
// replaces its displayed payload; original OpenIM content is never returned after an edit.
func (a *App) applyEdit(customer string, msg *Message) error {
	var raw string
	err := a.db.QueryRow("SELECT payload,revision,edited_at FROM message_edits WHERE customer=? AND seq=? AND message_id=?", customer, msg.Seq, msg.ID).Scan(&raw, &msg.EditVersion, &msg.EditedAt)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	var p Payload
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return err
	}
	msg.Payload = p
	return nil
}
func (a *App) editMessage(w http.ResponseWriter, r *http.Request, u, c User) {
	if u.Role != "agent" {
		problem(w, 403, "仅客服可编辑消息")
		return
	}
	var in struct {
		Seq             int64  `json:"seq"`
		ID              string `json:"id"`
		ExpectedVersion int64  `json:"expectedVersion"`
		Text            string `json:"text"`
		AttachmentID    string `json:"attachmentId"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Text = strings.TrimSpace(in.Text)
	if in.Seq < 1 || in.ID == "" || in.ExpectedVersion < 0 || len([]rune(in.Text)) > 4000 || (in.Text == "") == (in.AttachmentID == "") {
		problem(w, 400, "请输入 1–4000 字文字，或选择一个替换附件")
		return
	}
	a.mutation.Lock()
	defer a.mutation.Unlock()
	if _, err := a.authenticate(r); err != nil {
		problem(w, 401, "请重新登录")
		return
	}
	var state string
	err := a.db.QueryRow("SELECT state FROM sends WHERE id=? AND customer=?", in.ID, c.ID).Scan(&state)
	var cleared int64
	_ = a.db.QueryRow("SELECT cleared FROM conversations WHERE customer=?", c.ID).Scan(&cleared)
	var deleted int
	_ = a.db.QueryRow("SELECT count(*) FROM deletions WHERE customer=? AND seq=?", c.ID, in.Seq).Scan(&deleted)
	if err != nil || state == "deleted" || state == "deleted_pending" || in.Seq <= cleared || deleted > 0 {
		problem(w, 404, "消息不存在或已删除")
		return
	}
	remote, err := a.im.pull(r.Context(), c.ID, conversationID(c.ID, a.cfg.AgentID), in.Seq, in.Seq)
	if err != nil {
		problem(w, 503, "无法读取原消息，请稍后重试")
		return
	}
	var current Message
	found := false
	for _, m := range remote {
		if m.Seq != in.Seq || !((m.SendID == c.ID && m.RecvID == a.cfg.AgentID) || (m.SendID == a.cfg.AgentID && m.RecvID == c.ID)) {
			continue
		}
		if decoded, ok := decodeMessage(m); ok && decoded.ID == in.ID {
			current = decoded
			found = true
			break
		}
	}
	if !found {
		problem(w, 404, "消息不存在")
		return
	}
	if err = a.applyEdit(c.ID, &current); err != nil {
		problem(w, 503, "无法读取消息修订")
		return
	}
	currentAttachment := ""
	if current.Attachment != nil {
		currentAttachment = current.Attachment.ID
	}
	if current.EditVersion != in.ExpectedVersion {
		// A retry after a lost successful response must not create another revision or consume another file.
		if current.EditVersion == in.ExpectedVersion+1 && current.Text == in.Text && currentAttachment == in.AttachmentID {
			writeJSON(w, 200, current)
			return
		}
		problem(w, 409, "消息已被修改，请关闭编辑窗口，刷新后重新编辑")
		return
	}
	next := Payload{Version: 1, ID: current.ID, Kind: "text", Text: in.Text}
	if in.AttachmentID != "" {
		att, e := a.getAttachment(in.AttachmentID)
		if e != nil || att.Deleted || att.Customer != c.ID || att.Owner != u.ID || att.SendID != "" {
			problem(w, 400, "请选择本会话中新上传的附件")
			return
		}
		next.Kind = att.Kind
		next.Attachment = &att
	}
	raw, err := json.Marshal(next)
	if err != nil {
		problem(w, 500, "保存失败")
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		problem(w, 500, "保存失败")
		return
	}
	defer tx.Rollback()
	editedAt := time.Now().UnixMilli()
	_, err = tx.Exec("INSERT INTO message_edits(customer,seq,message_id,payload,revision,edited_at) VALUES(?,?,?,?,?,?) ON CONFLICT(customer,seq) DO UPDATE SET payload=excluded.payload,revision=excluded.revision,edited_at=excluded.edited_at", c.ID, in.Seq, in.ID, string(raw), current.EditVersion+1, editedAt)
	if err == nil {
		_, err = tx.Exec("UPDATE attachments SET deleted=1 WHERE customer=? AND send_id=?", c.ID, in.ID)
	}
	if err == nil && next.Attachment != nil {
		_, err = tx.Exec("UPDATE attachments SET send_id=? WHERE id=?", in.ID, next.Attachment.ID)
	}
	// Bind the original send so the new attachment is immediately readable even before background reconciliation.
	if err == nil {
		_, err = tx.Exec("UPDATE sends SET state='delivered',seq=? WHERE id=? AND customer=?", in.Seq, in.ID, c.ID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		problem(w, 500, "修改保存失败，请刷新核实")
		return
	}
	current.Payload = next
	current.EditVersion++
	current.EditedAt = editedAt
	a.publish()
	writeJSON(w, 200, current)
}
