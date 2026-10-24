package notify

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/id"
)

// EventGroups are the choices the WebUI shows for a channel ("what should come here").
// Each group is stored as one rule per event (task_id ”), so routing stays in targets().
var EventGroups = []struct {
	ID     string
	Events []string
}{
	{"failed", []string{"run_failed"}}, // includes "didn't finish" (same event)
	{"confirm", []string{"approval_needed", "confirm_needed"}},
	{"budget", []string{"budget_breached", "budget_warning"}},
	{"breaker", []string{"breaker_tripped"}},
	{"finished", []string{"run_finished"}}, // only tasks with success notices on
	{"system", []string{"update_available", "update_rolled_back", "disk_low", "plugin_notify"}},
}

// DefaultGroups is what a new channel gets unless the user changes it.
var DefaultGroups = []string{"failed", "confirm", "budget", "breaker", "system"}

// CodeError carries a stable code the WebUI translates (nt.err.<code>).
type CodeError struct{ Code string }

func (e CodeError) Error() string { return "notify: " + e.Code }

// Input is a channel as the WebUI edits it. URL and Secret are write-only: on update an
// empty value keeps what is stored. Config holds only the non-secret fields of the kind.
type Input struct {
	Kind   string            `json:"kind"`
	Name   string            `json:"name"`
	URL    string            `json:"url"`
	Secret string            `json:"secret"`
	Config map[string]string `json:"config"`
	Groups []string          `json:"groups"`
}

// Summary is a channel for the settings page: never the URL (robot URLs carry the
// access token), only its host for the tooltip.
type Summary struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind"`
	Name      string            `json:"name"`
	Enabled   bool              `json:"enabled"`
	Host      string            `json:"host,omitempty"`
	HasURL    bool              `json:"has_url"`
	HasSecret bool              `json:"has_secret"`
	Config    map[string]string `json:"config"`
	Groups    []string          `json:"groups"`
	All       bool              `json:"all"`   // a "*" rule (agentctl): every event
	Tasks     int               `json:"tasks"` // per-task rules, kept as they are
}

// public config keys per kind (everything else is dropped)
var publicKeys = map[string][]string{
	"telegram": {"chat_id", "api_base"},
	"email":    {"host", "port", "from", "to", "username"},
}

func urlKind(kind string) bool { return kind != "telegram" && kind != "email" }

func urlRef(chID string) string    { return "notify." + chID + ".url" }
func secretRef(chID string) string { return "notify." + chID }

func cleanInput(in Input) Input {
	in.Kind = strings.TrimSpace(in.Kind)
	in.Name = strings.TrimSpace(in.Name)
	in.URL = strings.TrimSpace(in.URL)
	in.Secret = strings.TrimSpace(in.Secret)
	cfg := map[string]string{}
	for _, k := range publicKeys[in.Kind] {
		if v := strings.TrimSpace(in.Config[k]); v != "" {
			cfg[k] = v
		}
	}
	in.Config = cfg
	seen := map[string]bool{}
	var gs []string
	for _, g := range in.Groups {
		for _, eg := range EventGroups {
			if eg.ID == g && !seen[g] {
				seen[g] = true
				gs = append(gs, g)
			}
		}
	}
	in.Groups = gs
	return in
}

func checkURL(u string) error {
	pu, err := url.Parse(u)
	if err != nil || (pu.Scheme != "https" && pu.Scheme != "http") || pu.Host == "" {
		return CodeError{"url_bad"}
	}
	return nil
}

// validate checks a cleaned input; has* say whether a stored URL / secret can stand in.
func validate(in Input, hasURL, hasSecret bool) error {
	ok := false
	for _, k := range Kinds {
		ok = ok || k == in.Kind
	}
	if !ok {
		return CodeError{"kind"}
	}
	if in.Name == "" || len([]rune(in.Name)) > 60 {
		return CodeError{"name"}
	}
	if len(in.URL) > 2048 || len(in.Secret) > 4096 {
		return CodeError{"too_long"}
	}
	if urlKind(in.Kind) {
		if in.URL == "" && !hasURL {
			return CodeError{"url_required"}
		}
		if in.URL != "" {
			if err := checkURL(in.URL); err != nil {
				return err
			}
		}
	}
	switch in.Kind {
	case "telegram":
		if in.Config["chat_id"] == "" {
			return CodeError{"chat_id"}
		}
		if in.Secret == "" && !hasSecret {
			return CodeError{"token"}
		}
		if b := in.Config["api_base"]; b != "" {
			if err := checkURL(b); err != nil {
				return err
			}
		}
	case "email":
		if in.Config["host"] == "" || in.Config["from"] == "" || in.Config["to"] == "" {
			return CodeError{"mail"}
		}
		if p := in.Config["port"]; p != "" {
			if n, err := net.LookupPort("tcp", p); err != nil || n <= 0 {
				return CodeError{"port"}
			}
		}
	}
	if len(in.Groups) == 0 {
		return CodeError{"groups"}
	}
	return nil
}

func (s *Service) get(ctx context.Context, chID string) (Channel, error) {
	var c Channel
	var cj string
	var en int
	err := s.DB.QueryRowContext(ctx, `SELECT id, kind, name, config_json, enabled FROM notify_channels WHERE id=?`, chID).Scan(&c.ID, &c.Kind, &c.Name, &cj, &en)
	if err != nil {
		return c, CodeError{"not_found"}
	}
	_ = json.Unmarshal([]byte(cj), &c.Config)
	if c.Config == nil {
		c.Config = map[string]string{}
	}
	c.Enabled = en == 1
	return c, nil
}

// channelURL is where a URL channel posts: the secret store for channels saved from the
// WebUI, config["url"] for older ones.
func (s *Service) channelURL(c Channel) string {
	if ref := c.Config["url_ref"]; ref != "" {
		v, _ := s.Secrets.Get(ref)
		return v
	}
	return c.Config["url"]
}

// Save creates (chID "") or updates a channel and replaces its all-task rules with the
// chosen groups. Per-task rules are left alone.
func (s *Service) Save(ctx context.Context, chID string, in Input) (Channel, error) {
	in = cleanInput(in)
	s.mu.Lock()
	defer s.mu.Unlock()
	var old Channel
	hasURL, hasSecret := false, false
	if chID != "" {
		var err error
		if old, err = s.get(ctx, chID); err != nil {
			return Channel{}, err
		}
		if old.Kind != in.Kind {
			return Channel{}, CodeError{"kind_change"}
		}
		hasURL = s.channelURL(old) != ""
		hasSecret = old.Config["secret_ref"] != ""
	}
	if err := validate(in, hasURL, hasSecret); err != nil {
		return Channel{}, err
	}
	c := Channel{ID: chID, Kind: in.Kind, Name: in.Name, Config: in.Config, Enabled: true}
	if chID == "" {
		c.ID = id.New("nch")
	} else {
		c.Enabled = old.Enabled
		for _, k := range []string{"url_ref", "secret_ref", "url"} {
			if v := old.Config[k]; v != "" {
				c.Config[k] = v
			}
		}
	}
	if urlKind(in.Kind) && in.URL != "" {
		if err := s.Secrets.Set(urlRef(c.ID), in.URL); err != nil {
			return Channel{}, err
		}
		c.Config["url_ref"] = urlRef(c.ID)
		delete(c.Config, "url") // moved out of the database
	}
	if in.Secret != "" {
		if err := s.Secrets.Set(secretRef(c.ID), in.Secret); err != nil {
			return Channel{}, err
		}
		c.Config["secret_ref"] = secretRef(c.ID)
	}
	b, _ := json.Marshal(c.Config)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Channel{}, err
	}
	defer tx.Rollback()
	if chID == "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO notify_channels(id, kind, name, config_json, enabled, created_at) VALUES(?,?,?,?,1,?)`, c.ID, c.Kind, c.Name, string(b), nowUTC())
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE notify_channels SET name=?, config_json=? WHERE id=?`, c.Name, string(b), c.ID)
	}
	if err != nil {
		return Channel{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM notify_rules WHERE channel_id=? AND task_id=''`, c.ID); err != nil {
		return Channel{}, err
	}
	for _, g := range in.Groups {
		for _, eg := range EventGroups {
			if eg.ID != g {
				continue
			}
			for _, ev := range eg.Events {
				if _, err = tx.ExecContext(ctx, `INSERT INTO notify_rules(id, event, task_id, channel_id, created_at) VALUES(?,?,?,?,?)`, id.New("nru"), ev, "", c.ID, nowUTC()); err != nil {
					return Channel{}, err
				}
			}
		}
	}
	return c, tx.Commit()
}

// SetEnabled turns a channel on or off without touching its rules.
func (s *Service) SetEnabled(ctx context.Context, chID string, on bool) error {
	v := 0
	if on {
		v = 1
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE notify_channels SET enabled=? WHERE id=?`, v, chID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return CodeError{"not_found"}
	}
	return nil
}

// Test sends a test message with what is in the form, before or without saving
// (Uptime Kuma's "Test" button). Blank URL / secret fall back to the stored channel.
func (s *Service) Test(ctx context.Context, chID string, in Input, m Message) error {
	in = cleanInput(in)
	if len(in.Groups) == 0 {
		in.Groups = []string{"failed"} // not relevant for a test
	}
	c := Channel{ID: chID, Kind: in.Kind, Name: in.Name, Config: in.Config}
	hasURL, hasSecret := false, false
	var old Channel
	if chID != "" {
		var err error
		if old, err = s.get(ctx, chID); err != nil {
			return err
		}
		hasURL, hasSecret = s.channelURL(old) != "", old.Config["secret_ref"] != ""
	}
	if err := validate(in, hasURL, hasSecret); err != nil {
		return err
	}
	u, sec := in.URL, in.Secret
	if u == "" && hasURL {
		u = s.channelURL(old)
	}
	if sec == "" && hasSecret {
		sec = s.secret(old)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return s.deliver(ctx, c, m, u, sec)
}

// Summaries lists channels for the settings page (no URLs, no secrets).
func (s *Service) Summaries(ctx context.Context) ([]Summary, error) {
	chs, err := s.Channels(ctx)
	if err != nil {
		return nil, err
	}
	type rr struct{ ev, task string }
	rules := map[string][]rr{}
	rows, err := s.DB.QueryContext(ctx, `SELECT channel_id, event, task_id FROM notify_rules`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ch string
		var r rr
		if rows.Scan(&ch, &r.ev, &r.task) == nil {
			rules[ch] = append(rules[ch], r)
		}
	}
	rows.Close()
	out := []Summary{}
	for _, c := range chs {
		x := Summary{ID: c.ID, Kind: c.Kind, Name: c.Name, Enabled: c.Enabled, Config: map[string]string{}, Groups: []string{},
			HasSecret: c.Config["secret_ref"] != ""}
		for _, k := range publicKeys[c.Kind] {
			if v := c.Config[k]; v != "" {
				x.Config[k] = v
			}
		}
		if urlKind(c.Kind) {
			if u := s.channelURL(c); u != "" {
				x.HasURL = true
				if pu, err := url.Parse(u); err == nil {
					x.Host = pu.Hostname()
				}
			}
		}
		evs := map[string]bool{}
		tasks := map[string]bool{}
		for _, r := range rules[c.ID] {
			if r.task != "" {
				tasks[r.task] = true
				continue
			}
			evs[r.ev] = true
			x.All = x.All || r.ev == "*"
		}
		x.Tasks = len(tasks)
		for _, eg := range EventGroups {
			on := x.All
			for _, ev := range eg.Events {
				on = on || evs[ev]
			}
			if on {
				x.Groups = append(x.Groups, eg.ID)
			}
		}
		out = append(out, x)
	}
	return out, nil
}

// sendMail speaks implicit TLS on 465 (what most Chinese and webmail providers list) and
// SMTP + STARTTLS (when offered) on other ports.
func sendMail(cfg map[string]string, password string, m Message) error {
	host, port, from, to := cfg["host"], cfg["port"], cfg["from"], cfg["to"]
	if host == "" || from == "" || to == "" {
		return errors.New("email needs host, from, to")
	}
	if port == "" {
		port = "587"
	}
	var auth smtp.Auth
	if u := cfg["username"]; u != "" {
		auth = smtp.PlainAuth("", u, password, host)
	}
	rcpt := splitAddrs(to)
	msg := BuildMail(from, to, m)
	env := from // envelope sender: the bare address of "Name <me@x.com>"
	if a, err := mail.ParseAddress(from); err == nil {
		env = a.Address
	}
	if port != "465" {
		return smtp.SendMail(net.JoinHostPort(host, port), auth, env, rcpt, msg)
	}
	d := &net.Dialer{Timeout: 15 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", net.JoinHostPort(host, port), &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(env); err != nil {
		return err
	}
	for _, r := range rcpt {
		if err := c.Rcpt(r); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func splitAddrs(to string) []string {
	var out []string
	for _, a := range strings.FieldsFunc(to, func(r rune) bool { return r == ',' || r == ';' }) {
		if a = strings.TrimSpace(a); a != "" {
			if p, err := mail.ParseAddress(a); err == nil {
				a = p.Address
			}
			out = append(out, a)
		}
	}
	return out
}
