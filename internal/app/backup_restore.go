package app

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

type restoreFile struct {
	target      string
	staged      string
	saved       string
	movedOld    bool
	installedNew bool
}

type restoreFileJournal struct {
	dir     string
	entries []*restoreFile
	byPath  map[string]*restoreFile
}

func newRestoreFileJournal(home string) (*restoreFileJournal, error) {
	dir, err := os.MkdirTemp(home, ".tidebot-restore-")
	if err != nil {
		return nil, err
	}
	j := &restoreFileJournal{dir: dir, byPath: map[string]*restoreFile{}}
	if err := os.MkdirAll(filepath.Join(dir, "new"), 0o700); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "old"), 0o700); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return j, nil
}

// add preflights a destination and gives its staged replacement a unique path.
// Existing symlinks, directories and other non-regular files are never removed.
func (j *restoreFileJournal) add(target string) (*restoreFile, error) {
	target = filepath.Clean(target)
	if old := j.byPath[target]; old != nil {
		return old, nil
	}
	fi, err := os.Lstat(target)
	if err == nil && !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("restore target %s is not a regular file", target)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	e := &restoreFile{
		target: target,
		staged: filepath.Join(j.dir, "new", fmt.Sprintf("%06d", len(j.entries))),
		saved:  filepath.Join(j.dir, "old", fmt.Sprintf("%06d", len(j.entries))),
	}
	j.entries = append(j.entries, e)
	j.byPath[target] = e
	return e, nil
}

// replace moves every old file aside before installing its staged replacement.
// Missing staged files are intentional deletions (old WAL/SHM, pre-restore).
func (j *restoreFileJournal) replace() error {
	for _, e := range j.entries {
		if err := os.MkdirAll(filepath.Dir(e.target), 0o700); err != nil {
			return err
		}
		fi, err := os.Lstat(e.target)
		switch {
		case err == nil:
			if !fi.Mode().IsRegular() {
				return fmt.Errorf("restore target %s is not a regular file", e.target)
			}
			if err := os.Rename(e.target, e.saved); err != nil {
				return err
			}
			e.movedOld = true
		case errors.Is(err, os.ErrNotExist):
		default:
			return err
		}
		if e.staged != "" {
			if err := os.Rename(e.staged, e.target); err != nil {
				return err
			}
			e.installedNew = true
		}
	}
	return nil
}

func (j *restoreFileJournal) rollback() error {
	var errs []error
	for i := len(j.entries) - 1; i >= 0; i-- {
		e := j.entries[i]
		if e.installedNew {
			if err := os.Remove(e.target); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, fmt.Errorf("remove new %s: %w", e.target, err))
			}
		}
		if e.movedOld {
			if err := os.MkdirAll(filepath.Dir(e.target), 0o700); err != nil {
				errs = append(errs, err)
				continue
			}
			if err := os.Rename(e.saved, e.target); err != nil {
				errs = append(errs, fmt.Errorf("restore old %s: %w", e.target, err))
			}
		}
	}
	return errors.Join(errs...)
}

func (j *restoreFileJournal) preservePreviousDatabase(dbPath string) error {
	dbFile := j.byPath[filepath.Clean(dbPath)]
	if dbFile == nil || !dbFile.movedOld {
		return nil
	}
	preRestore := j.byPath[filepath.Clean(dbPath+".pre-restore")]
	if preRestore == nil {
		return errors.New("restore journal has no pre-restore destination")
	}
	return os.Rename(dbFile.saved, preRestore.target)
}

func restoreBackup(file, home, passphrase string) (_ *BackupManifest, retErr error) {
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
	// Authenticate and decode secrets before touching the existing installation.
	// In particular, a wrong passphrase must not move the database aside first.
	blob := map[string][]byte{}
	if sf := files["secrets.sealed"]; sf != nil {
		rc, err := sf.Open()
		if err != nil {
			return nil, err
		}
		enc, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		plain, err := openWithPassphrase(enc, passphrase)
		if err != nil {
			return nil, errors.New("wrong passphrase")
		}
		if err := json.Unmarshal(plain, &blob); err != nil {
			return nil, fmt.Errorf("invalid secrets in backup: %w", err)
		}
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	j, err := newRestoreFileJournal(home)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if committed {
			_ = os.RemoveAll(j.dir)
			return
		}
		if err := j.rollback(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("rollback restore: %w", err))
			return // keep the journal for manual recovery
		}
		_ = os.RemoveAll(j.dir)
	}()

	dataDir := filepath.Join(home, "data")
	dbPath := filepath.Join(dataDir, "tidebot.db")
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	// Extract everything into the journal first. An archive/read/write failure
	// therefore cannot damage the live installation at all.
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
		e, err := j.add(dst)
		if err != nil {
			return nil, err
		}
		if e.staged == "" {
			return nil, fmt.Errorf("duplicate restore destination %s", dst)
		}
		staged := e.staged
		if err := extract(f, staged); err != nil {
			return nil, err
		}
		// add reserves a path; installation uses it and clears it only when the
		// same archive entry maps to an already-reserved database/WAL path.
		if staged != e.staged {
			return nil, fmt.Errorf("invalid restore staging for %s", dst)
		}
	}
	secretNames := make([]string, 0, len(blob))
	for n := range blob {
		if n == "secrets.enc" || n == "secrets.key" {
			secretNames = append(secretNames, n)
		}
	}
	sort.Strings(secretNames)
	for _, n := range secretNames {
		e, err := j.add(filepath.Join(home, n))
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(e.staged, blob[n], 0o600); err != nil {
			return nil, err
		}
	}
	// These files are never taken from the backup: opening the restored DB may
	// create fresh WAL/SHM files, and a successful restore keeps only the old DB.
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm", dbPath + ".pre-restore"} {
		e, err := j.add(path)
		if err != nil {
			return nil, err
		}
		if path != dbPath || e.staged != "" && j.byPath[filepath.Clean(dbPath)] != e {
			return nil, fmt.Errorf("invalid restore journal for %s", path)
		}
		if path != dbPath || files["data/tidebot.db"] == nil {
			if path != dbPath || e.staged != "" {
				// Reserved but absent entries perform deletions during replacement.
			}
		}
	}
	// Clear reservations for files that have no staged archive content. Entries
	// created from archive files keep their paths; newly ensured entries do not.
	for path, e := range j.byPath {
		if e.staged == "" {
			return nil, fmt.Errorf("empty staging path for %s", path)
		}
		if _, err := os.Lstat(e.staged); errors.Is(err, os.ErrNotExist) {
			e.staged = ""
		} else if err != nil {
			return nil, err
		}
	}
	if err := j.replace(); err != nil {
		return nil, err
	}

	var moves []homeMove
	if m.HomeDir != "" && m.HomeDir != home {
		moves = append(moves, homeMove{m.HomeDir, home})
	}
	if m.DataDir != "" && m.DataDir != dataDir {
		moves = append(moves, homeMove{m.DataDir, dataDir})
	}
	if j.byPath[filepath.Clean(dbPath)].installedNew {
		sqlDB, err := db.Open(dbPath)
		if err != nil {
			return nil, err
		}
		if len(moves) > 0 {
			err = remapRestoredPaths(context.Background(), sqlDB, moves)
		}
		// User-selected folders live outside the install and are not rewritten
		// (version content is immutable). Mark a one-time notice so the UI can
		// list tasks and workflows whose folders are missing on this computer.
		if err == nil {
			err = webui.MarkMissingWorkspaceNotice(context.Background(), sqlDB)
		}
		closeErr := sqlDB.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
	} else if len(moves) > 0 {
		return nil, errors.New("restore database is missing")
	}
	if err := j.preservePreviousDatabase(dbPath); err != nil {
		return nil, err
	}
	committed = true
	return &m, nil
}
