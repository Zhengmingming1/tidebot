package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zhengmingming1/tidebot/internal/safepath"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/capability/managed"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

var capStorageID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type capabilityStorageTool struct {
	Name       string `json:"name"`
	Source     string `json:"source"`
	Path       string `json:"path"`
	Version    string `json:"version"`
	Directory  string `json:"directory,omitempty"`
	Bytes      int64  `json:"bytes"`
	References int    `json:"references"`
}
type capabilityStorageView struct {
	Usage         managed.Usage           `json:"usage"`
	SnapshotBytes int64                   `json:"snapshot_bytes"`
	Tools         []capabilityStorageTool `json:"tools"`
	Limits        map[string]int64        `json:"limits"`
	Warnings      []string                `json:"warnings"`
}

// This owner-only endpoint is deliberately absent from session/model tools.
// Collection is explicit, never an automatic response to a quota warning.
func (a *App) capabilityStorageRequest(ctx context.Context, action string, r webui.CapabilityRequest) (any, error) {
	if action != "storage" && action != "storage_gc" {
		return nil, capError(400, "unsupported", "unsupported storage action")
	}
	if action == "storage_gc" && !r.Reviewed {
		return nil, capError(400, "confirmation_required", "explicit storage cleanup confirmation required")
	}
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if action == "storage_gc" {
		// Acquire SQLite's writer lock before inspecting proposals/references. A new
		// installer must claim its proposal before creating managed directories, so
		// it cannot race this collection. Existing installers cause a safe refusal.
		if _, err = tx.ExecContext(ctx, `UPDATE capabilities SET updated_at=updated_at WHERE 0`); err != nil {
			return nil, err
		}
		var applying int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM capability_proposals WHERE status='applying'`).Scan(&applying); err != nil {
			return nil, err
		}
		if applying != 0 {
			return nil, capError(409, "installation_in_progress", "wait for capability installation to finish before cleaning storage")
		}
	}
	refs, tools, err := a.capabilityStorageReferences(ctx, tx)
	if err != nil {
		return nil, err
	}
	installer := &managed.Installer{DataDir: a.Cfg.DataDir}
	if action == "storage_gc" {
		// Every persisted version is retained, not just current/installed versions.
		// Snapshot GC must remove obsolete locks before their dependencies can go.
		report, err := installer.GC(ctx, managed.GCOptions{References: refs, KeepPrevious: 1})
		if err != nil {
			return map[string]any{"report": report}, err
		}
		return report, tx.Commit()
	}
	// The view only reads. Release the pool's single connection before walking
	// managed directories, which can hold tens of thousands of files: holding it
	// would stall every other request and the scheduler for the whole walk.
	if err = tx.Rollback(); err != nil {
		return nil, err
	}
	if err = capabilityStorageToolSizes(ctx, tools); err != nil {
		return nil, err
	}
	// Request unlimited read-only accounting so an exceeded earlier category
	// does not hide subsequent categories from the UI.
	installer.Quotas = managed.Quotas{ToolsAndRuntimes: math.MaxInt64, Environment: math.MaxInt64, Cache: math.MaxInt64}
	usage, err := installer.CheckQuota(ctx)
	if err != nil {
		return nil, err
	}
	snapshots, err := a.caps().SnapshotUsage(ctx)
	if err != nil {
		return nil, err
	}
	out := capabilityStorageView{Usage: usage, SnapshotBytes: snapshots, Tools: tools, Limits: map[string]int64{"tools_and_runtimes": managed.DefaultToolsQuota, "cache": managed.DefaultCacheQuota, "environment": managed.DefaultEnvQuota}, Warnings: []string{}}
	if usage.ToolsAndRuntimes > managed.DefaultToolsQuota {
		out.Warnings = append(out.Warnings, "tools_and_runtimes")
	}
	if usage.Cache > managed.DefaultCacheQuota {
		out.Warnings = append(out.Warnings, "cache")
	}
	for name, bytes := range usage.Environments {
		if bytes > managed.DefaultEnvQuota {
			out.Warnings = append(out.Warnings, "environment:"+name)
		}
	}
	sort.Strings(out.Warnings)
	return out, nil
}

func (a *App) capabilityStorageReferences(ctx context.Context, tx *sql.Tx) ([]managed.Reference, []capabilityStorageTool, error) {
	// Check pin integrity explicitly rather than silently dropping a dangling pin
	// through an inner join. Include historical runs as well as active runs.
	var orphan int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM capability_run_bindings b LEFT JOIN capability_versions v ON v.id=b.version_id AND v.capability_id=b.capability_id WHERE v.id IS NULL`).Scan(&orphan)
	if err != nil {
		return nil, nil, err
	}
	if orphan != 0 {
		return nil, nil, errors.New("cannot collect storage with unresolved capability run pins")
	}
	rows, err := tx.QueryContext(ctx, `SELECT lock_json,lock_sha256 FROM capability_versions ORDER BY capability_id,id`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	refs := map[managed.Reference]bool{}
	inventory := map[string]*capabilityStorageTool{}
	for rows.Next() {
		var raw, digest string
		if err = rows.Scan(&raw, &digest); err != nil {
			return nil, nil, err
		}
		sum := sha256.Sum256([]byte(raw))
		if hex.EncodeToString(sum[:]) != digest {
			return nil, nil, errors.New("capability lock integrity mismatch; storage cleanup refused")
		}
		var lock capability.Lock
		if err = json.Unmarshal([]byte(raw), &lock); err != nil {
			return nil, nil, err
		}
		for name, resolved := range lock.Resolved {
			key := name + "\x00" + resolved.Source + "\x00" + resolved.Path + "\x00" + resolved.Version
			item := inventory[key]
			if item == nil {
				item = &capabilityStorageTool{Name: name, Source: resolved.Source, Path: resolved.Path, Version: resolved.Version}
				inventory[key] = item
			}
			item.References++
			if resolved.Source != "managed" {
				continue
			}
			ref, dir, e := capabilityManagedReference(a.Cfg.DataDir, resolved)
			if e != nil {
				return nil, nil, fmt.Errorf("managed reference %s: %w", name, e)
			}
			refs[ref] = true
			item.Directory = dir
		}
	}
	if err = rows.Err(); err != nil {
		return nil, nil, err
	}
	references := make([]managed.Reference, 0, len(refs))
	for ref := range refs {
		references = append(references, ref)
	}
	sort.Slice(references, func(i, j int) bool {
		a, b := references[i], references[j]
		return a.Kind+"/"+a.Name+"/"+a.Version < b.Kind+"/"+b.Name+"/"+b.Version
	})
	tools := make([]capabilityStorageTool, 0, len(inventory))
	for _, item := range inventory {
		tools = append(tools, *item)
	}
	sort.Slice(tools, func(i, j int) bool {
		a, b := tools[i], tools[j]
		return a.Name+"/"+a.Path+"/"+a.Version < b.Name+"/"+b.Path+"/"+b.Version
	})
	return references, tools, nil
}

// capabilityStorageWalk measures one managed directory (a variable for tests).
var capabilityStorageWalk = capabilityStorageSize

// capabilityStorageToolSizes fills in sizes outside any transaction. Commands
// of one runtime (node, npm, npx) and the versions that pin it share a
// directory, which is walked once.
func capabilityStorageToolSizes(ctx context.Context, tools []capabilityStorageTool) error {
	sizes := map[string]int64{}
	for i := range tools {
		dir := tools[i].Directory
		if dir == "" {
			continue
		}
		size, ok := sizes[dir]
		if !ok {
			var err error
			if size, err = capabilityStorageWalk(ctx, dir); err != nil {
				return err
			}
			sizes[dir] = size
		}
		tools[i].Bytes = size
	}
	return nil
}

func capabilityManagedReference(root string, resolved capability.Resolved) (managed.Reference, string, error) {
	target := resolved.Directory
	if target == "" {
		target = resolved.Path
	}
	if !filepath.IsAbs(root) || !filepath.IsAbs(target) {
		return managed.Reference{}, "", errors.New("absolute managed path required")
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return managed.Reference{}, "", err
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 3 || resolved.Directory != "" && len(parts) != 3 {
		return managed.Reference{}, "", errors.New("invalid managed directory depth")
	}
	if parts[0] != "tools" && parts[0] != "runtimes" && parts[0] != "envs" {
		return managed.Reference{}, "", errors.New("path is outside managed storage")
	}
	for _, part := range parts[:3] {
		if part == "." || part == ".." || !capStorageID.MatchString(part) {
			return managed.Reference{}, "", errors.New("invalid managed identity")
		}
	}
	dir := filepath.Join(root, parts[0], parts[1], parts[2])
	if resolved.Path != "" {
		executable, err := filepath.Rel(dir, resolved.Path)
		if err != nil || !filepath.IsAbs(resolved.Path) || executable == ".." || strings.HasPrefix(executable, ".."+string(os.PathSeparator)) {
			return managed.Reference{}, "", errors.New("managed executable outside its directory")
		}
	}
	return managed.Reference{Kind: parts[0], Name: parts[1], Version: parts[2]}, dir, nil
}

// Accounting never follows symlinks, including a replaced ancestor directory.
func capabilityStorageSize(ctx context.Context, root string) (int64, error) {
	for parent := root; ; parent = filepath.Dir(parent) {
		info, err := safepath.LstatDirectory(parent)
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return 0, errors.New("managed storage ancestor is not a directory")
		}
		if filepath.Dir(parent) == parent {
			break
		}
	}
	var size int64
	err := filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		// The view walks without a database lock, so a concurrent cleanup may
		// remove entries mid-walk; those simply no longer count.
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if !d.IsDir() {
			info, e := d.Info()
			if errors.Is(e, os.ErrNotExist) {
				return nil
			}
			if e != nil {
				return e
			}
			size += info.Size()
		}
		return nil
	})
	return size, err
}
