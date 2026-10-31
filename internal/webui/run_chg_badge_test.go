package webui

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRunChangeSumList: the runs list carries each edit run's change size (files, +/-),
// read from runs.file_changes in SQL; read runs, runs with nothing recorded and a
// corrupt record carry none, and the file list itself never rides on the list.
func TestRunChangeSumList(t *testing.T) {
	c := login(t)
	now := time.Now()
	for i, id := range []string{"cs1", "cs2", "cs3", "cs4", "cs5"} {
		seedRun(t, c, id, "p "+id, "succeeded", 0, now.Add(time.Duration(i)*time.Second))
	}
	db := c.s.deps.DB
	set := func(id, mode, fc string) {
		t.Helper()
		if _, err := db.Exec(`UPDATE runs SET run_mode=?, workspace_dir='/w', file_changes=? WHERE id=?`, mode, fc, id); err != nil {
			t.Fatal(err)
		}
	}
	set("cs1", "edit", `{"files":[{"path":"secret/a.txt","status":"M","add":12,"del":3}],"count":4,"add":40,"del":3}`)
	set("cs2", "edit", `{"files":[],"count":0,"add":0,"del":0}`)
	set("cs3", "edit", `{not json`)
	set("cs4", "read", `{"files":[],"count":9,"add":9,"del":9}`)
	// cs5: plain run, nothing recorded

	rec := c.do(http.MethodGet, "/api/runs?limit=10", "", nil)
	if rec.Code != 200 {
		t.Fatalf("list %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret/a.txt") {
		t.Fatal("list carries the file list")
	}
	var list []runDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	got := map[string]*runChangeSum{}
	for _, r := range list {
		got[r.ID] = r.Changes
	}
	if c1 := got["cs1"]; c1 == nil || c1.Files != 4 || c1.Add != 40 || c1.Del != 3 {
		t.Fatalf("cs1 %+v", c1)
	}
	if c2 := got["cs2"]; c2 == nil || c2.Files != 0 {
		t.Fatalf("cs2 (changed nothing) %+v", c2)
	}
	for _, id := range []string{"cs3", "cs4", "cs5"} {
		if got[id] != nil {
			t.Errorf("%s: %+v", id, got[id])
		}
	}

	// exports: CSV cells (empty = not recorded), JSONL object
	rr := c.do(http.MethodGet, "/api/runs/export?lang=en", "", nil)
	recs, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(rr.Body.String(), "\xEF\xBB\xBF"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if h := recs[0]; h[18] != "Files changed" || h[19] != "Lines added" || h[20] != "Lines removed" {
		t.Fatalf("head %v", h)
	}
	cells := map[string][]string{}
	for _, r := range recs[1:] {
		cells[r[4]] = r[18:21]
	}
	if strings.Join(cells["cs1"], ",") != "4,40,3" || strings.Join(cells["cs2"], ",") != "0,0,0" || strings.Join(cells["cs5"], ",") != ",," || strings.Join(cells["cs3"], ",") != ",," {
		t.Fatalf("csv %v", cells)
	}
	rr = c.do(http.MethodGet, "/api/runs/export?format=jsonl", "", nil)
	sc := bufio.NewScanner(strings.NewReader(rr.Body.String()))
	seen := 0
	for sc.Scan() {
		var e exportRun
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		switch e.ID {
		case "cs1":
			if e.Changes == nil || e.Changes.Files != 4 {
				t.Fatalf("jsonl cs1 %+v", e.Changes)
			}
			seen++
		case "cs5":
			if e.Changes != nil || strings.Contains(sc.Text(), `"changes"`) {
				t.Fatalf("jsonl cs5 %s", sc.Text())
			}
			seen++
		}
	}
	if seen != 2 {
		t.Fatalf("jsonl seen %d", seen)
	}
}

// TestRunChangeBadgeUI: list rows show the badge (files, +add, −del with zero sides left
// out), it opens the run at its Changed files card, escaped, both languages.
func TestRunChangeBadgeUI(t *testing.T) {
	js, _ := Static.ReadFile("static/app.js")
	src := strings.ReplaceAll(string(js), "\r\n", "\n")
	for _, s := range []string{
		`function runChgBadge(r) {`,
		`if (!c || !(c.files > 0)) return "";`,
		`(c.add ? '<span class="add">+' + esc(num(c.add)) + "</span>" : "")`,
		`(c.del ? '<span class="del">−' + esc(num(c.del)) + "</span>" : "")`,
		`'<a class="chg-badge' + (c.undone ? " undone" : "") + '" href="#/runs/' + enc(r.id) + '" data-chg-jump data-tip="' + esc(tip) + '" aria-label="' + esc(tip) + '">'`,
		`"</strong></a>" + runChgBadge(r) + "</div>"`,
		`if (state.chgJump === id && $("#rd-chg")) $("#rd-chg").scrollIntoView({ block: "start" });`,
		`e.target.closest("[data-chg-jump]")`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	css, _ := Static.ReadFile("static/styles.css")
	for _, s := range []string{".run-line { display: flex;", ".chg-badge { flex: none;", ".chg-badge:focus-visible", "#rd-chg { scroll-margin-top: calc(var(--topbar-h) + 16px); }"} {
		if !strings.Contains(string(css), s) {
			t.Errorf("styles lack %s", s)
		}
	}
	for _, f := range []string{"zh-CN", "en"} {
		b, _ := Static.ReadFile("static/i18n/" + f + ".json")
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"runs.chg_tip", "runs.chg_files_one", "runs.chg_files_many"} {
			if m[k] == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
		if !strings.Contains(m["runs.chg_tip"], "{f}") || !strings.Contains(m["runs.chg_tip"], "{a}") || !strings.Contains(m["runs.chg_tip"], "{d}") {
			t.Errorf("%s runs.chg_tip placeholders", f)
		}
	}
}
