package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type LibraryBlock struct {
	Kind    string `json:"kind"`
	Text    string `json:"text,omitempty"`
	ImageID string `json:"imageId,omitempty"`
}
type LibrarySection struct {
	Title  string         `json:"title"`
	Blocks []LibraryBlock `json:"blocks"`
}
type LibraryEntry struct {
	ID       string           `json:"id"`
	Kind     string           `json:"kind"`
	Title    string           `json:"title"`
	Category string           `json:"category"`
	Sections []LibrarySection `json:"sections"`
	Version  int              `json:"version"`
	Updated  int64            `json:"updated"`
}
type LibraryDelivery struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Total     int    `json:"total"`
	Sent      int    `json:"sent"`
	Created   int64  `json:"created"`
	Uncertain bool   `json:"uncertain"`
}

func (a *App) library(w http.ResponseWriter, r *http.Request, u User) {
	if u.Role != "agent" {
		problem(w, 403, "仅客服可使用资料库")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/library/")
	if path == "send" && r.Method == "POST" {
		a.sendLibrary(w, r, u)
		return
	}
	a.mutation.Lock()
	defer a.mutation.Unlock()
	if _, err := a.authenticate(r); err != nil {
		problem(w, 401, "请重新登录")
		return
	}
	switch {
	case path == "entries" || strings.HasPrefix(path, "entries/"):
		a.libraryEntries(w, r, strings.TrimPrefix(path, "entries"))
	case path == "images" && r.Method == "POST":
		a.uploadLibraryImage(w, r)
	case strings.HasPrefix(path, "images/") && r.Method == "GET":
		a.libraryImage(w, r, strings.TrimPrefix(path, "images/"))
	case strings.HasPrefix(path, "deliveries/") && r.Method == "DELETE":
		a.cancelLibraryDelivery(w, r, u, strings.TrimPrefix(path, "deliveries/"))
	case path == "deliveries" && r.Method == "GET":
		c, err := a.customer(r, u)
		if err != nil {
			problem(w, 403, err.Error())
			return
		}
		rows, err := a.db.Query("SELECT id,title,messages,created FROM library_deliveries WHERE customer=? AND owner=? AND cancelled=0 ORDER BY created DESC LIMIT 100", c.ID, u.ID)
		if err != nil {
			problem(w, 500, "读取发送记录失败")
			return
		}
		type saved struct {
			d        LibraryDelivery
			messages []SendInput
		}
		list := []saved{}
		for rows.Next() {
			var item saved
			var raw string
			if err = rows.Scan(&item.d.ID, &item.d.Title, &raw, &item.d.Created); err != nil {
				break
			}
			if err = json.Unmarshal([]byte(raw), &item.messages); err != nil {
				break
			}
			list = append(list, item)
		}
		scanErr := rows.Err()
		rows.Close()
		if err != nil || scanErr != nil {
			problem(w, 500, "读取发送记录失败")
			return
		}
		result := []LibraryDelivery{}
		for _, item := range list {
			item.d.Total = len(item.messages)
			item.d.Sent, item.d.Uncertain = a.deliveryProgress(item.messages)
			result = append(result, item.d)
		}
		writeJSON(w, 200, result)
	default:
		problem(w, 405, "不支持此操作")
	}
}
func (a *App) validateSections(sections []LibrarySection) error {
	if len(sections) < 1 || len(sections) > 20 {
		return errors.New("请保留 1–20 个章节")
	}
	blocks, totalText := 0, 0
	for si := range sections {
		s := &sections[si]
		s.Title = strings.TrimSpace(s.Title)
		if len([]rune(s.Title)) > 80 || len(s.Blocks) == 0 {
			return errors.New("章节标题最多 80 字，每章至少一段内容")
		}
		for bi := range s.Blocks {
			b := &s.Blocks[bi]
			blocks++
			switch b.Kind {
			case "text":
				b.Text = strings.TrimSpace(b.Text)
				b.ImageID = ""
				n := len([]rune(b.Text))
				totalText += n
				if n < 1 || n > 4000 {
					return errors.New("每段文字须为 1–4000 字")
				}
			case "image":
				b.Text = ""
				var n int
				if a.db.QueryRow("SELECT count(*) FROM library_images WHERE id=?", b.ImageID).Scan(&n) != nil || n != 1 {
					return errors.New("图片已失效，请重新上传")
				}
				if _, err := os.Stat(filepath.Join(a.cfg.DataDir, "library-images", b.ImageID)); err != nil {
					return errors.New("图片文件不存在，请重新上传")
				}
			default:
				return errors.New("内容仅支持文字和图片")
			}
		}
	}
	if blocks > 40 || totalText > 40000 {
		return errors.New("每份资料最多 40 段内容、共 40000 字")
	}
	return nil
}
func (a *App) libraryEntries(w http.ResponseWriter, r *http.Request, suffix string) {
	id := strings.TrimPrefix(suffix, "/")
	switch r.Method {
	case "GET":
		if id != "" {
			problem(w, 404, "资料不存在")
			return
		}
		rows, err := a.db.Query("SELECT id,kind,title,category,sections,version,updated FROM library_entries ORDER BY updated DESC,id")
		if err != nil {
			problem(w, 500, "读取资料失败")
			return
		}
		defer rows.Close()
		list := []LibraryEntry{}
		for rows.Next() {
			var e LibraryEntry
			var raw string
			if err = rows.Scan(&e.ID, &e.Kind, &e.Title, &e.Category, &raw, &e.Version, &e.Updated); err != nil {
				problem(w, 500, "读取资料失败")
				return
			}
			if json.Unmarshal([]byte(raw), &e.Sections) != nil {
				problem(w, 500, "资料内容损坏")
				return
			}
			list = append(list, e)
		}
		if rows.Err() != nil {
			problem(w, 500, "读取资料失败")
			return
		}
		writeJSON(w, 200, list)
	case "POST", "PUT":
		if (r.Method == "POST") != (id == "") {
			problem(w, 405, "不支持此操作")
			return
		}
		var in LibraryEntry
		if !readJSONLimit(w, r, &in, 512<<10) {
			return
		}
		in.Title = strings.TrimSpace(in.Title)
		in.Category = strings.Trim(strings.TrimSpace(in.Category), "/")
		if (in.Kind != "reply" && in.Kind != "intro") || in.Title == "" || len([]rune(in.Title)) > 80 || len([]rune(in.Category)) > 120 || strings.Count(in.Category, "/") > 3 {
			problem(w, 400, "请输入资料类型、1–80 字标题和最多四级分类（用 / 分隔）")
			return
		}
		if err := a.validateSections(in.Sections); err != nil {
			problem(w, 400, err.Error())
			return
		}
		if in.Kind == "reply" && (len(in.Sections) != 1 || in.Sections[0].Title != "" || len(in.Sections[0].Blocks) != 1 || in.Sections[0].Blocks[0].Kind != "text") {
			problem(w, 400, "快捷回复使用一段文字，图文内容请保存为业务介绍")
			return
		}
		raw, _ := json.Marshal(in.Sections)
		in.Updated = time.Now().UnixMilli()
		status := 200
		if r.Method == "POST" {
			var count int
			if err := a.db.QueryRow("SELECT count(*) FROM library_entries").Scan(&count); err != nil {
				problem(w, 500, "读取资料失败")
				return
			}
			if count >= 500 {
				problem(w, 400, "最多保存 500 份资料，请先清理不用的模板")
				return
			}
			in.ID = randomID()
			in.Version = 1
			status = 201
			if _, err := a.db.Exec("INSERT INTO library_entries(id,kind,title,category,sections,version,updated) VALUES(?,?,?,?,?,1,?)", in.ID, in.Kind, in.Title, in.Category, string(raw), in.Updated); err != nil {
				problem(w, 500, "保存失败")
				return
			}
		} else {
			result, err := a.db.Exec("UPDATE library_entries SET kind=?,title=?,category=?,sections=?,version=version+1,updated=? WHERE id=? AND version=?", in.Kind, in.Title, in.Category, string(raw), in.Updated, id, in.Version)
			if err != nil {
				problem(w, 500, "保存失败")
				return
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				problem(w, 409, "模板已被修改或删除，请返回列表刷新后再编辑；当前修改尚未保存")
				return
			}
			in.ID = id
			in.Version++
		}
		writeJSON(w, status, in)
	case "DELETE":
		version, err := strconv.Atoi(r.URL.Query().Get("version"))
		if id == "" || err != nil {
			problem(w, 400, "缺少模板版本")
			return
		}
		result, err := a.db.Exec("DELETE FROM library_entries WHERE id=? AND version=?", id, version)
		if err != nil {
			problem(w, 500, "删除失败")
			return
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			problem(w, 409, "模板已被修改或删除，请刷新列表")
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	default:
		problem(w, 405, "不支持此操作")
	}
}
func (a *App) uploadLibraryImage(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
	if err := r.ParseMultipartForm(6 << 20); err != nil {
		problem(w, 413, "图片不能超过 5 MB")
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, h, err := r.FormFile("file")
	if err != nil {
		problem(w, 400, "请选择图片")
		return
	}
	defer file.Close()
	b, err := io.ReadAll(io.LimitReader(file, (5<<20)+1))
	if err != nil || len(b) == 0 || len(b) > 5<<20 {
		problem(w, 413, "图片须为 1 字节至 5 MB")
		return
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || (format != "jpeg" && format != "png" && format != "gif") || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 20000000 {
		problem(w, 400, "仅支持不超过 2000 万像素的 JPG、PNG、GIF")
		return
	}
	var used int64
	if a.db.QueryRow("SELECT coalesce(sum(size),0) FROM library_images").Scan(&used) != nil {
		problem(w, 500, "读取图片用量失败")
		return
	}
	if used+int64(len(b)) > 500<<20 {
		problem(w, 413, "资料库图片已达 500 MB，请删除不再需要的模板；未引用图片会在 24 小时后清理")
		return
	}
	id := randomID()
	dir := filepath.Join(a.cfg.DataDir, "library-images")
	if os.MkdirAll(dir, 0700) != nil {
		problem(w, 500, "保存图片失败")
		return
	}
	name := filepath.Base(strings.ReplaceAll(h.Filename, "\\", "/"))
	if len([]rune(name)) > 100 {
		name = string([]rune(name)[:100])
	}
	if os.WriteFile(filepath.Join(dir, id), b, 0600) != nil {
		problem(w, 500, "保存图片失败")
		return
	}
	mime := "image/" + format
	if _, err = a.db.Exec("INSERT INTO library_images(id,name,mime,size,created) VALUES(?,?,?,?,?)", id, name, mime, len(b), time.Now().UnixMilli()); err != nil {
		os.Remove(filepath.Join(dir, id))
		problem(w, 500, "保存图片失败")
		return
	}
	writeJSON(w, 201, Attachment{ID: id, Name: name, MIME: mime, Kind: "image", Size: int64(len(b))})
}
func (a *App) libraryImage(w http.ResponseWriter, r *http.Request, id string) {
	var mime string
	var size int64
	if a.db.QueryRow("SELECT mime,size FROM library_images WHERE id=?", id).Scan(&mime, &size) != nil {
		problem(w, 404, "图片不存在")
		return
	}
	f, err := os.Open(filepath.Join(a.cfg.DataDir, "library-images", id))
	if err != nil {
		problem(w, 404, "图片不存在")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	io.Copy(w, f)
}

// Caller holds mutation. Images referenced by any template are retained; chat copies have separate identities.
func (a *App) cleanupLibraryImages() {
	used := map[string]bool{}
	rows, err := a.db.Query("SELECT sections FROM library_entries")
	if err != nil {
		return
	}
	for rows.Next() {
		var raw string
		var sections []LibrarySection
		if rows.Scan(&raw) != nil || json.Unmarshal([]byte(raw), &sections) != nil {
			rows.Close()
			return
		}
		for _, s := range sections {
			for _, b := range s.Blocks {
				if b.ImageID != "" {
					used[b.ImageID] = true
				}
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return
	}
	rows, err = a.db.Query("SELECT id FROM library_images WHERE created<?", time.Now().Add(-24*time.Hour).UnixMilli())
	if err != nil {
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			rows.Close()
			return
		}
		if !used[id] {
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return
	}
	for _, id := range ids {
		if err = os.Remove(filepath.Join(a.cfg.DataDir, "library-images", id)); err == nil || os.IsNotExist(err) {
			a.db.Exec("DELETE FROM library_images WHERE id=?", id)
		}
	}
}
func (a *App) deliveryProgress(messages []SendInput) (int, bool) {
	sent := 0
	uncertain := false
	for _, m := range messages {
		var state string
		a.db.QueryRow("SELECT state FROM sends WHERE id=?", m.ID).Scan(&state)
		switch state {
		case "accepted", "delivered", "deleted", "deleted_pending":
			sent++
		case "uncertain", "sending":
			uncertain = true
		}
	}
	return sent, uncertain
}
func (a *App) sendLibrary(w http.ResponseWriter, r *http.Request, u User) {
	c, err := a.customer(r, u)
	if err != nil {
		problem(w, 403, err.Error())
		return
	}
	var in struct {
		ID       string           `json:"id"`
		Title    string           `json:"title"`
		Sections []LibrarySection `json:"sections"`
	}
	if !readJSONLimit(w, r, &in, 512<<10) {
		return
	}
	messages, status, msg := a.prepareLibrary(r, u, c, in.ID, in.Title, in.Sections)
	if status != 0 {
		problem(w, status, msg)
		return
	}
	for _, m := range messages {
		// A message deliberately deleted after a partial send must not reappear on resume.
		var state string
		a.db.QueryRow("SELECT state FROM sends WHERE id=?", m.ID).Scan(&state)
		if state == "accepted" || state == "delivered" || state == "deleted" || state == "deleted_pending" {
			continue
		}
		status, result := a.sendOne(r, u, c, m, in.ID)
		if status >= 400 {
			_ = a.pruneLibraryDelivery(in.ID)
			sent, _ := a.deliveryProgress(messages)
			writeJSON(w, status, map[string]any{"error": result["error"], "id": in.ID, "sent": sent, "total": len(messages)})
			return
		}
	}
	if err := a.pruneLibraryDelivery(in.ID); err != nil {
		problem(w, 500, "消息已提交，发送记录更新失败，请继续这次发送以核实")
		return
	}

	writeJSON(w, 200, map[string]any{"id": in.ID, "sent": len(messages), "total": len(messages)})
}
func (a *App) prepareLibrary(r *http.Request, u, c User, id, title string, sections []LibrarySection) ([]SendInput, int, string) {
	a.mutation.Lock()
	defer a.mutation.Unlock()
	fail := func(code int, message string) ([]SendInput, int, string) { return nil, code, message }
	if _, err := a.authenticate(r); err != nil {
		return fail(401, "请重新登录")
	}
	fresh, err := a.user(c.ID)
	if err != nil || fresh.Disabled {
		return fail(403, "此客户账号已停用")
	}
	if len(id) < 16 || len(id) > 80 {
		return fail(400, "发送标识无效")
	}
	// Fingerprint the caller's snapshot, not the live template. Editing a template never changes an in-flight delivery.
	raw, _ := json.Marshal(struct {
		Title    string
		Sections []LibrarySection
	}{title, sections})
	sum := sha256.Sum256(raw)
	fp := hex.EncodeToString(sum[:])
	var oldCustomer, owner, oldFP, saved string
	var cancelled bool
	err = a.db.QueryRow("SELECT customer,owner,fingerprint,messages,cancelled FROM library_deliveries WHERE id=?", id).Scan(&oldCustomer, &owner, &oldFP, &saved, &cancelled)
	if err == nil {
		if cancelled {
			return fail(409, "本次发送已结束或会话已清空，请勿继续旧任务")
		}
		if oldCustomer != c.ID || owner != u.ID || (sections != nil && fp != oldFP) {
			return fail(409, "发送标识已被使用，请继续原来的发送")
		}
		var list []SendInput
		if json.Unmarshal([]byte(saved), &list) != nil {
			return fail(500, "发送快照损坏")
		}
		return list, 0, ""
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fail(500, "读取发送状态失败")
	}
	title = strings.TrimSpace(title)
	if title == "" || len([]rune(title)) > 80 {
		return fail(400, "请提供 1–80 字资料标题")
	}
	if err = a.validateSections(sections); err != nil {
		return fail(400, err.Error())
	}
	// Check the final recipient's quota before allocating any copies.
	var used, added int64
	if a.db.QueryRow("SELECT coalesce(sum(size),0) FROM attachments WHERE customer=? AND deleted=0", c.ID).Scan(&used) != nil {
		return fail(500, "读取附件用量失败")
	}
	for _, s := range sections {
		for _, b := range s.Blocks {
			if b.Kind == "image" {
				var size int64
				if a.db.QueryRow("SELECT size FROM library_images WHERE id=?", b.ImageID).Scan(&size) != nil {
					return fail(400, "图片已失效")
				}
				added += size
			}
		}
	}
	if used+added > 1<<30 {
		return fail(413, "此会话附件已达到 1 GB，请先清理")
	}
	tx, err := a.db.Begin()
	if err != nil {
		return fail(500, "无法保存发送快照")
	}
	defer tx.Rollback()
	paths := []string{}
	committed := false
	defer func() {
		if !committed {
			for _, p := range paths {
				os.Remove(p)
			}
		}
	}()
	list := []SendInput{}
	now := time.Now().UnixMilli()
	for _, s := range sections {
		if s.Title != "" {
			list = append(list, SendInput{ID: randomID(), Text: s.Title})
		}
		for _, b := range s.Blocks {
			m := SendInput{ID: randomID(), Text: b.Text}
			if b.Kind == "image" {
				var name, mime string
				var size int64
				if tx.QueryRow("SELECT name,mime,size FROM library_images WHERE id=?", b.ImageID).Scan(&name, &mime, &size) != nil {
					return fail(400, "图片已失效")
				}
				content, e := os.ReadFile(filepath.Join(a.cfg.DataDir, "library-images", b.ImageID))
				if e != nil {
					return fail(400, "图片读取失败")
				}
				m.AttachmentID = randomID()
				path := filepath.Join(a.cfg.DataDir, "attachments", m.AttachmentID)
				paths = append(paths, path)
				if os.WriteFile(path, content, 0600) != nil {
					return fail(500, "无法创建发送图片")
				}
				if _, err = tx.Exec("INSERT INTO attachments(id,customer,owner,name,mime,kind,size,created) VALUES(?,?,?,?,?,'image',?,?)", m.AttachmentID, c.ID, u.ID, name, mime, size, now); err != nil {
					return fail(500, "无法保存发送图片")
				}
				if _, err = tx.Exec("INSERT INTO library_delivery_assets(delivery,attachment) VALUES(?,?)", id, m.AttachmentID); err != nil {
					return fail(500, "无法保存发送状态")
				}
			}
			list = append(list, m)
		}
	}
	encoded, _ := json.Marshal(list)
	if _, err = tx.Exec("INSERT INTO library_deliveries(id,customer,owner,title,fingerprint,messages,created) VALUES(?,?,?,?,?,?,?)", id, c.ID, u.ID, title, fp, string(encoded), now); err != nil {
		return fail(500, "无法保存发送快照")
	}
	if tx.Commit() != nil {
		return fail(500, "无法保存发送快照")
	}
	committed = true
	return list, 0, ""
}

// Caller holds mutation. Submitted messages remain in chat; never-submitted attachment copies are discarded.
func (a *App) cancelLibraryDelivery(w http.ResponseWriter, r *http.Request, u User, id string) {
	c, err := a.customer(r, u)
	if err != nil {
		problem(w, 403, err.Error())
		return
	}
	var n int
	if a.db.QueryRow("SELECT count(*) FROM library_deliveries WHERE id=? AND customer=? AND owner=?", id, c.ID, u.ID).Scan(&n) != nil || n != 1 {
		problem(w, 404, "发送记录不存在")
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		problem(w, 500, "无法结束发送")
		return
	}
	defer tx.Rollback()
	_, err = tx.Exec("UPDATE attachments SET deleted=1 WHERE send_id='' AND id IN (SELECT attachment FROM library_delivery_assets WHERE delivery=?)", id)
	if err == nil {
		_, err = tx.Exec("DELETE FROM library_delivery_assets WHERE delivery=?", id)
	}
	if err == nil {
		_, err = tx.Exec("UPDATE library_deliveries SET cancelled=1,messages='[]' WHERE id=?", id)
	}
	if err != nil || tx.Commit() != nil {
		problem(w, 500, "无法结束发送")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// Remove content from submitted/deleted parts of a pending snapshot as well as completed deliveries.
func pruneLibraryDeliveryTx(tx *sql.Tx, id string) error {
	var raw string
	var cancelled bool
	if err := tx.QueryRow("SELECT messages,cancelled FROM library_deliveries WHERE id=?", id).Scan(&raw, &cancelled); err != nil {
		return err
	}
	if cancelled {
		return nil
	}
	var messages []SendInput
	if err := json.Unmarshal([]byte(raw), &messages); err != nil {
		return err
	}
	complete := true
	for i, m := range messages {
		var state string
		err := tx.QueryRow("SELECT state FROM sends WHERE id=?", m.ID).Scan(&state)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		switch state {
		case "accepted", "delivered", "deleted", "deleted_pending":
			messages[i] = SendInput{ID: m.ID}
		default:
			complete = false
		}
	}
	encoded, _ := json.Marshal(messages)
	if _, err := tx.Exec("UPDATE library_deliveries SET messages=? WHERE id=?", string(encoded), id); err != nil {
		return err
	}
	if complete {
		_, err := tx.Exec("DELETE FROM library_delivery_assets WHERE delivery=?", id)
		return err
	}
	return nil
}
func (a *App) pruneLibraryDelivery(id string) error {
	a.mutation.Lock()
	defer a.mutation.Unlock()
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = pruneLibraryDeliveryTx(tx, id); err != nil {
		return err
	}
	return tx.Commit()
}
func pruneCustomerDeliveriesTx(tx *sql.Tx, customer string) error {
	rows, err := tx.Query("SELECT id FROM library_deliveries WHERE customer=? AND cancelled=0", customer)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = pruneLibraryDeliveryTx(tx, id); err != nil {
			return err
		}
	}
	return nil
}
