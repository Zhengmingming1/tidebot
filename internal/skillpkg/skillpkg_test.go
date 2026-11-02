package skillpkg

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func mkzip(t *testing.T, files map[string]string, link string) []byte {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for n, c := range files {
		w, _ := zw.Create(n)
		w.Write([]byte(c))
	}
	if link != "" {
		h := &zip.FileHeader{Name: link}
		h.SetMode(os.ModeSymlink | 0o777)
		w, _ := zw.CreateHeader(h)
		w.Write([]byte("/etc/passwd"))
	}
	zw.Close()
	return b.Bytes()
}

func TestExtractRejectsZipSlipAndLinks(t *testing.T) {
	for _, bad := range []string{"../evil", "a/../../evil", "/abs", "C:/x", "..\\evil"} {
		d := t.TempDir()
		if err := Extract("x.zip", mkzip(t, map[string]string{bad: "x"}, ""), d); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	d := t.TempDir()
	if err := Extract("x.zip", mkzip(t, map[string]string{"SKILL.md": "x"}, "link"), d); err == nil || !strings.Contains(err.Error(), "links") {
		t.Fatalf("symlink: %v", err)
	}
	var tb bytes.Buffer
	gz := gzip.NewWriter(&tb)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "s/l", Typeflag: tar.TypeSymlink, Linkname: "/etc"})
	tw.Close()
	gz.Close()
	if err := Extract("x.tar.gz", tb.Bytes(), t.TempDir()); err == nil {
		t.Fatal("tar symlink accepted")
	}
}

func TestExtractLimits(t *testing.T) {
	many := map[string]string{}
	for i := 0; i <= MaxEntries; i++ {
		many[filepath.Join("f", string(rune('a'+i%26)), strings.Repeat("x", 1+i/26))] = "1"
	}
	if err := Extract("x.zip", mkzip(t, many, ""), t.TempDir()); err == nil {
		t.Fatal("entry limit")
	}
	big := strings.Repeat("A", MaxFileBytes+1)
	if err := Extract("x.zip", mkzip(t, map[string]string{"SKILL.md": big}, ""), t.TempDir()); err == nil {
		t.Fatal("file size limit")
	}
	if err := Extract("x.rar", []byte("x"), t.TempDir()); err == nil {
		t.Fatal("unknown type")
	}
}

func TestFindRootAndFrontmatter(t *testing.T) {
	d := t.TempDir()
	md := "---\nname: pdf-tools\ndescription: |\n  Fill PDF forms.\n---\n# body\n"
	if err := Extract("s.skill", mkzip(t, map[string]string{"pdf/SKILL.md": md, "pdf/x.py": "1", "__MACOSX/._x": "j"}, ""), d); err != nil {
		t.Fatal(err)
	}
	root, err := FindRoot(d)
	if err != nil || filepath.Base(root) != "pdf" {
		t.Fatal(root, err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "SKILL.md"))
	m, err := ParseFrontmatter(b)
	if err != nil || m.Name != "pdf-tools" || m.Description != "Fill PDF forms." {
		t.Fatalf("%+v %v", m, err)
	}
	if m, err := ParseFrontmatter([]byte("# no fm")); err != nil || m.Name != "" {
		t.Fatal("no frontmatter")
	}
	if _, err := ParseFrontmatter([]byte("---\nname: [\n---\n")); err == nil {
		t.Fatal("bad yaml")
	}
	if _, err := ParseFrontmatter([]byte("---\nname: x\n")); err == nil {
		t.Fatal("unclosed")
	}
	e := t.TempDir()
	Extract("x.zip", mkzip(t, map[string]string{"a/readme.md": "x", "b/SKILL.md": "x"}, ""), e)
	if _, err := FindRoot(e); err != ErrNoSkillMD {
		t.Fatal("ambiguous root accepted")
	}
}

func writeRootFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFindRootStandardPluginLayouts(t *testing.T) {
	for _, prefix := range []string{"", "package/"} {
		for _, manifest := range []bool{false, true} {
			name := "without-manifest"
			if manifest {
				name = "with-manifest"
			}
			t.Run(prefix+name, func(t *testing.T) {
				d := t.TempDir()
				files := map[string]string{
					prefix + "skills/sample/SKILL.md": "---\nname: sample\ndescription: Synthetic example\n---\n# Sample\n",
					prefix + "skills/sample/run.py":   "print('fixture')\n",
					prefix + "README.md":              "Package documentation",
					prefix + ".DS_Store":              "ignored metadata",
					prefix + "skills/.DS_Store":       "ignored metadata",
					".DS_Store":                       "outer metadata",
					"__MACOSX/._package":              "outer metadata",
				}
				if manifest {
					files[prefix+".claude-plugin/plugin.json"] = `{"name":"sample-plugin","skills":"./skills"}`
				}
				writeRootFixture(t, d, files)
				root, err := FindRoot(d)
				want := filepath.Join(d, filepath.FromSlash(prefix+"skills/sample"))
				if err != nil || root != want {
					t.Fatalf("root=%q error=%v; want %q", root, err, want)
				}
				entries, err := os.ReadDir(root)
				if err != nil || len(entries) != 2 {
					t.Fatalf("selected skill should contain only its two files: %v %v", entries, err)
				}
			})
		}
	}
	for _, prefix := range []string{"", "single/"} {
		t.Run("legacy/"+prefix, func(t *testing.T) {
			d := t.TempDir()
			writeRootFixture(t, d, map[string]string{prefix + "SKILL.md": "# Single skill"})
			root, err := FindRoot(d)
			if err != nil || root != filepath.Join(d, prefix) {
				t.Fatalf("legacy root=%q error=%v", root, err)
			}
		})
	}
}

func TestFindRootMultipleSkills(t *testing.T) {
	for _, prefix := range []string{"", "package/"} {
		t.Run(prefix, func(t *testing.T) {
			d := t.TempDir()
			writeRootFixture(t, d, map[string]string{
				prefix + "skills/zeta/SKILL.md":        "# Zeta",
				prefix + "skills/alpha/SKILL.md":       "# Alpha",
				prefix + "README.md":                   "not a skill",
				prefix + ".claude-plugin/plugin.json":  `{"name":"bundle"}`,
				prefix + "skills/unfinished/README.md": "not a candidate",
				prefix + "skills/.DS_Store":            "ignored",
			})
			root, err := FindRoot(d)
			var multiple *MultipleSkillsError
			if root != "" || !errors.As(err, &multiple) {
				t.Fatalf("multiple skills silently accepted: root=%q error=%v", root, err)
			}
			want := []string{prefix + "skills/alpha", prefix + "skills/zeta"}
			if !reflect.DeepEqual(multiple.Candidates, want) {
				t.Fatalf("candidates=%q, want relative slash paths %q", multiple.Candidates, want)
			}
			if strings.Contains(err.Error(), d) {
				t.Fatal("candidate error leaks the absolute staging directory")
			}
		})
	}
}

func TestFindRootDoesNotSearchArbitraryDepth(t *testing.T) {
	cases := map[string]map[string]string{
		"legacy ambiguous siblings": {"a/readme.md": "a", "b/SKILL.md": "b"},
		"arbitrary nested folder":   {"docs/nested/sample/SKILL.md": "sample"},
		"extra archive wrapper":     {"outer/inner/skills/sample/SKILL.md": "sample"},
		"nested below skill folder": {"skills/sample/nested/SKILL.md": "sample"},
		"custom manifest path":      {".claude-plugin/plugin.json": `{"skills":"./custom"}`, "custom/sample/SKILL.md": "sample"},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			d := t.TempDir()
			writeRootFixture(t, d, files)
			if root, err := FindRoot(d); root != "" || !errors.Is(err, ErrNoSkillMD) {
				t.Fatalf("unsupported layout accepted: root=%q error=%v", root, err)
			}
		})
	}
}

func TestFindRootRequiresRegularSkillFile(t *testing.T) {
	for _, name := range []string{"SKILL.md", "single/SKILL.md", "skills/sample/SKILL.md"} {
		t.Run("directory/"+name, func(t *testing.T) {
			d := t.TempDir()
			if err := os.MkdirAll(filepath.Join(d, name), 0o700); err != nil {
				t.Fatal(err)
			}
			if root, err := FindRoot(d); root != "" || !errors.Is(err, ErrNoSkillMD) {
				t.Fatalf("directory named SKILL.md accepted: %q %v", root, err)
			}
		})
	}
	for _, name := range []string{"SKILL.md", "single/SKILL.md", "skills/sample/SKILL.md", "skills/sample", "skills", "wrapper"} {
		t.Run("symlink/"+name, func(t *testing.T) {
			d, outside := t.TempDir(), t.TempDir()
			writeRootFixture(t, outside, map[string]string{"SKILL.md": "# Outside", "sample/SKILL.md": "# Outside child"})
			target := outside
			if strings.HasSuffix(name, "SKILL.md") {
				target = filepath.Join(outside, "SKILL.md")
			}
			link := filepath.Join(d, name)
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlink unavailable on this host: %v", err)
			}
			if root, err := FindRoot(d); root != "" || !errors.Is(err, ErrNoSkillMD) {
				t.Fatalf("symlink accepted as skill root/file: %q %v", root, err)
			}
		})
	}
}

type rootArchiveEntry struct {
	name, body string
	kind       byte // tar entry kinds, also mapped to the corresponding ZIP mode
}

func rootArchive(t *testing.T, format string, entries []rootArchiveEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	if format == ".zip" {
		zw := zip.NewWriter(&b)
		for _, e := range entries {
			h := &zip.FileHeader{Name: e.name}
			mode := os.FileMode(0o644)
			if e.kind == tar.TypeDir {
				mode = os.ModeDir | 0o755
			} else if e.kind == tar.TypeSymlink {
				mode = os.ModeSymlink | 0o777
			}
			h.SetMode(mode)
			w, err := zw.CreateHeader(h)
			if err != nil {
				t.Fatal(err)
			}
			if e.body != "" {
				if _, err := w.Write([]byte(e.body)); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		gz := gzip.NewWriter(&b)
		tw := tar.NewWriter(gz)
		for _, e := range entries {
			h := &tar.Header{Name: e.name, Typeflag: e.kind, Mode: 0o644}
			if e.kind == tar.TypeReg {
				h.Size = int64(len(e.body))
			} else if e.kind == tar.TypeSymlink || e.kind == tar.TypeLink {
				h.Linkname = "elsewhere"
			}
			if err := tw.WriteHeader(h); err != nil {
				t.Fatal(err)
			}
			if e.kind == tar.TypeReg {
				if _, err := tw.Write([]byte(e.body)); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return b.Bytes()
}

func TestExtractRootDirectoryHeaders(t *testing.T) {
	for _, format := range []string{".zip", ".tar.gz"} {
		for _, header := range []string{".", "./"} {
			t.Run(format+"/"+header, func(t *testing.T) {
				d := t.TempDir()
				data := rootArchive(t, format, []rootArchiveEntry{
					{header, "", tar.TypeDir},
					{"./package/README.md", "outer readme", tar.TypeReg},
					{"./package/.claude-plugin/plugin.json", `{"name":"synthetic"}`, tar.TypeReg},
					{"./package/skills/sample/SKILL.md", "# Sample", tar.TypeReg},
					{"./package/skills/sample/support.txt", "support", tar.TypeReg},
				})
				if err := Extract("fixture"+format, data, d); err != nil {
					t.Fatal(err)
				}
				if root, err := FindRoot(d); err != nil || root != filepath.Join(d, "package", "skills", "sample") {
					t.Fatalf("root directory header broke extraction: %q %v", root, err)
				}
			})
		}
	}
}

func TestExtractRootHeaderExceptionStaysNarrow(t *testing.T) {
	for _, format := range []string{".zip", ".tar.gz"} {
		cases := []rootArchiveEntry{
			{".", "not a directory", tar.TypeReg},
			{".", "elsewhere", tar.TypeSymlink},
			{"./", "", tar.TypeSymlink},
			{"../escaped", "outside", tar.TypeReg},
			{"../", "", tar.TypeDir},
			{"..\\escaped", "outside", tar.TypeReg},
		}
		if format == ".tar.gz" {
			cases = append(cases, rootArchiveEntry{".", "", tar.TypeLink})
		}
		for _, entry := range cases {
			t.Run(format+"/"+entry.name+"/"+string(entry.kind), func(t *testing.T) {
				base := t.TempDir()
				dest := filepath.Join(base, "stage")
				if err := os.Mkdir(dest, 0o700); err != nil {
					t.Fatal(err)
				}
				data := rootArchive(t, format, []rootArchiveEntry{entry})
				if err := Extract("fixture"+format, data, dest); err == nil {
					t.Fatalf("unsafe entry accepted: %+v", entry)
				}
				if _, err := os.Stat(filepath.Join(base, "escaped")); !os.IsNotExist(err) {
					t.Fatal("archive wrote outside the staging directory")
				}
			})
		}
	}
}
