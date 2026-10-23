package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// EnsurePhase1Columns adds columns introduced after phase 0 without failing on re-run.
func EnsurePhase1Columns(db *sql.DB) error {
	alters := []string{
		`ALTER TABLE runs ADD COLUMN task_version_id TEXT`,
		`ALTER TABLE runs ADD COLUMN has_next_run_items INTEGER NOT NULL DEFAULT 0`,
	}
	for _, stmt := range alters {
		if _, err := db.Exec(stmt); err != nil {
			msg := strings.ToLower(err.Error())
			if strings.Contains(msg, "duplicate column") {
				continue
			}
			return fmt.Errorf("ensure column: %w", err)
		}
	}
	return nil
}

// EnsurePhase1bColumns adds cost/auth related columns idempotently.
func EnsurePhase1bColumns(db *sql.DB) error {
	alters := []string{
		`ALTER TABLE runs ADD COLUMN cost_bucket TEXT NOT NULL DEFAULT 'production'`,
		`ALTER TABLE runs ADD COLUMN paused_by_budget INTEGER NOT NULL DEFAULT 0`,
		// Last accepted TOTP time step: a code (or an older one) is accepted once.
		`ALTER TABLE users ADD COLUMN totp_last_step INTEGER NOT NULL DEFAULT 0`,
	}
	for _, stmt := range alters {
		if _, err := db.Exec(stmt); err != nil {
			msg := strings.ToLower(err.Error())
			if strings.Contains(msg, "duplicate column") {
				continue
			}
			return fmt.Errorf("ensure phase1b column: %w", err)
		}
	}
	return nil
}
