package webui

import (
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/db"
)

// Runs stopped while quitting get their own localized reason; crash leftovers
// keep theirs. Both stored messages share the "interrupted:" prefix.
func TestRunStoppedOnQuitIsLocalized(t *testing.T) {
	if !strings.HasPrefix(db.RunStoppedOnQuitMsg, "interrupted: quit") || strings.HasPrefix(db.RunInterruptedMsg, "interrupted: quit") {
		t.Fatal("stored messages no longer match the WebUI patterns")
	}
	src := []byte(appJSSource(t))
	for _, want := range []string{`/^interrupted: quit/.test(msg)) return T("rd.err_quit")`, `T("rd.err_interrupted")`} {
		if !strings.Contains(string(src), want) {
			t.Errorf("app.js lost %q", want)
		}
	}
}
