package db

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A query whose context ends right after it produced rows must not leave its
// statement open: the leaked read transaction pinned the WAL (checkpoint busy,
// -wal grew without bound) and kept the file open after Close, which also broke
// TempDir cleanup on Windows. Fixed by modernc.org/sqlite >= v1.40.1.
func TestCancelledQueriesDoNotPinWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`CREATE TABLE zz(id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		if _, err := d.Exec(`INSERT INTO zz(v) VALUES(?)`, strings.Repeat("x", 100)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 1500; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(i%50)*time.Microsecond)
		var n int
		_ = d.QueryRowContext(ctx, `SELECT count(*) FROM zz a, zz b`).Scan(&n)
		cancel()
	}
	payload := strings.Repeat("y", 4000)
	for i := 0; i < 300; i++ {
		if _, err := d.Exec(`INSERT INTO zz(v) VALUES(?)`, payload); err != nil {
			t.Fatal(err)
		}
	}
	var busy, logFrames, done int
	if err := d.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &done); err != nil {
		t.Fatal(err)
	}
	if busy != 0 {
		t.Fatalf("checkpoint blocked by a leaked read: busy=%d log=%d done=%d", busy, logFrames, done)
	}
	if st, err := os.Stat(path + "-wal"); err == nil && st.Size() > 0 {
		t.Fatalf("-wal not truncated: %d bytes", st.Size())
	}
}
