package db

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every pooled connection gets the same settings, including ones opened after the first closed.
func TestOpenConnectionPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	check := func(round int) {
		t.Helper()
		want := map[string]string{
			"journal_mode": "wal", "synchronous": "1", "foreign_keys": "1",
			"busy_timeout": "5000", "journal_size_limit": "8388608",
		}
		for name, w := range want {
			var got string
			if err := d.QueryRow(`PRAGMA ` + name).Scan(&got); err != nil {
				t.Fatalf("round %d %s: %v", round, name, err)
			}
			if strings.ToLower(got) != w {
				t.Fatalf("round %d: PRAGMA %s = %q, want %q", round, name, got, w)
			}
		}
	}
	check(1)
	d.SetMaxIdleConns(0) // next query opens a fresh connection
	check(2)
}

// After a checkpoint the -wal file is cut back to the limit instead of keeping its peak size,
// and committed rows survive close + reopen.
func TestWALTrimmedAndDurable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := d.Exec(`CREATE TABLE big(v BLOB)`); err != nil {
		t.Fatal(err)
	}
	// Hold a read transaction on a second handle so auto-checkpoints can't recycle the log
	// while we write ~16 MiB.
	reader, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rtx, err := reader.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := rtx.QueryRow(`SELECT COUNT(*) FROM big`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	blob := make([]byte, 64<<10)
	for i := 0; i < 256; i++ {
		if _, err := d.Exec(`INSERT INTO big(v) VALUES(?)`, blob); err != nil {
			t.Fatal(err)
		}
	}
	walPath := path + "-wal"
	st, err := os.Stat(walPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() < 12<<20 {
		t.Fatalf("setup: wal only %d bytes, reader did not block checkpoints", st.Size())
	}
	_ = rtx.Rollback()
	_ = reader.Close()
	if _, err := d.Exec(`PRAGMA wal_checkpoint(PASSIVE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO big(v) VALUES(x'00')`); err != nil { // a write after the checkpoint applies the limit
		t.Fatal(err)
	}
	st, err = os.Stat(walPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() > 8<<20 {
		t.Fatalf("wal still %d bytes after checkpoint, want <= 8 MiB", st.Size())
	}
	if err := Optimize(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	if err := d2.QueryRow(`SELECT COUNT(*) FROM big`).Scan(&n); err != nil || n != 257 {
		t.Fatalf("rows after reopen = %d (%v), want 257", n, err)
	}
}
