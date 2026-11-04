package webui

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The designer has one palette strip and a canvas/settings pair above 900px.
// Narrow windows stack the settings instead of squeezing the drawing surface.
// Browser geometry and keyboard coverage lives in testdata/responsive-workspaces.cjs.
func TestWorkflowWorkspaceLayout(t *testing.T) {
	raw, err := Static.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	for _, rule := range []struct {
		selector string
		property string
		value    string
	}{
		{".wf-designer", "grid-template-columns", `minmax\(0,\s*1fr\)\s+280px`},
		{".wf-designer .wf-palette", "grid-column", `1\s*/\s*-1`},
		{".wf-designer .wf-palette", "display", "flex"},
		{".wf-designer .wf-palette", "flex-wrap", "wrap"},
		{".wf-designer .wf-palette", "max-height", `[1-9][0-9]*px`},
		{"#wf-palette-skills", "display", "contents"},
		{".wf-canvas-scroll", "overflow", "auto"},
		{".wf-canvas-scroll", "height", `clamp\([^;]*100dvh[^;]*\)`},
		{".wf-inspector", "position", "sticky"},
		{".wf-inspector", "max-height", `calc\(100dvh[^;]*\)`},
	} {
		requireLayoutDeclaration(t, css, rule.selector, rule.property, rule.value)
	}

	var narrow string
	for _, match := range regexp.MustCompile(`(?s)@media\s*\(max-width:\s*900px\)\s*\{(.*?)\n\}`).FindAllStringSubmatch(css, -1) {
		if strings.Contains(match[1], ".wf-designer") {
			narrow = match[1]
		}
	}
	if narrow == "" {
		t.Fatal("workflow editor needs a stacked layout at 900px and below")
	}
	requireLayoutDeclaration(t, narrow, ".wf-designer", "grid-template-columns", `minmax\(0,\s*1fr\)`)
	requireLayoutDeclaration(t, narrow, ".wf-inspector", "position", "static")
	requireLayoutDeclaration(t, narrow, ".wf-inspector", "max-height", "none")

	source, err := os.ReadFile("src/compositions-workflows.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), `<details class="wf-options"><summary>`) {
		t.Error("optional workflow settings should start collapsed in a native details control")
	}
	for _, field := range []string{"name", "max_parallel", "timeout_minutes", "description"} {
		if !strings.Contains(string(source), `name="`+field+`"`) {
			t.Errorf("workflow settings lost the %s form control", field)
		}
	}
}

func TestChatHistoryOwnsItsScrollArea(t *testing.T) {
	raw, err := Static.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	for _, rule := range []struct{ property, value string }{
		{"flex", `1\s+1\s+0`},
		{"min-width", "0"},
		{"min-height", "0"},
		{"overflow-x", "hidden"},
		{"overflow-y", "auto"},
	} {
		requireLayoutDeclaration(t, css, ".cs-list", rule.property, rule.value)
	}
	source, err := os.ReadFile("src/chat-page.js")
	if err != nil {
		t.Fatal(err)
	}
	item := regexpFunc(t, string(source), "  function csItemHtml(s, cur) {")
	if strings.Contains(item, "data-tip=") {
		t.Error("history item tooltips must not add pseudo-element overflow to its scroll container")
	}
	if !strings.Contains(item, `title="`) {
		t.Error("history metadata should remain available through a native title")
	}
}

func TestSelectPopupFitsOptionText(t *testing.T) {
	raw, err := Static.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	// Narrow triggers must not force ordinary provider names to wrap. The popup
	// controller still clamps maxWidth to the viewport for unusually long names.
	requireLayoutDeclaration(t, string(raw), ".select-popup", "width", "max-content")
	requireLayoutDeclaration(t, string(raw), ".select-popup-option > span:last-child", "overflow-wrap", "anywhere")
}

// Check declarations independently of whitespace or unrelated declarations. This
// avoids preserving an obsolete exact CSS block merely to satisfy a string test.
func requireLayoutDeclaration(t *testing.T, css, selector, property, valuePattern string) {
	t.Helper()
	pattern := `(?s)(?:^|[}\n])\s*` + regexp.QuoteMeta(selector) + `\s*\{[^}]*?\b` + regexp.QuoteMeta(property) + `\s*:\s*` + valuePattern + `\s*[;}]`
	if !regexp.MustCompile(pattern).MatchString(css) {
		t.Errorf("missing %s declaration %s: %s", selector, property, valuePattern)
	}
}
