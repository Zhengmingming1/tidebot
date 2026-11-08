package managed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/safepath"
)

const DefaultToolsQuota int64 = 2 << 30
const DefaultEnvQuota int64 = 500 << 20
const DefaultCacheQuota int64 = 1 << 30
const ownerFile = ".tidebot-managed.json"
const cacheOwnerFile = ".tidebot-cache.json"

var ErrQuotaExceeded = errors.New("managed storage quota exceeded")

type Quotas struct {
	ToolsAndRuntimes int64
	Environment      int64
	Cache            int64
}
type Usage struct {
	ToolsAndRuntimes int64            `json:"tools_and_runtimes"`
	Cache            int64            `json:"cache"`
	Environments     map[string]int64 `json:"environments"`
}
type Reference struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Version string `json:"version"`
}
type ownership struct {
	Format string `json:"format"`
	Reference
	Created  time.Time         `json:"created"`
	LastUsed time.Time         `json:"last_used"`
	Links    map[string]string `json:"links,omitempty"`
}

func (i *Installer) limits() Quotas {
	q := i.Quotas
	if q.ToolsAndRuntimes <= 0 {
		q.ToolsAndRuntimes = DefaultToolsQuota
	}
	if q.Environment <= 0 {
		q.Environment = DefaultEnvQuota
	}
	if q.Cache <= 0 {
		q.Cache = DefaultCacheQuota
	}
	return q
}
func validReference(r Reference) bool {
	return (r.Kind == "tools" || r.Kind == "runtimes" || r.Kind == "envs") && validID(r.Name) && validID(r.Version)
}
func treeSize(ctx context.Context, root string) (int64, error) {
	var size int64
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		size += info.Size()
		return nil
	})
	if os.IsNotExist(err) {
		return 0, nil
	}
	return size, err
}

// measureTree is the CheckQuota size walk. Tests replace it to count walks.
var measureTree = treeSize

type quotaCacheEntry struct {
	stamp string
	usage Usage
}

var quotaCacheMu sync.Mutex
var quotaCache = map[string]quotaCacheEntry{}

func copyUsage(u Usage) Usage {
	out := Usage{ToolsAndRuntimes: u.ToolsAndRuntimes, Cache: u.Cache, Environments: map[string]int64{}}
	for k, v := range u.Environments {
		out.Environments[k] = v
	}
	return out
}

// quotaDiskStamp is a shallow mtime fingerprint of tools/runtimes/envs/cache.
// It lists kind → name → version (and .install-* stages) without entering
// version contents, so it stays cheap when a runtime has tens of thousands of
// files. Creating/removing a staged or promoted version changes the stamp;
// in-place byte edits deep inside a version are not expected on this path.
func quotaDiskStamp(dataDir string) (string, error) {
	var b strings.Builder
	for _, kind := range []string{"tools", "runtimes", "envs", "cache"} {
		root := filepath.Join(dataDir, kind)
		info, err := os.Lstat(root)
		if os.IsNotExist(err) {
			b.WriteString(kind + "=missing\n")
			continue
		}
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s=%d\n", kind, info.ModTime().UnixNano())
		entries, err := os.ReadDir(root)
		if err != nil {
			return "", err
		}
		for _, e := range entries {
			info, err := stampInfo(filepath.Join(root, e.Name()))
			if err != nil {
				return "", err
			}
			if !e.IsDir() {
				fmt.Fprintf(&b, "%s/%s=f%d:%d\n", kind, e.Name(), info.ModTime().UnixNano(), info.Size())
				continue
			}
			fmt.Fprintf(&b, "%s/%s=d%d\n", kind, e.Name(), info.ModTime().UnixNano())
			children, err := os.ReadDir(filepath.Join(root, e.Name()))
			if err != nil {
				return "", err
			}
			for _, c := range children {
				ci, err := stampInfo(filepath.Join(root, e.Name(), c.Name()))
				if err != nil {
					return "", err
				}
				if c.IsDir() {
					fmt.Fprintf(&b, "%s/%s/%s=d%d\n", kind, e.Name(), c.Name(), ci.ModTime().UnixNano())
				} else {
					fmt.Fprintf(&b, "%s/%s/%s=f%d:%d\n", kind, e.Name(), c.Name(), ci.ModTime().UnixNano(), ci.Size())
				}
			}
		}
	}
	return b.String(), nil
}

// stampInfo reads mtime/size from the file itself. On Windows, DirEntry.Info
// returns the copy cached in the parent directory index, which NTFS updates
// lazily, so it can lag behind recent writes and make the stamp flap or miss
// a change; Lstat reads the authoritative record.
func stampInfo(path string) (os.FileInfo, error) {
	return os.Lstat(path)
}

func loadQuotaCache(dataDir, stamp string) (Usage, bool) {
	quotaCacheMu.Lock()
	defer quotaCacheMu.Unlock()
	ent, ok := quotaCache[dataDir]
	if !ok || ent.stamp != stamp {
		return Usage{}, false
	}
	return copyUsage(ent.usage), true
}

func storeQuotaCache(dataDir, stamp string, usage Usage) {
	quotaCacheMu.Lock()
	defer quotaCacheMu.Unlock()
	quotaCache[dataDir] = quotaCacheEntry{stamp: stamp, usage: copyUsage(usage)}
}

func enforceQuota(usage Usage, q Quotas) (Usage, error) {
	if usage.Environments == nil {
		usage.Environments = map[string]int64{}
	}
	if usage.ToolsAndRuntimes > q.ToolsAndRuntimes {
		return usage, fmt.Errorf("%w: tools and runtimes", ErrQuotaExceeded)
	}
	if usage.Cache > q.Cache {
		return usage, fmt.Errorf("%w: cache", ErrQuotaExceeded)
	}
	for name, n := range usage.Environments {
		if n > q.Environment {
			envName, _, _ := strings.Cut(name, "/")
			return usage, fmt.Errorf("%w: environment %s", ErrQuotaExceeded, envName)
		}
	}
	return usage, nil
}

// CheckQuota is read-only and never deletes to make room. Symlinks are not
// followed. The service should expose its error as a request to clean storage.
// Results are cached per data directory and invalidated when the shallow
// directory mtime stamp of tools/runtimes/envs/cache changes, so repeated
// installs and the extensions storage view do not re-walk unchanged trees.
func (i *Installer) CheckQuota(ctx context.Context) (Usage, error) {
	result := Usage{Environments: map[string]int64{}}
	if !filepath.IsAbs(i.DataDir) {
		return result, errors.New("invalid managed data directory")
	}
	q := i.limits()
	for _, kind := range []string{"tools", "runtimes", "envs", "cache"} {
		if err := checkExistingParents(filepath.Join(i.DataDir, kind)); err != nil && !os.IsNotExist(err) {
			return result, err
		}
	}
	stamp, err := quotaDiskStamp(i.DataDir)
	if err != nil {
		return result, err
	}
	if cached, ok := loadQuotaCache(i.DataDir, stamp); ok {
		return enforceQuota(cached, q)
	}
	for _, kind := range []string{"tools", "runtimes", "cache"} {
		n, err := measureTree(ctx, filepath.Join(i.DataDir, kind))
		if err != nil {
			return result, err
		}
		if kind == "cache" {
			result.Cache = n
		} else {
			result.ToolsAndRuntimes += n
		}
	}
	names, err := os.ReadDir(filepath.Join(i.DataDir, "envs"))
	if err != nil && !os.IsNotExist(err) {
		return result, err
	}
	for _, name := range names {
		if !name.IsDir() {
			continue
		}
		versions, e := os.ReadDir(filepath.Join(i.DataDir, "envs", name.Name()))
		if e != nil {
			return result, e
		}
		for _, version := range versions {
			if !version.IsDir() {
				continue
			}
			path := filepath.Join(i.DataDir, "envs", name.Name(), version.Name())
			n, e := measureTree(ctx, path)
			if e != nil {
				return result, e
			}
			result.Environments[name.Name()+"/"+version.Name()] = n
		}
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	storeQuotaCache(i.DataDir, stamp, result)
	return enforceQuota(result, q)
}

func (i *Installer) markStage(stage string, r Reference) error {
	meta := ownership{Format: "tidebot-managed/1", Reference: r, Created: time.Now().UTC(), LastUsed: time.Now().UTC(), Links: map[string]string{}}
	err := filepath.WalkDir(stage, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			rel, e := filepath.Rel(stage, path)
			if e != nil {
				return e
			}
			target, e := os.Readlink(path)
			if e != nil {
				return e
			}
			meta.Links[filepath.ToSlash(rel)] = target
		}
		return nil
	})
	if err != nil {
		return err
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(stage, ownerFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	ce := f.Close()
	if err != nil {
		return err
	}
	return ce
}
func readOwner(path string, r Reference) (ownership, error) {
	var meta ownership
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return meta, errors.New("unowned managed directory")
	}
	marker := filepath.Join(path, ownerFile)
	info, err = os.Lstat(marker)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return meta, errors.New("missing managed ownership marker")
	}
	b, err := os.ReadFile(marker)
	if err != nil {
		return meta, err
	}
	if json.Unmarshal(b, &meta) != nil || meta.Format != "tidebot-managed/1" || meta.Reference != r {
		return meta, errors.New("invalid managed ownership marker")
	}
	err = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink == 0 {
			return nil
		}
		rel, e := filepath.Rel(path, p)
		if e != nil {
			return e
		}
		target, e := os.Readlink(p)
		if e != nil {
			return e
		}
		if want, ok := meta.Links[filepath.ToSlash(rel)]; !ok || want != target {
			return errors.New("unowned symlink in managed directory")
		}
		return nil
	})
	return meta, err
}

// Touch marks an owned version as used, without following any symlink. The
// caller supplies explicit references, never an arbitrary deletion/file path.
func (i *Installer) Touch(ctx context.Context, r Reference) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if !validReference(r) {
		return errors.New("invalid managed reference")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	parent := filepath.Join(i.DataDir, r.Kind, r.Name)
	if err := checkExistingParents(parent); err != nil {
		return err
	}
	path := filepath.Join(parent, r.Version)
	meta, err := readOwner(path, r)
	if err != nil {
		return err
	}
	meta.LastUsed = time.Now().UTC()
	b, _ := json.Marshal(meta)
	return os.WriteFile(filepath.Join(path, ownerFile), b, 0600)
}

// This read-only check never creates directories and rejects ancestor links.
func checkExistingParents(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	parent := filepath.Dir(abs)
	if parent != abs {
		if err = checkExistingParents(parent); err != nil {
			return err
		}
	}
	info, err := safepath.LstatDirectory(abs)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed ancestor is a link or non-directory")
	}
	return nil
}

type GCOptions struct {
	References   []Reference
	KeepPrevious int
	Now          time.Time
	DryRun       bool
}
type GCReport struct {
	Removed    []Reference `json:"removed"`
	CacheFiles []string    `json:"cache_files"`
	Stages     []string    `json:"stages"`
	Skipped    []string    `json:"skipped"`
	FreedBytes int64       `json:"freed_bytes"`
}

// GC retains every referenced version plus at least one most-recent unreferenced
// version per tool/environment for rollback. Only ownership-marked trees can be
// removed. Unknown directories and unknown/changed symlinks are left untouched.
func (i *Installer) GC(ctx context.Context, opts GCOptions) (GCReport, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	report := GCReport{}
	if !filepath.IsAbs(i.DataDir) {
		return report, errors.New("invalid managed data directory")
	}
	if err := checkExistingParents(i.DataDir); err != nil {
		return report, err
	}
	refs := map[Reference]bool{}
	for _, r := range opts.References {
		if !validReference(r) {
			return report, errors.New("invalid GC reference")
		}
		refs[r] = true
	}
	keep := opts.KeepPrevious
	if keep < 1 {
		keep = 1
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	for _, kind := range []string{"tools", "runtimes", "envs"} {
		root := filepath.Join(i.DataDir, kind)
		if err := checkExistingParents(root); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return report, err
		}
		names, err := os.ReadDir(root)
		if err != nil {
			return report, err
		}
		for _, name := range names {
			if !name.IsDir() || !validID(name.Name()) {
				continue
			}
			parent := filepath.Join(root, name.Name())
			versions, err := os.ReadDir(parent)
			if err != nil {
				return report, err
			}
			var candidates []ownership
			for _, version := range versions {
				if !version.IsDir() || strings.HasPrefix(version.Name(), ".") || !validID(version.Name()) {
					continue
				}
				r := Reference{Kind: kind, Name: name.Name(), Version: version.Name()}
				if refs[r] {
					continue
				}
				meta, e := readOwner(filepath.Join(parent, version.Name()), r)
				if e != nil {
					report.Skipped = append(report.Skipped, kind+"/"+name.Name()+"/"+version.Name())
					continue
				}
				candidates = append(candidates, meta)
			}
			sort.Slice(candidates, func(a, b int) bool { return candidates[a].Created.After(candidates[b].Created) })
			for n := keep; n < len(candidates); n++ {
				if err := ctx.Err(); err != nil {
					return report, err
				}
				r := candidates[n].Reference
				path := filepath.Join(parent, r.Version)
				if _, err = readOwner(path, r); err != nil {
					return report, err
				}
				size, err := treeSize(ctx, path)
				if err != nil {
					return report, err
				}
				if !opts.DryRun {
					if err = os.RemoveAll(path); err != nil {
						return report, err
					}
				}
				report.Removed = append(report.Removed, r)
				report.FreedBytes += size
			}
		}
	}
	if err := i.gcCaches(ctx, now, opts.DryRun, &report); err != nil {
		return report, err
	}
	stages, err := sweepStages(ctx, i.DataDir, opts.DryRun)
	report.Stages = stages.Removed
	report.FreedBytes += stages.FreedBytes
	report.Skipped = append(report.Skipped, stages.Skipped...)
	return report, err
}

// StageReport is what SweepStages removed: install stages and downloads an
// interrupted install left next to the versions (relative slash paths).
type StageReport struct {
	Removed    []string `json:"removed"`
	Skipped    []string `json:"skipped"`
	FreedBytes int64    `json:"freed_bytes"`
}

// SweepStages removes the .install-* stage directories and .download-* files
// that an install interrupted by a crash, force quit or power loss leaves in
// <kind>/<name>/. Every install removes its own on return, so leftovers are
// never reused; they only count against the quota (and GC skips dot entries),
// until installs fail with ErrQuotaExceeded and nothing can clean them. Call it
// at startup before installs can run, or with no install in progress (GC).
// Links are never followed: a linked stage is skipped, and RemoveAll removes
// links inside a stage without touching their targets.
func SweepStages(ctx context.Context, dataDir string) (StageReport, error) {
	return sweepStages(ctx, dataDir, false)
}

func sweepStages(ctx context.Context, dataDir string, dry bool) (StageReport, error) {
	report := StageReport{Removed: []string{}}
	if !filepath.IsAbs(dataDir) {
		return report, errors.New("invalid managed data directory")
	}
	for _, kind := range []string{"tools", "runtimes", "envs"} {
		root := filepath.Join(dataDir, kind)
		if err := checkExistingParents(root); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return report, err
		}
		names, err := os.ReadDir(root)
		if err != nil {
			return report, err
		}
		for _, name := range names {
			if !name.IsDir() || !validID(name.Name()) {
				continue
			}
			parent := filepath.Join(root, name.Name())
			entries, err := os.ReadDir(parent)
			if err != nil {
				return report, err
			}
			for _, e := range entries {
				if err = ctx.Err(); err != nil {
					return report, err
				}
				stage := strings.HasPrefix(e.Name(), ".install-")
				download := strings.HasPrefix(e.Name(), ".download-")
				if !stage && !download {
					continue
				}
				rel := kind + "/" + name.Name() + "/" + e.Name()
				path := filepath.Join(parent, e.Name())
				info, err := os.Lstat(path)
				if err != nil {
					if os.IsNotExist(err) {
						continue
					}
					return report, err
				}
				if (stage && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0)) || (download && !info.Mode().IsRegular()) {
					report.Skipped = append(report.Skipped, rel)
					continue
				}
				size := info.Size()
				if stage {
					// A partial tree may hold unreadable entries; count what can be read.
					size = stageSize(path)
				}
				if !dry {
					if err = os.RemoveAll(path); err != nil {
						report.Skipped = append(report.Skipped, rel)
						continue
					}
				}
				report.Removed = append(report.Removed, rel)
				report.FreedBytes += size
			}
		}
	}
	return report, nil
}

func stageSize(root string) int64 {
	var size int64
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, e := d.Info(); e == nil {
				size += info.Size()
			}
		}
		return nil
	})
	return size
}

type cacheOwnership struct {
	Format string `json:"format"`
	Kind   string `json:"kind"`
}

func validateCache(path, kind string) error {
	marker := filepath.Join(path, cacheOwnerFile)
	info, err := os.Lstat(marker)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return errors.New("invalid cache ownership")
	}
	b, err := os.ReadFile(marker)
	var m cacheOwnership
	if err != nil || json.Unmarshal(b, &m) != nil || m.Format != "tidebot-cache/1" || m.Kind != kind {
		return errors.New("invalid cache ownership")
	}
	return nil
}
func ownCache(path, kind string) error {
	marker := filepath.Join(path, cacheOwnerFile)
	info, err := os.Lstat(marker)
	if err == nil {
		if !info.Mode().IsRegular() || info.Size() > 4096 {
			return errors.New("invalid cache ownership")
		}
		b, e := os.ReadFile(marker)
		var m cacheOwnership
		if e != nil || json.Unmarshal(b, &m) != nil || m.Format != "tidebot-cache/1" || m.Kind != kind {
			return errors.New("invalid cache ownership")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("refusing to adopt an unowned nonempty cache")
	}
	b, _ := json.Marshal(cacheOwnership{Format: "tidebot-cache/1", Kind: kind})
	return os.WriteFile(marker, b, 0600)
}

// TouchCache records an actual cache hit for retention. Callers must call it when
// reading entries themselves; package-manager runs conservatively touch all their
// current entries because their internal cache hits are not observable here.
func (i *Installer) TouchCache(ctx context.Context, kind, relative string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.touchCache(ctx, kind, relative)
}
func (i *Installer) touchCache(ctx context.Context, kind, relative string) error {
	if kind != "npm" && kind != "uv" && kind != "oci" {
		return errors.New("invalid cache kind")
	}
	root := filepath.Join(i.DataDir, "cache", kind)
	if err := checkExistingParents(root); err != nil {
		return err
	}
	if err := validateCache(root, kind); err != nil {
		return err
	}
	target := root
	if relative != "" {
		if !safeRelative(relative) {
			return errors.New("invalid cache entry")
		}
		target = filepath.Join(root, filepath.FromSlash(relative))
		if err := checkExistingParents(filepath.Dir(target)); err != nil {
			return err
		}
	}
	now := time.Now()
	return filepath.WalkDir(target, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.Type().IsRegular() && d.Name() != cacheOwnerFile {
			return os.Chtimes(path, now, now)
		}
		return nil
	})
}
func (i *Installer) gcCaches(ctx context.Context, now time.Time, dry bool, report *GCReport) error {
	for _, kind := range []string{"npm", "uv", "oci"} {
		root := filepath.Join(i.DataDir, "cache", kind)
		if err := checkExistingParents(root); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			report.Skipped = append(report.Skipped, "cache/"+kind)
			continue
		}
		if err := validateCache(root, kind); err != nil {
			report.Skipped = append(report.Skipped, "cache/"+kind)
			continue
		}
		type entry struct {
			path string
			size int64
			used time.Time
		}
		var files []entry
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				report.Skipped = append(report.Skipped, path)
				return nil
			}
			if !d.Type().IsRegular() || d.Name() == cacheOwnerFile {
				return nil
			}
			info, e := d.Info()
			if e != nil {
				return e
			}
			if now.Sub(info.ModTime()) >= 30*24*time.Hour {
				files = append(files, entry{path, info.Size(), info.ModTime()})
			}
			return nil
		})
		if err != nil {
			return err
		}
		sort.Slice(files, func(a, b int) bool { return files[a].used.Before(files[b].used) })
		for _, f := range files {
			if err := ctx.Err(); err != nil {
				return err
			}
			info, err := os.Lstat(f.path)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				continue
			}
			if !dry {
				if err = os.Remove(f.path); err != nil {
					return err
				}
			}
			report.CacheFiles = append(report.CacheFiles, f.path)
			report.FreedBytes += f.size
		}
	}
	return nil
}

func (i *Installer) finalize(ctx context.Context, stage, dest string, r Reference) error {
	if err := i.markStage(stage, r); err != nil {
		return err
	}
	if _, err := i.CheckQuota(ctx); err != nil {
		return err
	}
	return promote(ctx, stage, dest)
}
