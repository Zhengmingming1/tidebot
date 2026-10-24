// Package notify delivers user-facing events to channels (final-plan §13): email (SMTP),
// generic webhook, Feishu, DingTalk, WeCom and Telegram bots, with per-event/per-task rules,
// quiet hours and a daily digest for low-priority events. The desktop shell shows its own
// native notifications from the in-process event bus.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/netproxy"

	"github.com/Zhengmingming1/tidebot/internal/id"
	"github.com/Zhengmingming1/tidebot/internal/secrets"
)

// Message is one notification.
type Message struct {
	Event  string `json:"event"`
	TaskID string `json:"task_id,omitempty"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Urgent bool   `json:"urgent"`
	// Key + Params let clients render the message in their own language.
	Key    string            `json:"key,omitempty"`
	Params map[string]string `json:"params,omitempty"`
}

// Channel configuration (config_json). Secrets are referenced by name, never stored inline.
type Channel struct {
	ID      string            `json:"id"`
	Kind    string            `json:"kind"`
	Name    string            `json:"name"`
	Config  map[string]string `json:"config"`
	Enabled bool              `json:"enabled"`
}

// Kinds supported by Send.
var Kinds = []string{"email", "webhook", "feishu", "dingtalk", "wecom", "telegram"}

// LowPriority events go to the daily digest unless a rule targets them explicitly.
var LowPriority = map[string]bool{"run_finished": true}

// Service persists channels/rules and delivers messages.
type Service struct {
	DB      *sql.DB
	Secrets secrets.Backend
	HTTP    *http.Client
	Loc     *time.Location
	Now     func() time.Time
	mu      sync.Mutex
}

// New creates a Service.
func New(db *sql.DB, sec secrets.Backend) *Service {
	return &Service{DB: db, Secrets: sec, HTTP: netproxy.Client(15 * time.Second), Loc: time.Local, Now: time.Now}
}

// AddChannel validates and stores a channel. secretValue (optional) is stored in the secret
// backend under notify.<id> and referenced from config as secret_ref.
func (s *Service) AddChannel(ctx context.Context, kind, name string, cfg map[string]string, secretValue string) (Channel, error) {
	ok := false
	for _, k := range Kinds {
		ok = ok || k == kind
	}
	if !ok {
		return Channel{}, fmt.Errorf("unknown channel kind %q", kind)
	}
	if cfg == nil {
		cfg = map[string]string{}
	}
	for k := range cfg {
		if strings.Contains(strings.ToLower(k), "token") || strings.Contains(strings.ToLower(k), "password") {
			return Channel{}, errors.New("put tokens/passwords in the secret field, not in config")
		}
	}
	if u := cfg["url"]; u != "" {
		pu, err := url.Parse(u)
		if err != nil || (pu.Scheme != "https" && pu.Scheme != "http") {
			return Channel{}, errors.New("url must be http(s)")
		}
	}
	c := Channel{ID: id.New("nch"), Kind: kind, Name: name, Config: cfg, Enabled: true}
	if secretValue != "" {
		ref := "notify." + c.ID
		if err := s.Secrets.Set(ref, secretValue); err != nil {
			return Channel{}, err
		}
		c.Config["secret_ref"] = ref
	}
	b, _ := json.Marshal(c.Config)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO notify_channels(id, kind, name, config_json, enabled, created_at) VALUES(?,?,?,?,1,?)`,
		c.ID, kind, name, string(b), nowUTC())
	return c, err
}

// RemoveChannel deletes a channel, its rules and its secret.
func (s *Service) RemoveChannel(ctx context.Context, chID string) error {
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM notify_rules WHERE channel_id=?`, chID)
	_ = s.Secrets.Delete(secretRef(chID))
	_ = s.Secrets.Delete(urlRef(chID))
	_, err := s.DB.ExecContext(ctx, `DELETE FROM notify_channels WHERE id=?`, chID)
	return err
}

// Channels lists channels (config without secrets).
func (s *Service) Channels(ctx context.Context) ([]Channel, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, kind, name, config_json, enabled FROM notify_channels ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Channel
	for rows.Next() {
		var c Channel
		var cj string
		var en int
		if err := rows.Scan(&c.ID, &c.Kind, &c.Name, &cj, &en); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(cj), &c.Config)
		c.Enabled = en == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

// AddRule routes event ("*" = all) for taskID ("" = all tasks) to a channel.
func (s *Service) AddRule(ctx context.Context, event, taskID, channelID string) (string, error) {
	rid := id.New("nru")
	_, err := s.DB.ExecContext(ctx, `INSERT INTO notify_rules(id, event, task_id, channel_id, created_at) VALUES(?,?,?,?,?)`, rid, event, taskID, channelID, nowUTC())
	return rid, err
}

// targets returns channels matching the message; explicit marks a rule naming the event.
func (s *Service) targets(ctx context.Context, m Message) (chs []Channel, explicit bool, err error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT c.id, c.kind, c.name, c.config_json, r.event FROM notify_rules r
JOIN notify_channels c ON c.id=r.channel_id
WHERE c.enabled=1 AND (r.event=? OR r.event='*') AND (r.task_id='' OR r.task_id=?)`, m.Event, m.TaskID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var c Channel
		var cj, ev string
		if err := rows.Scan(&c.ID, &c.Kind, &c.Name, &cj, &ev); err != nil {
			return nil, false, err
		}
		explicit = explicit || ev == m.Event
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		_ = json.Unmarshal([]byte(cj), &c.Config)
		chs = append(chs, c)
	}
	return chs, explicit, rows.Err()
}

// Quiet hours: setting value "22:00-08:00" in Service.Quiet (empty = none).
func inQuiet(spec string, t time.Time) bool {
	parts := strings.Split(spec, "-")
	if len(parts) != 2 {
		return false
	}
	a, e1 := time.Parse("15:04", strings.TrimSpace(parts[0]))
	b, e2 := time.Parse("15:04", strings.TrimSpace(parts[1]))
	if e1 != nil || e2 != nil {
		return false
	}
	m := t.Hour()*60 + t.Minute()
	am, bm := a.Hour()*60+a.Minute(), b.Hour()*60+b.Minute()
	if am <= bm {
		return m >= am && m < bm
	}
	return m >= am || m < bm
}

// Dispatch records the message and delivers it now, or queues it for the digest when it is
// low priority (without an explicit rule) or quiet hours are on and it is not urgent.
func (s *Service) Dispatch(ctx context.Context, m Message, quiet string) (status string, err error) {
	chs, explicit, err := s.targets(ctx, m)
	if err != nil {
		return "", err
	}
	status = "logged"
	switch {
	case len(chs) == 0:
	case (LowPriority[m.Event] && !explicit) || (!m.Urgent && inQuiet(quiet, s.Now().In(s.Loc))):
		status = "queued"
	default:
		status = "delivered"
		var errs []string
		for _, c := range chs {
			if e := s.Send(ctx, c, m); e != nil {
				errs = append(errs, c.Name+": "+e.Error())
			}
		}
		if len(errs) > 0 {
			status, err = "failed", errors.New(strings.Join(errs, "; "))
		}
	}
	body := m.Title
	if m.Body != "" {
		body += "\n" + m.Body
	}
	var delivered any
	if status == "delivered" {
		delivered = nowUTC()
	}
	params := "{}"
	if len(m.Params) > 0 {
		if b, err := json.Marshal(m.Params); err == nil {
			params = string(b)
		}
	}
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO notifications(id, event, ref, body, status, created_at, delivered_at, msg_key, msg_params) VALUES(?,?,?,?,?,?,?,?,?)`,
		id.New("ntf"), m.Event, m.TaskID, body, status, nowUTC(), delivered, m.Key, params)
	return status, err
}

// FlushDigest sends all queued notifications as one message per channel-set and marks them.
func (s *Service) FlushDigest(ctx context.Context) (int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, event, COALESCE(ref,''), body FROM notifications WHERE status='queued' ORDER BY created_at`)
	if err != nil {
		return 0, err
	}
	type q struct{ id, event, ref, body string }
	var items []q
	for rows.Next() {
		var x q
		if err := rows.Scan(&x.id, &x.event, &x.ref, &x.body); err == nil {
			items = append(items, x)
		}
	}
	rows.Close()
	if len(items) == 0 {
		return 0, nil
	}
	byCh := map[string][]string{}
	chByID := map[string]Channel{}
	for _, it := range items {
		chs, _, _ := s.targets(ctx, Message{Event: it.event, TaskID: it.ref})
		for _, c := range chs {
			chByID[c.ID] = c
			byCh[c.ID] = append(byCh[c.ID], "• "+strings.ReplaceAll(it.body, "\n", " — "))
		}
	}
	var errs []string
	for cid, lines := range byCh {
		m := Message{Event: "digest", Title: fmt.Sprintf("Tidebot digest (%d)", len(lines)), Body: strings.Join(lines, "\n")}
		if err := s.Send(ctx, chByID[cid], m); err != nil {
			errs = append(errs, err.Error())
		}
	}
	for _, it := range items {
		_, _ = s.DB.ExecContext(ctx, `UPDATE notifications SET status='digested', delivered_at=? WHERE id=?`, nowUTC(), it.id)
	}
	if len(errs) > 0 {
		return len(items), errors.New(strings.Join(errs, "; "))
	}
	return len(items), nil
}

func (s *Service) secret(c Channel) string {
	if ref := c.Config["secret_ref"]; ref != "" {
		v, _ := s.Secrets.Get(ref)
		return v
	}
	return ""
}

// Send delivers one message to one channel.
func (s *Service) Send(ctx context.Context, c Channel, m Message) error {
	return s.deliver(ctx, c, m, s.channelURL(c), s.secret(c))
}

// deliver sends with an explicit URL and secret (the stored ones, or a form under test).
func (s *Service) deliver(ctx context.Context, c Channel, m Message, u, sec string) error {
	text := m.Title
	if m.Body != "" {
		text += "\n" + m.Body
	}
	switch c.Kind {
	case "webhook":
		return s.postJSON(ctx, u, m, sec, "")
	case "feishu":
		body := map[string]any{"msg_type": "text", "content": map[string]string{"text": text}}
		if sec != "" { // "signature verification" on the bot
			ts, sign := feishuSign(s.Now(), sec)
			body["timestamp"], body["sign"] = ts, sign
		}
		return s.postJSON(ctx, u, body, "", c.Kind)
	case "dingtalk", "wecom":
		if c.Kind == "dingtalk" && sec != "" { // "加签" on the robot
			u = dingtalkSign(u, s.Now(), sec)
		}
		return s.postJSON(ctx, u, map[string]any{"msgtype": "text", "text": map[string]string{"content": text}}, "", c.Kind)
	case "telegram":
		tok := sec
		if tok == "" || c.Config["chat_id"] == "" {
			return errors.New("telegram needs a bot token secret and chat_id")
		}
		base := c.Config["api_base"]
		if base == "" {
			base = "https://api.telegram.org"
		}
		return s.postJSON(ctx, base+"/bot"+tok+"/sendMessage", map[string]string{"chat_id": c.Config["chat_id"], "text": text}, "", c.Kind)
	case "email":
		return sendMail(c.Config, sec, m)
	}
	return fmt.Errorf("unsupported channel %q", c.Kind)
}

// postJSON posts payload to u. For the chat robots (kind != "") a 200 answer can still be a
// refusal (DingTalk keyword / IP checks, WeCom bad key, Feishu signature, Telegram chat),
// so their JSON reply is checked too.
func (s *Service) postJSON(ctx context.Context, u string, payload any, bearer, kind string) error {
	if u == "" {
		return errors.New("missing url")
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		// never echo the URL: Telegram puts the token in it
		return errors.New("request failed")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err := robotReply(kind, body); err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	return nil
}

// robotReply turns a robot's "200 but refused" JSON into an error.
func robotReply(kind string, body []byte) error {
	if kind == "" || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	var r struct {
		ErrCode     *int   `json:"errcode"` // DingTalk, WeCom
		ErrMsg      string `json:"errmsg"`
		Code        *int   `json:"code"` // Feishu / Lark
		Msg         string `json:"msg"`
		OK          *bool  `json:"ok"` // Telegram
		Description string `json:"description"`
		StatusCode  *int   `json:"StatusCode"` // older Feishu bots
		StatusMsg   string `json:"StatusMessage"`
	}
	if json.Unmarshal(body, &r) != nil {
		return nil
	}
	short := func(s string) string {
		s = strings.TrimSpace(s)
		if len([]rune(s)) > 160 {
			s = string([]rune(s)[:160]) + "…"
		}
		return s
	}
	switch {
	case r.ErrCode != nil && *r.ErrCode != 0:
		return fmt.Errorf("errcode %d: %s", *r.ErrCode, short(r.ErrMsg))
	case r.Code != nil && *r.Code != 0:
		return fmt.Errorf("code %d: %s", *r.Code, short(r.Msg))
	case r.StatusCode != nil && *r.StatusCode != 0:
		return fmt.Errorf("code %d: %s", *r.StatusCode, short(r.StatusMsg))
	case r.OK != nil && !*r.OK:
		return fmt.Errorf("telegram: %s", short(r.Description))
	}
	return nil
}

// dingtalkSign appends timestamp + sign (HMAC-SHA256 of "ms\nsecret", base64) to the URL.
func dingtalkSign(u string, now time.Time, secret string) string {
	ts := strconv.FormatInt(now.UnixMilli(), 10)
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(ts + "\n" + secret))
	sep := "&"
	if !strings.Contains(u, "?") {
		sep = "?"
	}
	return u + sep + "timestamp=" + ts + "&sign=" + url.QueryEscape(base64.StdEncoding.EncodeToString(h.Sum(nil)))
}

// feishuSign: key "seconds\nsecret", empty message, base64.
func feishuSign(now time.Time, secret string) (string, string) {
	ts := strconv.FormatInt(now.Unix(), 10)
	h := hmac.New(sha256.New, []byte(ts+"\n"+secret))
	return ts, base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// BuildMail renders an RFC 5322 message (exported for tests).
func BuildMail(from, to string, m Message) []byte {
	clean := func(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n",
		clean(from), clean(to), clean("[Tidebot] "+m.Title), m.Body)
	return []byte(b.String())
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }
