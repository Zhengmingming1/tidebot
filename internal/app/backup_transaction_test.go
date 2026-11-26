package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateBackupUsesUniquePathsAndRecords(t *testing.T) {
	a := newTestApp(t)
	first, err := a.CreateBackup(context.Background(), BackupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.CreateBackup(context.Background(), BackupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("consecutive backups share a path: %s", first)
	}
	for _, path := range []string{first, second} {
		if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() {
			t.Fatalf("backup %s is missing: %v", path, err)
		}
	}
	rows, err := a.ListBackups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	recorded := map[string]bool{}
	for _, row := range rows {
		recorded[row["path"].(string)] = true
	}
	if len(rows) != 2 || !recorded[first] || !recorded[second] {
		t.Fatalf("backup records do not match files: %+v", rows)
	}
}

func TestCreateBackupRecordFailureRemovesArchive(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.DB.Exec(`CREATE TRIGGER fail_backup_record BEFORE INSERT ON backups BEGIN SELECT RAISE(ABORT, 'forced backup record failure'); END`); err != nil {
		t.Fatal(err)
	}
	out, err := a.CreateBackup(context.Background(), BackupOptions{})
	if err == nil || out != "" {
		t.Fatalf("unrecorded backup returned out=%q err=%v", out, err)
	}
	entries, readErr := os.ReadDir(a.backupDir())
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("failed backup record left files behind: %+v", entries)
	}
	var count int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM backups`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed backup left %d database records", count)
	}
}

func TestRestoreBackupInvalidDatabaseRollsBackFiles(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	originals := map[string][]byte{
		filepath.Join(dataDir, "tidebot.db"):      []byte("original database bytes"),
		filepath.Join(dataDir, "tidebot.db-wal"):  []byte("original wal bytes"),
		filepath.Join(dataDir, "tidebot.db-shm"):  []byte("original shm bytes"),
		filepath.Join(home, "config.yaml"):        []byte("original config bytes\n"),
	}
	for path, content := range originals {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	archive := filepath.Join(t.TempDir(), "invalid.agentbak")
	writeBackupTransactionArchive(t, archive, BackupManifest{
		Format: BackupFormat, HomeDir: home, DataDir: dataDir,
	}, map[string][]byte{
		"data/tidebot.db":   []byte("not a SQLite database"),
		"config/config.yaml": []byte("replacement config\n"),
	})
	if _, err := RestoreBackup(archive, home, ""); err == nil {
		t.Fatal("restore accepted an invalid SQLite database")
	}
	for path, want := range originals {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("failed restore changed %s: got %q want %q", path, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, "tidebot.db.pre-restore")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed restore left a pre-restore database: %v", err)
	}
	journals, err := filepath.Glob(filepath.Join(home, ".tidebot-restore-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(journals) != 0 {
		t.Fatalf("successful rollback left journal directories: %v", journals)
	}
}

func writeBackupTransactionArchive(t *testing.T, path string, manifest BackupManifest, files map[string][]byte) {
	t.Helper()
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(out)
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string][]byte{"manifest.json": manifestJSON}
	for name, content := range files {
		entries[name] = content
	}
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
