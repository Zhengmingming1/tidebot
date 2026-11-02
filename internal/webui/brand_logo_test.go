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

// The animated mark and shipped static icon must remain the same curled-wave brand;
// multiple instances (sidebar, conversation, dialog) need distinct SVG paint servers.
func TestWaveBrandGeometry(t *testing.T) {
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
	script := "var markN=0;\n" + regexpFunc(t, src, "  function wavePath(layer) {") + regexpFunc(t, src, "  function waveMark() {") + "\nconst master=" + string(master) + ";\n" + `
const assert=require("node:assert/strict");
const paths=Array.from(master.matchAll(/<path d="([^"]+)"/g),m=>m[1]);
assert.equal(paths.length,3);
paths.forEach((p,i)=>assert.equal(wavePath(i),p));
const a=waveMark(),b=waveMark();
const ids=s=>Array.from(s.matchAll(/ id="([^"]+)"/g),m=>m[1]);
assert.equal(new Set(ids(a).concat(ids(b))).size,6);
assert.ok(a.includes('class="tw tw1"'));assert.ok(a.includes('class="tw tw2"'));
assert.ok(!a.includes("<circle"));assert.ok(!a.includes("tw-sand"));
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("brand geometry: %v\n%s", err, out)
	}
	css, _ := Static.ReadFile("static/styles.css")
	for _, rule := range []string{"animation: tide-crest 7s", "animation-name: tide-foreground", "animation-duration: 6s", "html.tb-busy .tide .tw { animation-duration: 3.2s; }", "html.tb-busy .tide .tw2 { animation-duration: 2.7s; }", "html.tb-hidden .tide * { animation-play-state: paused; }", "@media (prefers-reduced-motion: reduce) { .tide * { animation: none !important; } }"} {
		if !strings.Contains(string(css), rule) {
			t.Errorf("logo animation lost %s", rule)
		}
	}
}

// Every desktop status still has a distinct icon. macOS templates use alpha only;
// Windows/Linux colored icons keep their status badge over the new wave mark.
func TestWaveTrayAssets(t *testing.T) {
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
