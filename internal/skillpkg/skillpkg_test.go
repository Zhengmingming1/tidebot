package skillpkg

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
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
