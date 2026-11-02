package skillstore

import (
	"context"
	"fmt"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkResolveBindings16(b *testing.B) {
	d, err := db.Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer d.Close()
	s := New(d, version.New(d), nil, "")
	ctx := context.Background()
	dir := b.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Skill\nInstructions\n"), 0600); err != nil {
		b.Fatal(err)
	}
	var bindings []tasksvc.SkillBinding
	for i := 0; i < 16; i++ {
		v, err := s.ImportDir(ctx, fmt.Sprintf("skill-%d", i), dir, "benchmark")
		if err != nil {
			b.Fatal(err)
		}
		bindings = append(bindings, tasksvc.SkillBinding{SkillID: v.ID, Mode: "follow"})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.ResolveBindings(ctx, bindings); err != nil {
			b.Fatal(err)
		}
	}
}
