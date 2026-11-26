package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// writeTestPack writes a pack zip with one task bound to skill "bench" made of
// the given files (paths relative to the skill folder).
func writeTestPack(t testing.TB, dir, name string, files map[string][]byte) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	p := Pack{Format: PackFormat, Name: name, Tasks: []PackTask{{Name: name, Prompt: "Run the skill", Skills: []string{"bench"}}}, Skills: []string{"bench"}}
	raw, _ := json.Marshal(p)
	w, err := zw.Create("pack.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write(raw); err != nil {
		t.Fatal(err)
	}
	for fn, data := range files {
		w, err := zw.Create("skills/bench/" + fn)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, name+".zip")
	if err = os.WriteFile(out, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return out
}

// BenchmarkImportPack: a pack whose skill has 300 small files and one 4 MB file.
func BenchmarkImportPack(b *testing.B) {
	a := newTestApp(b)
	files := map[string][]byte{"SKILL.md": []byte("# Bench\n"), "data/big.bin": bytes.Repeat([]byte("x"), 4<<20)}
	for i := range 300 {
		files[fmt.Sprintf("ref/%03d.md", i)] = bytes.Repeat([]byte{byte('a' + i%26)}, 4<<10)
	}
	dir := b.TempDir()
	packs := make([]string, b.N)
	for i := range packs {
		packs[i] = writeTestPack(b, dir, fmt.Sprintf("bench-%d", i), files)
	}
	b.ResetTimer()
	for i := range b.N {
		if _, err := a.ImportPack(context.Background(), packs[i]); err != nil {
			b.Fatal(err)
		}
	}
}

// A skill file between the old 8 MB read cap and the 10 MB skill limit used to
// be cut short on import. Export then import must keep every byte.
func TestPackImportKeepsLargeSkillFileWhole(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	big := bytes.Repeat([]byte("0123456789abcdef"), (9<<20)/16)
	big = append(big, []byte("end-marker")...)
	file := writeTestPack(t, t.TempDir(), "large", map[string][]byte{"SKILL.md": []byte("# Large\n"), "data/big.txt": big})
	res, err := a.ImportPack(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	v, err := a.Tasks.Get(ctx, res.TaskIDs[0])
	if err != nil || len(v.Spec.SkillBindings) != 1 {
		t.Fatalf("task %v %v", v, err)
	}
	// its skill is in v1 itself: no skill-less v1 to roll back to
	if vs, err := a.Tasks.Ver.ListVersions(ctx, "task", res.TaskIDs[0]); err != nil || len(vs) != 1 {
		t.Fatalf("imported versions %d %v", len(vs), err)
	}
	vid, err := a.Skills.ResolveVersion(ctx, tasksvc.SkillBinding{SkillID: v.Spec.SkillBindings[0].SkillID, Mode: "follow"})
	if err != nil {
		t.Fatal(err)
	}
	files, err := a.Skills.Files(ctx, vid)
	if err != nil {
		t.Fatal(err)
	}
	if got := files["data/big.txt"]; !bytes.Equal(got, big) {
		t.Fatalf("big file changed on import: %d bytes, want %d", len(got), len(big))
	}
}

// Packs that break the skill import rules are refused before any skill or task
// is created, with a message naming the problem.
func TestPackImportRejectsBadSkillFilesBeforeImport(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string][]byte
		want  string
	}{
		{"file over the skill limit", map[string][]byte{"SKILL.md": []byte("# X"), "huge.bin": make([]byte, 10<<20+1)}, "larger than 10 MB"},
		{"Windows device name", map[string][]byte{"SKILL.md": []byte("# X"), "CON.txt": []byte("x")}, "unsafe path"},
		{"same path twice", map[string][]byte{"SKILL.md": []byte("# X"), "a.md": []byte("1"), "./a.md": []byte("2")}, "duplicate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			ctx := context.Background()
			file := writeTestPack(t, t.TempDir(), "bad", tc.files)
			if _, err := a.ImportPack(ctx, file); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error with %q, got %v", tc.want, err)
			}
			if tasks, err := a.Tasks.List(ctx); err != nil || len(tasks) != 0 {
				t.Fatalf("created tasks: %v %v", tasks, err)
			}
			if skills, err := a.Skills.List(ctx); err != nil || len(skills) != 0 {
				t.Fatalf("imported skills: %v %v", skills, err)
			}
		})
	}
}

// writeRawPack writes a pack zip with the given pack.json and skill folders.
func writeRawPack(t testing.TB, p Pack, skills map[string]map[string][]byte) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	p.Format = PackFormat
	raw, _ := json.Marshal(p)
	w, err := zw.Create("pack.json")
	if err == nil {
		_, err = w.Write(raw)
	}
	for sn, files := range skills {
		for fn, data := range files {
			if err == nil {
				w, err = zw.Create("skills/" + sn + "/" + fn)
			}
			if err == nil {
				_, err = w.Write(data)
			}
		}
	}
	if err == nil {
		err = zw.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "pack.zip")
	if err = os.WriteFile(out, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return out
}

// A task that Create or UpdateSpec would refuse, anywhere in the pack, is
// reported before any skill is imported or any task is made. It used to come
// after the skills and the tasks before it, which stayed without a record of
// the pack, so importing the fixed pack made them twice.
func TestPackImportChecksAllTasksBeforeImport(t *testing.T) {
	skill := map[string][]byte{"SKILL.md": []byte("# A\n")}
	good := PackTask{Name: "first", Prompt: "Run", Skills: []string{"a"}}
	many := make([]string, tasksvc.MaxSkillBindings+1)
	skills := map[string]map[string][]byte{}
	for i := range many {
		many[i] = fmt.Sprintf("s%02d", i)
		skills[many[i]] = skill
	}
	skills["a"] = skill
	for _, tc := range []struct {
		name string
		bad  PackTask
		want string
	}{
		{"bad cron", PackTask{Name: "second", Prompt: "Run", CronExpr: "not a cron"}, "cron"},
		{"timeout over a day", PackTask{Name: "second", Prompt: "Run", TimeoutMinutes: 2000}, "timeout"},
		{"negative timeout", PackTask{Name: "second", Prompt: "Run", TimeoutMinutes: -1}, "timeout"},
		{"no prompt", PackTask{Name: "second"}, "prompt"},
		{"no name", PackTask{Prompt: "Run"}, "name"},
		{"skill twice", PackTask{Name: "second", Prompt: "Run", Skills: []string{"a", "a"}}, "attached twice"},
		{"too many skills", PackTask{Name: "second", Prompt: "Run", Skills: many}, "at most 16"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			ctx := context.Background()
			file := writeRawPack(t, Pack{Name: "p", Tasks: []PackTask{good, tc.bad}, Skills: []string{"a"}}, skills)
			if _, err := a.ImportPack(ctx, file); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error with %q, got %v", tc.want, err)
			}
			if tasks, err := a.Tasks.List(ctx); err != nil || len(tasks) != 0 {
				t.Fatalf("created tasks: %d %v", len(tasks), err)
			}
			if skills, err := a.Skills.List(ctx); err != nil || len(skills) != 0 {
				t.Fatalf("imported skills: %d %v", len(skills), err)
			}
		})
	}
}

// An imported task is created paused for review with the pack reason, not
// created active and paused afterwards; a failure after it is made archives it.
func TestPackImportTasksStartPausedAndFailedImportArchives(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	skill := map[string]map[string][]byte{"a": {"SKILL.md": []byte("# A\n")}}
	file := writeRawPack(t, Pack{Name: "p", Skills: []string{"a"}, Tasks: []PackTask{
		{Name: "one", Prompt: "Run", CronExpr: "0 9 * * *", Skills: []string{"a"}},
		{Name: "two", Prompt: "Run"},
	}}, skill)
	res, err := a.ImportPack(ctx, file)
	if err != nil || len(res.TaskIDs) != 2 {
		t.Fatalf("import: %v %v", res, err)
	}
	if want := []string{"a"}; fmt.Sprint(res.Skills) != fmt.Sprint(want) {
		t.Fatalf("skills %v", res.Skills)
	}
	for _, id := range res.TaskIDs {
		task, err := a.Store.GetTask(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status != "paused" || task.PauseKind != tasksvc.PausePack || !task.PausedAt.Valid {
			t.Fatalf("task %s: status %q kind %q paused_at %v", task.Name, task.Status, task.PauseKind, task.PausedAt)
		}
	}

	// A database failure while making the second task: the first is archived
	// and the pack is not recorded, so a later import starts over.
	b := newTestApp(t)
	if _, err := b.Store.DB.ExecContext(ctx, `CREATE TRIGGER fail_two BEFORE INSERT ON tasks WHEN NEW.name='two'
BEGIN SELECT RAISE(ABORT, 'disk full'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ImportPack(ctx, file); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("want disk full, got %v", err)
	}
	tasks, err := b.Tasks.List(ctx)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks after a failed import: %d %v", len(tasks), err)
	}
	for _, v := range tasks {
		if v.Task.Status != "archived" {
			t.Fatalf("task %s left %s after a failed import", v.Task.Name, v.Task.Status)
		}
	}
	if _, err := b.Store.DB.ExecContext(ctx, `DROP TRIGGER fail_two`); err != nil {
		t.Fatal(err)
	}
	res, err = b.ImportPack(ctx, file)
	if err != nil || res.Duplicate || len(res.TaskIDs) != 2 {
		t.Fatalf("import again: %+v %v", res, err)
	}
}
