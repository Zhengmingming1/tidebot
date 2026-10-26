package app

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/buildinfo"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

// Claude Code status for the sidebar. Everything here is local and cheap:
// a PATH lookup, a cached `claude --version`, a few SQL reads and /proc.
// It never sends a request to a model provider; reachability comes from the
// last explicit "test connection" or the last finished run.

// CC states, in the order the UI cares about them.
const (
	CCReady   = "ready"
	CCRunning = "running"
	CCWarning = "warning"
	CCMissing = "missing"
	CCDown    = "down" // only produced client-side when the API itself is unreachable
)

// CCStatusTTL is how long a computed status is reused.
const CCStatusTTL = 30 * time.Second

type CCSession struct {
	RunID      string `json:"run_id"`
	TaskName   string `json:"task_name"`
	StartedAt  string `json:"started_at"`
	DurationMS int64  `json:"duration_ms"`
	RSSBytes   int64  `json:"rss_bytes"`
}

type CCProvider struct {
	Name      string `json:"name"`
	Model     string `json:"model"`
	Health    string `json:"health"` // ok | failing | unknown
	Source    string `json:"source"` // test | run | ""
	CheckedAt string `json:"checked_at,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	Detail    string `json:"detail,omitempty"`
	KeySet    bool   `json:"key_set"` // an API key/token is configured for it
}

type CCStatus struct {
	State      string      `json:"state"`
	Reasons    []string    `json:"reasons"` // machine keys: version_old, provider_failing, ...
	Found      bool        `json:"found"`
	Path       string      `json:"path,omitempty"`
	Version    string      `json:"version,omitempty"`
	MinVersion string      `json:"min_version"`
	Compatible bool        `json:"compatible"`
	Provider   CCProvider  `json:"provider"`
	Sessions   []CCSession `json:"sessions"`
	TotalRSS   int64       `json:"total_rss_bytes"`
	Core       struct {
		Version   string `json:"version"`
		UptimeSec int64  `json:"uptime_sec"`
		StartedAt string `json:"started_at"`
	} `json:"core"`
	CheckedAt string `json:"checked_at"`
	// How claude was located; Searched lists candidates when it wasn't.
	Method   string   `json:"method"`
	Searched []string `json:"searched,omitempty"`
	Note     string   `json:"note,omitempty"`
	Custom   bool     `json:"custom_path"` // a path was set by hand
	// Sched is the "all schedules paused" state, so the sidebar can show it on every page.
	Sched *SchedPauseView `json:"sched,omitempty"`
}

// CCState maps the facts to one headline state (pure; unit tested).
func CCState(found, compatible bool, running int, providerHealth string) (string, []string) {
	if !found {
		return CCMissing, []string{"not_found"}
	}
	var reasons []string
	if !compatible {
		reasons = append(reasons, "version_old")
	}
	if providerHealth == "failing" {
		reasons = append(reasons, "provider_failing")
	}
	if len(reasons) > 0 {
		return CCWarning, reasons
	}
	if running > 0 {
		return CCRunning, []string{}
	}
	return CCReady, []string{}
}

var providerErrRe = regexp.MustCompile(`(?i)\b(401|403|429|5\d\d)\b|unauthori|authenticat|invalid[ _-]?api[ _-]?key|api key|permission denied.*key|connection refused|econnrefused|enotfound|no such host|overloaded|rate.?limit|certificate`)

// ProviderErrorLike reports whether a run error message looks like the provider's
// fault (auth, network, quota) rather than the task's.
func ProviderErrorLike(msg string) bool { return msg != "" && providerErrRe.MatchString(msg) }

var ccCache struct {
	sync.Mutex
	st CCStatus
	at time.Time
}

// startedAt is set when the package loads in the core process (close enough to process start).
var coreStarted = time.Now()

func (a *App) claudeBin() string {
	if a.Cfg.ClaudeBin != "" {
		return a.Cfg.ClaudeBin
	}
	return "claude"
}

// ClaudeStatus returns the cached status; force recomputes it and re-reads the version.
func (a *App) ClaudeStatus(ctx context.Context, force bool) CCStatus {
	ccCache.Lock()
	defer ccCache.Unlock()
	if !force && time.Since(ccCache.at) < CCStatusTTL && ccCache.st.State != "" {
		st := ccCache.st
		a.fillLive(ctx, &st) // sessions are live and cheap
		return st
	}
	if force {
		claudeVer.Lock()
		claudeVer.at = time.Time{}
		claudeVer.Unlock()
	}
	st := CCStatus{MinVersion: MinClaudeVersion, Sessions: []CCSession{}}
	loc := a.ResolveClaude(ctx, force)
	st.Method, st.Note = loc.Method, loc.Note
	st.Custom = a.configuredClaude(ctx) != "" && a.configuredClaude(ctx) != "claude"
	if loc.Path != "" {
		st.Found, st.Path = true, loc.Path
		st.Version = a.claudeVersion()
	} else {
		st.Searched = loc.Searched
	}
	st.Compatible = ClaudeCompatible(st.Version)
	st.Provider = a.providerHealth(ctx)
	st.Core.Version = buildinfo.Version
	st.Core.StartedAt = coreStarted.UTC().Format(time.RFC3339)
	st.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	ccCache.st, ccCache.at = st, time.Now()
	a.fillLive(ctx, &st)
	return st
}

func (a *App) fillLive(ctx context.Context, st *CCStatus) {
	st.Core.UptimeSec = int64(time.Since(coreStarted).Seconds())
	st.Sessions, st.TotalRSS = []CCSession{}, 0
	for _, p := range runner.ActiveProcs() {
		s := CCSession{RunID: p.RunID, StartedAt: p.Started.UTC().Format(time.RFC3339), DurationMS: time.Since(p.Started).Milliseconds(), RSSBytes: p.RSSBytes}
		_ = a.DB.QueryRowContext(ctx, `SELECT COALESCE(t.name,'') FROM runs r LEFT JOIN tasks t ON t.id=r.task_id WHERE r.id=?`, p.RunID).Scan(&s.TaskName)
		st.Sessions = append(st.Sessions, s)
		st.TotalRSS += p.RSSBytes
	}
	st.State, st.Reasons = CCState(st.Found, st.Compatible, len(st.Sessions), st.Provider.Health)
}

type providerTest struct {
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latency_ms"`
	At        string `json:"at"`
	Status    int    `json:"status,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (a *App) recordProviderTest(ctx context.Context, name string, out map[string]any) {
	t := providerTest{At: time.Now().UTC().Format(time.RFC3339)}
	t.OK, _ = out["ok"].(bool)
	switch v := out["latency_ms"].(type) {
	case int64:
		t.LatencyMS = v
	case float64:
		t.LatencyMS = int64(v)
	}
	if v, ok := out["status"].(int); ok {
		t.Status = v
	}
	if e, ok := out["error"].(string); ok { // already masked by TestProvider
		t.Error = truncateRunes(e, 160)
	}
	b, _ := json.Marshal(t)
	_ = a.Auth.SetSetting(ctx, "provider.last_test."+name, string(b))
	ccCache.Lock()
	ccCache.at = time.Time{}
	ccCache.Unlock()
}

// providerHealth: the newer of the last explicit test and the last finished run.
func (a *App) providerHealth(ctx context.Context) CCProvider {
	name := a.Cfg.DefaultProvider
	out := CCProvider{Name: name, Health: "unknown"}
	if p, ok := a.Registry.Get(name); ok {
		out.Model = p.Model
		if _, err := a.ResolveAPIKey(p); err == nil {
			out.KeySet = true
		}
	}
	var test providerTest
	if raw, _ := a.Auth.GetSetting(ctx, "provider.last_test."+name); raw != "" {
		_ = json.Unmarshal([]byte(raw), &test)
	}
	var rs, rat, rerr string
	_ = a.DB.QueryRowContext(ctx, `SELECT status, COALESCE(finished_at,''), COALESCE(error_message,'') FROM runs
WHERE provider_name=? AND status IN ('succeeded','failed','timeout') ORDER BY COALESCE(finished_at, created_at) DESC LIMIT 1`, name).Scan(&rs, &rat, &rerr)
	if test.At != "" && (rat == "" || test.At >= rat) {
		out.Source, out.CheckedAt, out.LatencyMS = "test", test.At, test.LatencyMS
		out.Health = map[bool]string{true: "ok", false: "failing"}[test.OK]
		out.Detail = test.Error
		return out
	}
	if rat != "" {
		out.Source, out.CheckedAt = "run", rat
		switch {
		case rs == "succeeded":
			out.Health = "ok"
		case ProviderErrorLike(rerr):
			out.Health = "failing"
			out.Detail = truncateRunes(rerr, 160)
		default:
			out.Health = "ok" // the task failed, not the provider
		}
	}
	return out
}

func truncateRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
