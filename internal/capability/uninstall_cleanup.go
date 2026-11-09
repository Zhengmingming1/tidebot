package capability

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// UninstallResult distinguishes removed registrations from retained immutable
// data. Uninstall never deletes credentials, user files, or snapshot bytes.
type UninstallResult struct {
	OK            bool                 `json:"ok"`
	RemovedSkills []string             `json:"removed_skills"`
	Retained      []UninstallRetention `json:"retained"`
}
type UninstallRetention struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

func (s *Store) UninstallWithReport(ctx context.Context, capID, actor string) (UninstallResult, error) {
	out := UninstallResult{RemovedSkills: []string{}, Retained: []UninstallRetention{}}
	err := s.uninstall(ctx, capID, actor, &out)
	if err != nil {
		return UninstallResult{RemovedSkills: []string{}, Retained: []UninstallRetention{}}, err
	}
	out.OK = true
	return out, nil
}

// planSkillCleanup runs under the uninstall transaction's writer lock and only
// reads. Immutable skill versions/blobs remain available to retained snapshots.
// Only skills whose complete provenance belongs to this capability are listed
// for removal. The confirmed path compares this plan with the reviewed one and
// then applies the same plan, so history is scanned once per uninstall.
func planSkillCleanup(ctx context.Context, tx *sql.Tx, capID string, out *UninstallResult) error {
	own := map[string]map[string]bool{}
	shared := map[string]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT capability_id,lock_json,lock_sha256 FROM capability_versions`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid, raw, digest string
		if err = rows.Scan(&cid, &raw, &digest); err != nil {
			rows.Close()
			return err
		}
		sum := sha256.Sum256([]byte(raw))
		if hex.EncodeToString(sum[:]) != digest {
			rows.Close()
			return errors.New("capability lock integrity mismatch; uninstall cleanup refused")
		}
		var l Lock
		if err = json.Unmarshal([]byte(raw), &l); err != nil {
			rows.Close()
			return err
		}
		for _, c := range l.Components {
			if c.SkillID == "" {
				continue
			}
			if cid != capID {
				shared[c.SkillID] = true
				continue
			}
			if own[c.SkillID] == nil {
				own[c.SkillID] = map[string]bool{}
			}
			own[c.SkillID][c.VersionID] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(own))
	for sid := range own {
		ids = append(ids, sid)
	}
	sort.Strings(ids)
	candidates := map[string]bool{}
	for sid := range own {
		candidates[sid] = true
	}
	historyRefs, err := skillsReferencedInHistory(ctx, tx, candidates)
	if err != nil {
		return err
	}
	// Run pins belong to the capability, not to one skill: count them once.
	var runPins int
	if len(ids) > 0 {
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM capability_run_bindings WHERE capability_id=?`, capID).Scan(&runPins); err != nil {
			return err
		}
	}
	for _, sid := range ids {
		var present int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM skills WHERE id=?`, sid).Scan(&present); err != nil {
			return err
		}
		if present == 0 {
			continue
		}
		reason := ""
		if runPins > 0 {
			reason = "retained by capability run history"
		}
		if shared[sid] {
			reason = "referenced by another capability snapshot"
		}
		if reason == "" {
			reason, err = skillRegistrationRetention(ctx, tx, sid, own[sid], historyRefs)
			if err != nil {
				return err
			}
		}
		if reason != "" {
			out.Retained = append(out.Retained, UninstallRetention{"skill", sid, reason})
			continue
		}
		out.RemovedSkills = append(out.RemovedSkills, sid)
	}
	out.Retained = append(out.Retained,
		UninstallRetention{"snapshots", capID, "immutable capability and skill versions retained for audit; snapshot cleanup requires explicit confirmation"},
		UninstallRetention{"managed_dependencies", capID, "tools and environments remain referenced by retained snapshots; use reviewed storage cleanup after snapshot cleanup"},
		UninstallRetention{"credentials", capID, "credentials require separate explicit revoke and clear confirmation"})
	return nil
}

// applySkillCleanup deletes the registrations a plan lists for removal. It must
// run in the same transaction that produced the plan.
func applySkillCleanup(ctx context.Context, tx *sql.Tx, plan UninstallResult) error {
	for _, sid := range plan.RemovedSkills {
		if _, err := tx.ExecContext(ctx, `DELETE FROM pointers WHERE entity_type='skill' AND entity_id=?`, sid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM skills WHERE id=?`, sid); err != nil {
			return err
		}
	}
	return nil
}

func skillRegistrationRetention(ctx context.Context, tx *sql.Tx, sid string, ownedVersions, historyRefs map[string]bool) (string, error) {
	var count int
	var registeredName string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM skills WHERE id=?`, sid).Scan(&registeredName); err != nil {
		return "", err
	}
	// A provenance marker alone is insufficient: all versions must also occur in
	// this capability's immutable locks. User edits and optimized versions survive.
	rows, err := tx.QueryContext(ctx, `SELECT id,content_json FROM versions WHERE entity_type='skill' AND entity_id=?`, sid)
	if err != nil {
		return "", err
	}
	versions := 0
	foreign := false
	for rows.Next() {
		var vid, raw string
		if err = rows.Scan(&vid, &raw); err != nil {
			rows.Close()
			return "", err
		}
		var manifest struct {
			Name string `json:"name"`
		}
		if err = json.Unmarshal([]byte(raw), &manifest); err != nil {
			rows.Close()
			return "", err
		}
		if manifest.Name != registeredName {
			foreign = true
		}
		versions++
		if !ownedVersions[vid] {
			foreign = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	if foreign || versions == 0 {
		return "skill has independent or modified versions", nil
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM operation_log WHERE entity_type='skill' AND entity_id=? AND actor='capability:ui'`, sid).Scan(&count); err != nil {
		return "", err
	}
	if count == 0 {
		return "capability ownership cannot be established", nil
	}
	checks := []struct{ query, reason string }{
		{`SELECT count(*) FROM skills WHERE id=? AND (optimize_mode<>'off' OR canary_ratio<>0.2 OR canary_runs<>10 OR trust_level<>'local' OR last_analyzed_at IS NOT NULL)`, "skill has customized settings"},
		{`SELECT count(*) FROM operation_log WHERE entity_type='skill' AND entity_id=? AND actor<>'capability:ui'`, "skill has independent user changes"},
		{`SELECT count(*) FROM run_skill_bindings WHERE skill_id=?`, "retained by run history"},
		{`SELECT count(*) FROM canary_runs WHERE skill_id=?`, "retained by canary history"},
		{`SELECT count(*) FROM skill_proposals WHERE skill_id=?`, "retained by skill proposals"},
		{`SELECT count(*) FROM skill_repository_bindings WHERE skill_id=?`, "retained by a repository binding"},
		{`SELECT count(*) FROM version_tags WHERE entity_type='skill' AND entity_id=?`, "retained by an explicit version tag"},
		{`SELECT count(*) FROM pointers WHERE entity_type='skill' AND entity_id=? AND name<>'current'`, "retained by an explicit version pointer"},
	}
	for _, check := range checks {
		if err = tx.QueryRowContext(ctx, check.query, sid).Scan(&count); err != nil {
			return "", err
		}
		if count > 0 {
			return check.reason, nil
		}
	}
	if historyRefs[sid] {
		return "retained by task, workflow, session or proposal history", nil
	}
	return "", nil
}

// historyRefQueries are JSON blobs that may pin skill ids across history. Retention
// must see historical rows (not only current pointers), so these stay broader than
// the active-only checks that block uninstall itself.
var historyRefQueries = []string{
	`SELECT content_json FROM versions WHERE entity_type IN ('task','skill_set')`,
	`SELECT definition_json FROM workflow_versions`,
	`SELECT snapshot_json FROM workflow_runs`,
	`SELECT bindings_json FROM chat_sessions`,
	// Only proposals that can still be applied: one being applied, or a pending one
	// that has not expired (ClaimProposal refuses expired ones) and whose session
	// still exists (confirming goes through the session). Expired pending rows keep
	// status 'pending' in the table, so the expiry is checked here.
	// Uninstall proposals never install or pin anything: their payload is the
	// reviewed removal summary itself. Counting them would make the confirmed
	// (now 'applying') proposal retain the very skills it removes, so the
	// recomputed summary never matches and every confirmation fails.
	`SELECT payload_json FROM capability_proposals WHERE kind<>'uninstall' AND (status='applying'
	 OR (status='pending' AND expires_at > strftime('%Y-%m-%dT%H:%M:%fZ','now')
	     AND EXISTS (SELECT 1 FROM chat_sessions s WHERE s.id=capability_proposals.session_id)))`,
}

// historyScanHook lets tests count full history scans.
var historyScanHook func()

// skillsReferencedInHistory scans each history table once for all candidate skill
// ids. A cheap strings.Contains gate skips JSON parsing for blobs that cannot
// mention any remaining candidate, so a large historical backlog is no longer
// O(skills × rows) full parses on every uninstall.
func skillsReferencedInHistory(ctx context.Context, tx *sql.Tx, candidates map[string]bool) (map[string]bool, error) {
	found := map[string]bool{}
	if len(candidates) == 0 {
		return found, nil
	}
	if historyScanHook != nil {
		historyScanHook()
	}
	ids := make([]string, 0, len(candidates))
	for id := range candidates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, query := range historyRefQueries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var raw sql.NullString
			if err = rows.Scan(&raw); err != nil {
				rows.Close()
				return nil, err
			}
			remaining := map[string]bool{}
			for _, id := range ids {
				if !found[id] {
					remaining[id] = true
				}
			}
			if len(remaining) == 0 {
				rows.Close()
				return found, nil
			}
			gate := false
			for id := range remaining {
				if strings.Contains(raw.String, id) {
					gate = true
					break
				}
			}
			if !gate {
				continue
			}
			markReferencedSkills(raw.String, remaining, found)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if len(found) == len(candidates) {
			return found, nil
		}
	}
	return found, nil
}

// markReferencedSkills records every candidate skill id that appears as a JSON
// string leaf. Malformed non-empty JSON fails closed for candidates whose id
// substring is present (the Contains gate already selected this row).
func markReferencedSkills(raw string, candidates, found map[string]bool) {
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		if raw == "" {
			return
		}
		for id := range candidates {
			if strings.Contains(raw, id) {
				found[id] = true
			}
		}
		return
	}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			if candidates[x] {
				found[x] = true
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		case map[string]any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	walk(value)
}

// Rollback after reinstall can rehydrate exclusive registrations removed during
// uninstall. Content remains immutable; conflicting names fail the transaction.
func restoreSnapshotSkills(ctx context.Context, tx *sql.Tx, lock Lock) error {
	for _, c := range lock.Components {
		if c.SkillID == "" || c.VersionID == "" {
			continue
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM skills WHERE id=?`, c.SkillID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		var raw, created string
		err := tx.QueryRowContext(ctx, `SELECT content_json,created_at FROM versions WHERE id=? AND entity_type='skill' AND entity_id=?`, c.VersionID, c.SkillID).Scan(&raw, &created)
		// Legacy snapshots may reference externally supplied skills that were never
		// registered here. They are not ours to fabricate.
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM operation_log WHERE entity_type='skill' AND entity_id=? AND actor='capability:ui'`, c.SkillID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return errors.New("missing skill registration has no capability ownership provenance")
		}
		var manifest struct {
			Name string `json:"name"`
		}
		if err = json.Unmarshal([]byte(raw), &manifest); err != nil {
			return err
		}
		if manifest.Name == "" {
			return errors.New("retained skill manifest has no name")
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO skills(id,user_id,name,optimize_mode,canary_ratio,canary_runs,trust_level,created_at) VALUES(?,'owner',?,'off',0.2,10,'local',?)`, c.SkillID, manifest.Name, created); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO pointers(entity_type,entity_id,name,version_id,updated_at) VALUES('skill',?,'current',?,?)`, c.SkillID, c.VersionID, created); err != nil {
			return err
		}
	}
	return nil
}

// PreviewUninstall computes the same reference-safe cleanup without deleting or
// changing registrations. The transaction is always rolled back.
func (s *Store) PreviewUninstall(ctx context.Context, capID, versionID string) (UninstallResult, error) {
	out := UninstallResult{RemovedSkills: []string{}, Retained: []UninstallRetention{}}
	if versionID == "" {
		return out, errors.New("exact version required")
	}
	err := s.uninstallPinned(ctx, capID, versionID, "preview", &out, true, nil)
	return out, err
}

// UninstallVersionWithReport refuses a stale owner-reviewed version under the
// same writer lock used for reference checks and removal.
func (s *Store) UninstallVersionWithReport(ctx context.Context, capID, versionID, actor string, reviewed ...UninstallResult) (UninstallResult, error) {
	out := UninstallResult{RemovedSkills: []string{}, Retained: []UninstallRetention{}}
	if versionID == "" {
		return out, errors.New("exact version required")
	}
	var expected *UninstallResult
	if len(reviewed) > 0 {
		expected = &reviewed[0]
	}
	err := s.uninstallPinned(ctx, capID, versionID, actor, &out, false, expected)
	out.OK = err == nil
	return out, err
}
