package webui

import (
	"encoding/json"
	"math/rand"
	"net/http"
	"strings"
	"testing"
)

func TestLineDiffReconstructs(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	words := []string{"a", "b", "c", "d", "e"}
	gen := func() []string {
		n := rnd.Intn(30)
		out := make([]string, n)
		for i := range out {
			out[i] = words[rnd.Intn(len(words))]
		}
		return out
	}
	for iter := 0; iter < 500; iter++ {
		a, b := gen(), gen()
		var ra, rb []string
		edits := 0
		for _, l := range LineDiff(a, b) {
			switch l.Op {
			case " ":
				ra, rb = append(ra, l.Text), append(rb, l.Text)
			case "-":
				ra = append(ra, l.Text)
				edits++
			case "+":
				rb = append(rb, l.Text)
				edits++
			}
		}
		if strings.Join(ra, "\n") != strings.Join(a, "\n") || strings.Join(rb, "\n") != strings.Join(b, "\n") {
			t.Fatalf("iter %d: script does not rebuild inputs\na=%v\nb=%v", iter, a, b)
		}
		if edits > len(a)+len(b) {
			t.Fatalf("too many edits")
		}
	}
	if d := LineDiff([]string{"x", "y", "z"}, []string{"x", "Y", "z"}); len(d) != 4 {
		t.Fatalf("minimal diff expected, got %v", d)
	}
}

func TestDiffFilesStatuses(t *testing.T) {
	old := map[string][]byte{"SKILL.md": []byte("one\ntwo\n"), "gone.txt": []byte("x"), "same": []byte("s"), "bin": {0, 1}}
	nw := map[string][]byte{"SKILL.md": []byte("one\nTWO\n"), "new.txt": []byte("y"), "same": []byte("s"), "bin": {0, 2}}
	got := map[string]string{}
	for _, f := range DiffFiles(old, nw, 3) {
		got[f.Path] = f.Status
	}
	want := map[string]string{"SKILL.md": "modified", "gone.txt": "removed", "new.txt": "added", "same": "same", "bin": "binary"}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s: %s want %s", k, got[k], v)
		}
	}
}

func importSkill(t *testing.T, c *client, md string, mode string) {
	t.Helper()
	body, ct := multipartZip(t, map[string]string{"s/SKILL.md": md, "s/tool.py": "print(1)\n"})
	rr := c.do(http.MethodPost, "/api/skills/upload", ct, body)
	var p skillPreview
	_ = json.Unmarshal(rr.Body.Bytes(), &p)
	if code, out := c.json(http.MethodPost, "/api/skills/import", map[string]string{"stage_id": p.StageID, "name": p.Name, "mode": mode}); code != 200 {
		t.Fatalf("import %d %v", code, out)
	}
}

func TestSkillPreviewTreeAndDiff(t *testing.T) {
	c := login(t)
	body, ct := multipartZip(t, map[string]string{"s/SKILL.md": "---\nname: Notes\n---\nbody\n", "s/lib/a.py": "x"})
	rr := c.do(http.MethodPost, "/api/skills/upload", ct, body)
	var p skillPreview
	_ = json.Unmarshal(rr.Body.Bytes(), &p)
	if len(p.Tree) != 2 || p.Size == 0 || len(p.Checks) != 4 || p.Checks[2].OK { // no description → that check fails
		t.Fatalf("preview %+v", p)
	}
	importSkill(t, c, "---\nname: Diffy\ndescription: d\n---\nline one\nline two\n", "")
	importSkill(t, c, "---\nname: Diffy\ndescription: d\n---\nline one\nline 2\n", "version")
	var skills []map[string]any
	rr = c.do(http.MethodGet, "/api/skills", "", nil)
	_ = json.Unmarshal(rr.Body.Bytes(), &skills)
	var sid string
	for _, s := range skills {
		if s["name"] == "Diffy" {
			sid = s["id"].(string)
		}
	}
	_, v := c.json(http.MethodGet, "/api/skills/"+sid+"/versions", nil)
	vs := v["versions"].([]any)
	if len(vs) != 2 {
		t.Fatalf("versions %v", vs)
	}
	a, b := vs[0].(map[string]any)["id"].(string), vs[1].(map[string]any)["id"].(string)
	code, d := c.json(http.MethodGet, "/api/skills/"+sid+"/diff?a="+a+"&b="+b, nil)
	if code != 200 || d["added"].(float64) != 1 || d["removed"].(float64) != 1 {
		t.Fatalf("diff %d %v", code, d)
	}
	// a version id from another skill (or junk) is refused
	if code, _ := c.json(http.MethodGet, "/api/skills/"+sid+"/diff?a=nope&b="+b, nil); code != 400 {
		t.Fatalf("foreign version %d", code)
	}
	if code, _ := c.json(http.MethodGet, "/api/skills/other/diff?a="+a+"&b="+b, nil); code != 400 {
		t.Fatalf("wrong skill %d", code)
	}
	if rr := c.do(http.MethodGet, "/api/skills/"+sid+"/files?v="+b, "", nil); rr.Code != 200 || !strings.Contains(rr.Body.String(), "SKILL.md") {
		t.Fatalf("files %d", rr.Code)
	}
}

func TestAmendmentEditAndConfirmAcross(t *testing.T) {
	c := login(t)
	_, tk := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "am", "prompt": "p", "provider": "anthropic", "cron": "0 9 * * *", "enabled": true})
	tid := tk["id"].(string)
	_, _ = c.json(http.MethodPost, "/api/tasks/"+tid+"/amendments", map[string]any{"utterance": "always add a summary"})
	rr := c.do(http.MethodGet, "/api/tasks/"+tid+"/amendments", "", nil)
	var items []map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &items)
	if len(items) == 0 {
		t.Skip("mock structurer produced no items")
	}
	iid := items[0]["id"].(string)
	if code, out := c.json(http.MethodPost, "/api/amendments/"+iid+"/edit", map[string]any{"content": "edited text", "scope": "permanent"}); code != 200 || out["content"] != "edited text" {
		t.Fatalf("edit %d %v", code, out)
	}
	if code, _ := c.json(http.MethodPost, "/api/amendments/"+iid+"/edit", map[string]any{"content": ""}); code != 400 {
		t.Fatalf("empty edit %d", code)
	}
	// ids from another task are refused
	_, other := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "o", "prompt": "p", "provider": "anthropic", "cron": "0 9 * * *", "enabled": true})
	if code, _ := c.json(http.MethodPost, "/api/tasks/"+other["id"].(string)+"/amendments-confirm", map[string]any{"ids": []string{iid}}); code != 400 {
		t.Fatalf("cross task %d", code)
	}
	ids := []string{}
	for _, it := range items {
		if it["status"] == "pending" {
			ids = append(ids, it["id"].(string))
		}
	}
	code, res := c.json(http.MethodPost, "/api/tasks/"+tid+"/amendments-confirm", map[string]any{"ids": ids})
	if code != 200 || res["version_id"] == "" {
		t.Fatalf("confirm %d %v", code, res)
	}
	_, after := c.json(http.MethodGet, "/api/tasks/"+tid, nil)
	if !strings.Contains(strings.Join(func() []string {
		out := []string{}
		for _, x := range after["constraints"].([]any) {
			out = append(out, x.(string))
		}
		return out
	}(), "|"), "edited text") {
		t.Fatalf("edited text not applied: %v", after["constraints"])
	}
	// a second edit after confirming is refused
	if code, _ := c.json(http.MethodPost, "/api/amendments/"+iid+"/edit", map[string]any{"content": "late"}); code != 400 {
		t.Fatalf("late edit %d", code)
	}
}
