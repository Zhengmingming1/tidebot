package webui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/auth"
)

func centerSeen(t *testing.T, c *client) string {
	t.Helper()
	code, out := c.json(http.MethodGet, "/api/notifications/center", nil)
	if code != 200 {
		t.Fatalf("center %d", code)
	}
	v, _ := out["seen_at"].(string)
	return v
}

// TestNotifRead: the read mark is kept by tidebot (shared by every browser and the desktop
// window), only moves forward, is stored in UTC seconds like created_at, is capped at now,
// and refuses bad input, GET and requests without the CSRF token.
func TestNotifRead(t *testing.T) {
	c := login(t)
	if v := centerSeen(t, c); v != "" {
		t.Fatalf("fresh install has a mark: %q", v)
	}
	sh := time.FixedZone("CST", 8*3600)
	mark := time.Date(2025, 10, 31, 8, 0, 0, 500, sh)
	code, out := c.json(http.MethodPost, "/api/notifications/read", map[string]any{"seen_at": mark.Format(time.RFC3339Nano)})
	if code != 200 || out["seen_at"] != "2025-10-31T00:00:00Z" {
		t.Fatalf("mark %d %v", code, out)
	}
	if v := centerSeen(t, c); v != "2025-10-31T00:00:00Z" {
		t.Fatalf("center seen_at %q", v)
	}
	// a stale tab sends an older mark: refused silently, the newer one stays
	code, out = c.json(http.MethodPost, "/api/notifications/read", map[string]any{"seen_at": "2025-10-30T00:00:00Z"})
	if code != 200 || out["seen_at"] != "2025-10-31T00:00:00Z" {
		t.Fatalf("mark moved back %d %v", code, out)
	}
	// a client clock in the future can't silence notices still to come
	code, out = c.json(http.MethodPost, "/api/notifications/read", map[string]any{"seen_at": "2099-01-01T00:00:00Z"})
	got, _ := time.Parse(time.RFC3339, out["seen_at"].(string))
	if code != 200 || got.After(time.Now().Add(time.Second)) || got.Before(time.Now().Add(-time.Minute)) {
		t.Fatalf("future mark not capped %d %v", code, out)
	}
	for _, bad := range []map[string]any{{}, {"seen_at": "yesterday"}, {"seen_at": "2025-10-31' OR 1=1 --"}} {
		if code, _ := c.json(http.MethodPost, "/api/notifications/read", bad); code != 400 {
			t.Fatalf("bad %v accepted: %d", bad, code)
		}
	}
	if code, _ := c.json(http.MethodGet, "/api/notifications/read", nil); code != 405 {
		t.Fatalf("GET %d", code)
	}
	before := centerSeen(t, c)
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/read", bytes.NewReader([]byte(`{"seen_at":"2026-01-01T00:00:00Z"}`)))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	rr := httptest.NewRecorder()
	c.s.routes().ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("csrf %d", rr.Code)
	}
	if v := centerSeen(t, c); v != before {
		t.Fatalf("csrf-less request changed the mark: %q", v)
	}
}

// TestNotifReadUI runs the bell's read-mark logic under node: a newer mark from tidebot is
// adopted, a mark this browser is ahead with is sent once, a failed send retries, and
// "mark all read" never moves the mark back; both languages carry the tooltip.
func TestNotifReadUI(t *testing.T) {
	js, _ := Static.ReadFile("static/app.js")
	src := string(js)
	for _, s := range []string{
		`"/api/notifications/read", { method: "POST", body: { seen_at: v }, read: true }`,
		`window.addEventListener("focus", seenSync)`, `e.key !== "tb_notif_seen"`,
		`id="nc-read" title="' + esc(T("nc.mark_all_tip"))`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js missing %q", s)
		}
	}
	a, b := strings.Index(src, "  function markAllRead("), strings.Index(src, "  function renderNotifCenter(")
	n1, n2 := strings.Index(src, "  function noteNotifs("), strings.Index(src, "  // Red dot on Settings (sidebar)")
	if a < 0 || b < a || n1 < 0 || n2 < n1 {
		t.Fatal("read-mark functions not found")
	}
	if regexpNative.MatchString(src[a:b]) {
		t.Error("read mark must not use native dialogs")
	}
	for _, lang := range []string{"zh-CN", "en"} {
		if strings.TrimSpace(readDict(t, lang)["nc.mark_all_tip"]) == "" {
			t.Errorf("%s missing nc.mark_all_tip", lang)
		}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := `var store = {}, localStorage = { setItem: function (k, v) { store[k] = v; } };
var listeners = {}, window = { addEventListener: function (k, f) { listeners[k] = f; } }, document = { addEventListener: function () {}, hidden: false };
var state = { notifSeen: "", csrf: "x" }, fav = {}, sent = [], fail = false;
function $() { return null; } function $$() { return []; } function paintFavicon() {} function paintChannelDot() {}
function api(p, o) { if (p !== "/api/notifications/read") return Promise.resolve({}); sent.push(o.body.seen_at);
  return fail ? Promise.reject(new Error("down")) : Promise.resolve({ seen_at: o.body.seen_at }); }
` + src[a:b] + src[n1:n2] + `
var bad = function (m) { console.error(m, JSON.stringify({ state: state, sent: sent, store: store })); process.exitCode = 1; };
var tick = function () { return new Promise(function (r) { setTimeout(r, 0); }); };
(async function () {
  // tidebot is ahead (read in the desktop window): adopt, send nothing
  noteNotifs({ items: [{ created_at: "2026-10-31T01:00:00Z", group: "failed" }], seen_at: "2026-10-31T01:00:00Z" });
  if (state.notifSeen !== "2026-10-31T01:00:00Z" || store.tb_notif_seen !== state.notifSeen || sent.length) bad("adopt");
  if (fav.failed !== 0) bad("read failure still counted");
  // this browser is ahead (read before the mark was shared): send once
  state.notifSeen = "2026-10-31T02:00:00Z";
  noteNotifs({ items: [], seen_at: "2026-10-31T01:00:00Z" });
  noteNotifs({ items: [], seen_at: "2026-10-31T01:00:00Z" });
  await tick();
  if (sent.join() !== "2026-10-31T02:00:00Z") bad("push once");
  // a failed send retries on the next refresh
  fail = true; state.notifSeen = "2026-10-31T03:00:00Z"; sent = [];
  noteNotifs({ items: [], seen_at: "" }); await tick(); await tick();
  fail = false; noteNotifs({ items: [], seen_at: "" }); await tick();
  if (sent.length !== 2) bad("retry");
  // mark all read never moves the mark back
  markAllRead([{ created_at: "2026-10-30T00:00:00Z" }]);
  if (state.notifSeen !== "2026-10-31T03:00:00Z") bad("moved back");
  markAllRead([{ created_at: "2026-10-31T04:00:00Z" }]); await tick();
  if (state.notifSeen !== "2026-10-31T04:00:00Z" || sent[sent.length - 1] !== "2026-10-31T04:00:00Z") bad("mark all");
  // another tab of this browser marked read
  listeners.storage({ key: "tb_notif_seen", newValue: "2026-10-31T05:00:00Z" });
  if (state.notifSeen !== "2026-10-31T05:00:00Z") bad("storage");
  listeners.storage({ key: "tb_notif_seen", newValue: "2025-10-31T00:00:00Z" });
  if (state.notifSeen !== "2026-10-31T05:00:00Z") bad("storage moved back");
})();
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("read mark: %v\n%s", err, out)
	}
}
