package webui

import (
	"regexp"
	"strings"
	"testing"
)

// At 1024×655 (content column ~800px) the workflow editor keeps step settings
// beside the canvas: before, the inspector dropped to a full-width row ~700px
// below the selected node. The palette turns into a strip above both columns and
// the inspector sticks under the top bar while the page scrolls.
func TestWorkflowInspectorBesideCanvasAt1024(t *testing.T) {
	raw, _ := Static.ReadFile("static/styles.css")
	css := strings.ReplaceAll(string(raw), "\r\n", "\n")
	head := "@media (min-width: 901px) and (max-width: 1250px) {"
	at := strings.Index(css, head)
	if at < 0 {
		t.Fatal("missing 901-1250px workflow designer block")
	}
	end := strings.Index(css[at:], "\n}\n")
	if end < 0 {
		t.Fatal("unterminated block")
	}
	block := css[at : at+end]
	for _, want := range []string{
		".wf-designer { grid-template-columns: minmax(0,1fr) 300px; }",
		".wf-designer .wf-palette { grid-column: 1/-1; display: flex;",
		"max-height: 112px", // many skills scroll inside the strip, not push the canvas down
		"#wf-palette-skills { display: contents; }",
		".wf-inspector { grid-column: auto; position: sticky; top: calc(var(--topbar-h) + 12px); max-height: calc(100vh - var(--topbar-h) - 24px); }",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("block lacks %q", want)
		}
	}
	// It must win over every earlier ≤1250px rule that stacks the inspector below.
	for _, m := range regexp.MustCompile(`@media \(max-width: 1250px\) \{[^@]*?wf-(designer|inspector)`).FindAllStringIndex(css, -1) {
		if m[0] > at {
			t.Errorf("a later ≤1250px workflow rule at byte %d overrides the side-by-side layout", m[0])
		}
	}
	// Narrow windows keep the stacked layout: 300px settings + a usable canvas need ~800px.
	if !strings.Contains(css, "@media (max-width: 1250px) {\n  .wf-designer { grid-template-columns: 160px minmax(0,1fr); }\n  .wf-inspector { grid-column: 1/-1; max-height: none; }") {
		t.Error("stacked layout for windows under 901px is gone")
	}
}
