package capability

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zhengmingming1/tidebot/internal/safepath"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/id"
)

type Store struct {
	DB      *sql.DB
	DataDir string
	// MaxSnapshotDiskBytes defaults to 2 GiB. A negative value disables the quota.
	MaxSnapshotDiskBytes int64
	// ScanSource is a trusted application scanner that includes adapter-specific
	// findings. It must derive its result from the supplied immutable files.
	ScanSource func(Source, map[string][]byte) (Scan, error)
}

// installMu serializes snapshot installs between the quota check and commit.
// Stores are created per request, so the lock is package level.
var installMu sync.Mutex

// snapshotUsage is replaced by tests to observe the quota walk.
var snapshotUsage = func(s *Store, ctx context.Context) (int64, error) { return s.SnapshotUsage(ctx) }

func NewStore(db *sql.DB, dataDir string) *Store { return &Store{DB: db, DataDir: dataDir} }

type Record struct {
	LocallyModified bool                  `json:"locally_modified"`
	LocalSkills     []SkillReconciliation `json:"local_skills,omitempty"`
	ID              string                `json:"id"`
	Title           string                `json:"title"`
	VersionID       string                `json:"version_id"`
	CreatedAt       string                `json:"created_at"`
	UpdatedAt       string                `json:"updated_at"`
	Lock            Lock                  `json:"lock"`
}
type Event struct {
	ID           string `json:"id"`
	CapabilityID string `json:"capability_id"`
	VersionID    string `json:"version_id"`
	Actor        string `json:"actor"`
	Action       string `json:"action"`
	Verdict      string `json:"verdict"`
	Result       string `json:"result"`
	CreatedAt    string `json:"created_at"`
}

func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func safePath(p string) bool {
	return p != "" && p != "." && path.Clean(p) == p && !path.IsAbs(p) && p != ".." && !strings.HasPrefix(p, "../") && !strings.ContainsAny(p, "\\:\x00\r\n")
}

// HashFiles uses the same canonical JSON path-to-digest map as skill manifests.
func HashFiles(files map[string][]byte) (map[string]string, string, error) {
	if len(files) == 0 || len(files) > MaxFiles {
		return nil, "", errors.New("invalid snapshot file count")
	}
	hashes := make(map[string]string, len(files))
	folded := map[string]bool{}
	total := 0
	for p, b := range files {
		if !safePath(p) {
			return nil, "", fmt.Errorf("unsafe snapshot path %q", p)
		}
		lower := strings.ToLower(p)
		if folded[lower] {
			return nil, "", errors.New("case-colliding snapshot paths")
		}
		folded[lower] = true
		for _, part := range strings.Split(p, "/") {
			stem := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
			if strings.TrimRight(part, " .") != part || stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9' {
				return nil, "", errors.New("nonportable snapshot path")
			}
		}
		total += len(b)
		if len(b) > MaxFileBytes || total > MaxSnapshotBytes {
			return nil, "", errors.New("snapshot exceeds size limit")
		}
		hashes[p] = digest(b)
	}
	raw, _ := json.Marshal(hashes)
	return hashes, digest(raw), nil
}

// Install publishes a new immutable snapshot and database version together. It
// does not install dependencies or register skills: the caller does those in its
// reviewed installation workflow and passes the resulting component references.
func (s *Store) Install(ctx context.Context, lock Lock, files map[string][]byte) (Record, error) {
	return s.install(ctx, "", "", "", lock, files, nil)
}

// InstallProposal atomically adopts a claimed proposal, snapshot and session binding.
func (s *Store) InstallProposal(ctx context.Context, proposalID, sessionID, actor string, lock Lock, files map[string][]byte) (Record, error) {
	if proposalID == "" || sessionID == "" || actor == "" {
		return Record{}, errors.New("proposal identity required")
	}
	return s.install(ctx, proposalID, sessionID, actor, lock, files, nil)
}

// InstallProposalReconciled adopts reviewed local choices along with the new lock.
func (s *Store) InstallProposalReconciled(ctx context.Context, proposalID, sessionID, actor string, lock Lock, files map[string][]byte, resolutions map[string]SkillResolution) (Record, error) {
	if proposalID == "" || sessionID == "" || actor == "" {
		return Record{}, errors.New("proposal identity required")
	}
	return s.install(ctx, proposalID, sessionID, actor, lock, files, resolutions)
}
func (s *Store) install(ctx context.Context, proposalID, sessionID, actor string, lock Lock, files map[string][]byte, resolutions map[string]SkillResolution) (Record, error) {
	var empty Record
	reviewedLock := lock
	action := "install"
	if err := lock.Validate(); err != nil {
		return empty, err
	}
	hashes, hash, err := HashFiles(files)
	if err != nil {
		return empty, err
	}
	if lock.FilesSHA256 != "" && lock.FilesSHA256 != hash {
		return empty, errors.New("snapshot differs from reviewed files")
	}
	lock.FilesSHA256 = hash
	lock.Scan, err = s.scanSource(lock.Source, files)
	if err != nil {
		return empty, err
	}
	if proposalID != "" {
		lock.InstalledBy = actor
	}
	lock.InstalledAt = time.Now().UTC().Format(time.RFC3339Nano)
	raw, err := json.Marshal(lock)
	if err != nil {
		return empty, err
	}
	hashRaw, _ := json.Marshal(hashes)
	versionID := id.New("capver")
	base := filepath.Join(s.DataDir, "capabilities", lock.ID)
	if err = mkdirNoSymlink(base); err != nil {
		return empty, err
	}
	staging, err := os.MkdirTemp(base, ".stage-")
	if err != nil {
		return empty, err
	}
	defer os.RemoveAll(staging)
	for p, b := range files {
		target := filepath.Join(staging, "src", filepath.FromSlash(p))
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return empty, err
		}
		if err = os.WriteFile(target, b, 0444); err != nil {
			return empty, err
		}
	}
	if err = os.WriteFile(filepath.Join(staging, "capability.lock.json"), raw, 0444); err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	destination := filepath.Join(base, versionID)
	if err = os.Rename(staging, destination); err != nil {
		return empty, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(destination)
		}
	}()
	// The quota walk covers the whole capabilities/ tree, which can take a
	// long time. The pool has a single connection, so walk before BeginTx;
	// otherwise every other request waits for the disk walk. installMu keeps
	// installs serialized from the quota check to commit, as the held
	// connection used to.
	installMu.Lock()
	defer installMu.Unlock()
	limit := s.MaxSnapshotDiskBytes
	if limit == 0 {
		limit = 2 << 30
	}
	if limit > 0 {
		usage, e := snapshotUsage(s, ctx)
		if e != nil {
			return empty, e
		}
		if usage > limit {
			return empty, errors.New("capability snapshot disk quota exceeded")
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if proposalID != "" {
		var originalRaw, kind, baseVersion string
		if err = tx.QueryRowContext(ctx, `SELECT payload_json,kind,base_version_id FROM capability_proposals WHERE id=? AND session_id=? AND status='applying' AND decided_by=?`, proposalID, sessionID, actor).Scan(&originalRaw, &kind, &baseVersion); err != nil {
			return empty, ErrProposalConflict
		}
		var original Lock
		if err = json.Unmarshal([]byte(originalRaw), &original); err != nil {
			return empty, err
		}
		// Registration assigns IDs, but cannot change the reviewed capability intent.
		normalized := reviewedLock
		normalized.Components = append([]Component(nil), reviewedLock.Components...)
		for i := range normalized.Components {
			normalized.Components[i].SkillID = ""
			normalized.Components[i].VersionID = ""
			normalized.Components[i].FilesSHA256 = ""
			normalized.Components[i].LocallyModified = false
		}
		original.Components = append([]Component(nil), original.Components...)
		for i := range original.Components {
			original.Components[i].SkillID = ""
			original.Components[i].VersionID = ""
			original.Components[i].FilesSHA256 = ""
			original.Components[i].LocallyModified = false
		}
		normalized.Resolved = nil
		original.Resolved = nil
		normalized.InstalledBy = ""
		normalized.InstalledAt = ""
		original.InstalledBy = ""
		original.InstalledAt = ""
		a, _ := json.Marshal(original)
		b, _ := json.Marshal(normalized)
		if string(a) != string(b) {
			return empty, errors.New("capability differs from reviewed proposal")
		}
		action = kind
		if kind != "install" && kind != "update" {
			return empty, errors.New("proposal cannot install a snapshot")
		}
		if kind == "update" {
			var current string
			if err = tx.QueryRowContext(ctx, `SELECT current_version_id FROM capabilities WHERE id=? AND status='installed'`, lock.ID).Scan(&current); err != nil {
				return empty, err
			}
			if current != baseVersion {
				return empty, errors.New("capability changed since update review")
			}
			var payload proposalPayload
			if err = json.Unmarshal([]byte(originalRaw), &payload); err != nil {
				return empty, err
			}
			if err = applySkillReconciliation(ctx, tx, payload, lock, resolutions, lock.InstalledAt); err != nil {
				return empty, err
			}
		}
		if kind == "install" {
			var count int
			if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM capabilities WHERE id=? AND status!='uninstalled'`, lock.ID).Scan(&count); err != nil {
				return empty, err
			}
			if count != 0 {
				return empty, errors.New("capability already installed")
			}
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO capabilities(id,title,current_version_id,created_at,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET title=excluded.title,current_version_id=excluded.current_version_id,updated_at=excluded.updated_at,status='installed'`, lock.ID, lock.Title, versionID, lock.InstalledAt, lock.InstalledAt)
	if err != nil {
		return empty, err
	}
	// Installing or updating never carries external-tool grants across versions.
	// Delete old grants as well so a later rollback cannot reactivate them.
	if _, err = tx.ExecContext(ctx, `DELETE FROM capability_tool_grants WHERE capability_id=?`, lock.ID); err != nil {
		return empty, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO capability_versions(id,capability_id,lock_json,lock_sha256,files_json,created_at) VALUES(?,?,?,?,?,?)`, versionID, lock.ID, string(raw), digest(raw), string(hashRaw), lock.InstalledAt)
	if err != nil {
		return empty, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO capability_events(id,capability_id,version_id,actor,action,verdict,result,created_at) VALUES(?,?,?,?,?,?,?,?)`, id.New("capevt"), lock.ID, versionID, lock.InstalledBy, action, lock.Scan.Verdict, "installed", lock.InstalledAt)
	if err != nil {
		return empty, err
	}
	if proposalID != "" {
		var originalRaw string
		if err = tx.QueryRowContext(ctx, `SELECT payload_json FROM capability_proposals WHERE id=? AND session_id=? AND status='applying' AND decided_by=?`, proposalID, sessionID, actor).Scan(&originalRaw); err != nil {
			return empty, ErrProposalConflict
		}
		var original Lock
		if err = json.Unmarshal([]byte(originalRaw), &original); err != nil {
			return empty, err
		}
		if original.ID != lock.ID || original.FilesSHA256 != lock.FilesSHA256 {
			return empty, errors.New("proposal snapshot mismatch")
		}
		var bindingsRaw string
		if err = tx.QueryRowContext(ctx, `SELECT bindings_json FROM chat_sessions WHERE id=? AND status!='closed'`, sessionID).Scan(&bindingsRaw); err != nil {
			return empty, err
		}
		bindings := struct {
			Skills       []string `json:"skills,omitempty"`
			Capabilities []string `json:"capabilities,omitempty"`
		}{}
		if strings.TrimSpace(bindingsRaw) != "[]" {
			if err = json.Unmarshal([]byte(bindingsRaw), &bindings); err != nil {
				return empty, err
			}
		}
		found := false
		for _, b := range bindings.Capabilities {
			if b == lock.ID {
				found = true
			}
		}
		if !found {
			bindings.Capabilities = append(bindings.Capabilities, lock.ID)
		}
		if len(bindings.Capabilities) > 16 || len(bindings.Skills) > 16 {
			return empty, errors.New("session binding limit exceeded")
		}
		bindingJSON, _ := json.Marshal(bindings)
		if _, err = tx.ExecContext(ctx, `UPDATE chat_sessions SET bindings_json=?,updated_at=? WHERE id=?`, string(bindingJSON), lock.InstalledAt, sessionID); err != nil {
			return empty, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE capability_proposals SET status='applied',decided_at=? WHERE id=? AND status='applying'`, lock.InstalledAt, proposalID); err != nil {
			return empty, err
		}
	}
	var createdAt string
	if err = tx.QueryRowContext(ctx, `SELECT created_at FROM capabilities WHERE id=?`, lock.ID).Scan(&createdAt); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	committed = true
	result := Record{ID: lock.ID, Title: lock.Title, VersionID: versionID, CreatedAt: createdAt, UpdatedAt: lock.InstalledAt, Lock: lock}
	for _, component := range lock.Components {
		result.LocallyModified = result.LocallyModified || component.LocallyModified
	}
	return result, nil
}

// Reject existing symlinks all the way to the filesystem root. This is not a
// sandbox against another process running as the same OS user.
func mkdirNoSymlink(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	parent := filepath.Dir(abs)
	if parent != abs {
		if err = mkdirNoSymlink(parent); err != nil {
			return err
		}
	}
	info, err := safepath.LstatDirectory(abs)
	if errors.Is(err, os.ErrNotExist) {
		return os.Mkdir(abs, 0700)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe snapshot directory %s", abs)
	}
	return nil
}
func (s *Store) Get(ctx context.Context, capID string) (Record, error) {
	var r Record
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT c.id,c.title,c.current_version_id,c.created_at,c.updated_at,v.lock_json FROM capabilities c JOIN capability_versions v ON v.id=c.current_version_id WHERE c.id=? AND c.status='installed'`, capID).Scan(&r.ID, &r.Title, &r.VersionID, &r.CreatedAt, &r.UpdatedAt, &raw)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &r.Lock)
	}
	if err == nil {
		err = s.populateLocalSkills(ctx, &r)
	}
	return r, err
}
func (s *Store) List(ctx context.Context) ([]Record, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT c.id,c.title,c.current_version_id,c.created_at,c.updated_at,v.lock_json FROM capabilities c JOIN capability_versions v ON v.id=c.current_version_id WHERE c.status='installed' ORDER BY c.title,c.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		var r Record
		var raw string
		if err = rows.Scan(&r.ID, &r.Title, &r.VersionID, &r.CreatedAt, &r.UpdatedAt, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &r.Lock); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		if err = s.populateLocalSkills(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (s *Store) Verify(ctx context.Context, capID, versionID string) error {
	if !safeID.MatchString(capID) || !safeID.MatchString(versionID) {
		return errors.New("invalid snapshot identity")
	}
	var lockHash, filesRaw string
	if err := s.DB.QueryRowContext(ctx, `SELECT lock_sha256,files_json FROM capability_versions WHERE capability_id=? AND id=?`, capID, versionID).Scan(&lockHash, &filesRaw); err != nil {
		return err
	}
	base := filepath.Join(s.DataDir, "capabilities", capID, versionID)
	if err := checkExistingDirectory(base); err != nil {
		return err
	}
	var hashes map[string]string
	if err := json.Unmarshal([]byte(filesRaw), &hashes); err != nil {
		return err
	}
	expected := map[string]string{"capability.lock.json": lockHash}
	for p, h := range hashes {
		expected["src/"+p] = h
	}
	seen := 0
	err := filepath.WalkDir(base, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("snapshot contains symlink")
		}
		if d.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(base, p)
		if e != nil {
			return e
		}
		hash, ok := expected[filepath.ToSlash(rel)]
		if !ok {
			return fmt.Errorf("unexpected snapshot file %s", rel)
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() || info.Size() > MaxSnapshotBytes {
			return errors.New("invalid snapshot file")
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		if digest(b) != hash {
			return fmt.Errorf("snapshot modified: %s", rel)
		}
		seen++
		return nil
	})
	if err != nil {
		return err
	}
	if seen != len(expected) {
		return errors.New("snapshot files missing")
	}
	return nil
}
func (s *Store) Events(ctx context.Context, capID string) ([]Event, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,capability_id,version_id,actor,action,verdict,result,created_at FROM capability_events WHERE capability_id=? ORDER BY created_at DESC,id DESC LIMIT 200`, capID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err = rows.Scan(&e.ID, &e.CapabilityID, &e.VersionID, &e.Actor, &e.Action, &e.Verdict, &e.Result, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func sortedPaths(files map[string][]byte) []string {
	p := make([]string, 0, len(files))
	for k := range files {
		p = append(p, k)
	}
	sort.Strings(p)
	return p
}

// scanSource never adopts scan metadata supplied by a manifest or bundle.
func (s *Store) scanSource(source Source, files map[string][]byte) (Scan, error) {
	if s.ScanSource != nil {
		return s.ScanSource(source, files)
	}
	switch source.Adapter {
	case "", "skill-dir", "tidebot-native":
		return ScanFiles(files), nil
	default:
		return Scan{}, errors.New("adapter-aware capability scanner unavailable")
	}
}
