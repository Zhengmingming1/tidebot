// Package sessiontools is the read-only MCP server tidebot gives every session
// turn (server name "tidebot"): finding and searching files in the folders the
// turn may read (Claude Code's --bare mode has no Glob or Grep), and the
// capability tools (search, inspect, check, list, propose, test), which call
// back into the running core over its local socket with the turn's one-time
// token. Nothing here writes a file, runs a command or installs anything: an
// install is only ever a proposal the owner confirms on a card tidebot draws.
package sessiontools

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	maxListed     = 400      // paths find_files returns
	maxMatches    = 200      // lines search_files returns
	maxFileBytes  = 2 << 20  // files larger than this are not searched
	maxLineBytes  = 400      // a matching line is cut to this
	maxWalked     = 50000    // entries visited per call
	maxTotalBytes = 64 << 20 // bytes read per search
)

// Root is a folder a turn may read (its working folder, staged skills).
type Root struct {
	Label string
	Dir   string
}

// ParseRoots reads "label=dir" entries separated by the OS path-list separator.
func ParseRoots(s string) []Root {
	var out []Root
	for _, e := range filepath.SplitList(s) {
		l, d, ok := strings.Cut(e, "=")
		if !ok || d == "" || !filepath.IsAbs(d) {
			continue
		}
		out = append(out, Root{Label: l, Dir: filepath.Clean(d)})
	}
	return out
}

// FormatRoots is ParseRoots' inverse.
func FormatRoots(rs []Root) string {
	var parts []string
	for _, r := range rs {
		parts = append(parts, r.Label+"="+r.Dir)
	}
	return strings.Join(parts, string(filepath.ListSeparator))
}

// globRE turns a glob into a regexp over slash paths: ** spans folders, * and ?
// stay inside one, {a,b} picks one of several.
func globRE(glob string) (*regexp.Regexp, error) {
	glob = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(glob)), "./")
	if glob == "" {
		glob = "**"
	}
	if !strings.Contains(glob, "/") {
		glob = "**/" + glob // like Glob: a bare pattern matches at any depth
	}
	var b strings.Builder
	b.WriteString("^")
	inAlt := false
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch {
		case c == '*' && i+1 < len(glob) && glob[i+1] == '*':
			i++
			if i+1 < len(glob) && glob[i+1] == '/' {
				i++
				b.WriteString("(?:.*/)?")
			} else {
				b.WriteString(".*")
			}
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '{' && !inAlt:
			inAlt = true
			b.WriteString("(?:")
		case c == '}' && inAlt:
			inAlt = false
			b.WriteString(")")
		case c == ',' && inAlt:
			b.WriteString("|")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

var errStop = errors.New("stop")

// walk visits regular files under the roots (never following links, skipping
// .git) whose slash path relative to the root matches glob.
func walk(roots []Root, sub, glob string, each func(r Root, rel, abs string, size int64) error) error {
	re, err := globRE(glob)
	if err != nil {
		return fmt.Errorf("bad pattern: %v", err)
	}
	seen := 0
	sub = strings.TrimSpace(sub)
	for _, r := range roots {
		start := r.Dir
		if sub != "" && sub != "." {
			if filepath.IsAbs(sub) {
				clean := filepath.Clean(sub)
				rel, err := filepath.Rel(r.Dir, clean)
				if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					continue // inside another root, or none
				}
				start = clean
			} else {
				clean := filepath.Clean(filepath.FromSlash(sub))
				if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
					return errors.New("path must be inside the session's folders")
				}
				start = filepath.Join(r.Dir, clean)
			}
		}
		err := filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if seen++; seen > maxWalked {
				return errStop
			}
			if d.IsDir() {
				if d.Name() == ".git" && p != start {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil // links and devices are not followed or read
			}
			rel, err := filepath.Rel(r.Dir, p)
			if err != nil || strings.HasPrefix(rel, "..") {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if !re.MatchString(rel) {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			return each(r, rel, p, info.Size())
		})
		if errors.Is(err, errStop) {
			return err
		}
	}
	return nil
}

// show is the path the model can pass to Read: absolute, in the OS's form.
func show(r Root, rel string) string { return filepath.Join(r.Dir, filepath.FromSlash(rel)) }

// FindFiles lists files matching a glob ("**/*.go", "docs/*.md").
func FindFiles(roots []Root, pattern, path string) (string, error) {
	var out []string
	more := false
	err := walk(roots, path, pattern, func(r Root, rel, _ string, _ int64) error {
		if len(out) >= maxListed {
			more = true
			return errStop
		}
		out = append(out, show(r, rel))
		return nil
	})
	if err != nil && !errors.Is(err, errStop) {
		return "", err
	}
	sort.Strings(out)
	if len(out) == 0 {
		return "No files match.", nil
	}
	s := strings.Join(out, "\n")
	if more || errors.Is(err, errStop) {
		s += fmt.Sprintf("\n… (stopped at %d entries; narrow the pattern or path)", len(out))
	}
	return s, nil
}

// SearchFiles finds lines matching a regular expression in text files.
func SearchFiles(roots []Root, pattern, path, glob string, ignoreCase bool) (string, error) {
	if strings.TrimSpace(pattern) == "" {
		return "", errors.New("pattern required")
	}
	if ignoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("bad regular expression: %v", err)
	}
	var out []string
	total := int64(0)
	err = walk(roots, path, glob, func(r Root, rel, abs string, size int64) error {
		if size > maxFileBytes {
			return nil
		}
		if total += size; total > maxTotalBytes {
			return errStop
		}
		b, err := os.ReadFile(abs)
		if err != nil || bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
			return nil // unreadable or binary
		}
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for n := 1; sc.Scan(); n++ {
			line := sc.Text()
			if !re.MatchString(line) {
				continue
			}
			if len(line) > maxLineBytes {
				line = line[:maxLineBytes] + "…"
			}
			out = append(out, fmt.Sprintf("%s:%d: %s", show(r, rel), n, line))
			if len(out) >= maxMatches {
				return errStop
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStop) {
		return "", err
	}
	if len(out) == 0 {
		return "No matches.", nil
	}
	s := strings.Join(out, "\n")
	if errors.Is(err, errStop) {
		s += "\n… (stopped; narrow the pattern, path or glob)"
	}
	return s, nil
}
