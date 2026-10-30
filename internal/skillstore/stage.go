package skillstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/skillpkg"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

// PluginName is the plugin a staged skill set is loaded as (--plugin-dir), so a
// staged skill is invoked as "/tidebot:<name>". Claude Code only sees it in runs
// tidebot starts with that flag; nothing is written where Claude Code looks on
// its own (a project's .claude/, ~/.claude).
const PluginName = "tidebot"

// StagedSkill is one skill written into a stage directory.
type StagedSkill struct {
	SkillID     string
	VersionID   string
	Name        string // command name: /tidebot:<Name>
	Description string
	Dir         string // absolute folder holding SKILL.md
	SkillMD     []byte
}

// Command is the slash command that invokes the skill in a run.
func (s StagedSkill) Command() string { return "/" + PluginName + ":" + s.Name }

var unsafeName = regexp.MustCompile(`[^a-z0-9_-]+`)

// CommandName turns a skill name into the safe set Claude Code accepts as a
// command: lower-case a-z, 0-9, '-' and '_'; "" when nothing is left.
func CommandName(name string) string {
	n := unsafeName.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	n = strings.Trim(n, "-_")
	if len(n) > 64 {
		n = strings.Trim(n[:64], "-_")
	}
	return n
}

// ResolveVersion returns the version a binding runs: its pinned version, or the
// skill's current one when it follows the latest.
func (s *Service) ResolveVersion(ctx context.Context, b tasksvc.SkillBinding) (string, error) {
	if b.Mode != "follow" && b.VersionID != "" {
		return b.VersionID, nil
	}
	cur, err := s.Ver.CurrentVersion(ctx, version.EntitySkill, b.SkillID)
	if err != nil {
		return "", fmt.Errorf("skill %s current: %w", b.SkillID, err)
	}
	return cur.ID, nil
}

// Stage writes the bound skills as a Claude Code plugin under root (a directory
// tidebot owns, e.g. <DataDir>/skillstage/<run>): root/.claude-plugin/plugin.json
// and root/skills/<name>/. Load it with --plugin-dir root. File paths from the
// store must stay inside the skill folder.
func (s *Service) Stage(ctx context.Context, root string, bindings []tasksvc.SkillBinding) ([]StagedSkill, error) {
	if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o700); err != nil {
		return nil, err
	}
	manifest, _ := json.Marshal(map[string]string{
		"name": PluginName, "description": "Skills staged by tidebot for one run",
	})
	if err := os.WriteFile(filepath.Join(root, ".claude-plugin", "plugin.json"), manifest, 0o600); err != nil {
		return nil, err
	}
	var out []StagedSkill
	used := map[string]bool{}
	for i, b := range bindings {
		verID, err := s.ResolveVersion(ctx, b)
		if err != nil {
			return out, err
		}
		files, man, err := s.versionFiles(ctx, verID)
		if err != nil {
			return out, err
		}
		name := ""
		if md, ok := files["SKILL.md"]; ok {
			if meta, err := skillpkg.ParseFrontmatter(md); err == nil {
				name = CommandName(meta.Name)
			}
		}
		for _, c := range []string{man.Name, b.SkillName, b.SkillID} {
			if name == "" {
				name = CommandName(c)
			}
		}
		if name == "" {
			name = fmt.Sprintf("skill-%d", i+1)
		}
		for base, n := name, 2; used[name]; n++ {
			name = fmt.Sprintf("%s-%d", base, n)
		}
		used[name] = true
		dir := filepath.Join(root, "skills", name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return out, err
		}
		paths := make([]string, 0, len(files))
		for p := range files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			rel := filepath.FromSlash(p)
			if !filepath.IsLocal(rel) {
				return out, fmt.Errorf("skill %s: unsafe file path %q", b.SkillID, p)
			}
			dest := filepath.Join(dir, rel)
			if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
				return out, err
			}
			if err := os.WriteFile(dest, files[p], 0o600); err != nil {
				return out, err
			}
		}
		st := StagedSkill{SkillID: b.SkillID, VersionID: verID, Name: name, Dir: dir, SkillMD: files["SKILL.md"]}
		if meta, err := skillpkg.ParseFrontmatter(st.SkillMD); err == nil {
			st.Description = meta.Description
		}
		out = append(out, st)
	}
	return out, nil
}

// versionFiles loads every file of a skill version (path -> content).
func (s *Service) versionFiles(ctx context.Context, verID string) (map[string][]byte, Manifest, error) {
	var man Manifest
	ver, err := s.Ver.GetVersion(ctx, verID)
	if err != nil {
		return nil, man, err
	}
	if err := json.Unmarshal([]byte(ver.ContentJSON), &man); err != nil {
		return nil, man, err
	}
	files, err := s.Files(ctx, verID)
	if err != nil {
		return nil, man, err
	}
	if len(files) == 0 { // older versions: manifest keys + blobs by hash only
		for p, sha := range man.Files {
			data, err := s.getBlob(ctx, sha)
			if err != nil {
				return nil, man, err
			}
			files[p] = data
		}
	}
	return files, man, nil
}

// versionHashes returns path -> sha256 for every skill version tidebot has.
func (s *Service) versionHashes(ctx context.Context) (map[string]map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, COALESCE(content_json,'') FROM versions WHERE entity_type=?`, version.EntitySkill)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]string{}
	for rows.Next() {
		var vid, cj string
		if err := rows.Scan(&vid, &cj); err != nil {
			rows.Close()
			return nil, err
		}
		var man Manifest
		_ = json.Unmarshal([]byte(cj), &man)
		m := map[string]string{}
		for p, h := range man.Files {
			m[p] = h
		}
		out[vid] = m
	}
	rows.Close()
	rows, err = s.DB.QueryContext(ctx, `SELECT f.version_id, f.path, f.sha256 FROM version_files f JOIN versions v ON v.id=f.version_id WHERE v.entity_type=?`, version.EntitySkill)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var vid, p, h string
		if err := rows.Scan(&vid, &p, &h); err != nil {
			return nil, err
		}
		if !seen[vid] { // version_files is the source of truth when present
			out[vid] = map[string]string{}
			seen[vid] = true
		}
		out[vid][p] = h
	}
	return out, rows.Err()
}

// Limits for hashing a folder that may be a legacy copy.
const (
	legacyMaxFiles = 500
	legacyMaxBytes = 20 << 20
)

// errNotPlain: the folder holds something tidebot never writes (a symlink, a
// special file) or is too big to be one of its copies.
var errNotPlain = errors.New("not a plain skill copy")

// hashDir returns slash path -> sha256 of every regular file under dir.
func hashDir(dir string) (map[string]string, error) {
	out := map[string]string{}
	var total int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return errNotPlain
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return errNotPlain
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		total += fi.Size()
		if len(out) >= legacyMaxFiles || total > legacyMaxBytes {
			return errNotPlain
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		sum := sha256.Sum256(b)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	return out, err
}

// LegacyResult reports what CleanLegacyCopies did with one folder.
type LegacyResult struct {
	Path      string
	Removed   bool
	VersionID string // the tidebot version it matched (removed)
	Reason    string // why it was kept
}

// CleanLegacyCopies removes skill folders older tidebot versions wrote into
// <workspace>/.claude/skills/. A folder is removed only when its files are
// exactly those of a skill version tidebot stores (same paths, same SHA-256,
// nothing extra); anything else (the user's own skills, copies they edited,
// symlinks) is kept. Empty .claude/skills and .claude folders left behind are
// removed too; nothing else is touched.
func (s *Service) CleanLegacyCopies(ctx context.Context, workspace string) ([]LegacyResult, error) {
	base := filepath.Join(workspace, ".claude", "skills")
	if fi, err := os.Lstat(base); err != nil || !fi.IsDir() {
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		return nil, nil // nothing there, or a symlink / file we leave alone
	}
	if fi, err := os.Lstat(filepath.Join(workspace, ".claude")); err != nil || !fi.IsDir() {
		return nil, err
	}
	ents, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	known, err := s.versionHashes(ctx)
	if err != nil {
		return nil, err
	}
	var out []LegacyResult
	removed := false
	for _, e := range ents {
		p := filepath.Join(base, e.Name())
		if e.Type()&fs.ModeSymlink != 0 || !e.IsDir() {
			continue
		}
		r := LegacyResult{Path: p}
		got, err := hashDir(p)
		switch {
		case err != nil:
			r.Reason = "not a plain copy"
		case len(got) == 0:
			r.Reason = "empty"
		default:
			r.VersionID = matchVersion(known, got)
			if r.VersionID == "" {
				r.Reason = "not a tidebot copy or changed"
			}
		}
		if r.VersionID != "" {
			if err := os.RemoveAll(p); err != nil {
				r.Reason, r.VersionID = "remove failed: "+err.Error(), ""
			} else {
				r.Removed, removed = true, true
			}
		}
		out = append(out, r)
	}
	if removed {
		if os.Remove(base) == nil { // only succeeds when empty
			_ = os.Remove(filepath.Join(workspace, ".claude"))
		}
	}
	return out, nil
}

func matchVersion(known map[string]map[string]string, got map[string]string) string {
	ids := make([]string, 0, len(known))
	for id := range known {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		want := known[id]
		if len(want) != len(got) || len(want) == 0 {
			continue
		}
		same := true
		for p, h := range want {
			if got[p] != h {
				same = false
				break
			}
		}
		if same {
			return id
		}
	}
	return ""
}
