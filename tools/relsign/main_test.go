package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChangelogCutsCleanly(t *testing.T) {
	long := "下载表\n---\n" + strings.Repeat("这一行是更新说明。\n", 300)
	got := changelog(long)
	if len(got) > 4000 || !utf8.ValidString(got) || strings.Contains(got, "下载表") {
		t.Fatalf("len %d valid %v", len(got), utf8.ValidString(got))
	}
	if !strings.HasSuffix(got, "这一行是更新说明。") {
		t.Fatalf("not cut at a line end: %q", got[len(got)-20:])
	}
	if changelog("a\n---\n短\n") != "短" {
		t.Fatal("short")
	}
}

func TestChangelogIsPlainText(t *testing.T) {
	in := "table\n---\n# v1.2.3\n\n## 中文\n\n### 新增\n- **会话**可选 `推理强度`，见 [文档](https://x/y)\n"
	want := "v1.2.3\n\n中文\n\n新增\n- 会话可选 推理强度，见 文档"
	if got := changelog(in); got != want {
		t.Fatalf("got %q", got)
	}
}
