package main

import (
	"bytes"
	"context"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxFile = 10 << 20

// Files never have publicly accessible URLs. Private fields never leave the gateway.
type Attachment struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	MIME     string  `json:"mime"`
	Kind     string  `json:"kind"`
	Size     int64   `json:"size"`
	Duration float64 `json:"duration"`
	Customer string  `json:"-"`
	Owner    string  `json:"-"`
	SendID   string  `json:"-"`
	Deleted  bool    `json:"-"`
}

func (a *App) getAttachment(id string) (Attachment, error) {
	var x Attachment
	err := a.db.QueryRow("SELECT id,name,mime,kind,size,duration,customer,owner,send_id,deleted FROM attachments WHERE id=?", id).Scan(&x.ID, &x.Name, &x.MIME, &x.Kind, &x.Size, &x.Duration, &x.Customer, &x.Owner, &x.SendID, &x.Deleted)
	return x, err
}
func (a *App) upload(w http.ResponseWriter, r *http.Request, u User) {
	c, err := a.customer(r, u)
	if err != nil {
		problem(w, 403, err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFile+(1<<20))
	if err = r.ParseMultipartForm(maxFile + (1 << 20)); err != nil {
		problem(w, 413, "附件不能超过 10 MB")
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, h, err := r.FormFile("file")
	if err != nil {
		problem(w, 400, "请选择附件")
		return
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil || len(b) == 0 || len(b) > maxFile {
		problem(w, 413, "文件大小须为 1 字节至 10 MB")
		return
	}
	kind := r.FormValue("kind")
	if u.Role != "agent" && !c.Permissions.allows(kind) {
		problem(w, 403, "客服已关闭此类附件的发送权限")
		return
	}
	mime := http.DetectContentType(b)
	var duration float64
	switch kind {
	case "image":
		if len(b) > 5<<20 {
			problem(w, 413, "图片不能超过 5 MB")
			return
		}
		cfg, format, e := image.DecodeConfig(bytes.NewReader(b))
		if e != nil || (format != "jpeg" && format != "png" && format != "gif") || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 20000000 {
			problem(w, 400, "仅支持不超过 2000 万像素的 JPG、PNG、GIF")
			return
		}
		mime = "image/" + format
	case "voice":
		mime = "audio/webm" // replaced by validated transcoded WAV below
	case "file":
		mime = "application/octet-stream"
	default:
		problem(w, 400, "附件类型不支持")
		return
	}
	id := randomID()
	path := filepath.Join(a.cfg.DataDir, "attachments", id)
	if err = os.WriteFile(path, b, 0600); err != nil {
		problem(w, 500, "附件保存失败")
		return
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(path)
		}
	}()
	if kind == "voice" {
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		// Decode at most 61 seconds to mono PCM. This verifies duration even for browser WebM without a duration header,
		// strips metadata and avoids trusting client MIME/duration. File-only protocol denies network reads.
		cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "file,pipe", "-threads", "1", "-i", path, "-map", "0:a:0", "-vn", "-t", "61", "-ac", "1", "-ar", "16000", "-f", "s16le", "pipe:1")
		pcm, e := cmd.Output()
		if e != nil {
			problem(w, 400, "无法解析语音，请重新录制")
			return
		}
		duration = float64(len(pcm)) / 32000
		if duration <= 0 || duration > 60.15 {
			problem(w, 400, "语音最长 60 秒")
			return
		}
		// Browser recorder stop callbacks can trail the 60-second timer by a few milliseconds.
		// Keep every accepted output at <= 60 seconds; longer inputs were rejected above.
		if duration > 60 {
			pcm = pcm[:60*32000]
			duration = 60
		}
		// Fixed PCM WAV supports playback in Android WebView and has an unambiguous duration.
		wav := makeWAV(pcm)
		if e = os.WriteFile(path, wav, 0600); e != nil {
			problem(w, 500, "语音保存失败")
			return
		}
		b = wav
		mime = "audio/wav"
	}
	name := filepath.Base(strings.ReplaceAll(h.Filename, "\\", "/"))
	if len([]rune(name)) > 100 {
		name = string([]rune(name)[:100])
	}
	if kind == "voice" {
		name = "语音.wav"
	}
	a.mutation.Lock()
	defer a.mutation.Unlock()
	if _, err = a.authenticate(r); err != nil {
		problem(w, 401, "请重新登录")
		return
	}
	fresh, e := a.user(c.ID)
	if e != nil || fresh.Disabled {
		problem(w, 403, "账号已停用")
		return
	}
	if u.Role != "agent" && !fresh.Permissions.allows(kind) {
		problem(w, 403, "客服已关闭此类附件的发送权限")
		return
	}
	var used int64
	_ = a.db.QueryRow("SELECT coalesce(sum(size),0) FROM attachments WHERE customer=? AND deleted=0", c.ID).Scan(&used)
	if used+int64(len(b)) > 1<<30 {
		problem(w, 413, "此会话附件已达到 1 GB，请联系客服清理")
		return
	}
	var pending int
	_ = a.db.QueryRow("SELECT count(*) FROM attachments WHERE owner=? AND send_id='' AND deleted=0", u.ID).Scan(&pending)
	if pending >= 20 {
		problem(w, 429, "待发送附件过多，请稍后重试")
		return
	}
	_, err = a.db.Exec("INSERT INTO attachments(id,customer,owner,name,mime,kind,size,duration,created) VALUES(?,?,?,?,?,?,?,?,?)", id, c.ID, u.ID, name, mime, kind, len(b), duration, time.Now().UnixMilli())
	if err != nil {
		problem(w, 500, "附件保存失败")
		return
	}
	keep = true
	writeJSON(w, 201, Attachment{ID: id, Name: name, MIME: mime, Kind: kind, Size: int64(len(b)), Duration: math.Round(duration*10) / 10})
}
func makeWAV(pcm []byte) []byte {
	b := make([]byte, 44+len(pcm))
	copy(b, "RIFF")
	put := func(i int, v uint32) {
		b[i] = byte(v)
		b[i+1] = byte(v >> 8)
		b[i+2] = byte(v >> 16)
		b[i+3] = byte(v >> 24)
	}
	put(4, uint32(36+len(pcm)))
	copy(b[8:], "WAVEfmt ")
	put(16, 16)
	b[20] = 1
	b[22] = 1
	put(24, 16000)
	put(28, 32000)
	b[32] = 2
	b[34] = 16
	copy(b[36:], "data")
	put(40, uint32(len(pcm)))
	copy(b[44:], pcm)
	return b
}
func (a *App) attachment(w http.ResponseWriter, r *http.Request, u User) {
	id := strings.TrimPrefix(r.URL.Path, "/api/attachments/")
	x, err := a.getAttachment(id)
	if err != nil || x.Deleted || (u.Role != "agent" && x.Customer != u.ID) {
		problem(w, 404, "附件不存在或已删除")
		return
	}
	if x.SendID == "" {
		if x.Owner != u.ID {
			problem(w, 404, "附件不存在")
			return
		}
	} else {
		var state string
		_ = a.db.QueryRow("SELECT state FROM sends WHERE id=?", x.SendID).Scan(&state)
		if (state == "deleted" || state == "deleted_pending") || state == "" || (state != "delivered" && state != "accepted" && x.Owner != u.ID) {
			problem(w, 404, "附件不可用")
			return
		}
	}
	if x.Kind == "file" {
		// Files are retained on the server; plain text is the initial in-app preview type. No export/download endpoint.
		b, e := os.ReadFile(filepath.Join(a.cfg.DataDir, "attachments", x.ID))
		if e != nil {
			problem(w, 404, "文件不存在")
			return
		}
		mt := http.DetectContentType(b)
		if !strings.HasPrefix(mt, "text/plain") {
			problem(w, 415, "此文件已保存在服务器，第一版暂不支持预览此格式")
			return
		}
		if len(b) > 256<<10 {
			problem(w, 413, "文本预览上限为 256 KB")
			return
		}
		writeJSON(w, 200, map[string]string{"text": string(b)})
		return
	}
	f, e := os.Open(filepath.Join(a.cfg.DataDir, "attachments", x.ID))
	if e != nil {
		problem(w, 404, "附件不存在")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", x.MIME)
	w.Header().Set("Content-Length", strconv.FormatInt(x.Size, 10))
	_, _ = io.Copy(w, f)
}
