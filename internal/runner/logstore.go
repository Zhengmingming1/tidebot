package runner

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Finished run logs are kept gzipped (<run_id>.log.gz) once nobody writes to them any
// more. Human-readable logs are repetitive text and shrink 5–10×; readers go through
// OpenLog / ReadLog and don't care which form is on disk. A live log is never touched.
// Idea from Cronicle (lib/job.js uploadJobLog: a finished job's log.txt is piped through
// gzip into storage as log.txt.gz). No code copied.

const (
	// GzSuffix is what a compressed run log ends with (after the run id).
	GzSuffix = ".log.gz"
	gzTmp    = ".log.gz.tmp"
	// minCompressBytes: below one filesystem block a smaller file frees nothing.
	minCompressBytes = 4096
)

// ErrNoLog means the run has neither a plain nor a compressed log.
var ErrNoLog = errors.New("no log for this run")

// LogRunID returns the run id of a file in the log folder (plain, compressed or a
// leftover temp file) and whether the name is one of those.
func LogRunID(name string) (string, bool) {
	for _, suf := range []string{gzTmp, GzSuffix, ".log"} {
		if strings.HasSuffix(name, suf) && len(name) > len(suf) {
			return strings.TrimSuffix(name, suf), true
		}
	}
	return "", false
}

type gzFile struct {
	*gzip.Reader
	f *os.File
}

func (g gzFile) Close() error { g.Reader.Close(); return g.f.Close() }

// OpenLogFile opens the log at plainPath (<dir>/<run_id>.log), or its gzipped form when
// the plain file is gone. compressed says which one; the reader yields the original bytes.
func OpenLogFile(plainPath string) (rc io.ReadCloser, compressed bool, err error) {
	if plainPath == "" {
		return nil, false, ErrNoLog
	}
	f, err := os.Open(plainPath)
	if err == nil {
		return f, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	gf, err := os.Open(plainPath + ".gz")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, ErrNoLog
		}
		return nil, false, err
	}
	zr, err := gzip.NewReader(gf)
	if err != nil {
		gf.Close()
		return nil, true, err
	}
	return gzFile{zr, gf}, true, nil
}

// ReadLogFile reads a whole run log, plain or gzipped.
func ReadLogFile(plainPath string) ([]byte, error) {
	rc, _, err := OpenLogFile(plainPath)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// GzipRawSize reads the original size a .gz file records in its last 4 bytes (ISIZE,
// modulo 4 GiB, which no run log reaches). ok is false for a short or unreadable file.
func GzipRawSize(path string) (int64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	var b [4]byte
	if fi, err := f.Stat(); err != nil || fi.Size() < 18 {
		return 0, false
	}
	if _, err := f.Seek(-4, io.SeekEnd); err != nil {
		return 0, false
	}
	if _, err := io.ReadFull(f, b[:]); err != nil {
		return 0, false
	}
	return int64(binary.LittleEndian.Uint32(b[:])), true
}

// CompressLogFile replaces <run_id>.log with <run_id>.log.gz and keeps its modification
// time (retention counts days from it). It returns the bytes freed; 0 with no error when
// the file is too small or wouldn't shrink. The plain file stays until the gzip copy is
// complete and synced; if it can't be removed (Windows: a reader still has it open) the
// copy is dropped and the next sweep tries again.
func CompressLogFile(plainPath string) (int64, error) {
	fi, err := os.Stat(plainPath)
	if err != nil {
		return 0, err
	}
	if !fi.Mode().IsRegular() || fi.Size() < minCompressBytes {
		return 0, nil
	}
	raw, err := os.ReadFile(plainPath)
	if err != nil {
		return 0, err
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	zw.Name = filepath.Base(plainPath)
	zw.ModTime = fi.ModTime()
	if _, err := zw.Write(raw); err != nil {
		return 0, err
	}
	if err := zw.Close(); err != nil {
		return 0, err
	}
	if int64(buf.Len())+minCompressBytes/4 >= fi.Size() {
		return 0, nil
	}
	gz := plainPath + ".gz"
	tmp := strings.TrimSuffix(plainPath, ".log") + gzTmp
	if err := writeSynced(tmp, buf.Bytes()); err != nil {
		os.Remove(tmp)
		return 0, err
	}
	// A run that wrote again meanwhile (it shouldn't: only finished runs come here) wins.
	if fi2, err := os.Stat(plainPath); err != nil || fi2.Size() != fi.Size() || !fi2.ModTime().Equal(fi.ModTime()) {
		os.Remove(tmp)
		return 0, err
	}
	_ = os.Chtimes(tmp, fi.ModTime(), fi.ModTime())
	if err := os.Rename(tmp, gz); err != nil {
		os.Remove(tmp)
		return 0, err
	}
	if err := os.Remove(plainPath); err != nil {
		os.Remove(gz)
		return 0, err
	}
	return fi.Size() - int64(buf.Len()), nil
}

func writeSynced(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// CompactReport says what CompressFinishedLogs did.
type CompactReport struct {
	Files      int   `json:"files"`
	FreedBytes int64 `json:"freed_bytes"`
}

// CompressFinishedLogs gzips the plain logs in LogDir that no run writes to any more: not
// a run this runner has in flight, not in busy (queued/running ids from the database),
// and untouched for at least quiet. Temp files left by a crash are removed.
func (r *Runner) CompressFinishedLogs(busy map[string]bool, quiet time.Duration, now time.Time) CompactReport {
	var rep CompactReport
	if r.LogDir == "" {
		return rep
	}
	ents, err := os.ReadDir(r.LogDir)
	if err != nil {
		return rep
	}
	r.mu.Lock()
	live := make(map[string]bool, len(r.cancels))
	for id := range r.cancels {
		live[id] = true
	}
	r.mu.Unlock()
	for _, e := range ents {
		name := e.Name()
		fi, err := e.Info()
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		p := filepath.Join(r.LogDir, name)
		if strings.HasSuffix(name, gzTmp) {
			if now.Sub(fi.ModTime()) > time.Hour {
				os.Remove(p)
			}
			continue
		}
		if !strings.HasSuffix(name, ".log") {
			continue
		}
		id := strings.TrimSuffix(name, ".log")
		if id == "" || live[id] || busy[id] || now.Sub(fi.ModTime()) < quiet {
			continue
		}
		if n, err := CompressLogFile(p); err == nil && n > 0 {
			rep.Files++
			rep.FreedBytes += n
		}
	}
	return rep
}
