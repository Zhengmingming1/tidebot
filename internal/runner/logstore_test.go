package runner

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleLog(n int) []byte {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		b.WriteString("[10:00:0" + string(rune('0'+i%10)) + "] 工具调用 Read(\"docs/计划.md\") → ok\r\n")
	}
	b.WriteString("[[end]]\n")
	return []byte(b.String())
}

func writeAged(t *testing.T, p string, b []byte, age time.Duration) time.Time {
	t.Helper()
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Add(-age).Truncate(time.Second)
	if err := os.Chtimes(p, ts, ts); err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestLogRunID(t *testing.T) {
	for name, want := range map[string]string{"run_a.log": "run_a", "run_a.log.gz": "run_a", "run_a.log.gz.tmp": "run_a"} {
		if id, ok := LogRunID(name); !ok || id != want {
			t.Fatalf("%s → %q %v", name, id, ok)
		}
	}
	for _, name := range []string{".log", "notes.txt", "run_a.gz"} {
		if _, ok := LogRunID(name); ok {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestCompressLogFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "run_1.log")
	raw := sampleLog(64 << 10)
	mod := writeAged(t, p, raw, 48*time.Hour)
	freed, err := CompressLogFile(p)
	if err != nil || freed <= int64(len(raw))/2 {
		t.Fatalf("freed %d of %d, err %v", freed, len(raw), err)
	}
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("plain log still there")
	}
	fi, err := os.Stat(p + ".gz")
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(mod) {
		t.Fatalf("mtime %v, want %v (retention counts from it)", fi.ModTime(), mod)
	}
	if fi.Size()+freed != int64(len(raw)) {
		t.Fatalf("size %d + freed %d != %d", fi.Size(), freed, len(raw))
	}
	got, err := ReadLogFile(p)
	if err != nil || string(got) != string(raw) {
		t.Fatalf("round trip differs (err %v)", err)
	}
	if n, ok := GzipRawSize(p + ".gz"); !ok || n != int64(len(raw)) {
		t.Fatalf("raw size %d %v", n, ok)
	}
	if _, packed, err := OpenLogFile(p); err != nil || !packed {
		t.Fatalf("packed %v err %v", packed, err)
	}
	if _, _, err := OpenLogFile(filepath.Join(dir, "none.log")); !errors.Is(err, ErrNoLog) {
		t.Fatalf("missing log: %v", err)
	}
}

func TestCompressLogFileLeavesSmallAndRandom(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "s.log")
	writeAged(t, small, sampleLog(1000), time.Hour)
	noise := make([]byte, 16<<10)
	_, _ = rand.Read(noise)
	rnd := filepath.Join(dir, "r.log")
	writeAged(t, rnd, noise, time.Hour)
	for _, p := range []string{small, rnd} {
		if n, err := CompressLogFile(p); err != nil || n != 0 {
			t.Fatalf("%s: freed %d err %v", p, n, err)
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s gone", p)
		}
		if _, err := os.Stat(p + ".gz"); err == nil {
			t.Fatalf("%s.gz written", p)
		}
	}
	// Plain wins when both exist (a sweep that couldn't remove the plain file).
	os.WriteFile(small+".gz", []byte("junk"), 0o600)
	if _, packed, err := OpenLogFile(small); err != nil || packed {
		t.Fatalf("plain should win: packed %v err %v", packed, err)
	}
}

func TestCompressFinishedLogsSkipsLiveBusyRecent(t *testing.T) {
	dir := t.TempDir()
	r := New(nil, nil, 1)
	r.LogDir = dir
	old := 2 * time.Hour
	for _, id := range []string{"done", "busy", "live"} {
		writeAged(t, filepath.Join(dir, id+".log"), sampleLog(32<<10), old)
	}
	writeAged(t, filepath.Join(dir, "fresh.log"), sampleLog(32<<10), time.Minute)
	writeAged(t, filepath.Join(dir, "crash.log.gz.tmp"), []byte("x"), old)
	writeAged(t, filepath.Join(dir, "note.txt"), sampleLog(32<<10), old)
	_, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	r.mu.Lock()
	r.cancels["live"] = cancel
	r.mu.Unlock()

	rep := r.CompressFinishedLogs(map[string]bool{"busy": true}, 10*time.Minute, time.Now())
	if rep.Files != 1 || rep.FreedBytes <= 0 {
		t.Fatalf("report %+v", rep)
	}
	exists := func(n string) bool { _, err := os.Stat(filepath.Join(dir, n)); return err == nil }
	for n, want := range map[string]bool{
		"done.log": false, "done.log.gz": true, "busy.log": true, "live.log": true, "fresh.log": true,
		"busy.log.gz": false, "crash.log.gz.tmp": false, "note.txt": true,
	} {
		if exists(n) != want {
			t.Fatalf("%s exists=%v, want %v", n, !want, want)
		}
	}
	// A second sweep has nothing left to do.
	if rep := r.CompressFinishedLogs(map[string]bool{"busy": true}, 10*time.Minute, time.Now()); rep.Files != 0 {
		t.Fatalf("second sweep %+v", rep)
	}
}
