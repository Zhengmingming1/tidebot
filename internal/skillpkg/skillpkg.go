// Package skillpkg unpacks uploaded skill packages (.zip, .skill, .tar.gz/.tgz, or a folder
// upload) safely and reads SKILL.md frontmatter.
package skillpkg

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Limits for one package.
const (
	MaxArchiveBytes = 20 << 20 // uploaded archive
	MaxTotalBytes   = 50 << 20 // uncompressed
	MaxFileBytes    = 10 << 20 // per file
	MaxEntries      = 2000
)

// ErrNoSkillMD is returned when no SKILL.md is found at the root or in a single top folder.
var ErrNoSkillMD = errors.New("no SKILL.md found: put SKILL.md at the top of the archive or inside a single top-level folder")

// CleanRel validates an archive entry name and returns a safe relative slash path.
func CleanRel(name string) (string, error) {
	n := strings.ReplaceAll(name, "\\", "/")
	if n == "" || strings.HasPrefix(n, "/") || (len(n) > 1 && n[1] == ':') {
		return "", fmt.Errorf("unsafe path %q", name)
	}
	c := path.Clean(n)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("unsafe path %q", name)
	}
	for _, seg := range strings.Split(c, "/") {
		if seg == ".." {
			return "", fmt.Errorf("unsafe path %q", name)
		}
	}
	return c, nil
}

type writer struct {
	dest    string
	total   int64
	entries int
}

func (w *writer) add(name string, r io.Reader) error { return w.addMode(name, r, false) }

func (w *writer) addMode(name string, r io.Reader, exec bool) error {
	rel, err := CleanRel(name)
	if err != nil {
		return err
	}
	// ignore OS junk
	base := path.Base(rel)
	if strings.HasPrefix(rel, "__MACOSX/") || base == ".DS_Store" {
		return nil
	}
	w.entries++
	if w.entries > MaxEntries {
		return fmt.Errorf("too many files (max %d)", MaxEntries)
	}
	dst := filepath.Join(w.dest, filepath.FromSlash(rel))
	if !strings.HasPrefix(dst, filepath.Clean(w.dest)+string(os.PathSeparator)) {
		return fmt.Errorf("unsafe path %q", name)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	perm := os.FileMode(0o644)
	if exec {
		perm = 0o755
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return fmt.Errorf("duplicate or bad entry %q", name)
	}
	n, err := io.Copy(f, io.LimitReader(r, MaxFileBytes+1))
	f.Close()
	if err != nil {
		return err
	}
	if n > MaxFileBytes {
		return fmt.Errorf("%s is larger than %d MB", rel, MaxFileBytes>>20)
	}
	w.total += n
	if w.total > MaxTotalBytes {
		return fmt.Errorf("package expands beyond %d MB", MaxTotalBytes>>20)
	}
	return nil
}

// Extract unpacks an archive into dest (which must exist and be empty).
// Symlinks, absolute paths and ".." entries are rejected.
func Extract(filename string, data []byte, dest string) error {
	if len(data) > MaxArchiveBytes {
		return fmt.Errorf("archive larger than %d MB", MaxArchiveBytes>>20)
	}
	w := &writer{dest: dest}
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return errors.New("not a valid .tar.gz")
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return errors.New("corrupt .tar.gz")
			}
			switch h.Typeflag {
			case tar.TypeDir:
				if _, err := CleanRel(h.Name); err != nil {
					return err
				}
			case tar.TypeReg:
				if err := w.addMode(h.Name, tr, h.Mode&0o111 != 0); err != nil {
					return err
				}
			case tar.TypeSymlink, tar.TypeLink:
				return fmt.Errorf("links are not allowed (%s)", h.Name)
			case tar.TypeXGlobalHeader:
			default:
				return fmt.Errorf("unsupported entry %s", h.Name)
			}
		}
	case strings.HasSuffix(lower, ".zip") || strings.HasSuffix(lower, ".skill"):
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return errors.New("not a valid zip file")
		}
		if len(zr.File) > MaxEntries {
			return fmt.Errorf("too many files (max %d)", MaxEntries)
		}
		for _, f := range zr.File {
			mode := f.Mode()
			if mode&os.ModeSymlink != 0 {
				return fmt.Errorf("links are not allowed (%s)", f.Name)
			}
			if f.FileInfo().IsDir() {
				if _, err := CleanRel(f.Name); err != nil {
					return err
				}
				continue
			}
			if !mode.IsRegular() {
				return fmt.Errorf("unsupported entry %s", f.Name)
			}
			rc, err := f.Open()
			if err != nil {
				return errors.New("corrupt zip entry")
			}
			err = w.addMode(f.Name, rc, mode&0o111 != 0)
			rc.Close()
			if err != nil {
				return err
			}
		}
		return nil
	default:
		return errors.New("unsupported file type: use .zip, .skill or .tar.gz")
	}
}

// FileEntry is one file from a folder upload.
type FileEntry struct {
	Path string
	Data io.Reader
}

// WriteFiles stores a folder upload with the same checks as Extract.
func WriteFiles(files []FileEntry, dest string) error {
	w := &writer{dest: dest}
	for _, f := range files {
		if err := w.add(f.Path, f.Data); err != nil {
			return err
		}
	}
	return nil
}

// FindRoot returns the directory holding SKILL.md: dest itself or its single top folder.
func FindRoot(dest string) (string, error) {
	if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err == nil {
		return dest, nil
	}
	ents, err := os.ReadDir(dest)
	if err != nil {
		return "", err
	}
	var dirs []string
	for _, e := range ents {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		} else {
			return "", ErrNoSkillMD
		}
	}
	if len(dirs) == 1 {
		root := filepath.Join(dest, dirs[0])
		if _, err := os.Stat(filepath.Join(root, "SKILL.md")); err == nil {
			return root, nil
		}
	}
	return "", ErrNoSkillMD
}

// Meta is the SKILL.md frontmatter we care about.
type Meta struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
}

// ParseFrontmatter reads the leading "---" YAML block of SKILL.md.
// A file without frontmatter yields an empty Meta and no error.
func ParseFrontmatter(md []byte) (Meta, error) {
	var m Meta
	s := strings.TrimPrefix(string(md), "\ufeff")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return m, nil
	}
	rest := s[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return m, errors.New("SKILL.md frontmatter is not closed with ---")
	}
	if err := yaml.Unmarshal([]byte(rest[:end]), &m); err != nil {
		return m, fmt.Errorf("SKILL.md frontmatter: %v", err)
	}
	m.Name = strings.TrimSpace(m.Name)
	m.Description = strings.TrimSpace(m.Description)
	if len(m.Description) > 1024 {
		m.Description = m.Description[:1024]
	}
	return m, nil
}

// FindFile returns the directory holding marker (e.g. plugin.yaml): dest or its single top folder.
func FindFile(dest, marker string) (string, error) {
	if _, err := os.Stat(filepath.Join(dest, marker)); err == nil {
		return dest, nil
	}
	ents, _ := os.ReadDir(dest)
	if len(ents) == 1 && ents[0].IsDir() {
		root := filepath.Join(dest, ents[0].Name())
		if _, err := os.Stat(filepath.Join(root, marker)); err == nil {
			return root, nil
		}
	}
	return "", fmt.Errorf("no %s found at the top of the archive or in a single top-level folder", marker)
}
