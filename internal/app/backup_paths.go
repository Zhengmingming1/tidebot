package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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

// restoreJSONColumns are JSON text columns that may embed those same absolute
// paths (capability lock resolved/preference paths, run detail snapshots, a
// file-watch trigger path). Immutable hashed version content (versions.content_json,
// workflow definitions, plugin manifests) is left alone: those workspace_dir
// values are the user's own folders and the rows are content-addressed.
var restoreJSONColumns = []struct{ table, col string }{
	{"capability_versions", "lock_json"},
	{"capability_proposals", "payload_json"},
	{"runs", "effective_cost_policy_json"},
	{"runs", "agent_config_changes"},
	{"trigger_bindings", "config_json"},
}

const capabilityVersionsNoUpdate = `CREATE TRIGGER capability_versions_no_update BEFORE UPDATE ON capability_versions
BEGIN SELECT RAISE(ABORT, 'capability versions are immutable'); END`

// winStyle reports whether p was written on Windows (drive letter or UNC / backslashes).
func winStyle(p string) bool {
	if len(p) >= 2 && p[1] == ':' && (p[0]|0x20) >= 'a' && (p[0]|0x20) <= 'z' {
		return true
	}
	return strings.HasPrefix(p, `\\`) || (strings.Contains(p, `\`) && !strings.Contains(p, "/"))
}

// remapPath moves p from a backed-up folder to its restored place: p must be the
// folder itself or inside it (a whole path element, so ~/.tidebot does not take
// ~/.tidebot-old). Matching is byte for byte, except a Windows backup is compared
// case-insensitively and treats 8.3 short names (C:\Users\RUNNER~1) as the same
// prefix as the long form (C:\Users\runneradmin). The rest of the path is
// rejoined with this OS's separator, so a backup from Windows restored on
// macOS / Linux (or the other way) gives usable paths. The longest matching
// folder wins (a data folder inside the home folder).
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
		if old == "" {
			continue
		}
		var tail string
		var n int
		if win {
			var ok bool
			tail, n, ok = winPrefixRemain(p, old)
			if !ok || n <= bestLen {
				continue
			}
			tail = strings.ReplaceAll(tail, `\`, "/")
		} else {
			if len(p) < len(old) || len(old) <= bestLen {
				continue
			}
			if p[:len(old)] != old {
				continue
			}
			tail = p[len(old):]
			if tail != "" && !strings.ContainsAny(tail[:1], seps) && !strings.ContainsAny(old[len(old)-1:], seps) {
				continue // ~/.tidebot-old is not inside ~/.tidebot
			}
			n = len(old)
		}
		best, bestLen, rest = i, n, tail
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

// winPrefixRemain reports whether p is old or a path under it on Windows (case,
// slash, and 8.3 short vs long names). n is the number of matched path
// elements (the drive or UNC root counts as one) so a data folder inside the
// home folder still wins.
func winPrefixRemain(p, old string) (rest string, n int, ok bool) {
	pRoot, pParts := winSplit(p)
	oRoot, oParts := winSplit(old)
	if pRoot == "" || oRoot == "" || !strings.EqualFold(pRoot, oRoot) || len(pParts) < len(oParts) {
		return "", 0, false
	}
	for i, part := range oParts {
		if !winNameEqual(pParts[i], part) {
			return "", 0, false
		}
	}
	if len(pParts) > len(oParts) {
		rest = strings.Join(pParts[len(oParts):], "/")
	}
	return rest, 1 + len(oParts), true
}

func isDriveLetter(c byte) bool {
	c |= 0x20
	return c >= 'a' && c <= 'z'
}

func winIsSep(c byte) bool { return c == '\\' || c == '/' }

// winSplit splits a Windows path into a drive or UNC root and the folders
// under it. "C:\a\b", "C:/a/b" and "c:\a\b\" all give root "C:" and [a b].
func winSplit(p string) (root string, parts []string) {
	switch {
	case len(p) >= 2 && p[1] == ':' && isDriveLetter(p[0]):
		root, p = p[:2], p[2:]
	case len(p) >= 2 && p[0] == '\\' && p[1] == '\\':
		i := 2
		for i < len(p) && !winIsSep(p[i]) {
			i++
		}
		j := i + 1
		for j < len(p) && !winIsSep(p[j]) {
			j++
		}
		if j <= i+1 {
			return p, nil
		}
		root, p = p[:j], p[j:]
	default:
		return "", nil
	}
	for len(p) > 0 {
		if winIsSep(p[0]) {
			p = p[1:]
			continue
		}
		i := 0
		for i < len(p) && !winIsSep(p[i]) {
			i++
		}
		parts = append(parts, p[:i])
		p = p[i:]
	}
	return root, parts
}

func winNameEqual(a, b string) bool {
	if strings.EqualFold(a, b) {
		return true
	}
	return win83Alias(a, b) || win83Alias(b, a)
}

// win83Alias reports whether short looks like a generated 8.3 name for long
// (RUNNER~1 for runneradmin, PROGRA~1 for Program Files). A name that already
// fits 8.3 is stored as-is, so RUNNER is not an alias of runneradmin. The
// numeric suffix is ignored: on a given volume ~1 vs ~2 is assigned by
// creation order, which another machine cannot know.
func win83Alias(short, long string) bool {
	prefix, ext, ok := win83Tilde(short)
	if !ok || win83Fits(long) {
		return false
	}
	base, longExt := win83Parts(long)
	if !win83ExtEqual(ext, longExt) {
		return false
	}
	return prefix != "" && len(prefix) <= 6 && len(base) >= len(prefix) && strings.EqualFold(base[:len(prefix)], prefix)
}

func win83ExtEqual(shortExt, longExt string) bool {
	if strings.EqualFold(shortExt, longExt) {
		return true
	}
	return len(longExt) > 3 && strings.EqualFold(shortExt, longExt[:3])
}

// win83Tilde parses a generated 8.3 name (NAME~N or NAME~N.EXT) and returns
// the letters before the tilde.
func win83Tilde(name string) (prefix, ext string, ok bool) {
	if strings.ContainsAny(name, `/\`) {
		return "", "", false
	}
	base, ext := win83StemExt(name)
	if len(base) > 8 || len(ext) > 3 {
		return "", "", false
	}
	i := strings.LastIndex(base, "~")
	if i < 1 || i > 6 {
		return "", "", false
	}
	num := base[i+1:]
	if num == "" {
		return "", "", false
	}
	for j := 0; j < len(num); j++ {
		if num[j] < '0' || num[j] > '9' {
			return "", "", false
		}
	}
	return base[:i], ext, true
}

func win83StemExt(name string) (base, ext string) {
	i := strings.LastIndex(name, ".")
	if i <= 0 {
		return name, ""
	}
	return name[:i], name[i+1:]
}

// win83Parts maps a long name the way 8.3 generation does (uppercase, spaces
// dropped, +,;=[] to _, extra dots in the stem dropped) and splits the extension.
func win83Parts(name string) (base, ext string) {
	var b strings.Builder
	b.Grow(len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch c {
		case ' ':
			continue
		case '+', ',', ';', '=', '[', ']':
			b.WriteByte('_')
		default:
			if c >= 'a' && c <= 'z' {
				c -= 'a' - 'A'
			}
			b.WriteByte(c)
		}
	}
	base, ext = win83StemExt(b.String())
	if strings.Contains(base, ".") {
		base = strings.ReplaceAll(base, ".", "")
	}
	return base, ext
}

// win83Fits reports whether name is already a valid 8.3 name, so Windows would
// store it as-is instead of inventing a NAME~N alias.
func win83Fits(name string) bool {
	if strings.ContainsAny(name, " +,;=[]") {
		return false
	}
	base, ext := win83StemExt(name)
	return !strings.Contains(base, ".") && len(base) > 0 && len(base) <= 8 && len(ext) <= 3
}

// remapJSON rewrites any string in raw JSON that remapPath would move. Objects,
// arrays, and non-path strings are left alone; invalid or empty JSON is unchanged.
// Re-encoding only happens when a path actually moved, so an unchanged value keeps
// its original bytes (key order, spacing).
func remapJSON(raw string, moves []homeMove) (string, bool) {
	if raw == "" || !json.Valid([]byte(raw)) {
		return raw, false
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return raw, false
	}
	nv, ok := remapJSONValue(v, moves)
	if !ok {
		return raw, false
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(nv); err != nil {
		return raw, false
	}
	return string(bytes.TrimSpace(buf.Bytes())), true
}

func remapJSONValue(v any, moves []homeMove) (any, bool) {
	switch t := v.(type) {
	case string:
		if np, ok := remapPath(t, moves); ok && np != t {
			return np, true
		}
		return v, false
	case map[string]any:
		changed := false
		for k, child := range t {
			if nv, ok := remapJSONValue(child, moves); ok {
				t[k] = nv
				changed = true
			}
		}
		return t, changed
	case []any:
		changed := false
		for i, child := range t {
			if nv, ok := remapJSONValue(child, moves); ok {
				t[i] = nv
				changed = true
			}
		}
		return t, changed
	default:
		return v, false
	}
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
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
	if err := remapRestoredTextColumns(ctx, tx, moves); err != nil {
		return err
	}
	if err := remapRestoredJSONColumns(ctx, tx, moves); err != nil {
		return err
	}
	return tx.Commit()
}

func remapRestoredTextColumns(ctx context.Context, tx *sql.Tx, moves []homeMove) error {
	for _, c := range restorePathColumns {
		changes, err := collectRemappedText(ctx, tx, c.table, c.col, moves)
		if err != nil {
			return err
		}
		if err := applyRemappedText(ctx, tx, c.table, c.col, changes); err != nil {
			return err
		}
	}
	return nil
}

func remapRestoredJSONColumns(ctx context.Context, tx *sql.Tx, moves []homeMove) error {
	droppedLockTrigger := false
	for _, c := range restoreJSONColumns {
		rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT rowid, %s FROM %s WHERE %s <> ''`, c.col, c.table, c.col))
		if err != nil {
			return fmt.Errorf("path mapping %s.%s: %w", c.table, c.col, err)
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
				return fmt.Errorf("path mapping %s.%s: %w", c.table, c.col, err)
			}
			if np, ok := remapJSON(ch.p, moves); ok && np != ch.p {
				changes = append(changes, change{ch.rowid, np})
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return fmt.Errorf("path mapping %s.%s: %w", c.table, c.col, err)
		}
		if len(changes) == 0 {
			continue
		}
		lockJSON := c.table == "capability_versions" && c.col == "lock_json"
		if lockJSON && !droppedLockTrigger {
			if _, err := tx.ExecContext(ctx, `DROP TRIGGER IF EXISTS capability_versions_no_update`); err != nil {
				return fmt.Errorf("path mapping %s.%s: %w", c.table, c.col, err)
			}
			droppedLockTrigger = true
		}
		q := fmt.Sprintf(`UPDATE %s SET %s = ? WHERE rowid = ?`, c.table, c.col)
		if lockJSON {
			q = `UPDATE capability_versions SET lock_json = ?, lock_sha256 = ? WHERE rowid = ?`
		}
		st, err := tx.PrepareContext(ctx, q)
		if err != nil {
			return fmt.Errorf("path mapping %s.%s: %w", c.table, c.col, err)
		}
		for _, ch := range changes {
			var err error
			if lockJSON {
				_, err = st.ExecContext(ctx, ch.p, sha256hex(ch.p), ch.rowid)
			} else {
				_, err = st.ExecContext(ctx, ch.p, ch.rowid)
			}
			if err != nil {
				st.Close()
				return fmt.Errorf("path mapping %s.%s: %w", c.table, c.col, err)
			}
		}
		st.Close()
	}
	if droppedLockTrigger {
		if _, err := tx.ExecContext(ctx, capabilityVersionsNoUpdate); err != nil {
			return fmt.Errorf("path mapping capability_versions: %w", err)
		}
	}
	return nil
}

type remappedText struct {
	rowid int64
	p     string
}

func collectRemappedText(ctx context.Context, tx *sql.Tx, table, col string, moves []homeMove) ([]remappedText, error) {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT rowid, %s FROM %s WHERE %s <> ''`, col, table, col))
	if err != nil {
		return nil, fmt.Errorf("path mapping %s: %w", table, err)
	}
	defer rows.Close()
	var changes []remappedText
	for rows.Next() {
		var ch remappedText
		if err := rows.Scan(&ch.rowid, &ch.p); err != nil {
			return nil, fmt.Errorf("path mapping %s: %w", table, err)
		}
		if np, ok := remapPath(ch.p, moves); ok && np != ch.p {
			changes = append(changes, remappedText{ch.rowid, np})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("path mapping %s: %w", table, err)
	}
	return changes, nil
}

func applyRemappedText(ctx context.Context, tx *sql.Tx, table, col string, changes []remappedText) error {
	if len(changes) == 0 {
		return nil
	}
	st, err := tx.PrepareContext(ctx, fmt.Sprintf(`UPDATE %s SET %s = ? WHERE rowid = ?`, table, col))
	if err != nil {
		return fmt.Errorf("path mapping %s: %w", table, err)
	}
	defer st.Close()
	for _, ch := range changes {
		if _, err := st.ExecContext(ctx, ch.p, ch.rowid); err != nil {
			return fmt.Errorf("path mapping %s: %w", table, err)
		}
	}
	return nil
}
