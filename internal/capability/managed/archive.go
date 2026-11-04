package managed

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Asset struct {
	URL       string     `json:"url"`
	SHA256    string     `json:"sha256"`
	Format    string     `json:"format"` // zip, tar.gz, or binary
	Signature *Signature `json:"signature,omitempty"`
}
type ArchiveRecipe struct {
	nodeBundle  bool              // set only by InstallNode after official layout validation
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Backend     string            `json:"backend"` // http-archive or github-release
	Runtime     bool              `json:"runtime,omitempty"`
	Assets      map[string]Asset  `json:"assets"`      // keys: linux-amd64, darwin-arm64, etc.
	Executables map[string]string `json:"executables"` // command -> archive-relative path
}

func (i *Installer) InstallArchive(ctx context.Context, r ArchiveRecipe) (Result, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	ctx, cancel := i.bounded(ctx)
	defer cancel()
	if !pinned(r.Version) || (r.Backend != "http-archive" && r.Backend != "github-release") {
		return Result{}, errors.New("archive requires an exact version and supported backend")
	}
	goos, arch := i.platform()
	a, ok := r.Assets[goos+"-"+arch]
	if !ok {
		return Result{}, errors.New("no archive for this platform")
	}
	digest, err := hex.DecodeString(a.SHA256)
	if err != nil || len(digest) != 32 {
		return Result{}, errors.New("archive requires pinned SHA256")
	}
	if len(r.Executables) == 0 {
		return Result{}, errors.New("archive has no declared executable")
	}
	for name, p := range r.Executables {
		if !validID(name) || !safeRelative(p) {
			return Result{}, errors.New("invalid executable path")
		}
	}
	if a.Signature != nil && i.Signatures == nil {
		return Result{}, errors.New("declared signature cannot be verified")
	}
	kind := "tools"
	if r.Runtime {
		kind = "runtimes"
	}
	if _, err = i.CheckQuota(ctx); err != nil {
		return Result{}, err
	}
	stage, dest, err := i.stage(kind, r.Name, r.Version)
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(stage)
	// Download lives outside the extraction root so an archive cannot overwrite it.
	f, err := os.CreateTemp(filepath.Dir(stage), ".download-")
	if err != nil {
		return Result{}, err
	}
	filename := f.Name()
	defer os.Remove(filename)
	h := sha256.New()
	err = i.download(ctx, a.URL, io.MultiWriter(f, h), MaxDownloadBytes)
	closeErr := f.Close()
	if err != nil {
		return Result{}, err
	}
	if closeErr != nil {
		return Result{}, closeErr
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), a.SHA256) {
		return Result{}, errors.New("archive SHA256 mismatch")
	}
	verification := "sha256-integrity-only"
	if a.Signature != nil {
		if err = i.Signatures.Verify(ctx, filename, *a.Signature); err != nil {
			return Result{}, fmt.Errorf("signature verification: %w", err)
		}
		verification = "sha256-and-signature"
	}
	if a.Format == "binary" {
		if len(r.Executables) != 1 {
			return Result{}, errors.New("binary asset requires one executable")
		}
		for _, p := range r.Executables {
			if err = copyFile(filename, filepath.Join(stage, filepath.FromSlash(p)), 0755); err != nil {
				return Result{}, err
			}
		}
	} else if err = extractNodeAware(ctx, filename, stage, a.Format, r.nodeBundle); err != nil {
		return Result{}, err
	}
	if r.nodeBundle {
		if err = writeNodeLaunchers(stage, r.Version, goos, arch); err != nil {
			return Result{}, err
		}
	}
	result := Result{Source: "managed", Directory: dest, Version: r.Version, Verification: verification, Executables: map[string]string{}}
	seen := map[string]bool{}
	for name, p := range r.Executables {
		src := filepath.Join(stage, filepath.FromSlash(p))
		info, e := os.Lstat(src)
		if e != nil || !info.Mode().IsRegular() {
			return Result{}, errors.New("declared executable missing or not a regular file")
		}
		if e = os.Chmod(src, 0755); e != nil {
			return Result{}, e
		}
		path := filepath.Join(dest, filepath.FromSlash(p))
		result.Executables[name] = path
		dir := filepath.Dir(path)
		if !seen[dir] {
			result.BinDirs = append(result.BinDirs, dir)
			seen[dir] = true
		}
	}
	_ = os.Remove(filename)
	if err = i.finalize(ctx, stage, dest, Reference{Kind: kind, Name: r.Name, Version: r.Version}); err != nil {
		return Result{}, err
	}
	return result, nil
}
func (i *Installer) download(ctx context.Context, raw string, w io.Writer, limit int64) error {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"))) {
		return errors.New("download requires HTTPS")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	client := i.Client
	if client == nil {
		client = &http.Client{}
	}
	if original, ok := client.(*http.Client); ok {
		clone := *original
		previous := clone.CheckRedirect
		clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" || req.URL.User != nil {
				return errors.New("insecure download redirect")
			}
			if previous != nil {
				return previous(req, via)
			}
			return nil
		}
		client = &clone
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if u.Scheme == "https" && resp.Request != nil && resp.Request.URL.Scheme != "https" {
		return errors.New("insecure download redirect")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download status %d", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return errors.New("download exceeds size limit")
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return errors.New("download exceeds size limit")
	}
	return ctx.Err()
}
func copyFile(src, dest string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	cerr := out.Close()
	if err != nil {
		return err
	}
	return cerr
}
func extract(ctx context.Context, filename, root, format string) error {
	return extractNodeAware(ctx, filename, root, format, false)
}
func extractNodeAware(ctx context.Context, filename, root, format string, nodeBundle bool) error {
	var total int64
	count := 0
	write := func(name string, mode os.FileMode, size int64, r io.Reader) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		name = strings.TrimSuffix(name, "/")
		if !safeRelative(name) {
			return errors.New("unsafe archive path")
		}
		count++
		if count > MaxArchiveFiles || size < 0 || size > MaxInstalledBytes-total {
			return errors.New("archive exceeds extraction limits")
		}
		dest := filepath.Join(root, filepath.FromSlash(name))
		if mode.IsDir() {
			return os.MkdirAll(dest, 0700)
		}
		if !mode.IsRegular() {
			return errors.New("archive contains link or special file")
		}
		total += size
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return err
		}
		f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		n, err := io.Copy(f, io.LimitReader(contextReader{ctx: ctx, r: r}, size+1))
		cerr := f.Close()
		if err != nil {
			return err
		}
		if n != size {
			return errors.New("invalid archive entry size")
		}
		return cerr
	}
	switch format {
	case "zip":
		z, err := zip.OpenReader(filename)
		if err != nil {
			return err
		}
		defer z.Close()
		for _, f := range z.File {
			if f.UncompressedSize64 > uint64(MaxInstalledBytes) {
				return errors.New("zip entry exceeds limit")
			}
			r, err := f.Open()
			if err != nil {
				return err
			}
			err = write(f.Name, f.Mode(), int64(f.UncompressedSize64), r)
			r.Close()
			if err != nil {
				return err
			}
		}
	case "tar.gz":
		f, err := os.Open(filename)
		if err != nil {
			return err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if nodeBundle && h.Typeflag == tar.TypeSymlink && knownNodeLink(h.Name, h.Linkname) {
				if err := ctx.Err(); err != nil {
					return err
				}
				count++
				if count > MaxArchiveFiles {
					return errors.New("archive exceeds extraction limits")
				}
				continue
			}
			if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA && h.Typeflag != tar.TypeDir {
				return errors.New("tar contains link or special file")
			}
			if err = write(h.Name, h.FileInfo().Mode(), h.Size, tr); err != nil {
				return err
			}
		}
	default:
		return errors.New("unsupported archive format")
	}
	return ctx.Err()
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
