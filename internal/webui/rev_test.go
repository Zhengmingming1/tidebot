package webui

import (
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func revChanges(t *testing.T, c *client) string {
	t.Helper()
	code, out := c.json(http.MethodGet, "/api/rev", nil)
	rev, _ := out["rev"].(string)
	changes, _, ok := strings.Cut(rev, ".")
	if code != 200 || !ok || changes == "" {
		t.Fatalf("rev %d %v", code, out)
	}
	return changes
}

// TestRevAPI: reads (the home page's polls included) leave the token alone; any write moves it.
func TestRevAPI(t *testing.T) {
	c := login(t)
	c.json(http.MethodGet, "/api/me", nil) // first authed request may refresh last_seen_at
	before := revChanges(t, c)
	for i := 0; i < 3; i++ {
		for _, p := range []string{"/api/overview", "/api/dashboard", "/api/runs", "/api/notifications/center", "/api/rev"} {
			if rr := c.do(http.MethodGet, p, "", nil); rr.Code != 200 {
				t.Fatalf("%s %d", p, rr.Code)
			}
		}
	}
	if got := revChanges(t, c); got != before {
		t.Fatalf("reads moved the token: %s -> %s", before, got)
	}
	if code, out := c.json(http.MethodPut, "/api/settings/language", map[string]string{"language": "zh-CN"}); code != 200 {
		t.Fatalf("write %d %v", code, out)
	}
	if got := revChanges(t, c); got == before {
		t.Fatal("a write did not move the token")
	}
	if code, _ := c.json(http.MethodPost, "/api/rev", nil); code != 405 {
		t.Fatalf("post %d", code)
	}
	// no session → 401, like the rest of /api/
	if rr := (&client{t: t, s: c.s}).do(http.MethodGet, "/api/rev", "", nil); rr.Code != 401 {
		t.Fatalf("anon %d", rr.Code)
	}
}

// TestRevGate: live pages only refetch when the token moved, retry after a failed reload,
// and poll less often when nothing runs and nobody is using the page.
func TestRevGate(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n") // Windows checkouts may use CRLF
	for _, want := range []string{
		`if (ticks % liveEvery(!!$("#running .spin")) === 0) {`,
		`return gate(function () { return Promise.all([api("/api/overview"), api("/api/dashboard")])`,
		`livePoll(function () { return gate(reload); }, 2000)`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	gate := regexp.MustCompile(`(?s)  function revGate\(\) \{.*?\n  \}\n`).FindString(src)
	every := regexp.MustCompile(`  function liveEvery\(busy\) \{[^\n]*\}\n`).FindString(src)
	if gate == "" || every == "" {
		t.Fatal("revGate/liveEvery not found")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := `var rev="1.100", calls=0, fail=false, state={lastInput:Date.now()};
function apiRaw(p){ if(p!=="/api/rev") throw new Error(p); return Promise.resolve({rev:rev}); }
` + gate + every + `
var g=revGate(), bad=[];
function load(){ calls++; return fail ? Promise.reject(new Error("x")) : Promise.resolve(); }
(async function(){
  await g(load); if(calls!==1) bad.push("first poll must load: "+calls);
  await g(load); await g(load); if(calls!==1) bad.push("unchanged token reloaded: "+calls);
  rev="2.100"; fail=true; try{ await g(load); }catch(e){} if(calls!==2) bad.push("changed token: "+calls);
  fail=false; await g(load); if(calls!==3) bad.push("failed reload not retried: "+calls);
  await g(load); if(calls!==3) bad.push("reloaded after success: "+calls);
  rev="2.101"; await g(load); if(calls!==4) bad.push("minute turnover: "+calls);
  if(liveEvery(true)!==2||liveEvery(false)!==5) bad.push("active cadence");
  state.lastInput=Date.now()-300000; if(liveEvery(false)!==15||liveEvery(true)!==2) bad.push("idle cadence");
  if(bad.length){console.error(bad.join("\n"));process.exitCode=1;}
})();
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("revGate: %v\n%s", err, out)
	}
}
