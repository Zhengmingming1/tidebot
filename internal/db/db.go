// Package db opens SQLite (pure Go) and applies embedded migrations.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// connPragmas are applied to every connection the pool opens.
//
//   - busy_timeout first, so setting WAL waits instead of failing if another process holds a lock.
//   - synchronous(NORMAL): in WAL mode SQLite then syncs only at checkpoints instead of on every
//     commit (about 1000 fsyncs per 1000 small writes before, a handful now). The database stays
//     consistent after an app crash or power loss; at worst the last commits before an OS crash
//     or power cut are rolled back. Live usage is saved every few seconds, so that is acceptable.
//   - journal_size_limit: after a checkpoint the -wal file is cut back to 8 MiB instead of staying
//     at its largest size (it can grow while a long read blocks the checkpoint).
const connPragmas = "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)" +
	"&_pragma=synchronous(NORMAL)&_pragma=journal_size_limit(8388608)"

// Open creates/opens a SQLite database at path with WAL and foreign keys.
func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?%s", path, connPragmas)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := Migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := EnsurePhase1Columns(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := EnsurePhase1bColumns(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	// Long-lived connection: let SQLite analyze tables it has no statistics for yet, with a cap
	// so it stays in the millisecond range on large databases (https://sqlite.org/lang_analyze.html).
	_, _ = db.Exec(`PRAGMA analysis_limit=400`)
	_, _ = db.Exec(`PRAGMA optimize=0x10002`)
	return db, nil
}

// Optimize refreshes query-planner statistics for tables whose size changed a lot since the
// last run. Cheap when nothing changed; call every few hours and before closing.
func Optimize(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `PRAGMA optimize`)
	return err
}

// Migrate applies pending SQL files from migrations/ in lexical order.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for i, name := range names {
		version := i + 1
		var exists int
		if err := db.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, version).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply %s: %w", name, err)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, version, now); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
