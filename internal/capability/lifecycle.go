package capability

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/Zhengmingming1/tidebot/internal/safepath"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/id"
)

type Diff struct {
	Added               []string `json:"added"`
	Changed             []string `json:"changed"`
	Removed             []string `json:"removed"`
	PermissionsChanged  bool     `json:"permissions_changed"`
	RequirementsChanged bool     `json:"requirements_changed"`
	FromVersion         string   `json:"from_version"`
	Scan                Scan     `json:"scan"`
}

func (s *Store) GetVersion(ctx context.Context, capID, versionID string) (Record, error) {
	var r Record
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT c.id,v.id,c.created_at,v.created_at,v.lock_json FROM capability_versions v JOIN capabilities c ON c.id=v.capability_id WHERE c.id=? AND v.id=?`, capID, versionID).Scan(&r.ID, &r.VersionID, &r.CreatedAt, &r.UpdatedAt, &raw)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &r.Lock)
		r.Title = r.Lock.Title
	}
	return r, err
}
func (s *Store) Versions(ctx context.Context, capID string) ([]Record, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT c.id,v.id,c.created_at,v.created_at,v.lock_json FROM capability_versions v JOIN capabilities c ON c.id=v.capability_id WHERE c.id=? ORDER BY v.created_at DESC,v.id DESC`, capID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		var r Record
		var raw string
		if err = rows.Scan(&r.ID, &r.VersionID, &r.CreatedAt, &r.UpdatedAt, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &r.Lock); err != nil {
			return nil, err
		}
		r.Title = r.Lock.Title
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) Diff(ctx context.Context, capID string, candidate Lock, files map[string][]byte) (Diff, error) {
	d := Diff{Added: []string{}, Changed: []string{}, Removed: []string{}}
	current, err := s.Get(ctx, capID)
	if err != nil {
		return d, err
	}
	hashes, hash, err := HashFiles(files)
	if err != nil {
		return d, err
	}
	if candidate.ID != capID || candidate.FilesSHA256 != "" && candidate.FilesSHA256 != hash {
		return d, errors.New("candidate snapshot mismatch")
	}
	var raw string
	if err = s.DB.QueryRowContext(ctx, `SELECT files_json FROM capability_versions WHERE id=? AND capability_id=?`, current.VersionID, capID).Scan(&raw); err != nil {
		return d, err
	}
	old := map[string]string{}
	if err = json.Unmarshal([]byte(raw), &old); err != nil {
		return d, err
	}
	for p, h := range hashes {
		if previous, ok := old[p]; !ok {
			d.Added = append(d.Added, p)
		} else if previous != h {
			d.Changed = append(d.Changed, p)
		}
	}
	for p := range old {
		if _, ok := hashes[p]; !ok {
			d.Removed = append(d.Removed, p)
		}
	}
	sort.Strings(d.Added)
	sort.Strings(d.Changed)
	sort.Strings(d.Removed)
	a, _ := json.Marshal(current.Lock.Permissions)
	b, _ := json.Marshal(candidate.Permissions)
	d.PermissionsChanged = string(a) != string(b)
	a, _ = json.Marshal(current.Lock.Requires)
	b, _ = json.Marshal(candidate.Requires)
	d.RequirementsChanged = string(a) != string(b)
	d.FromVersion = current.VersionID
	d.Scan, err = s.scanSource(candidate.Source, files)
	return d, err
}

// Rollback changes only the follow pointer. Explicit version bindings remain pinned.
func (s *Store) Rollback(ctx context.Context, capID, versionID, actor string) (Record, error) {
	if actor == "" {
		return Record{}, errors.New("actor required")
	}
	r, err := s.GetVersion(ctx, capID, versionID)
	if err != nil {
		return r, err
	}
	if err = s.Verify(ctx, capID, versionID); err != nil {
		return r, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return r, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `UPDATE capabilities SET current_version_id=?,title=?,updated_at=? WHERE id=? AND status='installed' AND EXISTS(SELECT 1 FROM capability_versions WHERE capability_id=? AND id=?)`, versionID, r.Title, now, capID, capID, versionID)
	if err != nil {
		return r, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return r, sql.ErrNoRows
	}
	if err = restoreSnapshotSkills(ctx, tx, r.Lock); err != nil {
		return r, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM capability_tool_grants WHERE capability_id=?`, capID); err != nil {
		return r, err
	}
	if err = recordEvent(ctx, tx, capID, versionID, actor, "rollback", r.Lock.Scan.Verdict, "applied", now); err != nil {
		return r, err
	}
	if err = tx.Commit(); err != nil {
		return r, err
	}
	r.UpdatedAt = now
	return r, nil
}
func recordEvent(ctx context.Context, tx *sql.Tx, capID, versionID, actor, action, verdict, result, now string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO capability_events(id,capability_id,version_id,actor,action,verdict,result,created_at) VALUES(?,?,?,?,?,?,?,?)`, id.New("capevt"), capID, versionID, actor, action, verdict, result, now)
	return err
}

type Binding struct {
	OwnerKind    string `json:"owner_kind"`
	OwnerID      string `json:"owner_id"`
	CapabilityID string `json:"capability_id"`
	VersionID    string `json:"version_id,omitempty"`
}

func (s *Store) Bind(ctx context.Context, b Binding) error {
	switch b.OwnerKind {
	case "session", "task", "workflow_node", "button":
	default:
		return errors.New("invalid binding owner")
	}
	if b.OwnerID == "" {
		return errors.New("binding owner required")
	}
	if b.VersionID != "" {
		if _, err := s.GetVersion(ctx, b.CapabilityID, b.VersionID); err != nil {
			return err
		}
	}
	result, err := s.DB.ExecContext(ctx, `INSERT INTO capability_bindings(owner_kind,owner_id,capability_id,version_id,created_at) SELECT ?,?,?,?,? WHERE EXISTS(SELECT 1 FROM capabilities WHERE id=? AND status='installed') AND (?='' OR EXISTS(SELECT 1 FROM capability_versions WHERE capability_id=? AND id=?)) ON CONFLICT(owner_kind,owner_id,capability_id) DO UPDATE SET version_id=excluded.version_id`, b.OwnerKind, b.OwnerID, b.CapabilityID, b.VersionID, time.Now().UTC().Format(time.RFC3339Nano), b.CapabilityID, b.VersionID, b.CapabilityID, b.VersionID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}
func (s *Store) Unbind(ctx context.Context, b Binding) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM capability_bindings WHERE owner_kind=? AND owner_id=? AND capability_id=?`, b.OwnerKind, b.OwnerID, b.CapabilityID)
	return err
}
func (s *Store) ResolveBinding(ctx context.Context, b Binding) (Record, error) {
	if _, err := s.Get(ctx, b.CapabilityID); err != nil {
		return Record{}, err
	}
	var r Record
	var err error
	if b.VersionID == "" {
		r, err = s.Get(ctx, b.CapabilityID)
	} else {
		r, err = s.GetVersion(ctx, b.CapabilityID, b.VersionID)
	}
	if err == nil {
		err = s.Verify(ctx, r.ID, r.VersionID)
	}
	return r, err
}

var ErrReferenced = errors.New("capability is still referenced; remove its bindings first")

// Uninstall removes availability and exclusive skill registrations, never shared
// skills, credentials or managed tools.
// Historical snapshots are retained for explicit GC and audit recovery.
func (s *Store) Uninstall(ctx context.Context, capID, actor string) error {
	_, err := s.UninstallWithReport(ctx, capID, actor)
	return err
}

func (s *Store) uninstall(ctx context.Context, capID, actor string, report *UninstallResult) error {
	return s.uninstallPinned(ctx, capID, "", actor, report, false, nil)
}

func (s *Store) uninstallPinned(ctx context.Context, capID, versionID, actor string, report *UninstallResult, preview bool, reviewed *UninstallResult) error {
	if actor == "" {
		return errors.New("actor required")
	}
	r, err := s.Get(ctx, capID)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize reference checks and registration removal with installers/binders.
	if _, err = tx.ExecContext(ctx, `UPDATE capabilities SET updated_at=updated_at WHERE id=?`, capID); err != nil {
		return err
	}
	var current string
	if err = tx.QueryRowContext(ctx, `SELECT current_version_id FROM capabilities WHERE id=? AND status='installed'`, capID).Scan(&current); err != nil {
		return err
	}
	if current != r.VersionID || versionID != "" && current != versionID {
		return errors.New("capability changed; review uninstall again")
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM capability_bindings WHERE capability_id=?`, capID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrReferenced
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM capability_run_bindings b JOIN runs r ON r.id=b.run_id WHERE b.capability_id=? AND r.status IN ('running','queued','pending')`, capID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrReferenced
	}
	// Existing application schemas keep skill bindings in immutable JSON. Check
	// current task/workflow definitions, active run pins and all session bindings.
	skills := map[string]bool{}
	versions, err := tx.QueryContext(ctx, `SELECT lock_json FROM capability_versions WHERE capability_id=?`, capID)
	if err != nil {
		return err
	}
	for versions.Next() {
		var raw string
		if err = versions.Scan(&raw); err != nil {
			versions.Close()
			return err
		}
		var l Lock
		if err = json.Unmarshal([]byte(raw), &l); err != nil {
			versions.Close()
			return err
		}
		for _, c := range l.Components {
			if c.SkillID != "" {
				skills[c.SkillID] = true
			}
		}
	}
	if err = versions.Err(); err != nil {
		versions.Close()
		return err
	}
	versions.Close()
	queries := []string{`SELECT bindings_json FROM chat_sessions`, `SELECT v.content_json FROM pointers p JOIN versions v ON v.id=p.version_id WHERE p.entity_type IN ('task','skill_set')`, `SELECT v.definition_json FROM workflows w JOIN workflow_versions v ON v.workflow_id=w.id AND v.version=w.version WHERE w.deleted=0`, `SELECT w.snapshot_json FROM workflow_runs w WHERE w.status IN ('running','pending')`}
	for _, query := range queries {
		rows, e := tx.QueryContext(ctx, query)
		if e != nil {
			return e
		}
		for rows.Next() {
			var raw sql.NullString
			if e = rows.Scan(&raw); e != nil {
				rows.Close()
				return e
			}
			if references(raw.String, capID, skills) {
				rows.Close()
				return ErrReferenced
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	for skillID := range skills {
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM run_skill_bindings b JOIN runs r ON r.id=b.run_id WHERE b.skill_id=? AND r.status IN ('running','queued','pending')`, skillID).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return ErrReferenced
		}
	}
	if reviewed != nil {
		current := UninstallResult{RemovedSkills: []string{}, Retained: []UninstallRetention{}}
		if err = cleanupSkillRegistrations(ctx, tx, capID, &current, true); err != nil {
			return err
		}
		want, _ := json.Marshal(reviewed)
		got, _ := json.Marshal(current)
		if string(want) != string(got) {
			return errors.New("uninstall contents changed; review a new proposal")
		}
	}
	if err = cleanupSkillRegistrations(ctx, tx, capID, report, preview); err != nil {
		return err
	}
	if preview {
		return nil
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM capability_tool_grants WHERE capability_id=?`, capID); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `UPDATE capabilities SET status='uninstalled',updated_at=? WHERE id=?`, now, capID); err != nil {
		return err
	}
	if err = recordEvent(ctx, tx, capID, r.VersionID, actor, "uninstall", r.Lock.Scan.Verdict, "unavailable; snapshots retained", now); err != nil {
		return err
	}
	return tx.Commit()
}
func references(raw, capID string, skills map[string]bool) bool {
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return raw != ""
	} // Malformed binding state fails closed.
	var walk func(any) bool
	walk = func(v any) bool {
		switch x := v.(type) {
		case string:
			return capID != "" && x == capID || skills[x]
		case []any:
			for _, child := range x {
				if walk(child) {
					return true
				}
			}
		case map[string]any:
			for _, child := range x {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	return walk(value)
}

type Bundle struct {
	Format string            `json:"format"`
	Lock   Lock              `json:"lock"`
	Files  map[string][]byte `json:"files"`
}

const BundleFormat = "tidebot-capability-bundle/1"

// ExportBundle emits a portable, hash-checked archive. It never includes resolved
// local paths or secret values. Source files are intentionally included verbatim.
func (s *Store) ExportBundle(ctx context.Context, capID, versionID string) ([]byte, error) {
	r, err := s.GetVersion(ctx, capID, versionID)
	if err != nil {
		return nil, err
	}
	if err = s.Verify(ctx, capID, versionID); err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	base := filepath.Join(s.DataDir, "capabilities", capID, versionID, "src")
	err = filepath.WalkDir(base, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		relative, e := filepath.Rel(base, p)
		if e != nil {
			return e
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		files[filepath.ToSlash(relative)] = b
		return nil
	})
	if err != nil {
		return nil, err
	}
	r.Lock.Resolved = nil
	r.Lock.DependencyPreferences = nil
	r.Lock.InstalledBy = ""
	r.Lock.InstalledAt = ""
	for i := range r.Lock.Components {
		r.Lock.Components[i].SkillID = ""
		r.Lock.Components[i].VersionID = ""
	}
	return json.Marshal(Bundle{Format: BundleFormat, Lock: r.Lock, Files: files})
}

// ParseBundle only prepares untrusted import data; importing is always a new
// proposal and confirmation. Trust labels from an imported file are not trusted.
func ParseBundle(raw []byte) (Lock, map[string][]byte, error) {
	var b Bundle
	if len(raw) > 90<<20 {
		return Lock{}, nil, errors.New("bundle exceeds size limit")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&b); err != nil {
		return Lock{}, nil, err
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return Lock{}, nil, errors.New("trailing bundle data")
	}
	if b.Format != BundleFormat || b.Lock.Format != LockFormat || !safeID.MatchString(b.Lock.ID) {
		return Lock{}, nil, errors.New("unsupported bundle")
	}
	_, hash, err := HashFiles(b.Files)
	if err != nil {
		return Lock{}, nil, err
	}
	if hash != b.Lock.FilesSHA256 {
		return Lock{}, nil, errors.New("bundle file hash mismatch")
	}
	b.Lock.Source.Trust = "unknown"
	b.Lock.Source.Registry = nil
	b.Lock.Resolved = nil
	b.Lock.DependencyPreferences = nil
	b.Lock.InstalledAt = ""
	b.Lock.InstalledBy = ""
	b.Lock.Scan = ScanFiles(b.Files)
	for i := range b.Lock.Components {
		b.Lock.Components[i].LocallyModified = false
		b.Lock.Components[i].SkillID = ""
		b.Lock.Components[i].VersionID = ""
	}
	return b.Lock, b.Files, nil
}

// RecoverInterruptedProposals is called once at startup, before accepting UI
// work. It does not retry installations or execute upstream instructions.
func (s *Store) RecoverInterruptedProposals(ctx context.Context) (int64, error) {
	result, err := s.DB.ExecContext(ctx, `UPDATE capability_proposals SET status='failed',decided_at=? WHERE status='applying'`, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) SnapshotUsage(ctx context.Context) (int64, error) {
	var total int64
	base := filepath.Join(s.DataDir, "capabilities")
	err := filepath.WalkDir(base, func(_ string, d os.DirEntry, e error) error {
		if errors.Is(e, os.ErrNotExist) {
			return nil
		}
		if e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink in snapshot tree")
		}
		if !d.IsDir() {
			info, e := d.Info()
			if e != nil {
				return e
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// GCResult reports only capability snapshots. Shared managed tools and envs
// have their own lifecycle and are never removed by this collector.
type GCResult struct {
	RemovedVersions []string `json:"removed_versions"`
	BytesFreed      int64    `json:"bytes_freed"`
}

// GC keeps the current version plus one previous version for rollback, all
// explicit version pins, and all historical run records. It removes only paths
// derived from database identities under the owned capability root.
func (s *Store) GC(ctx context.Context, capID, actor string) (GCResult, error) {
	out := GCResult{RemovedVersions: []string{}}
	if actor == "" || !safeID.MatchString(capID) {
		return out, errors.New("valid capability and actor required")
	}
	versions, err := s.Versions(ctx, capID)
	if err != nil {
		return out, err
	}
	var current string
	if err = s.DB.QueryRowContext(ctx, `SELECT current_version_id FROM capabilities WHERE id=?`, capID).Scan(&current); err != nil {
		return out, err
	}
	keep := map[string]bool{current: true}
	for _, v := range versions {
		if v.VersionID != current {
			keep[v.VersionID] = true
			break
		}
	}
	for _, v := range versions {
		if keep[v.VersionID] {
			continue
		}
		if err = ctx.Err(); err != nil {
			return out, err
		}
		tx, e := s.DB.BeginTx(ctx, nil)
		if e != nil {
			return out, e
		}
		// Recheck the current pointer inside the deletion transaction.
		var references int
		e = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM capabilities WHERE id=? AND current_version_id=?)+(SELECT count(*) FROM capability_bindings WHERE capability_id=? AND version_id=?)+(SELECT count(*) FROM capability_run_bindings WHERE capability_id=? AND version_id=?)`, capID, v.VersionID, capID, v.VersionID, capID, v.VersionID).Scan(&references)
		if e != nil {
			tx.Rollback()
			return out, e
		}
		if references > 0 {
			tx.Rollback()
			continue
		}
		// Preserve registered skill versions used by historical runs as well.
		for _, component := range v.Lock.Components {
			if component.VersionID == "" {
				continue
			}
			var n int
			e = tx.QueryRowContext(ctx, `SELECT count(*) FROM run_skill_bindings WHERE version_id=?`, component.VersionID).Scan(&n)
			if e != nil {
				tx.Rollback()
				return out, e
			}
			references += n
		}
		if references > 0 {
			tx.Rollback()
			continue
		}
		if !safeID.MatchString(v.VersionID) {
			tx.Rollback()
			return out, errors.New("invalid version identity")
		}
		base := filepath.Join(s.DataDir, "capabilities", capID)
		target := filepath.Join(base, v.VersionID)
		if e = checkExistingDirectory(base); e != nil {
			tx.Rollback()
			return out, e
		}
		var size int64
		e = filepath.WalkDir(target, func(_ string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.Type()&os.ModeSymlink != 0 {
				return errors.New("refusing GC of symlink snapshot")
			}
			if !d.IsDir() {
				info, e := d.Info()
				if e != nil {
					return e
				}
				size += info.Size()
			}
			return nil
		})
		if e != nil {
			tx.Rollback()
			return out, e
		}
		trash := filepath.Join(base, ".gc-"+v.VersionID)
		if _, e = os.Lstat(trash); !errors.Is(e, os.ErrNotExist) {
			tx.Rollback()
			return out, errors.New("snapshot GC recovery required")
		}
		if e = os.Rename(target, trash); e != nil {
			tx.Rollback()
			return out, e
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM capability_versions WHERE id=? AND capability_id=?`, v.VersionID, capID); e == nil {
			e = recordEvent(ctx, tx, capID, v.VersionID, actor, "gc", v.Lock.Scan.Verdict, "snapshot removed", time.Now().UTC().Format(time.RFC3339Nano))
		}
		if e == nil {
			e = tx.Commit()
		} else {
			tx.Rollback()
		}
		if e != nil {
			_ = os.Rename(trash, target)
			return out, e
		}
		if e = os.RemoveAll(trash); e != nil {
			return out, e
		}
		out.RemovedVersions = append(out.RemovedVersions, v.VersionID)
		out.BytesFreed += size
	}
	return out, nil
}
func checkExistingDirectory(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	for {
		info, e := safepath.LstatDirectory(abs)
		if e != nil {
			return e
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe snapshot directory")
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return nil
		}
		abs = parent
	}
}

// CreateUpdateProposal freezes a candidate and its base version after generating
// a reviewable diff. InstallProposal rejects adoption if that base has changed.
func (s *Store) CreateUpdateProposal(ctx context.Context, sessionID, runID string, candidate Lock, files map[string][]byte) (Proposal, Diff, error) {
	d, err := s.Diff(ctx, candidate.ID, candidate, files)
	if err != nil {
		return Proposal{}, d, err
	}
	_, hash, err := HashFiles(files)
	if err != nil {
		return Proposal{}, d, err
	}
	candidate.FilesSHA256 = hash
	candidate.Scan = d.Scan
	p, err := s.CreateProposal(ctx, Proposal{SessionID: sessionID, RunID: runID, Kind: "update", BaseVersionID: d.FromVersion, Lock: candidate, Diff: &d})
	return p, d, err
}

// RecoverStorage reconciles filesystem publication interrupted by a crash. Call
// only during startup, before accepting installs/runs. It never touches shared
// tools, source repositories, registered skills or arbitrary data directories.
func (s *Store) RecoverStorage(ctx context.Context) (int, error) {
	root := filepath.Join(s.DataDir, "capabilities")
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err := checkExistingDirectory(root); err != nil {
		return 0, err
	}
	caps, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, capDir := range caps {
		if err = ctx.Err(); err != nil {
			return count, err
		}
		if !capDir.IsDir() || !safeID.MatchString(capDir.Name()) {
			continue
		}
		base := filepath.Join(root, capDir.Name())
		if err = checkExistingDirectory(base); err != nil {
			return count, err
		}
		entries, e := os.ReadDir(base)
		if e != nil {
			return count, e
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			target := filepath.Join(base, name)
			remove := false
			switch {
			case strings.HasPrefix(name, ".stage-"):
				remove = true
			case strings.HasPrefix(name, ".gc-capver_"):
				versionID := strings.TrimPrefix(name, ".gc-")
				var present int
				if e = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM capability_versions WHERE capability_id=? AND id=?`, capDir.Name(), versionID).Scan(&present); e != nil {
					return count, e
				}
				if present > 0 {
					original := filepath.Join(base, versionID)
					if _, e = os.Lstat(original); !errors.Is(e, os.ErrNotExist) {
						return count, errors.New("conflicting snapshot recovery paths")
					}
					if e = os.Rename(target, original); e != nil {
						return count, e
					}
					count++
					continue
				}
				remove = true
			case strings.HasPrefix(name, "capver_") && safeID.MatchString(name):
				var present int
				if e = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM capability_versions WHERE capability_id=? AND id=?`, capDir.Name(), name).Scan(&present); e != nil {
					return count, e
				}
				remove = present == 0
			}
			if remove {
				if e = os.RemoveAll(target); e != nil {
					return count, e
				}
				count++
			}
		}
	}
	return count, nil
}
