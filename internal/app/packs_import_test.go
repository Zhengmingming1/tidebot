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
