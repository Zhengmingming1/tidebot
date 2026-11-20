package app

import (
	"context"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// GetChatSession carries each turn's change summary (not the file list) so the
// session page can show a badge and undo without opening the run. A missing
// run, a read run, and invalid JSON all look like "nothing recorded".
func TestChatSessionTurnChangeSummary(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	now := nowUTC()
	ins := func(idx int, mode, fc, undoneAt, undoneFiles string) string {
		t.Helper()
		rid := addChatTurnRow(t, a, ses.ID, idx, "succeeded", "ok")
		if _, err := a.DB.Exec(`UPDATE runs SET run_mode=?, file_changes=?, changes_undone_at=?, changes_undone_files=? WHERE id=?`,
			mode, fc, undoneAt, undoneFiles, rid); err != nil {
			t.Fatal(err)
		}
		return rid
	}
	r1 := ins(1, "edit", `{"files":[{"path":"a.txt"},{"path":"<b>.txt"}],"count":2,"add":3,"del":1}`, "", "")
	r2 := ins(2, "exec", `{"files":[{"path":"b.txt"}],"count":1,"add":1,"del":0}`, now, "")
	r3 := ins(3, "edit", `{"files":[{"path":"c.txt"},{"path":"d.txt"}],"count":2,"add":4,"del":2,"patch_cut":true}`, "", `["c.txt"]`)
	ins(4, "read", `{"files":[{"path":"x.txt"}],"count":1,"add":1,"del":0}`, "", "")
	ins(5, "edit", `{not json`, "", "")
	ins(6, "edit", "", "", "")
	gone := ins(7, "edit", `{"count":1,"add":1,"del":0}`, "", "")
	if _, err := a.DB.Exec(`DELETE FROM runs WHERE id=?`, gone); err != nil {
		t.Fatal(err)
	}

	d, err := a.GetChatSession(ctx, ses.ID)
	if err != nil || len(d.TurnList) != 7 {
		t.Fatalf("detail: n=%d %v", len(d.TurnList), err)
	}
	got := func(i int) *struct {
		files, add, del, undoneN int
		undone, cut              bool
	} {
		c := d.TurnList[i].Changes
		if c == nil {
			return nil
		}
		return &struct {
			files, add, del, undoneN int
			undone, cut              bool
		}{c.Files, c.Add, c.Del, c.UndoneN, c.Undone, c.PatchCut}
	}
	if d.TurnList[0].RunID != r1 || got(0) == nil || got(0).files != 2 || got(0).add != 3 || got(0).del != 1 || got(0).undone || got(0).undoneN != 0 || got(0).cut {
		t.Fatalf("edit turn: %+v %+v", d.TurnList[0], got(0))
	}
	if d.TurnList[1].RunID != r2 || got(1) == nil || got(1).files != 1 || !got(1).undone || got(1).cut {
		t.Fatalf("undone turn: %+v %+v", d.TurnList[1], got(1))
	}
	if d.TurnList[2].RunID != r3 || got(2) == nil || got(2).files != 2 || got(2).undone || got(2).undoneN != 1 || !got(2).cut {
		t.Fatalf("partly cut turn: %+v %+v", d.TurnList[2], got(2))
	}
	for i, name := range []string{"read", "invalid json", "empty", "deleted run"} {
		if got(i+3) != nil {
			t.Fatalf("%s still has changes: %+v", name, got(i+3))
		}
	}
}
