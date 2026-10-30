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
