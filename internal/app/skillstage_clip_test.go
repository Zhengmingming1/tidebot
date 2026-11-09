package app

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The --append-system-prompt fallback (older Claude Code) is cut to 16 KB on a
// character boundary: a Chinese skill must not end in a broken byte sequence.
func TestSkillStagePromptCutKeepsUTF8(t *testing.T) {
	st := &skillStage{Dir: t.TempDir()}
	args := st.argsWithText(false, false, "xx"+strings.Repeat("技能", 4000))
	text := args[len(args)-1]
	if args[len(args)-2] != "--append-system-prompt" || !utf8.ValidString(text) || !strings.HasSuffix(text, "\n…(truncated)") {
		t.Fatalf("prompt arg: valid=%v tail=%q", utf8.ValidString(text), text[len(text)-20:])
	}
	if n := len(strings.TrimSuffix(text, "\n…(truncated)")); n > 16<<10 || n < 16<<10-3 {
		t.Fatalf("cut to %d bytes", n)
	}
}
