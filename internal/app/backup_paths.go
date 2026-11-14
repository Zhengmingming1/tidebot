package app

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
)

// homeMove is one folder of the backed-up install (its home or data folder) and
// where it lives after the restore.
type homeMove struct{ old, new string }

// restorePathColumns are the columns that hold absolute paths that may sit in the
// backed-up install's home or data folder (task default folders, a session's
// task folder or scratch folder, plugin installs, backup files, dependency picks).
var restorePathColumns = []struct{ table, col string }{
	{"tasks", "workspace_dir"},
	{"runs", "workspace_dir"},
	{"chat_sessions", "workspace_dir"},
	{"plugin_versions", "dir"},
	{"backups", "path"},
	{"capability_dependency_preferences", "path"},
}

// winStyle reports whether p was written on Windows (drive letter or UNC / backslashes).
func winStyle(p string) bool {
	if len(p) >= 2 && p[1] == ':' && (p[0]|0x20) >= 'a' && (p[0]|0x20) <= 'z' {
		return true
	}
	return strings.HasPrefix(p, `\\`) || (strings.Contains(p, `\`) && !strings.Contains(p, "/"))
}

// remapPath moves p from a backed-up folder to its restored place: p must be the
// folder itself or inside it (a whole path element, so ~/.tidebot does not take
// ~/.tidebot-old), compared byte for byte (case-insensitively for a Windows
// backup). The rest of the path is rejoined with this OS's separator, so a backup
// from Windows restored on macOS / Linux (or the other way) gives usable paths.
// The longest matching folder wins (a data folder inside the home folder).
func remapPath(p string, moves []homeMove) (string, bool) {
	best, bestLen, rest := -1, 0, ""
	for i, m := range moves {
		old, win := m.old, winStyle(m.old)
		seps := "/"
		if win {
			seps = `/\`
		}
		// "C:\" and "/" keep their separator; other folders drop a trailing one
		for len(old) > 1 && strings.ContainsAny(old[len(old)-1:], seps) && !(win && len(old) == 3 && old[1] == ':') {
			old = old[:len(old)-1]
		}
		if old == "" || len(p) < len(old) || len(old) <= bestLen {
			continue
		}
		if head := p[:len(old)]; head != old && !(win && strings.EqualFold(head, old)) {
			continue
		}
		tail := p[len(old):]
		if tail != "" && !strings.ContainsAny(tail[:1], seps) && !strings.ContainsAny(old[len(old)-1:], seps) {
			continue // ~/.tidebot-old is not inside ~/.tidebot
		}
		if win {
			tail = strings.ReplaceAll(tail, `\`, "/")
		}
		best, bestLen, rest = i, len(old), tail
	}
	if best < 0 {
		return p, false
	}
	parts := []string{moves[best].new}
	for _, s := range strings.Split(rest, "/") {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return filepath.Join(parts...), true
}

// remapRestoredPaths rewrites the stored paths under the backed-up install's folders
// to the restored ones, in one transaction. Matching is done here rather than in SQL:
// SQLite's substr / length count characters, Go's len counts bytes, so a home folder
// with a non-ASCII name (/Users/张三/.tidebot) never matched before.
func remapRestoredPaths(ctx context.Context, sqlDB *sql.DB, moves []homeMove) error {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, c := range restorePathColumns {
		rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT rowid, %s FROM %s WHERE %s <> ''`, c.col, c.table, c.col))
		if err != nil {
			return fmt.Errorf("path mapping %s: %w", c.table, err)
		}
		type change struct {
			rowid int64
			p     string
		}
		var changes []change
		for rows.Next() {
			var ch change
			if err := rows.Scan(&ch.rowid, &ch.p); err != nil {
				rows.Close()
				return fmt.Errorf("path mapping %s: %w", c.table, err)
			}
			if np, ok := remapPath(ch.p, moves); ok && np != ch.p {
				changes = append(changes, change{ch.rowid, np})
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return fmt.Errorf("path mapping %s: %w", c.table, err)
		}
		if len(changes) == 0 {
			continue
		}
		st, err := tx.PrepareContext(ctx, fmt.Sprintf(`UPDATE %s SET %s = ? WHERE rowid = ?`, c.table, c.col))
		if err != nil {
			return fmt.Errorf("path mapping %s: %w", c.table, err)
		}
		for _, ch := range changes {
			if _, err := st.ExecContext(ctx, ch.p, ch.rowid); err != nil {
				st.Close()
				return fmt.Errorf("path mapping %s: %w", c.table, err)
			}
		}
		st.Close()
	}
	return tx.Commit()
}
