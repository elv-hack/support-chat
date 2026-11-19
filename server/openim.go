package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type OpenIM struct {
	cfg     Config
	client  *http.Client
	mu      sync.Mutex
	token   string
	expires time.Time
}
type IMError struct {
	Code    int
	Message string
}

func (e *IMError) Error() string { return fmt.Sprintf("OpenIM error %d: %s", e.Code, e.Message) }
func NewOpenIM(c Config) *OpenIM {
	return &OpenIM{cfg: c, client: &http.Client{Timeout: 15 * time.Second}}
}
func (im *OpenIM) request(ctx context.Context, path, token string, input, output any) error {
	b, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(im.cfg.OpenIMURL, "/")+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("operationID", randomID())
	if token != "" {
		req.Header.Set("token", token)
	}
	res, err := im.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("OpenIM HTTP %d", res.StatusCode)
	}
	var env struct {
		Code    int             `json:"errCode"`
		Message string          `json:"errMsg"`
		Data    json.RawMessage `json:"data"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&env); err != nil {
		return err
	}
	if env.Code != 0 {
		return &IMError{env.Code, env.Message}
	}
	if output != nil {
		return json.Unmarshal(env.Data, output)
	}
	return nil
}
func (im *OpenIM) adminToken(ctx context.Context) (string, error) {
	im.mu.Lock()
	defer im.mu.Unlock()
	if im.token != "" && time.Now().Before(im.expires) {
		return im.token, nil
	}
	var out struct {
		Token string `json:"token"`
		TTL   int64  `json:"expireTimeSeconds"`
	}
	if err := im.request(ctx, "/auth/get_admin_token", "", map[string]string{"secret": im.cfg.OpenIMSecret, "userID": im.cfg.OpenIMAdmin}, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", errors.New("OpenIM returned empty token")
	}
	ttl := out.TTL
	if ttl < 120 {
		ttl = 120
	}
	im.token = out.Token
	im.expires = time.Now().Add(time.Duration(ttl-60) * time.Second)
	return im.token, nil
}
func (im *OpenIM) call(ctx context.Context, path string, input, output any) error {
	t, err := im.adminToken(ctx)
	if err != nil {
		return err
	}
	err = im.request(ctx, path, t, input, output)
	var ie *IMError
	if errors.As(err, &ie) && ie.Code >= 1501 && ie.Code <= 1507 {
		im.mu.Lock()
		im.token = ""
		im.mu.Unlock()
	}
	return err
}
func (im *OpenIM) ensureUser(ctx context.Context, id, name string) error {
	var out struct {
		Users []struct {
			ID string `json:"userID"`
		} `json:"usersInfo"`
	}
	if err := im.call(ctx, "/user/get_users_info", map[string]any{"userIDs": []string{id}}, &out); err != nil {
		return err
	}
	for _, u := range out.Users {
		if u.ID == id {
			return nil
		}
	}
	return im.call(ctx, "/user/user_register", map[string]any{"users": []map[string]any{{"userID": id, "nickname": name}}}, nil)
}
func conversationID(a, b string) string {
	ids := []string{a, b}
	sort.Strings(ids)
	return "si_" + strings.Join(ids, "_")
}
func (im *OpenIM) maxSeqs(ctx context.Context, id string) (map[string]int64, error) {
	var out struct {
		Max map[string]int64 `json:"maxSeqs"`
	}
	err := im.call(ctx, "/msg/newest_seq", map[string]string{"userID": id}, &out)
	return out.Max, err
}

type IMMessage struct {
	Seq         int64  `json:"seq"`
	SendID      string `json:"sendID"`
	RecvID      string `json:"recvID"`
	ClientID    string `json:"clientMsgID"`
	Content     string `json:"content"`
	ContentType int    `json:"contentType"`
	Status      int    `json:"status"`
	SendTime    int64  `json:"sendTime"`
}

func (im *OpenIM) pull(ctx context.Context, user, conv string, begin, end int64) ([]IMMessage, error) {
	var out struct {
		Msgs map[string]struct {
			Msgs []IMMessage `json:"Msgs"`
		} `json:"msgs"`
	}
	err := im.call(ctx, "/msg/pull_msg_by_seq", map[string]any{"userID": user, "seqRanges": []map[string]any{{"conversationID": conv, "begin": begin, "end": end, "num": end - begin + 1}}, "order": 1}, &out)
	return out.Msgs[conv].Msgs, err
}

// Use OpenIM custom messages so every attachment remains an authenticated gateway resource.
// No public object URL or administrator token is exposed to the WebView.
type Payload struct {
	Version    int         `json:"v"`
	ID         string      `json:"id"`
	Kind       string      `json:"kind"`
	Text       string      `json:"text,omitempty"`
	Attachment *Attachment `json:"attachment,omitempty"`
}
type Message struct {
	EditVersion int64 `json:"editVersion"`
	EditedAt    int64 `json:"editedAt,omitempty"`
	Payload
	Seq    int64  `json:"seq"`
	Sender string `json:"sender"`
	SentAt int64  `json:"sentAt"`
}

func decodeMessage(m IMMessage) (Message, bool) {
	if m.ContentType != 110 || m.Status >= 4 {
		return Message{}, false
	}
	content := []byte(m.Content)
	if !json.Valid(content) {
		var err error
		content, err = base64.StdEncoding.DecodeString(m.Content)
		if err != nil {
			return Message{}, false
		}
	}
	var custom struct {
		Data string `json:"data"`
	}
	var p Payload
	if json.Unmarshal(content, &custom) != nil || json.Unmarshal([]byte(custom.Data), &p) != nil || p.Version != 1 || p.ID == "" {
		return Message{}, false
	}
	return Message{Payload: p, Seq: m.Seq, Sender: m.SendID, SentAt: m.SendTime}, true
}
func (im *OpenIM) send(ctx context.Context, sender, receiver, name string, p Payload) (string, error) {
	b, _ := json.Marshal(p)
	var out struct {
		ClientID string `json:"clientMsgID"`
	}
	err := im.call(ctx, "/msg/send_msg", map[string]any{"sendID": sender, "recvID": receiver, "senderNickname": name, "senderPlatformID": 2, "contentType": 110, "sessionType": 1, "content": map[string]any{"data": string(b), "description": "support-chat/v1", "extension": ""}, "notOfflinePush": true}, &out)
	if err == nil && out.ClientID == "" {
		err = errors.New("missing OpenIM message ID")
	}
	return out.ClientID, err
}
func (im *OpenIM) delete(ctx context.Context, conv string, seqs []int64) error {
	return im.call(ctx, "/msg/delete_msg_phsical_by_seq", map[string]any{"conversationID": conv, "seqs": seqs}, nil)
}
