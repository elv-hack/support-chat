package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

type Permissions struct {
	Text  bool `json:"text"`
	Image bool `json:"image"`
	Voice bool `json:"voice"`
	File  bool `json:"file"`
}

func permissionsFromMask(n int) Permissions {
	return Permissions{n&1 != 0, n&2 != 0, n&4 != 0, n&8 != 0}
}
func (p Permissions) mask() int {
	n := 0
	if p.Text {
		n |= 1
	}
	if p.Image {
		n |= 2
	}
	if p.Voice {
		n |= 4
	}
	if p.File {
		n |= 8
	}
	return n
}
func (p Permissions) allows(kind string) bool {
	switch kind {
	case "text":
		return p.Text
	case "image":
		return p.Image
	case "voice":
		return p.Voice
	case "file":
		return p.File
	}
	return false
}

const defaultSettingsJSON = `{"brandName":"echat","agentName":"专属客服","welcomeTitle":"","welcomeText":"","announcement":"","primaryColor":"#71f6b1"}`

type AppSettings struct {
	BrandName    string `json:"brandName"`
	AgentName    string `json:"agentName"`
	WelcomeTitle string `json:"welcomeTitle"`
	WelcomeText  string `json:"welcomeText"`
	Announcement string `json:"announcement"`
	PrimaryColor string `json:"primaryColor"`
	Revision     int64  `json:"revision"`
}

func (a *App) serviceID() string {
	var id string
	_ = a.db.QueryRow("SELECT value FROM service_identity WHERE id=1").Scan(&id)
	return id
}
func (a *App) settings() (AppSettings, error) {
	var s AppSettings
	var raw string
	var rev int64
	err := a.db.QueryRow("SELECT payload,revision FROM app_settings WHERE id=1").Scan(&raw, &rev)
	if err != nil {
		return s, err
	}
	err = json.Unmarshal([]byte(raw), &s)
	if s.BrandName == "一对一" {
		s.BrandName = "echat"
	}
	s.Revision = rev
	return s, err
}
func (a *App) clientConfig(w http.ResponseWriter, r *http.Request) {
	s, err := a.settings()
	if err != nil {
		problem(w, 503, "配置暂不可用")
		return
	}
	writeJSON(w, 200, map[string]any{"settings": s, "webVersion": buildVersion, "serviceId": a.serviceID(), "protocol": 1})
}

var colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (a *App) adminSettings(w http.ResponseWriter, r *http.Request, u User) {
	if u.Role != "agent" {
		problem(w, 403, "仅客服可编辑设置")
		return
	}
	if r.Method == "GET" {
		s, err := a.settings()
		if err != nil {
			problem(w, 500, "读取设置失败")
			return
		}
		writeJSON(w, 200, s)
		return
	}
	if r.Method != "PUT" {
		problem(w, 405, "不支持此操作")
		return
	}
	var s AppSettings
	if !readJSON(w, r, &s) {
		return
	}
	s.BrandName = strings.TrimSpace(s.BrandName)
	s.AgentName = strings.TrimSpace(s.AgentName)
	s.WelcomeTitle = strings.TrimSpace(s.WelcomeTitle)
	s.WelcomeText = strings.TrimSpace(s.WelcomeText)
	s.Announcement = strings.TrimSpace(s.Announcement)
	if s.BrandName == "" || len([]rune(s.BrandName)) > 24 || s.AgentName == "" || len([]rune(s.AgentName)) > 24 || len([]rune(s.WelcomeTitle)) > 80 || len([]rune(s.WelcomeText)) > 500 || len([]rune(s.Announcement)) > 500 || !colorPattern.MatchString(s.PrimaryColor) {
		problem(w, 400, "请填写有效名称、欢迎文字和主题色，公告最多 500 字")
		return
	}
	a.mutation.Lock()
	defer a.mutation.Unlock()
	if _, err := a.authenticate(r); err != nil {
		problem(w, 401, "请重新登录")
		return
	}
	expected := s.Revision
	s.Revision++
	raw, _ := json.Marshal(s)
	result, err := a.db.Exec("UPDATE app_settings SET payload=?,revision=revision+1 WHERE id=1 AND revision=?", string(raw), expected)
	if err != nil {
		problem(w, 500, "保存设置失败")
		return
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		problem(w, 409, "设置已在其他页面更新，请刷新后编辑")
		return
	}
	a.publish()
	writeJSON(w, 200, s)
}
