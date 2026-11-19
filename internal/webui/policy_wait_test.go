package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestPolicyWaitAPI: GET /api/tasks and GET /api/tasks/:id carry policy_wait
// while a scheduled run waits, and omit it when the task is not waiting.
func TestPolicyWaitAPI(t *testing.T) {
	c := login(t)
	_, created := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "Nightly", "prompt": "check", "provider": "anthropic", "cron": "0 2 * * *", "enabled": true})
	id := created["id"].(string)
	_, other := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "Other", "prompt": "check", "provider": "anthropic", "cron": "0 3 * * *", "enabled": true})
	oid := other["id"].(string)

	var mu sync.Mutex
	reason := ""
	c.s.deps.PolicyWaits = func() map[string]string {
		mu.Lock()
		defer mu.Unlock()
		if reason == "" {
			return nil
		}
		return map[string]string{id: reason}
	}

	_, d := c.json(http.MethodGet, "/api/tasks/"+id, nil)
	if d["policy_wait"] != nil {
		t.Fatalf("idle detail %v", d)
	}
	mu.Lock()
	reason = "dir_busy"
	mu.Unlock()
	_, d = c.json(http.MethodGet, "/api/tasks/"+id, nil)
	if d["policy_wait"] != "dir_busy" {
		t.Fatalf("dir_busy detail %v", d)
	}
	mu.Lock()
	reason = "accounting_pending"
	mu.Unlock()
	_, d = c.json(http.MethodGet, "/api/tasks/"+id, nil)
	if d["policy_wait"] != "accounting_pending" {
		t.Fatalf("ledger detail %v", d)
	}

	var list []struct {
		ID         string `json:"id"`
		PolicyWait string `json:"policy_wait"`
	}
	rec := c.do(http.MethodGet, "/api/tasks", "", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	found, stray := "", ""
	for _, r := range list {
		if r.ID == id {
			found = r.PolicyWait
		}
		if r.ID == oid && r.PolicyWait != "" {
			stray = r.PolicyWait
		}
	}
	if found != "accounting_pending" || stray != "" {
		t.Fatalf("list wait %q stray %q", found, stray)
	}

	mu.Lock()
	reason = ""
	mu.Unlock()
	_, d = c.json(http.MethodGet, "/api/tasks/"+id, nil)
	if d["policy_wait"] != nil {
		t.Fatalf("cleared detail %v", d)
	}
}

// TestPolicyWaitRev: a scheduled wait moves /api/rev and the change stream
// without a database write, and clearing it moves them back.
func TestPolicyWaitRev(t *testing.T) {
	shortEvents(t, 20*time.Millisecond, time.Hour)
	c := login(t)
	c.json(http.MethodGet, "/api/me", nil)
	var mu sync.Mutex
	waits := map[string]string{}
	c.s.deps.PolicyWaits = func() map[string]string {
		mu.Lock()
		defer mu.Unlock()
		if len(waits) == 0 {
			return nil
		}
		out := make(map[string]string, len(waits))
		for k, v := range waits {
			out[k] = v
		}
		return out
	}

	_, before := c.json(http.MethodGet, "/api/rev", nil)
	idle, _ := before["rev"].(string)
	if idle == "" || strings.Count(idle, ".") != 1 {
		t.Fatalf("idle rev %q", idle)
	}

	ts := httptest.NewServer(c.s.routes())
	t.Cleanup(ts.Close)
	es, code := openEvents(t, ts.URL, c.sid)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	first, ok := es.next(3 * time.Second)
	if !ok || first.Rev == "" {
		t.Fatalf("first %+v %v", first, ok)
	}

	mu.Lock()
	waits["task_1"] = "dir_busy"
	mu.Unlock()
	st := es.wait("policy wait", func(s evState) bool { return s.Rev != first.Rev })
	_, waiting := c.json(http.MethodGet, "/api/rev", nil)
	got, _ := waiting["rev"].(string)
	if got == idle || !strings.HasPrefix(got, strings.Split(idle, ".")[0]+".") || strings.Count(got, ".") != 2 {
		t.Fatalf("waiting rev %q idle %q event %q", got, idle, st.Rev)
	}

	mu.Lock()
	delete(waits, "task_1")
	mu.Unlock()
	es.wait("wait cleared", func(s evState) bool {
		_, rest, ok := strings.Cut(s.Rev, ".")
		return ok && !strings.Contains(rest, ".")
	})
	_, after := c.json(http.MethodGet, "/api/rev", nil)
	cleared, _ := after["rev"].(string)
	if strings.Count(cleared, ".") != 1 {
		t.Fatalf("cleared rev %q", cleared)
	}
}

func TestPolicyWaitUI(t *testing.T) {
	src := strings.ReplaceAll(appJSSource(t), "\r\n", "\n")
	for _, want := range []string{
		`var POLICY_WAITS = ["dir_busy", "accounting_pending"];`,
		`function policyWaitPill(t, small) {`,
		`taskPill(t.status, t) + policyWaitPill(t) + idChip(T("td.task_id"), t.id)`,
		`"</td><td>" + taskPill(t.status, t) + policyWaitPill(t, true) + `,
		`taskPill(t.status, t, true) + policyWaitPill(t, true)`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	i := strings.Index(src, "var POLICY_WAITS = ")
	j := strings.Index(src, "function pauseNotice(")
	if i < 0 || j < i {
		t.Fatal("policy wait block not found")
	}
	block := src[i:j]
	for _, bad := range []string{"confirm(", "alert(", "prompt(", "innerHTML"} {
		if strings.Contains(block, bad) {
			t.Errorf("policy wait block uses %s", bad)
		}
	}
	for _, want := range []string{`esc(label)`, `esc(tip)`} {
		if !strings.Contains(block, want) {
			t.Errorf("policy wait block lacks %s", want)
		}
	}
	css := cssSource(t)
	if !strings.Contains(css, ".pill.pw-pill {") {
		t.Errorf("styles.css lacks .pill.pw-pill")
	}
	keys := []string{"td.wait_dir_busy", "td.wait_accounting_pending", "td.wait_dir_busy_tip", "td.wait_accounting_pending_tip"}
	for _, lang := range []string{"zh-CN", "en"} {
		raw, err := os.ReadFile("static/i18n/" + lang + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range keys {
			if m[k] == "" {
				t.Errorf("%s lacks %s", lang, k)
			}
		}
	}

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	script := `
function T(k){ return k === "td.wait_dir_busy" ? "等目录空出" : k === "td.wait_accounting_pending" ? "等记账写入" : k === "td.wait_dir_busy_tip" ? "dir tip <x>" : k === "td.wait_accounting_pending_tip" ? "ledger tip" : k; }
function esc(s){ return String(s).replace(/[&<>"]/g, function(c){ return ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;"})[c]; }); }
` + block + `
var bad = [];
if (policyWaitPill({status:"active"}) !== "") bad.push("idle");
if (policyWaitPill({status:"active", policy_wait:"nope"}) !== "") bad.push("unknown reason");
var p = policyWaitPill({status:"active", policy_wait:"dir_busy"});
if (p.indexOf("等目录空出") < 0 || p.indexOf("pw-pill") < 0 || p.indexOf("dir tip &lt;x&gt;") < 0) bad.push("dir pill: " + p);
var q = policyWaitPill({status:"active", policy_wait:"accounting_pending"}, true);
if (q.indexOf("等记账写入") < 0 || q.indexOf(" sm") < 0 || q.indexOf("ledger tip") < 0) bad.push("ledger pill: " + q);
if (policyWaitPill({status:"active", policy_wait:"<script>"}) !== "") bad.push("raw value leaked");
console.log(JSON.stringify(bad));
`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var fails []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &fails); err != nil {
		t.Fatalf("node output %s", out)
	}
	for _, f := range fails {
		t.Error(f)
	}
}
