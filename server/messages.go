package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (a *App) messages(w http.ResponseWriter, r *http.Request, u User) {
	c, err := a.customer(r, u)
	if err != nil {
		problem(w, 403, err.Error())
		return
	}
	switch r.Method {
	case "GET":
		a.history(w, r, u, c)
	case "POST":
		a.send(w, r, u, c)
	case "DELETE":
		if u.Role != "agent" {
			problem(w, 403, "仅客服可删除消息")
			return
		}
		a.deleteMessages(w, r, c)
	default:
		problem(w, 405, "不支持此操作")
	}
}
func (a *App) history(w http.ResponseWriter, r *http.Request, u, c User) {
	a.mutation.Lock()
	defer a.mutation.Unlock()
	if _, err := a.authenticate(r); err != nil {
		problem(w, 401, "请重新登录")
		return
	}
	conv := conversationID(c.ID, a.cfg.AgentID)
	maxes, err := a.im.maxSeqs(r.Context(), c.ID)
	if err != nil {
		problem(w, 503, "OpenIM 历史记录暂不可用")
		return
	}
	maxSeq := maxes[conv]
	end := maxSeq
	if raw := r.URL.Query().Get("before"); raw != "" {
		n, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || n < 1 {
			problem(w, 400, "分页游标不正确")
			return
		}
		if n-1 < end {
			end = n - 1
		}
	}
	begin := end - 49
	if begin < 1 {
		begin = 1
	}
	var cleared int64
	_ = a.db.QueryRow("SELECT cleared FROM conversations WHERE customer=?", c.ID).Scan(&cleared)
	if begin <= cleared {
		begin = cleared + 1
	}
	list := []Message{}
	if end >= begin {
		remote, e := a.im.pull(r.Context(), c.ID, conv, begin, end)
		if e != nil {
			problem(w, 503, "OpenIM 历史记录读取失败")
			return
		}
		for _, m := range remote {
			if (m.SendID != c.ID && m.SendID != a.cfg.AgentID) || (m.RecvID != c.ID && m.RecvID != a.cfg.AgentID) {
				continue
			}
			msg, ok := decodeMessage(m)
			if !ok || m.Seq < begin || m.Seq > end {
				continue
			}
			var state string
			_ = a.db.QueryRow("SELECT state FROM sends WHERE id=? AND customer=?", msg.ID, c.ID).Scan(&state)
			var deleted int
			_ = a.db.QueryRow("SELECT count(*) FROM deletions WHERE customer=? AND seq=?", c.ID, m.Seq).Scan(&deleted)
			if (state == "deleted" || state == "deleted_pending") || deleted > 0 {
				_, _ = a.db.Exec("INSERT OR IGNORE INTO deletions(customer,seq) VALUES(?,?)", c.ID, m.Seq)
				continue
			}
			_, _ = a.db.Exec("UPDATE sends SET state='delivered',remote_id=?,seq=? WHERE id=? AND customer=? AND state NOT IN ('deleted','deleted_pending')", m.ClientID, m.Seq, msg.ID, c.ID)
			// Attachment metadata is authoritative in our private store, never trust arbitrary URLs in IM content.
			if msg.Attachment != nil {
				att, e := a.getAttachment(msg.Attachment.ID)
				if e != nil || att.Customer != c.ID || att.SendID != msg.ID || att.Deleted {
					continue
				}
				msg.Attachment = &att
			}
			list = append(list, msg)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Seq < list[j].Seq })
	var next int64
	if begin > cleared+1 {
		next = begin
	}
	writeJSON(w, 200, map[string]any{"messages": list, "before": next, "maxSeq": maxSeq})
}

type SendInput struct {
	ID           string `json:"id"`
	Text         string `json:"text"`
	AttachmentID string `json:"attachmentId"`
}

func (a *App) send(w http.ResponseWriter, r *http.Request, u, c User) {
	var in SendInput
	if !readJSON(w, r, &in) {
		return
	}
	status, result := a.sendOne(r, u, c, in, "")
	writeJSON(w, status, result)
}

// Shared by ordinary messages and resumable library deliveries; each message keeps its own idempotency key.
func (a *App) sendOne(r *http.Request, u, c User, in SendInput, delivery string) (int, map[string]string) {
	if len(in.ID) < 16 || len(in.ID) > 80 {
		return 400, map[string]string{"error": "消息标识无效"}
	}
	in.Text = strings.TrimSpace(in.Text)
	if len([]rune(in.Text)) > 4000 || (in.Text == "") == (in.AttachmentID == "") {
		return 400, map[string]string{"error": "请输入 1–4000 字文字，或选择一个附件"}
	}
	a.mutation.Lock()
	defer a.mutation.Unlock()
	if _, err := a.authenticate(r); err != nil {
		return 401, map[string]string{"error": "请重新登录"}
	}
	if delivery != "" {
		var cancelled bool
		if a.db.QueryRow("SELECT cancelled FROM library_deliveries WHERE id=? AND customer=? AND owner=?", delivery, c.ID, u.ID).Scan(&cancelled) != nil || cancelled {
			return 409, map[string]string{"error": "本次发送已结束或会话已清空，请勿继续旧任务"}
		}
	}
	fresh, e := a.user(c.ID)
	if e != nil || fresh.Disabled {
		return 403, map[string]string{"error": "此客户账号已停用"}
	}
	b, _ := json.Marshal(in)
	sum := sha256.Sum256(b)
	fp := hex.EncodeToString(sum[:])
	var oldFP, state, sender, customer string
	err := a.db.QueryRow("SELECT fingerprint,state,sender,customer FROM sends WHERE id=?", in.ID).Scan(&oldFP, &state, &sender, &customer)
	if err == nil {
		if oldFP != fp || sender != u.ID || customer != c.ID {
			return 409, map[string]string{"error": "消息标识已被使用"}
		}
		if state == "uncertain" || state == "sending" {
			return 409, map[string]string{"error": "发送结果待确认，请刷新记录；系统不会自动重复发送"}
		}
		if state == "deleted" || state == "deleted_pending" {
			return 409, map[string]string{"error": "此消息已被删除"}
		}
		return 200, map[string]string{"state": state}
	}
	p := Payload{Version: 1, ID: in.ID, Kind: "text", Text: in.Text}
	if in.AttachmentID != "" {
		att, e := a.getAttachment(in.AttachmentID)
		if e != nil || att.Owner != u.ID || att.Customer != c.ID || att.Deleted || att.SendID != "" {
			return 400, map[string]string{"error": "附件无效或已发送"}
		}
		p.Kind = att.Kind
		p.Attachment = &att
	}
	// Persist the attempt before calling OpenIM. A timeout is ambiguous: never blindly replay send_msg.
	tx, err := a.db.Begin()
	if err != nil {
		return 500, map[string]string{"error": "无法保存发送状态"}
	}
	defer tx.Rollback()
	_, err = tx.Exec("INSERT INTO sends(id,customer,sender,fingerprint,state,created) VALUES(?,?,?,?,'sending',?)", in.ID, c.ID, u.ID, fp, time.Now().UnixMilli())
	if err == nil && in.AttachmentID != "" {
		_, err = tx.Exec("UPDATE attachments SET send_id=? WHERE id=?", in.ID, in.AttachmentID)
	}
	if err != nil {
		return 500, map[string]string{"error": "无法保存发送状态"}
	}
	if err = tx.Commit(); err != nil {
		return 500, map[string]string{"error": "无法保存发送状态"}
	}
	receiver := a.cfg.AgentID
	if u.Role == "agent" {
		receiver = c.ID
	}
	remoteID, err := a.im.send(r.Context(), u.ID, receiver, u.Name, p)
	if err != nil {
		_, _ = a.db.Exec("UPDATE sends SET state='uncertain' WHERE id=?", in.ID)
		return 502, map[string]string{"error": "发送结果待确认，请查看记录后再操作；系统不会自动重复发送"}
	}
	if _, err = a.db.Exec("UPDATE sends SET state='accepted',remote_id=? WHERE id=?", remoteID, in.ID); err != nil {
		return 500, map[string]string{"error": "消息已提交，状态保存失败，请刷新核实"}
	}
	a.publish()
	return 201, map[string]string{"state": "accepted"}
}
func (a *App) deleteMessages(w http.ResponseWriter, r *http.Request, c User) {
	a.mutation.Lock()
	defer a.mutation.Unlock()
	if _, err := a.authenticate(r); err != nil {
		problem(w, 401, "请重新登录")
		return
	}
	var in struct {
		Seq int64 `json:"seq"`
		All bool  `json:"all"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if (!in.All && in.Seq < 1) || (in.All && in.Seq != 0) {
		problem(w, 400, "请选择消息或清空会话")
		return
	}
	conv := conversationID(c.ID, a.cfg.AgentID)
	seqs, err := a.im.maxSeqs(r.Context(), c.ID)
	if err != nil {
		problem(w, 503, "无法核实 OpenIM 消息状态，请稍后重试")
		return
	}
	maxSeq := seqs[conv]
	var sendID string
	if !in.All {
		if in.Seq > maxSeq {
			problem(w, 404, "消息不存在")
			return
		}
		msgs, e := a.im.pull(r.Context(), c.ID, conv, in.Seq, in.Seq)
		if e != nil {
			problem(w, 503, "消息读取失败")
			return
		}
		for _, m := range msgs {
			if p, ok := decodeMessage(m); ok {
				sendID = p.ID
			}
		}
	}
	tx, err := a.db.Begin()
	if err != nil {
		problem(w, 500, "删除失败")
		return
	}
	defer tx.Rollback()
	if in.All {
		_, err = tx.Exec("INSERT INTO conversations(customer,cleared,agent_read,customer_read) VALUES(?,?,?,?) ON CONFLICT(customer) DO UPDATE SET cleared=max(cleared,excluded.cleared),agent_read=max(agent_read,excluded.agent_read),customer_read=max(customer_read,excluded.customer_read)", c.ID, maxSeq, maxSeq, maxSeq)
		if err == nil {
			_, err = tx.Exec("UPDATE sends SET state=CASE WHEN state IN ('sending','uncertain','accepted','deleted_pending') THEN 'deleted_pending' ELSE 'deleted' END WHERE customer=?", c.ID)
		}
		if err == nil {
			_, err = tx.Exec("UPDATE attachments SET deleted=1 WHERE customer=?", c.ID)
		}
		if err == nil {
			_, err = tx.Exec("UPDATE library_deliveries SET cancelled=1,messages='[]' WHERE customer=?", c.ID)
		}
		if err == nil {
			_, err = tx.Exec("DELETE FROM library_delivery_assets WHERE delivery IN (SELECT id FROM library_deliveries WHERE customer=?)", c.ID)
		}
		// Range tombstones are compact; a persistent purge cursor physically removes old OpenIM content.
		if err == nil {
			_, err = tx.Exec("INSERT INTO purge_ranges(customer,upto,cursor) VALUES(?,?,1) ON CONFLICT(customer) DO UPDATE SET upto=max(upto,excluded.upto)", c.ID, maxSeq)
		}
	} else {
		_, err = tx.Exec("INSERT OR IGNORE INTO deletions(customer,seq) VALUES(?,?)", c.ID, in.Seq)
		if err == nil {
			_, err = tx.Exec("UPDATE sends SET state='deleted' WHERE id=? AND customer=?", sendID, c.ID)
		}
		if err == nil {
			_, err = tx.Exec("UPDATE attachments SET deleted=1 WHERE send_id=? AND customer=?", sendID, c.ID)
		}
		if err == nil {
			err = pruneCustomerDeliveriesTx(tx, c.ID)
		}
	}
	if err != nil {
		problem(w, 500, "删除失败")
		return
	}
	if err = tx.Commit(); err != nil {
		problem(w, 500, "删除失败")
		return
	}
	a.publish()
	writeJSON(w, 200, map[string]any{"ok": true, "cleanup": "queued"})
}
func (a *App) markRead(w http.ResponseWriter, r *http.Request, u User) {
	c, err := a.customer(r, u)
	if err != nil {
		problem(w, 403, err.Error())
		return
	}
	var in struct {
		Seq int64 `json:"seq"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Seq < 0 {
		problem(w, 400, "序号无效")
		return
	}
	seqs, err := a.im.maxSeqs(r.Context(), c.ID)
	if err != nil {
		problem(w, 503, "OpenIM 暂不可用")
		return
	}
	if in.Seq > seqs[conversationID(c.ID, a.cfg.AgentID)] {
		problem(w, 400, "序号无效")
		return
	}
	col := "customer_read"
	if u.Role == "agent" {
		col = "agent_read"
	}
	_, err = a.db.Exec("INSERT INTO conversations(customer,"+col+") VALUES(?,?) ON CONFLICT(customer) DO UPDATE SET "+col+"=max("+col+",excluded."+col+")", c.ID, in.Seq)
	if err != nil {
		problem(w, 500, "更新失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
