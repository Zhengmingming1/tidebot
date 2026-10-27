package notify

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"time"
)

// Delivery is the last send to one channel, for the settings list (healthchecks keeps
// last_notify / last_error on each channel the same way). Times are UTC RFC 3339.
type Delivery struct {
	At     string `json:"at"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	MS     int64  `json:"ms"`
	OKAt   string `json:"ok_at,omitempty"` // last success, also while it is failing now
	Streak int    `json:"streak"`          // failures in a row (0 when the last one worked)
}

var (
	reURL      = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>]+`)
	reBotToken = regexp.MustCompile(`\b\d{6,}:[A-Za-z0-9_-]{20,}\b`)
)

// cleanErr keeps a send error short and free of anything secret: robot URLs carry their
// access token and Telegram puts the bot token in the path, so URLs become their host.
func cleanErr(err error) string {
	if err == nil {
		return ""
	}
	s := reURL.ReplaceAllStringFunc(err.Error(), func(u string) string {
		rest := u[strings.Index(u, "://")+3:]
		if i := strings.IndexAny(rest, "/?#@"); i >= 0 {
			if rest[i] == '@' { // user:pass@host
				rest = rest[i+1:]
				if j := strings.IndexAny(rest, "/?#"); j >= 0 {
					rest = rest[:j]
				}
			} else {
				rest = rest[:i]
			}
		}
		return rest
	})
	s = reBotToken.ReplaceAllString(s, "***")
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

// DownAfter is how many failed sends in a row make a channel "down": one notice when it
// gets there, one when it works again (Uptime Kuma notifies on DOWN / UP flips only, not on
// every failed beat).
const DownAfter = 3

// Health is a channel going down or coming back, for Service.OnHealth.
type Health struct {
	Channel Channel
	Down    bool
	Streak  int    // failures in a row (Down only)
	Err     string // cleaned last error (Down only)
}

// record stores how a send to a saved channel went. Unsaved channels (a form under test)
// have no row and are skipped. real is false for the settings page's test button: a test
// moves the streak but never raises a down / up notice (the user is looking at the result).
func (s *Service) record(ctx context.Context, c Channel, start time.Time, err error, real bool) {
	if c.ID == "" {
		return
	}
	at := start.UTC().Format(time.RFC3339)
	ms := time.Since(start).Milliseconds()
	if err == nil {
		res, _ := s.DB.ExecContext(ctx, `UPDATE notify_channels SET down_notified=0 WHERE id=? AND down_notified=1`, c.ID)
		_, _ = s.DB.ExecContext(ctx, `UPDATE notify_channels SET last_at=?, last_ok=1, last_error='', last_ms=?, last_ok_at=?, fail_streak=0 WHERE id=?`, at, ms, at, c.ID)
		if res != nil && real {
			if n, _ := res.RowsAffected(); n == 1 {
				s.health(Health{Channel: c})
			}
		}
		return
	}
	msg := cleanErr(err)
	var streak, notified int
	if s.DB.QueryRowContext(ctx, `UPDATE notify_channels SET last_at=?, last_ok=0, last_error=?, last_ms=?, fail_streak=fail_streak+1 WHERE id=? RETURNING fail_streak, down_notified`,
		at, msg, ms, c.ID).Scan(&streak, &notified) != nil || !real || streak < DownAfter || notified != 0 {
		return
	}
	// the guard makes two sends failing at once raise one notice
	res, e := s.DB.ExecContext(ctx, `UPDATE notify_channels SET down_notified=1 WHERE id=? AND down_notified=0`, c.ID)
	if e != nil {
		return
	}
	if n, _ := res.RowsAffected(); n == 1 {
		s.health(Health{Channel: c, Down: true, Streak: streak, Err: msg})
	}
}

func (s *Service) health(h Health) {
	if s.OnHealth != nil {
		s.OnHealth(h)
	}
}

// FailingCount is how many enabled channels failed their last send (the settings nav dot).
func (s *Service) FailingCount(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM notify_channels WHERE enabled=1 AND last_at IS NOT NULL AND last_ok=0`).Scan(&n)
	return n, err
}

// resetDelivery forgets the last result once the address, secret or settings changed:
// the old error says nothing about the new setup.
func resetDelivery(ctx context.Context, tx *sql.Tx, chID string) error {
	_, err := tx.ExecContext(ctx, `UPDATE notify_channels SET last_at=NULL, last_ok=0, last_error='', last_ms=0, last_ok_at=NULL, fail_streak=0, down_notified=0 WHERE id=?`, chID)
	return err
}

// deliveries reads the last result of every channel that has one.
func (s *Service) deliveries(ctx context.Context) (map[string]*Delivery, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, last_at, last_ok, last_error, last_ms, COALESCE(last_ok_at,''), fail_streak FROM notify_channels WHERE last_at IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*Delivery{}
	for rows.Next() {
		var cid string
		var ok int
		d := &Delivery{}
		if err := rows.Scan(&cid, &d.At, &ok, &d.Error, &d.MS, &d.OKAt, &d.Streak); err != nil {
			return nil, err
		}
		d.OK = ok == 1
		out[cid] = d
	}
	return out, rows.Err()
}

// sameSetup: a test of a saved channel with nothing changed in the form counts as a real
// delivery (the row's status follows it); a test of edited, unsaved values does not.
func sameSetup(old Channel, in Input) bool {
	if in.URL != "" || in.Secret != "" || in.Kind != old.Kind {
		return false
	}
	for _, k := range publicKeys[old.Kind] {
		if in.Config[k] != old.Config[k] {
			return false
		}
	}
	return true
}
