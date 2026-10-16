package app

import (
	"archive/zip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/buildinfo"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/id"
)

// BackupFormat identifies .agentbak archives (zip).
const BackupFormat = "tidebot-backup/1"

// AutoBackupKeep is how many automatic daily backups are kept.
const AutoBackupKeep = 7

// BackupManifest is manifest.json inside an .agentbak.
type BackupManifest struct {
	Format            string `json:"format"`
	AppVersion        string `json:"app_version"`
	CreatedAt         string `json:"created_at"`
	HomeDir           string `json:"home_dir"`
	IncludesSecrets   bool   `json:"includes_secrets"`
	IncludesArtifacts bool   `json:"includes_artifacts"`
}

// BackupOptions control what goes in.
type BackupOptions struct {
	Out              string `json:"out"`
	IncludeArtifacts bool   `json:"include_artifacts"`
	// Passphrase, when set, includes the encrypted secret store re-encrypted with a key
	// derived from it (PBKDF2-SHA256, AES-GCM). Without it secrets are left out.
	Passphrase string `json:"passphrase"`
	Kind       string `json:"kind"` // auto | manual
}

func (a *App) backupDir() string { return filepath.Join(a.Cfg.HomeDir, "backups") }

// CreateBackup writes an .agentbak with a consistent DB snapshot (VACUUM INTO).
func (a *App) CreateBackup(ctx context.Context, o BackupOptions) (string, error) {
	if o.Kind == "" {
		o.Kind = "manual"
	}
	if o.Out == "" {
		_ = os.MkdirAll(a.backupDir(), 0o700)
		o.Out = filepath.Join(a.backupDir(), fmt.Sprintf("tidebot-%s-%s.agentbak", o.Kind, time.Now().Format("20060102-150405")))
	}
	tmp, err := os.MkdirTemp("", "tidebot-backup-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	snap := filepath.Join(tmp, "tidebot.db")
	if _, err := a.DB.ExecContext(ctx, `VACUUM INTO ?`, snap); err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}
	f, err := os.OpenFile(o.Out+".part", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	zw := zip.NewWriter(f)
	m := BackupManifest{Format: BackupFormat, AppVersion: buildinfo.Version, CreatedAt: time.Now().UTC().Format(time.RFC3339),
		HomeDir: a.Cfg.HomeDir, IncludesSecrets: o.Passphrase != "", IncludesArtifacts: o.IncludeArtifacts}
	addFile := func(name, src string) error {
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, in)
		return err
	}
	fail := func(err error) (string, error) {
		zw.Close()
		f.Close()
		os.Remove(o.Out + ".part")
		return "", err
	}
	if err := addFile("data/tidebot.db", snap); err != nil {
		return fail(err)
	}
	for _, n := range []string{"config.yaml", "config.yml", "config.json", "providers.yaml"} {
		p := filepath.Join(a.Cfg.HomeDir, n)
		if _, err := os.Stat(p); err == nil {
			if err := addFile("config/"+n, p); err != nil {
				return fail(err)
			}
		}
	}
	if o.IncludeArtifacts {
		for _, sub := range []string{"runs", "artifacts"} {
			root := filepath.Join(a.Cfg.DataDir, sub)
			_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
				if err != nil || !fi.Mode().IsRegular() {
					return nil
				}
				rel, _ := filepath.Rel(a.Cfg.DataDir, p)
				return addFile("data/"+filepath.ToSlash(rel), p)
			})
		}
	}
	if o.Passphrase != "" {
		blob := map[string][]byte{}
		for _, n := range []string{"secrets.enc", "secrets.key"} {
			if b, err := os.ReadFile(filepath.Join(a.Cfg.HomeDir, n)); err == nil {
				blob[n] = b
			}
		}
		plain, _ := json.Marshal(blob)
		enc, err := sealWithPassphrase(plain, o.Passphrase)
		if err != nil {
			return fail(err)
		}
		w, _ := zw.Create("secrets.sealed")
		_, _ = w.Write(enc)
	}
	mj, _ := json.MarshalIndent(m, "", "  ")
	w, _ := zw.Create("manifest.json")
	_, _ = w.Write(mj)
	if err := zw.Close(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		return fail(err)
	}
	if err := os.Rename(o.Out+".part", o.Out); err != nil {
		return "", err
	}
	fi, _ := os.Stat(o.Out)
	_, _ = a.DB.ExecContext(ctx, `INSERT INTO backups(id, path, size_bytes, kind, created_at) VALUES(?,?,?,?,?)`,
		id.New("bak"), o.Out, fi.Size(), o.Kind, time.Now().UTC().Format(time.RFC3339))
	if o.Kind == "auto" {
		a.pruneAutoBackups(ctx)
	}
	return o.Out, nil
}

func (a *App) pruneAutoBackups(ctx context.Context) {
	rows, err := a.DB.QueryContext(ctx, `SELECT id, path FROM backups WHERE kind='auto' ORDER BY created_at DESC`)
	if err != nil {
		return
	}
	type row struct{ id, path string }
	var all []row
	for rows.Next() {
		var r row
		_ = rows.Scan(&r.id, &r.path)
		all = append(all, r)
	}
	rows.Close()
	for i, r := range all {
		if i >= AutoBackupKeep {
			_ = os.Remove(r.path)
			_, _ = a.DB.ExecContext(ctx, `DELETE FROM backups WHERE id=?`, r.id)
		}
	}
}

// ListBackups returns known backups, newest first.
func (a *App) ListBackups(ctx context.Context) ([]map[string]any, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT id, path, size_bytes, kind, created_at FROM backups ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var i, p, k, c string
		var s int64
		_ = rows.Scan(&i, &p, &s, &k, &c)
		out = append(out, map[string]any{"id": i, "path": p, "size_bytes": s, "kind": k, "created_at": c})
	}
	return out, nil
}

// runAutoBackup makes one automatic backup per day (secrets excluded).
func (a *App) runAutoBackup(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		var last string
		_ = a.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(created_at),'') FROM backups WHERE kind='auto'`).Scan(&last)
		lt, _ := time.Parse(time.RFC3339, last)
		if time.Since(lt) > 24*time.Hour {
			if p, err := a.CreateBackup(ctx, BackupOptions{Kind: "auto"}); err != nil {
				a.Log.Warn("auto backup", "error", err)
			} else {
				a.Log.Info("auto backup", "path", p)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RestoreBackup restores an .agentbak into home. It must run with the daemon stopped
// (agentd restore). Paths under the old home dir are rewritten to the new one; an
// existing database is kept as tidebot.db.pre-restore.
func RestoreBackup(file, home, passphrase string) (*BackupManifest, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var m BackupManifest
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		clean := filepath.ToSlash(filepath.Clean(f.Name))
		if strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") || f.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("unsafe path %s", f.Name)
		}
		files[clean] = f
	}
	mf := files["manifest.json"]
	if mf == nil {
		return nil, errors.New("not a tidebot backup")
	}
	if err := readJSON(mf, &m); err != nil || m.Format != BackupFormat {
		return nil, errors.New("not a tidebot backup")
	}
	if m.IncludesSecrets && passphrase == "" {
		return nil, errors.New("this backup contains secrets; a passphrase is required")
	}
	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dataDir, "tidebot.db")
	if _, err := os.Stat(dbPath); err == nil {
		_ = os.Rename(dbPath, dbPath+".pre-restore")
		for _, s := range []string{"-wal", "-shm"} {
			_ = os.Remove(dbPath + s)
		}
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		f := files[n]
		var dst string
		switch {
		case strings.HasPrefix(n, "data/"):
			dst = filepath.Join(dataDir, filepath.FromSlash(strings.TrimPrefix(n, "data/")))
		case strings.HasPrefix(n, "config/"):
			dst = filepath.Join(home, filepath.Base(n))
		default:
			continue
		}
		if err := extract(f, dst); err != nil {
			return nil, err
		}
	}
	if sf := files["secrets.sealed"]; sf != nil {
		rc, _ := sf.Open()
		enc, _ := io.ReadAll(rc)
		rc.Close()
		plain, err := openWithPassphrase(enc, passphrase)
		if err != nil {
			return nil, errors.New("wrong passphrase")
		}
		blob := map[string][]byte{}
		_ = json.Unmarshal(plain, &blob)
		for n, b := range blob {
			if n == "secrets.enc" || n == "secrets.key" {
				if err := os.WriteFile(filepath.Join(home, n), b, 0o600); err != nil {
					return nil, err
				}
			}
		}
	}
	if m.HomeDir != "" && m.HomeDir != home {
		sqlDB, err := db.Open(dbPath)
		if err != nil {
			return nil, err
		}
		defer sqlDB.Close()
		old := m.HomeDir
		for _, q := range []string{
			`UPDATE tasks SET workspace_dir = ? || substr(workspace_dir, ?) WHERE substr(workspace_dir, 1, ?) = ?`,
			`UPDATE runs SET workspace_dir = ? || substr(workspace_dir, ?) WHERE substr(workspace_dir, 1, ?) = ?`,
			`UPDATE plugin_versions SET dir = ? || substr(dir, ?) WHERE substr(dir, 1, ?) = ?`,
			`UPDATE backups SET path = ? || substr(path, ?) WHERE substr(path, 1, ?) = ?`,
		} {
			if _, err := sqlDB.Exec(q, home, len(old)+1, len(old), old); err != nil {
				return nil, fmt.Errorf("path mapping: %w", err)
			}
		}
	}
	return &m, nil
}

func readJSON(f *zip.File, v any) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return json.NewDecoder(io.LimitReader(rc, 1<<20)).Decode(v)
}

func extract(f *zip.File, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(rc, 8<<30)); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

const pbkdfIter = 600000

func passKey(pass string, salt []byte) ([]byte, error) {
	return pbkdf2.Key(sha256.New, pass, salt, pbkdfIter, 32)
}

func sealWithPassphrase(plain []byte, pass string) ([]byte, error) {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	key, err := passKey(pass, salt)
	if err != nil {
		return nil, err
	}
	blk, _ := aes.NewCipher(key)
	g, _ := cipher.NewGCM(blk)
	nonce := make([]byte, g.NonceSize())
	_, _ = rand.Read(nonce)
	return append(append(salt, nonce...), g.Seal(nil, nonce, plain, nil)...), nil
}

func openWithPassphrase(enc []byte, pass string) ([]byte, error) {
	if len(enc) < 16+12 {
		return nil, errors.New("short")
	}
	key, err := passKey(pass, enc[:16])
	if err != nil {
		return nil, err
	}
	blk, _ := aes.NewCipher(key)
	g, _ := cipher.NewGCM(blk)
	return g.Open(nil, enc[16:16+g.NonceSize()], enc[16+g.NonceSize():], nil)
}
