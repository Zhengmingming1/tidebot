package webui

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestLogToolsInHeader: copy / download / full screen are built apart from the filter bar
// and moved into the log card header, so the bar keeps one row at 1024px.
func TestLogToolsInHeader(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	bar := regexp.MustCompile(`(?s)host\.innerHTML = '<div class="lv-bar">.*?'<div class="lv-wrap">`).FindString(src)
	if bar == "" {
		t.Fatal("log bar markup not found")
	}
	for _, id := range []string{"lv-copy", "lv-dl", "lv-full"} {
		if strings.Contains(bar, id) {
			t.Errorf("%s is back in the filter bar", id)
		}
		if !strings.Contains(src, `$("#`+id+`", tools)`) {
			t.Errorf("%s not wired from the header tools", id)
		}
	}
	if !strings.Contains(src, `head.appendChild(tools)`) {
		t.Error("log tools are not moved into the card header")
	}
}
