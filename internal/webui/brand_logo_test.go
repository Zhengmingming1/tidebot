package webui

import (
	"bytes"
	"encoding/json"
	"image/png"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The animated mark, boot screen and shipped static icon share the Tide T brand;
// multiple instances (sidebar, conversation, dialog) need distinct SVG paint servers.
func TestTideBrandGeometry(t *testing.T) {
	mark, err := os.ReadFile("../../branding/mark.svg")
	if err != nil {
		t.Fatal(err)
	}
	fav, err := Static.ReadFile("static/favicon.svg")
	if err != nil || !bytes.Equal(mark, fav) {
		t.Fatal("favicon.svg must be generated from branding/mark.svg")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	src := liveSrc(t)
	master, _ := json.Marshal(string(mark))
	boot, err := Static.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	bootJSON, _ := json.Marshal(string(boot))
	script := "var markN=0;\n" + regexpFunc(t, src, "  function tidePath(layer) {") + regexpFunc(t, src, "  function tideMark() {") + "\nconst master=" + string(master) + ";\nconst boot=" + string(bootJSON) + ";\n" + `
const assert=require("node:assert/strict");
const paths=Array.from(master.matchAll(/<path d="([^"]+)"/g),m=>m[1]);
assert.equal(paths.length,3);
paths.forEach((p,i)=>{assert.equal(tidePath(i),p);assert.ok(boot.includes('d="'+p+'"'));});
const a=tideMark(),b=tideMark();
const ids=s=>Array.from(s.matchAll(/ id="([^"]+)"/g),m=>m[1]);
assert.equal(new Set(ids(a).concat(ids(b))).size,4);
assert.ok(a.includes('class="tw"'));assert.ok(boot.includes('class="tw"'));
assert.ok(!a.includes("<circle"));assert.ok(!a.includes("tw-sand"));
assert.ok(!boot.includes("mk-sun"));assert.ok(!boot.includes("tw-sand"));
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("brand geometry: %v\n%s", err, out)
	}
	css, _ := Static.ReadFile("static/styles.css")
	for _, rule := range []string{"animation: tide-level 7s", "transform: translateY(-.6px)", "html.tb-busy .tide .tw { animation-duration: 3.2s; }", "html.tb-hidden .tide * { animation-play-state: paused; }", "@media (prefers-reduced-motion: reduce) { .tide * { animation: none !important; } }"} {
		if !strings.Contains(string(css), rule) {
			t.Errorf("logo animation lost %s", rule)
		}
	}
}

// Every desktop status still has a distinct icon. macOS templates use alpha only;
// Windows/Linux colored icons keep their status badge over the Tide T mark.
func TestTideTrayAssets(t *testing.T) {
	for _, suffix := range []string{"", "-template"} {
		seen := map[string]bool{}
		for _, status := range []string{"normal", "running", "error"} {
			path := "../../cmd/tidebot-desktop/assets/tray-" + status + suffix + ".png"
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			im, err := png.Decode(bytes.NewReader(b))
			if err != nil || im.Bounds().Dx() < 22 || im.Bounds().Dy() < 22 {
				t.Fatalf("invalid tray icon %s: %v", path, err)
			}
			if seen[string(b)] {
				t.Fatalf("duplicate tray status icon %s", path)
			}
			seen[string(b)] = true
		}
	}
}
