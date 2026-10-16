// Package version implements immutable content versions, pointers, tags, and append-only operation log (D1).
package version

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/id"
)

const (
	PointerCurrent = "current"
	PointerCanary  = "canary"

	EntityTask  = "task"
	EntitySkill = "skill"
)

// Service manages the shared version tables.
type Service struct {
	DB *sql.DB
}

func New(db *sql.DB) *Service { return &Service{DB: db} }

// Version is an immutable snapshot.
type Version struct {
	ID              string
	UserID          string
	EntityType      string
	EntityID        string
	VersionNo       int
	ContentHash     string
	ParentVersionID sql.NullString
	ContentJSON     string
	Source          string
	SourceRef       sql.NullString
	Message         sql.NullString
	Archived        bool
	CreatedAt       string
}

// OpLogEntry is one append-only operation_log row.
type OpLogEntry struct {
	ID            int64
	At            string
	BatchID       sql.NullString
	EntityType    string
	EntityID      string
	Action        string
	Pointer       sql.NullString
	FromVersionID sql.NullString
	ToVersionID   sql.NullString
	Actor         string
	Reason        sql.NullString
	DetailJSON    sql.NullString
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

// CommitInput describes a content change attempt.
type CommitInput struct {
	EntityType string
	EntityID   string
	Content    any // marshaled to content_json
	Source     string
	SourceRef  string
	Message    string
	Actor      string
	Reason     string
	BatchID    string
}

// CommitResult describes what happened.
type CommitResult struct {
	Version       *Version
	Created       bool // false if reused existing hash or no-op
	PointerMoved  bool
	SameAsCurrent bool
}

// Commit creates a new version if content hash is new, or moves pointer if hash matches an older version.
// If hash equals current, it's a no-op (no log).
func (s *Service) Commit(ctx context.Context, in CommitInput) (CommitResult, error) {
	if in.Actor == "" {
		in.Actor = "user"
	}
	if in.Source == "" {
		in.Source = "user_edit"
	}
	hash, raw, err := HashJSON(in.Content)
	if err != nil {
		return CommitResult{}, err
	}

	cur, err := s.GetPointer(ctx, in.EntityType, in.EntityID, PointerCurrent)
	if err != nil && err != sql.ErrNoRows {
		return CommitResult{}, err
	}
	var parentID sql.NullString
	var curVer *Version
	if err == nil && cur.Valid {
		parentID = cur
		curVer, err = s.GetVersion(ctx, cur.String)
		if err != nil {
			return CommitResult{}, err
		}
		if curVer.ContentHash == hash {
			return CommitResult{Version: curVer, SameAsCurrent: true}, nil
		}
	}

	// Reuse existing version with same hash?
	existing, err := s.FindByHash(ctx, in.EntityType, in.EntityID, hash)
	if err != nil && err != sql.ErrNoRows {
		return CommitResult{}, err
	}
	if err == nil {
		from := ""
		if cur.Valid {
			from = cur.String
		}
		if err := s.MovePointer(ctx, in.EntityType, in.EntityID, PointerCurrent, existing.ID, in.Actor, "content matches existing version", in.BatchID); err != nil {
			return CommitResult{}, err
		}
		_ = from
		return CommitResult{Version: existing, PointerMoved: true}, nil
	}

	nextNo, err := s.nextVersionNo(ctx, in.EntityType, in.EntityID)
	if err != nil {
		return CommitResult{}, err
	}
	vid := id.New("ver")
	createdAt := nowUTC()
	_, err = s.DB.ExecContext(ctx, `
INSERT INTO versions(id, user_id, entity_type, entity_id, version_no, content_hash, parent_version_id,
  content_json, source, source_ref, message, archived, created_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,0,?)`,
		vid, "owner", in.EntityType, in.EntityID, nextNo, hash, parentID,
		string(raw), in.Source, nullStr(in.SourceRef), nullStr(in.Message), createdAt,
	)
	if err != nil {
		return CommitResult{}, fmt.Errorf("insert version: %w", err)
	}
	ver := &Version{
		ID: vid, UserID: "owner", EntityType: in.EntityType, EntityID: in.EntityID,
		VersionNo: nextNo, ContentHash: hash, ParentVersionID: parentID,
		ContentJSON: string(raw), Source: in.Source, CreatedAt: createdAt,
	}
	if err := s.appendOp(ctx, in.EntityType, in.EntityID, "version_create", "", "", vid, in.Actor, in.Reason, in.BatchID, nil); err != nil {
		return CommitResult{}, err
	}
	if err := s.MovePointer(ctx, in.EntityType, in.EntityID, PointerCurrent, vid, in.Actor, in.Reason, in.BatchID); err != nil {
		return CommitResult{}, err
	}
	return CommitResult{Version: ver, Created: true, PointerMoved: true}, nil
}

func (s *Service) nextVersionNo(ctx context.Context, entityType, entityID string) (int, error) {
	var n sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `
SELECT MAX(version_no) FROM versions WHERE entity_type=? AND entity_id=?`, entityType, entityID).Scan(&n)
	if err != nil {
		return 0, err
	}
	if !n.Valid {
		return 1, nil
	}
	return int(n.Int64) + 1, nil
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// GetPointer returns the version_id for a named pointer.
func (s *Service) GetPointer(ctx context.Context, entityType, entityID, name string) (sql.NullString, error) {
	var v sql.NullString
	err := s.DB.QueryRowContext(ctx, `
SELECT version_id FROM pointers WHERE entity_type=? AND entity_id=? AND name=?`,
		entityType, entityID, name).Scan(&v)
	return v, err
}

// MovePointer sets a pointer (rollback = move current). Writes operation_log.
func (s *Service) MovePointer(ctx context.Context, entityType, entityID, pointerName, toVersionID, actor, reason, batchID string) error {
	from, _ := s.GetPointer(ctx, entityType, entityID, pointerName)
	now := nowUTC()
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO pointers(entity_type, entity_id, name, version_id, updated_at)
VALUES(?,?,?,?,?)
ON CONFLICT(entity_type, entity_id, name) DO UPDATE SET version_id=excluded.version_id, updated_at=excluded.updated_at`,
		entityType, entityID, pointerName, nullStr(toVersionID), now,
	)
	if err != nil {
		return err
	}
	fromID := ""
	if from.Valid {
		fromID = from.String
	}
	return s.appendOp(ctx, entityType, entityID, "pointer_move", pointerName, fromID, toVersionID, actor, reason, batchID, nil)
}

// SetTag moves/creates a tag pointing at a version.
func (s *Service) SetTag(ctx context.Context, entityType, entityID, tag, versionID, actor, reason string) error {
	now := nowUTC()
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO version_tags(entity_type, entity_id, tag, version_id, updated_at)
VALUES(?,?,?,?,?)
ON CONFLICT(entity_type, entity_id, tag) DO UPDATE SET version_id=excluded.version_id, updated_at=excluded.updated_at`,
		entityType, entityID, tag, versionID, now,
	)
	if err != nil {
		return err
	}
	return s.appendOp(ctx, entityType, entityID, "tag_set", "", "", versionID, actor, reason, "", map[string]string{"tag": tag})
}

// GetVersion loads one version by id.
func (s *Service) GetVersion(ctx context.Context, versionID string) (*Version, error) {
	row := s.DB.QueryRowContext(ctx, `
SELECT id, user_id, entity_type, entity_id, version_no, content_hash, parent_version_id,
  content_json, source, source_ref, message, archived, created_at
FROM versions WHERE id=?`, versionID)
	return scanVersion(row)
}

// FindByHash finds a version by content hash within an entity.
func (s *Service) FindByHash(ctx context.Context, entityType, entityID, hash string) (*Version, error) {
	row := s.DB.QueryRowContext(ctx, `
SELECT id, user_id, entity_type, entity_id, version_no, content_hash, parent_version_id,
  content_json, source, source_ref, message, archived, created_at
FROM versions WHERE entity_type=? AND entity_id=? AND content_hash=?`, entityType, entityID, hash)
	return scanVersion(row)
}

// ListVersions returns versions for an entity ordered by version_no.
func (s *Service) ListVersions(ctx context.Context, entityType, entityID string) ([]Version, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, user_id, entity_type, entity_id, version_no, content_hash, parent_version_id,
  content_json, source, source_ref, message, archived, created_at
FROM versions WHERE entity_type=? AND entity_id=? ORDER BY version_no`, entityType, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Version
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// CurrentVersion returns the version at current pointer, if any.
func (s *Service) CurrentVersion(ctx context.Context, entityType, entityID string) (*Version, error) {
	p, err := s.GetPointer(ctx, entityType, entityID, PointerCurrent)
	if err != nil {
		return nil, err
	}
	if !p.Valid || p.String == "" {
		return nil, sql.ErrNoRows
	}
	return s.GetVersion(ctx, p.String)
}

// ListOpLog returns recent operation log entries for an entity.
func (s *Service) ListOpLog(ctx context.Context, entityType, entityID string, limit int) ([]OpLogEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, at, batch_id, entity_type, entity_id, action, pointer, from_version_id, to_version_id, actor, reason, detail_json
FROM operation_log WHERE entity_type=? AND entity_id=? ORDER BY id DESC LIMIT ?`, entityType, entityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OpLogEntry
	for rows.Next() {
		var e OpLogEntry
		if err := rows.Scan(&e.ID, &e.At, &e.BatchID, &e.EntityType, &e.EntityID, &e.Action, &e.Pointer,
			&e.FromVersionID, &e.ToVersionID, &e.Actor, &e.Reason, &e.DetailJSON); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) appendOp(ctx context.Context, entityType, entityID, action, pointer, fromID, toID, actor, reason, batchID string, detail any) error {
	var detailJSON sql.NullString
	if detail != nil {
		b, err := json.Marshal(detail)
		if err != nil {
			return err
		}
		detailJSON = sql.NullString{String: string(b), Valid: true}
	}
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO operation_log(user_id, at, batch_id, entity_type, entity_id, action, pointer, from_version_id, to_version_id, actor, reason, detail_json)
VALUES('owner',?,?,?,?,?,?,?,?,?,?,?)`,
		nowUTC(), nullStr(batchID), entityType, entityID, action, nullStr(pointer), nullStr(fromID), nullStr(toID), actor, nullStr(reason), detailJSON,
	)
	return err
}

type scannable interface {
	Scan(dest ...any) error
}

func scanVersion(row scannable) (*Version, error) {
	var v Version
	var archived int
	if err := row.Scan(&v.ID, &v.UserID, &v.EntityType, &v.EntityID, &v.VersionNo, &v.ContentHash, &v.ParentVersionID,
		&v.ContentJSON, &v.Source, &v.SourceRef, &v.Message, &archived, &v.CreatedAt); err != nil {
		return nil, err
	}
	v.Archived = archived != 0
	return &v, nil
}

// AssertOpLogAppendOnly tries UPDATE/DELETE and expects failure (for tests).
func (s *Service) AssertOpLogAppendOnly(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE operation_log SET reason='x' WHERE 1=0`)
	// empty update might not fire trigger on some engines; insert then update
	_, err = s.DB.ExecContext(ctx, `
INSERT INTO operation_log(user_id, at, entity_type, entity_id, action, actor) VALUES('owner',?,?,?,?,?)`,
		nowUTC(), "task", "probe", "probe", "system")
	if err != nil {
		return err
	}
	var id int64
	_ = s.DB.QueryRowContext(ctx, `SELECT id FROM operation_log WHERE entity_id='probe' ORDER BY id DESC LIMIT 1`).Scan(&id)
	_, err = s.DB.ExecContext(ctx, `UPDATE operation_log SET reason='mutated' WHERE id=?`, id)
	if err == nil {
		return fmt.Errorf("expected update to be blocked")
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM operation_log WHERE id=?`, id)
	if err == nil {
		return fmt.Errorf("expected delete to be blocked")
	}
	return nil
}
