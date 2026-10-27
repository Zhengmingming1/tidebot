package db

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCapRunResult(t *testing.T) {
	if got, cut := CapRunResult("# 结果\r\n- a\r\n"); got != "# 结果\n- a\n" || cut {
		t.Fatalf("crlf: %q %v", got, cut)
	}
	// A multi-byte rune straddling the cap is dropped whole, never split.
	long := strings.Repeat("a", MaxRunResultBytes-1) + "潮汐"
	got, cut := CapRunResult(long)
	if !cut || !utf8.ValidString(got) || len(got) != MaxRunResultBytes-1 {
		t.Fatalf("cut=%v valid=%v len=%d", cut, utf8.ValidString(got), len(got))
	}
}

func TestRunResultStore(t *testing.T) {
	sqlDB, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	s := NewStore(sqlDB)
	ctx := context.Background()
	if _, err := sqlDB.Exec(`INSERT INTO runs(id, status, prompt, created_at) VALUES('r1','succeeded','p','2026-10-27T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if txt, cut, err := s.RunResult(ctx, "r1"); err != nil || txt != "" || cut {
		t.Fatalf("empty: %q %v %v", txt, cut, err)
	}
	if err := s.SetRunResult(ctx, "r1", "## Done\n\n'; DROP TABLE runs; --"); err != nil {
		t.Fatal(err)
	}
	if txt, cut, err := s.RunResult(ctx, "r1"); err != nil || txt != "## Done\n\n'; DROP TABLE runs; --" || cut {
		t.Fatalf("stored: %q %v %v", txt, cut, err)
	}
	if err := s.SetRunResult(ctx, "r1", strings.Repeat("x", MaxRunResultBytes+10)); err != nil {
		t.Fatal(err)
	}
	if txt, cut, _ := s.RunResult(ctx, "r1"); len(txt) != MaxRunResultBytes || !cut {
		t.Fatalf("capped len=%d cut=%v", len(txt), cut)
	}
}
