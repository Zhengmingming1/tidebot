package sessiontools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BenchmarkSearchFiles (TIDEBOT_PERF numbers in docs/backlog.md): 40 files × 1 MiB of short CJK/ASCII lines (CRLF), one match per file.
func BenchmarkSearchFiles(b *testing.B) {
	d := b.TempDir()
	line := "第一行 some ordinary text 用于测试 search_files 的性能\r\n"
	body := strings.Repeat(line, (1<<20)/len(line)) + "needle 命中\r\n"
	for i := 0; i < 40; i++ {
		if err := os.WriteFile(filepath.Join(d, "f"+string(rune('a'+i%26))+string(rune('a'+i/26))+".md"), []byte(body), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	roots := []Root{{Label: "work", Dir: d}}
	for _, c := range []struct {
		name, pattern string
		ic            bool
	}{{"hit", "needle", false}, {"miss", "absent", false}, {"ignorecase", "NEEDLE", true}} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := SearchFiles(roots, c.pattern, "", "*.md", c.ic); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
