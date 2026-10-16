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
