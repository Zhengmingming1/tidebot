package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/auth"
	"github.com/Zhengmingming1/tidebot/internal/db"
)

// Queued runs carry their place in line, the "running" filter includes them, and
// POST /api/runs/<id>/stop stops queued or running runs (409 once finished, 404 unknown,
// CSRF required). A start answer for a run that has to wait says where it is in line.
func TestStopRunAndQueuePos(t *testing.T) {
	c := login(t)
	now := time.Now()
	seedRun(t, c, "run_q", "wait for me", "succeeded", 0, now)
	seedRun(t, c, "run_r", "busy", "succeeded", 0, now.Add(-time.Minute))
	seedRun(t, c, "run_done", "old", "succeeded", 0.01, now.Add(-time.Hour))
	if _, err := c.s.deps.DB.Exec(`UPDATE runs SET status='queued', started_at=NULL, finished_at=NULL WHERE id='run_q'`); err != nil {
		t.Fatal(err)
	}
	_, _ = c.s.deps.DB.Exec(`UPDATE runs SET status='running', finished_at=NULL WHERE id='run_r'`)
	c.s.deps.QueuePos = func(id string) int {
		if id == "run_q" {
			return 2
		}
		return 0
	}
	var stopped []string
	c.s.deps.StopRun = func(id string) (string, error) {
		var st string
		if err := c.s.deps.DB.QueryRow(`SELECT status FROM runs WHERE id=?`, id).Scan(&st); err != nil {
			return "", errors.New("run not found")
		}
		if st != "queued" && st != "running" {
			return st, errors.New("not active")
		}
		stopped = append(stopped, id)
		return st, nil
	}

	if code, out := c.json(http.MethodGet, "/api/runs/run_q", nil); code != 200 || out["queue_pos"] != float64(2) {
		t.Fatalf("detail %d %v", code, out["queue_pos"])
	}
	if _, out := c.json(http.MethodGet, "/api/runs/run_r", nil); out["queue_pos"] != nil {
		t.Fatalf("running run has a queue_pos: %v", out["queue_pos"])
	}
	list := listRuns(t, c, "status=running")
	if len(list) != 2 || list[0]["id"] != "run_q" || list[1]["id"] != "run_r" {
		t.Fatalf("running filter: %v", list)
	}

	if code, out := c.json(http.MethodPost, "/api/runs/run_q/stop", nil); code != 202 || out["was"] != "queued" {
		t.Fatalf("stop queued %d %v", code, out)
	}
	if code, out := c.json(http.MethodPost, "/api/runs/run_r/stop", nil); code != 202 || out["was"] != "running" {
		t.Fatalf("stop running %d %v", code, out)
	}
	if code, _ := c.json(http.MethodPost, "/api/runs/run_done/stop", nil); code != 409 {
		t.Fatalf("stop finished: %d", code)
	}
	if code, _ := c.json(http.MethodPost, "/api/runs/run_nope/stop", nil); code != 404 {
		t.Fatalf("stop unknown: %d", code)
	}
	if code, _ := c.json(http.MethodGet, "/api/runs/run_r/stop", nil); code != 404 {
		t.Fatalf("GET stop: %d", code)
	}
	// No CSRF token: refused before anything is stopped.
	req := httptest.NewRequest(http.MethodPost, "/api/runs/run_r/stop", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	rr := httptest.NewRecorder()
	c.s.routes().ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("no csrf: %d", rr.Code)
	}
	if strings.Join(stopped, ",") != "run_q,run_r" {
		t.Fatalf("stopped %v", stopped)
	}

	c.s.deps.QuickRun = func(prompt, prov string) (string, error) { return "run_q", nil }
	code, out := c.json(http.MethodPost, "/api/runs/quick", map[string]any{"prompt": "hi"})
	if code != 202 || out["run_id"] != "run_q" || out["queue_pos"] != float64(2) {
		t.Fatalf("queued start %d %v", code, out)
	}
	c.s.deps.QuickRun = func(prompt, prov string) (string, error) { return "run_r", nil }
	if _, out := c.json(http.MethodPost, "/api/runs/quick", map[string]any{"prompt": "hi"}); out["queue_pos"] != nil {
		t.Fatalf("started run has queue_pos: %v", out)
	}
}

// The WebUI wiring: stop buttons (list + detail), the queued pill, the stop reason.
func TestStopRunUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`function stopRun(id, st) {`,
		`api("/api/runs/" + enc(id) + "/stop", { method: "POST", body: {} })`,
		`if (st === "queued") return go();`,
		`if (e.status === 409) { toast(T("run.stop_gone")); return true; }`,
		`data-stop="' + esc(r.id) + '"`,
		`id="rd-stop"`,
		`statusPill(r.status, r.queue_pos)`,
		`T("st.queued_n", { n: pos })`,
		`/^stopped: by user/.test(msg)) return T("rd.err_stopped")`,
		`if (was === "queued" && r.status !== "queued")`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	if !strings.HasPrefix(db.RunStoppedByUserMsg, "stopped: by user") {
		t.Fatal("stored stop message no longer matches the WebUI pattern")
	}
	keys := []string{"st.queued_n", "st.queued_tip", "toast.queued_n", "run.stop", "run.stop_tip", "run.dequeue", "run.dequeue_tip",
		"run.stop_title", "run.stop_body", "run.stopped", "run.dequeued", "run.stop_gone", "rd.err_stopped", "rd.waiting", "rd.queued_at", "rd.never_started"}
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		raw, _ := os.ReadFile(f)
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(f, err)
		}
		for _, k := range keys {
			if m[k] == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
	}
}
