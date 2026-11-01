package gitsnap

import (
	"strconv"
	"strings"
)

// Per-file parts of a patch Finish produced, for undoing one file of a run.
//
// Finish diffs with --no-renames, so every section's header names the same path on
// both sides: "diff --git a/P b/P", or C-quoted (`"a/P" "b/P"`) when P holds a quote,
// a backslash or a control character (core.quotePath=false keeps other bytes as is).
// A type change (file ↔ symlink) gives two sections for one path; both go together.
// Sections are matched by the header git would write for the path, never parsed.

// sections splits a patch into its "diff --git" sections (each with its trailing
// newline). Text before the first header is dropped.
func sections(patch string) []string {
	var out []string
	start := -1
	for i := 0; i < len(patch); {
		j := strings.IndexByte(patch[i:], '\n')
		end := len(patch)
		if j >= 0 {
			end = i + j + 1
		}
		if strings.HasPrefix(patch[i:end], "diff --git ") {
			if start >= 0 {
				out = append(out, patch[start:i])
			}
			start = i
		}
		i = end
	}
	if start >= 0 {
		out = append(out, patch[start:])
	}
	return out
}

// sectionHeader is the "diff --git" line git writes for path (no newline).
func sectionHeader(path string) string {
	if q, ok := cquote(path); ok {
		return `diff --git "a/` + q + `" "b/` + q + `"`
	}
	return "diff --git a/" + path + " b/" + path
}

// cquote quotes p the way git's quote_c_style does with core.quotePath=false;
// ok is false when p needs no quoting.
func cquote(p string) (string, bool) {
	var b strings.Builder
	need := false
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch c {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\a':
			b.WriteString(`\a`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\v':
			b.WriteString(`\v`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if c < 0x20 || c == 0x7f {
				o := strconv.FormatInt(int64(c), 8)
				b.WriteString(`\` + strings.Repeat("0", 3-len(o)) + o)
			} else {
				b.WriteByte(c)
				continue
			}
		}
		need = true
	}
	return b.String(), need
}

func headerOf(sec string) string {
	if i := strings.IndexByte(sec, '\n'); i >= 0 {
		sec = sec[:i]
	}
	return strings.TrimSuffix(sec, "\r")
}

// FilePatch returns only the sections of patch that change one of paths (” when none).
func FilePatch(patch string, paths []string) string {
	return pick(patch, paths, true)
}

// PatchWithout returns patch minus the sections that change one of paths.
func PatchWithout(patch string, paths []string) string {
	return pick(patch, paths, false)
}

func pick(patch string, paths []string, keep bool) string {
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		want[sectionHeader(p)] = true
	}
	var b strings.Builder
	for _, s := range sections(patch) {
		if want[headerOf(s)] == keep {
			b.WriteString(s)
		}
	}
	return b.String()
}
