package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"modernc.org/sqlite"
)

// slowOpenMarker in a dsn makes every new connection to that file take slowOpenDelay, like a cold
// disk or an antivirus scanning a freshly created database file.
const (
	slowOpenMarker = "tidebot-slow-open"
	slowOpenDelay  = 5300 * time.Millisecond
)

func init() {
	sqlite.RegisterConnectionHook(func(_ sqlite.ExecQuerierContext, dsn string) error {
		if strings.Contains(dsn, slowOpenMarker) {
			time.Sleep(slowOpenDelay)
		}
		return nil
	})
}

// A slow but successful open must not fail. Before, Open pinged under a 5-second deadline that the
// driver cannot interrupt, so an open that merely took longer failed with "ping sqlite: context
// deadline exceeded" (seen on Windows CI in TestSessionSeenThrottled).
func TestOpenSlowConnection(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), slowOpenMarker)
	path := filepath.Join(dir, "slow.db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open after %v: %v", time.Since(start).Round(time.Millisecond), err)
	}
	defer d.Close()
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil || n == 0 {
		t.Fatalf("migrations not applied: n=%d err=%v", n, err)
	}
}

// A database held by another connection still fails, bounded by busy_timeout and with SQLite's
// own reason rather than a bare deadline.
func TestOpenLockedDatabase(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "locked.db")
	holder, err := sql.Open("sqlite", fmt.Sprintf("file:%s", path))
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	holder.SetMaxOpenConns(1)
	if _, err := holder.Exec(`CREATE TABLE t(x)`); err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(`BEGIN EXCLUSIVE`); err != nil {
		t.Fatal(err)
	}
	defer holder.Exec(`ROLLBACK`)
	start := time.Now()
	d, err := Open(path)
	if err == nil {
		d.Close()
		t.Fatal("Open succeeded on an exclusively locked database")
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("Open waited %v on a locked database", elapsed)
	}
	if msg := err.Error(); !strings.Contains(msg, "locked") && !strings.Contains(msg, "busy") {
		t.Fatalf("error does not name the lock: %v", err)
	}
}
