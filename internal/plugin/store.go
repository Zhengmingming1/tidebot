package plugin

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/netproxy"

	"github.com/Zhengmingming1/tidebot/internal/id"
	"github.com/Zhengmingming1/tidebot/internal/secrets"
)

// Store manages installed plugins.
type Store struct {
	DB      *sql.DB
	Root    string // $APP_HOME/plugins
	Secrets secrets.Backend
	// OnUninstall lets the app pause tasks / unregister triggers that depend on a plugin.
	OnUninstall func(ctx context.Context, pluginID string)
}

// Info is a plugin with its current version.
type Info struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Enabled  bool          `json:"enabled"`
	Source   string        `json:"source"` // local | official | ai-generated
	Current  *VersionInfo  `json:"current,omitempty"`
	Versions []VersionInfo `json:"versions,omitempty"`
}

// VersionInfo is one immutable plugin version.
type VersionInfo struct {
	ID          string    `json:"id"`
	Version     string    `json:"version"`
	ContentHash string    `json:"content_hash"`
	Approved    []string  `json:"approved_permissions"`
	Requested   []string  `json:"requested_permissions"`
	Dir         string    `json:"-"`
	Manifest    *Manifest `json:"manifest"`
	CreatedAt   string    `json:"created_at"`
}

// HashDir hashes all regular files (path + content), sorted; symlinks are refused.
func HashDir(dir string) (string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not allowed in plugins: %s", p)
		}
		if d.Type().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		rel, _ := filepath.Rel(dir, f)
		h.Write([]byte(filepath.ToSlash(rel) + "\x00"))
		fh, err := os.Open(f)
		if err != nil {
			return "", err
		}
		_, err = io.Copy(h, fh)
		fh.Close()
		if err != nil {
			return "", err
		}
		h.Write([]byte{1})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		t := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(t, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(t, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm()&0o755)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		return err
	})
}

// Stage copies a plugin directory into the store as an immutable, unapproved version.
// expectedHash (optional) must match the package hash. Approval is a separate step.
func (s *Store) Stage(ctx context.Context, srcDir, expectedHash string) (*VersionInfo, error) {
	return s.StageSource(ctx, srcDir, expectedHash, SourceLocal)
}

// Plugin sources.
const (
	SourceLocal    = "local"
	SourceOfficial = "official"
	SourceAI       = "ai-generated"
)

// OfficialVerifier reports whether (pluginID, contentHash) is an unmodified official
// catalog plugin. It is set by the official package; nil means no official plugins.
var OfficialVerifier func(pluginID, hash string) bool

// StageSource is Stage with an explicit source. Manifests that use a builtin runtime are
// refused unless they are byte-for-byte an official catalog entry.
func (s *Store) StageSource(ctx context.Context, srcDir, expectedHash, source string) (*VersionInfo, error) {
	m, err := ParseManifest(srcDir)
	if err != nil {
		return nil, err
	}
	hash, err := HashDir(srcDir)
	if err != nil {
		return nil, err
	}
	if expectedHash != "" && !strings.EqualFold(expectedHash, hash) {
		return nil, errors.New("package hash mismatch")
	}
	if m.UsesBuiltin() && (source != SourceOfficial || OfficialVerifier == nil || !OfficialVerifier(m.ID, hash)) {
		return nil, errors.New("builtin runtimes are reserved for official plugins")
	}
	if source == "" {
		source = SourceLocal
	}
	var existing string
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM plugin_versions WHERE plugin_id=? AND content_hash=?`, m.ID, hash).Scan(&existing); err == nil {
		return s.version(ctx, existing)
	}
	dst := filepath.Join(s.Root, m.ID, hash[:16])
	if err := os.RemoveAll(dst); err != nil {
		return nil, err
	}
	if err := copyDir(srcDir, dst); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO plugins(id, name, installed_at, source) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, source=excluded.source`, m.ID, m.Name, now, source); err != nil {
		return nil, err
	}
	mj, _ := json.Marshal(m)
	vid := id.New("plv")
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO plugin_versions(id, plugin_id, version, content_hash, manifest_json, dir, created_at) VALUES(?,?,?,?,?,?,?)`,
		vid, m.ID, m.Version, hash, string(mj), dst, now); err != nil {
		return nil, err
	}
	return s.version(ctx, vid)
}

func (s *Store) version(ctx context.Context, vid string) (*VersionInfo, error) {
	var v VersionInfo
	var mj, ap string
	err := s.DB.QueryRowContext(ctx, `SELECT id, version, content_hash, manifest_json, approved_permissions_json, dir, created_at FROM plugin_versions WHERE id=?`, vid).
		Scan(&v.ID, &v.Version, &v.ContentHash, &mj, &ap, &v.Dir, &v.CreatedAt)
	if err != nil {
		return nil, errors.New("plugin version not found")
	}
	v.Manifest = &Manifest{}
	_ = json.Unmarshal([]byte(mj), v.Manifest)
	if ap != "" {
		_ = json.Unmarshal([]byte(ap), &v.Approved)
	}
	v.Requested = v.Manifest.Permissions.PermissionList()
	return &v, nil
}

// Approve records the owner's approval of exactly the requested permissions and makes the
// version current. approved must list every requested permission (item-by-item consent).
func (s *Store) Approve(ctx context.Context, versionID string, approved []string) error {
	v, err := s.version(ctx, versionID)
	if err != nil {
		return err
	}
	if missing := NewPermissions(approved, v.Requested); len(missing) > 0 {
		return fmt.Errorf("not approved: %s", strings.Join(missing, ", "))
	}
	req := v.Requested
	if req == nil {
		req = []string{}
	}
	b, _ := json.Marshal(req)
	if _, err := s.DB.ExecContext(ctx, `UPDATE plugin_versions SET approved_permissions_json=? WHERE id=?`, string(b), versionID); err != nil {
		return err
	}
	return s.SetCurrent(ctx, v.Manifest.ID, versionID)
}

// SetCurrent switches the current pointer (upgrade or rollback). A version whose
// permissions were never approved cannot become current.
func (s *Store) SetCurrent(ctx context.Context, pluginID, versionID string) error {
	v, err := s.version(ctx, versionID)
	if err != nil || v.Manifest.ID != pluginID {
		return errors.New("version does not belong to plugin")
	}
	// A version must have been reviewed (Approve or Grant); declined/revoked items simply stay off.
	if v.Approved == nil {
		return errors.New("permissions of this version are not approved")
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE plugins SET current_version_id=? WHERE id=?`, versionID, pluginID)
	return err
}

// Get returns a plugin with all versions.
func (s *Store) Get(ctx context.Context, pluginID string) (*Info, error) {
	var in Info
	var cur sql.NullString
	var en int
	if err := s.DB.QueryRowContext(ctx, `SELECT id, name, enabled, current_version_id, source FROM plugins WHERE id=?`, pluginID).Scan(&in.ID, &in.Name, &en, &cur, &in.Source); err != nil {
		return nil, errors.New("plugin not found")
	}
	in.Enabled = en == 1
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM plugin_versions WHERE plugin_id=? ORDER BY created_at`, pluginID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var x string
		_ = rows.Scan(&x)
		ids = append(ids, x)
	}
	rows.Close()
	for _, x := range ids {
		v, err := s.version(ctx, x)
		if err != nil {
			continue
		}
		in.Versions = append(in.Versions, *v)
		if cur.Valid && cur.String == x {
			vv := *v
			in.Current = &vv
		}
	}
	return &in, nil
}

// List returns all plugins.
func (s *Store) List(ctx context.Context) ([]Info, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM plugins ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var x string
		_ = rows.Scan(&x)
		ids = append(ids, x)
	}
	rows.Close()
	var out []Info
	for _, x := range ids {
		if in, err := s.Get(ctx, x); err == nil {
			out = append(out, *in)
		}
	}
	return out, nil
}

// SetSecret stores a declared secret for the plugin.
func (s *Store) SetSecret(ctx context.Context, pluginID, name, value string) error {
	in, err := s.Get(ctx, pluginID)
	if err != nil || in.Current == nil {
		return errors.New("plugin not installed")
	}
	ok := false
	for _, d := range in.Current.Manifest.Permissions.Secrets {
		ok = ok || d == name
	}
	if !ok {
		return errors.New("secret not declared in plugin.yaml")
	}
	return s.Secrets.Set(secretKey(pluginID, name), value)
}

func secretKey(pluginID, name string) string { return "plugin." + pluginID + "." + name }

// ResolveEnv expands ${secret:x} references for a plugin.
func (s *Store) ResolveEnv(pluginID string, env map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range env {
		out[k] = reSecretRef.ReplaceAllStringFunc(v, func(m string) string {
			name := reSecretRef.FindStringSubmatch(m)[1]
			val, _ := s.Secrets.Get(secretKey(pluginID, name))
			return val
		})
	}
	return out
}

// Uninstall removes triggers, secrets, kv and (unless keepData) files; dependents are handled
// by OnUninstall. Version rows are kept for the audit trail but the plugin is detached.
func (s *Store) Uninstall(ctx context.Context, pluginID string, keepData bool) error {
	in, err := s.Get(ctx, pluginID)
	if err != nil {
		return err
	}
	if s.OnUninstall != nil {
		s.OnUninstall(ctx, pluginID)
	}
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM trigger_bindings WHERE plugin_id=?`, pluginID)
	for _, v := range in.Versions {
		for _, sec := range v.Manifest.Permissions.Secrets {
			_ = s.Secrets.Delete(secretKey(pluginID, sec))
		}
		if fields, err := ParseSchema(v.Manifest.ConfigSchema); err == nil {
			for _, f := range fields {
				if f.WriteOnly {
					_ = s.Secrets.Delete(secretKey(pluginID, cfgSecretName(f.Key)))
				}
			}
		}
	}
	if !keepData {
		_, _ = s.DB.ExecContext(ctx, `DELETE FROM plugin_kv WHERE plugin_id=?`, pluginID)
		_ = os.RemoveAll(filepath.Join(s.Root, pluginID))
	}
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM plugin_versions WHERE plugin_id=?`, pluginID)
	_, err = s.DB.ExecContext(ctx, `DELETE FROM plugins WHERE id=?`, pluginID)
	return err
}

// KVGet / KVSet are the plugin-private store.
func (s *Store) KVGet(ctx context.Context, pluginID, key string) (string, bool) {
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM plugin_kv WHERE plugin_id=? AND key=?`, pluginID, key).Scan(&v)
	return v, err == nil
}

// KVSet stores a value (max 64 KiB).
func (s *Store) KVSet(ctx context.Context, pluginID, key, value string) error {
	if len(value) > 64<<10 || len(key) > 256 {
		return errors.New("kv value too large")
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO plugin_kv(plugin_id, key, value) VALUES(?,?,?) ON CONFLICT(plugin_id, key) DO UPDATE SET value=excluded.value`, pluginID, key, value)
	return err
}

// MCPConfig builds the --mcp-config document for enabled plugins' connectors.
// sandbox replaces side-effect connectors with nothing (dry runs).
func (s *Store) MCPConfig(ctx context.Context, sandbox bool) ([]byte, int, error) {
	list, err := s.List(ctx)
	if err != nil {
		return nil, 0, err
	}
	servers := map[string]any{}
	for _, p := range list {
		if !p.Enabled || p.Current == nil {
			continue
		}
		for _, c := range p.Current.Manifest.Contributes.Connectors {
			if sandbox && c.SideEffects {
				continue
			}
			cmd, args := filepath.Join(p.Current.Dir, c.MCP.Command), c.MCP.Args
			if c.MCP.Builtin != "" {
				if BuiltinExe == "" {
					continue
				}
				cmd, args = BuiltinExe, append([]string{BuiltinArg, c.MCP.Builtin, "mcp"}, c.MCP.Args...)
			}
			env := s.ResolveEnv(p.ID, c.MCP.Env)
			if c.MCP.Builtin != "" {
				// Official runtimes read their settings (incl. write-only secrets) from here;
				// the file holding this document is 0600, like ${secret:} refs above.
				cj, _ := json.Marshal(s.ActivationConfig(ctx, p.ID))
				env["TIDEBOT_PLUGIN_CONFIG"] = string(cj)
				env["TIDEBOT_PLUGIN_ID"] = p.ID
				for _, kv := range netproxy.Env() {
					if k, v, ok := strings.Cut(kv, "="); ok {
						env[k] = v
					}
				}
			}
			servers[p.ID+"__"+c.ID] = map[string]any{
				"command": cmd,
				"args":    args,
				"env":     env,
			}
		}
	}
	b, err := json.Marshal(map[string]any{"mcpServers": servers})
	return b, len(servers), err
}
