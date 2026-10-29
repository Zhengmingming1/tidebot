package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"runtime"
	"sort"
	"strings"
)

// Local session origins (CreateLocalSession): the desktop window's in-process bridge and the
// loopback fallback. Only the process that created one knows its id.
const (
	OriginDesktop         = "desktop"
	OriginDesktopLoopback = "desktop-loopback"
)

// ErrCurrentSession: the session list can't end the window it is shown in (use sign out).
var ErrCurrentSession = errors.New("this is the current session")

// ErrNoSession: no live session has that handle.
var ErrNoSession = errors.New("session not found")

// SessionInfo is one signed-in browser or desktop window as shown in Settings › Security.
// Handle stands in for the session id, which never leaves the server.
type SessionInfo struct {
	Handle   string `json:"id"`
	Kind     string `json:"kind"` // "browser" | "desktop"
	Client   string `json:"client"`
	OS       string `json:"os"`
	IP       string `json:"ip"`
	Created  string `json:"created_at"`
	LastSeen string `json:"last_seen_at"`
	Expires  string `json:"expires_at"`
	Current  bool   `json:"current"`
}

// SessionHandle derives the public, stable handle of a session id (not reversible).
func SessionHandle(id string) string {
	h := sha256.Sum256([]byte("tidebot-session-handle\x00" + id))
	return hex.EncodeToString(h[:8])
}

// ParseUA reads a browser name with its major version and the OS name from a User-Agent.
// Only fixed names and digits come out, so nothing the client sends is stored verbatim.
func ParseUA(ua string) (client, osName string) {
	if len(ua) > 512 {
		ua = ua[:512]
	}
	for _, b := range []struct{ tok, name string }{
		{"EdgA/", "Edge"}, {"EdgiOS/", "Edge"}, {"Edg/", "Edge"}, {"OPR/", "Opera"},
		{"SamsungBrowser/", "Samsung Internet"}, {"Vivaldi/", "Vivaldi"}, {"YaBrowser/", "Yandex"},
		{"FxiOS/", "Firefox"}, {"Firefox/", "Firefox"}, {"CriOS/", "Chrome"}, {"Chrome/", "Chrome"},
		{"curl/", "curl"},
	} {
		if i := strings.Index(ua, b.tok); i >= 0 {
			client = withMajor(b.name, ua[i+len(b.tok):])
			break
		}
	}
	if client == "" && strings.Contains(ua, "Safari/") {
		if i := strings.Index(ua, "Version/"); i >= 0 {
			client = withMajor("Safari", ua[i+len("Version/"):])
		} else {
			client = "Safari"
		}
	}
	switch {
	case strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPod"):
		osName = "iOS"
	case strings.Contains(ua, "iPad"):
		osName = "iPadOS"
	case strings.Contains(ua, "Android"):
		osName = "Android"
	case strings.Contains(ua, "CrOS"):
		osName = "ChromeOS"
	case strings.Contains(ua, "Windows"):
		osName = "Windows"
	case strings.Contains(ua, "Macintosh") || strings.Contains(ua, "Mac OS X"):
		osName = "macOS"
	case strings.Contains(ua, "Linux") || strings.Contains(ua, "X11"):
		osName = "Linux"
	}
	return client, osName
}

func withMajor(name, rest string) string {
	n := 0
	for n < len(rest) && n < 4 && rest[n] >= '0' && rest[n] <= '9' {
		n++
	}
	if n == 0 {
		return name
	}
	return name + " " + rest[:n]
}

// HostOS names the OS this process runs on, in the same words as ParseUA.
func HostOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "macOS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	}
	return runtime.GOOS
}

// SetSessionDevice records the browser / OS of a session (empty values are kept empty).
func (s *Service) SetSessionDevice(ctx context.Context, id, client, osName string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE sessions SET client=?, os=? WHERE id=?`, clip(client, 40), clip(osName, 40), id)
	return err
}

func clip(v string, n int) string {
	if len(v) > n {
		return v[:n]
	}
	return v
}

// ListSessions returns the owner's live sessions, current first, then the most recently
// used. Expired rows are dropped on the way (they would never be accepted again).
func (s *Service) ListSessions(ctx context.Context, current string) ([]SessionInfo, error) {
	now := nowUTC()
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at<=?`, now)
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, ip, client, os, created_at, last_seen_at, expires_at FROM sessions
WHERE user_id='owner' AND expires_at>? ORDER BY last_seen_at DESC LIMIT 200`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionInfo{}
	for rows.Next() {
		var id string
		var x SessionInfo
		if err := rows.Scan(&id, &x.IP, &x.Client, &x.OS, &x.Created, &x.LastSeen, &x.Expires); err != nil {
			return nil, err
		}
		x.Handle = SessionHandle(id)
		x.Current = id == current
		x.Kind = "browser"
		if x.IP == OriginDesktop || x.IP == OriginDesktopLoopback {
			x.Kind, x.IP = "desktop", ""
			if x.OS == "" {
				x.OS = HostOS()
			}
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Current && !out[j].Current })
	return out, nil
}

// SignOutSession ends the session behind handle. The current one is refused.
func (s *Service) SignOutSession(ctx context.Context, handle, current string) error {
	if handle == "" || len(handle) > 64 {
		return ErrNoSession
	}
	if SessionHandle(current) == handle {
		return ErrCurrentSession
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM sessions WHERE user_id='owner'`)
	if err != nil {
		return err
	}
	var target string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil && SessionHandle(id) == handle {
			target = id
		}
	}
	rows.Close()
	if target == "" {
		return ErrNoSession
	}
	return s.Logout(ctx, target)
}

// PruneLocalSessions drops the sessions an earlier run of the desktop app left behind: their
// ids lived only in that process, so nothing can use them any more, yet they counted as
// "other sessions". keep is this process's own session.
func (s *Service) PruneLocalSessions(ctx context.Context, origin, keep string) (int, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE ip=? AND id<>?`, origin, keep)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
