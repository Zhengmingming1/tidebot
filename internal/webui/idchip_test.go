package webui

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestIDChip: ids moved out of page rows into an escaped copy chip; the schedule row
// shows only "next run" and keeps cron/time zone/later runs in its tooltip.
func TestIDChip(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		`taskPill(t.status) + idChip(T("td.task_id"), t.id)`,
		`idChip(T("sk.id"), s.id)`,
		`idChip(T("rd.run_id"), r.id)`,
		`T("td.sched_tip"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	if strings.Contains(src, `esc(T("td.task_id")) + "</dt>`) {
		t.Error("task id is back as a page row")
	}
	fn := regexp.MustCompile(`function idChip\(label, id\) \{.*\}\n`).FindString(src)
	if fn == "" {
		t.Fatal("idChip not found")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := `function esc(s){return String(s).replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/"/g,"&quot;");}
function T(k,v){return k+":"+(v.l||"")+(v.id||"");}
` + fn + `
var h = idChip("Task", 'x"><img src=1>');
if (h.indexOf("<img") >= 0 || h.indexOf('data-copyid="x&quot;>&lt;img') < 0) { console.error(h); process.exitCode = 1; }
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("idChip: %v\n%s", err, out)
	}
}
