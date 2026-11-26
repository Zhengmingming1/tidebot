package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreBackupWrongPassphraseLeavesExistingFilesUntouched(t *testing.T) {
	a := newTestApp(t)
	if err := a.Secrets.Set("provider:backup-regression", "secret"); err != nil {
		t.Fatal(err)
	}
	backup, err := a.CreateBackup(context.Background(), BackupOptions{Passphrase: "correct"})
	if err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	originalDB := []byte("existing database must survive a bad passphrase")
	originalConfig := []byte("existing config must survive too\n")
	dbPath := filepath.Join(dataDir, "tidebot.db")
	configPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(dbPath, originalDB, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, originalConfig, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := RestoreBackup(backup, home, "wrong"); err == nil {
		t.Fatal("restore with a wrong passphrase succeeded")
	}
	gotDB, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	gotConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotDB, originalDB) || !bytes.Equal(gotConfig, originalConfig) {
		t.Fatal("failed authentication changed existing files")
	}
	if _, err := os.Stat(dbPath + ".pre-restore"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed authentication created a pre-restore database: %v", err)
	}
}

func TestCreateBackupReportsSecretReadErrors(t *testing.T) {
	a := newTestApp(t)
	secretPath := filepath.Join(a.Cfg.HomeDir, "secrets.enc")
	if err := os.RemoveAll(secretPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(secretPath, 0o700); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "broken.agentbak")
	if _, err := a.CreateBackup(context.Background(), BackupOptions{Out: out, Passphrase: "pw"}); err == nil {
		t.Fatal("backup succeeded despite an unreadable secret store")
	}
	for _, path := range []string{out, out + ".part"} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("failed backup left %s behind: %v", path, err)
		}
	}
}
