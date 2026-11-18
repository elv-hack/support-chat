package main

import (
	"database/sql"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{3,32}$`)

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if host == "" {
		host = r.RemoteAddr
	}
	a.mu.Lock()
	lim := a.limits[host]
	if time.Now().After(lim.Until) {
		lim = rateEntry{Until: time.Now().Add(time.Minute)}
	}
	lim.Count++
	a.limits[host] = lim
	a.mu.Unlock()
	if lim.Count > 15 {
		problem(w, 429, "登录尝试过于频繁，请一分钟后重试")
		return
	}
	if len(in.Password) > 72 || len(in.Username) > 32 {
		problem(w, 401, "账号或密码不正确")
		return
	}
	a.mutation.Lock()
	defer a.mutation.Unlock()
	var id string
	var hash []byte
	err := a.db.QueryRow("SELECT id,hash FROM users WHERE username=?", in.Username).Scan(&id, &hash)
	if err != nil {
		hash = []byte("$2a$10$7EqJtq98hPqEX7fNZaFWoO5b3LCsUnZ6LL7JdXDcKf1NKzFnjhJeW")
	}
	passwordErr := bcrypt.CompareHashAndPassword(hash, []byte(in.Password))
	u, ue := a.user(id)
	if err != nil || passwordErr != nil || ue != nil || u.Disabled || (in.Role != "" && in.Role != u.Role) {
		problem(w, 401, "账号、密码或登录入口不正确，或账号已停用")
		return
	}
	if err = a.im.ensureUser(r.Context(), u.ID, u.Name); err != nil {
		problem(w, 503, "聊天服务暂不可用，请稍后重试")
		return
	}
	a.revoke(id)
	t := randomID()
	a.mu.Lock()
	a.sessions[t] = session{UserID: id, Expires: time.Now().Add(8 * time.Hour)}
	a.mu.Unlock()
	writeJSON(w, 200, map[string]any{"token": t, "user": u})
}
func (a *App) customer(r *http.Request, u User) (User, error) {
	id := r.URL.Query().Get("customer")
	if u.Role != "agent" {
		if id != "" && id != u.ID {
			return User{}, errors.New("无权访问此会话")
		}
		return u, nil
	}
	c, err := a.user(id)
	if err != nil || c.Role != "customer" {
		return User{}, errors.New("客户不存在")
	}
	return c, nil
}
func (a *App) customers(w http.ResponseWriter, r *http.Request, u User) {
	if u.Role != "agent" {
		problem(w, 403, "仅客服可管理账号")
		return
	}
	if r.Method == "GET" {
		rows, err := a.db.Query("SELECT id,username,name,role,disabled,permissions FROM users WHERE role='customer' ORDER BY created DESC")
		if err != nil {
			problem(w, 500, "读取失败")
			return
		}
		users := []User{}
		for rows.Next() {
			var c User
			var mask int
			if rows.Scan(&c.ID, &c.Username, &c.Name, &c.Role, &c.Disabled, &mask) == nil {
				c.Permissions = permissionsFromMask(mask)
				users = append(users, c)
			}
		}
		rows.Close()
		_, err = a.im.maxSeqs(r.Context(), u.ID)
		if err != nil {
			problem(w, 503, "OpenIM 暂不可用")
			return
		}
		for i := range users {
			var read int64
			_ = a.db.QueryRow("SELECT agent_read FROM conversations WHERE customer=?", users[i].ID).Scan(&read)
			_ = a.db.QueryRow("SELECT count(*) FROM sends WHERE customer=? AND sender=? AND state='delivered' AND seq>?", users[i].ID, users[i].ID, read).Scan(&users[i].Unread)
			_ = a.db.QueryRow("SELECT coalesce(max(created),0) FROM sends WHERE customer=?", users[i].ID).Scan(&users[i].LastActive)
		}
		writeJSON(w, 200, users)
		return
	}
	if r.Method != "POST" {
		problem(w, 405, "不支持此操作")
		return
	}
	var in struct {
		Username string `json:"username"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !usernamePattern.MatchString(in.Username) || len([]rune(in.Name)) < 1 || len([]rune(in.Name)) > 40 {
		problem(w, 400, "账号需为 3–32 位字母、数字或 ._-，称呼为 1–40 字")
		return
	}
	if err := validPassword(in.Password); err != nil {
		problem(w, 400, err.Error())
		return
	}
	a.mutation.Lock()
	defer a.mutation.Unlock()
	if _, err := a.authenticate(r); err != nil {
		problem(w, 401, "请重新登录")
		return
	}
	var existing string
	if err := a.db.QueryRow("SELECT id FROM users WHERE username=?", in.Username).Scan(&existing); err != sql.ErrNoRows {
		problem(w, 409, "账号已存在")
		return
	}
	c := User{ID: "c" + randomID()[:24], Username: in.Username, Name: in.Name, Role: "customer", Permissions: permissionsFromMask(15)}
	if err := a.im.ensureUser(r.Context(), c.ID, c.Name); err != nil {
		problem(w, 503, "OpenIM 创建用户失败")
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if _, err := a.db.Exec("INSERT INTO users(id,username,name,role,hash,created) VALUES(?,?,?,'customer',?,?)", c.ID, c.Username, c.Name, hash, time.Now().UnixMilli()); err != nil {
		problem(w, 500, "账号创建失败")
		return
	}
	a.publish()
	writeJSON(w, 201, c)
}
func (a *App) manageUser(w http.ResponseWriter, r *http.Request, u User) {
	if u.Role != "agent" {
		problem(w, 403, "仅客服可管理账号")
		return
	}
	if r.Method != "POST" {
		problem(w, 405, "不支持此操作")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/customers/"), "/")
	if len(parts) != 2 {
		problem(w, 404, "接口不存在")
		return
	}
	a.mutation.Lock()
	defer a.mutation.Unlock()
	if _, err := a.authenticate(r); err != nil {
		problem(w, 401, "请重新登录")
		return
	}
	c, err := a.user(parts[0])
	if err != nil || c.Role != "customer" {
		problem(w, 404, "客户不存在")
		return
	}
	switch parts[1] {
	case "permissions":
		var in struct {
			Name        string       `json:"name"`
			Permissions *Permissions `json:"permissions"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		if in.Permissions == nil || in.Name == "" || len([]rune(in.Name)) > 40 {
			problem(w, 400, "请输入 1–40 字客户称呼和完整发送权限")
			return
		}
		if _, err = a.db.Exec("UPDATE users SET name=?,permissions=? WHERE id=?", in.Name, in.Permissions.mask(), c.ID); err != nil {
			problem(w, 500, "权限保存失败")
			return
		}
		a.publish()
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	case "password":
		var in struct {
			Password string `json:"password"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		if err = validPassword(in.Password); err != nil {
			problem(w, 400, err.Error())
			return
		}
		var hash []byte
		hash, err = bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if err == nil {
			_, err = a.db.Exec("UPDATE users SET hash=? WHERE id=?", hash, c.ID)
		}
	case "disable":
		_, err = a.db.Exec("UPDATE users SET disabled=1 WHERE id=?", c.ID)
	case "enable":
		_, err = a.db.Exec("UPDATE users SET disabled=0 WHERE id=?", c.ID)
	case "kick":
	default:
		problem(w, 404, "接口不存在")
		return
	}
	if err != nil {
		problem(w, 500, "操作失败")
		return
	}
	a.revoke(c.ID)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
