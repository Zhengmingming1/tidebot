package app

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Zhengmingming1/tidebot/internal/provider"
	"github.com/Zhengmingming1/tidebot/internal/textclip"
)

// TestMaskBeforeClip: provider errors are masked first and cut after, so a key that straddles
// the 300-byte cut can't survive in part; the cut keeps UTF-8 whole.
func TestMaskBeforeClip(t *testing.T) {
	key := "fake" + strings.Repeat("Q", 30) // built at run time, not a credential
	msg := strings.Repeat("错", 95) + " key " + key + " 无效"
	got := textclip.Head(provider.MaskSecret(upstreamMessage([]byte(`{"error":{"message":"`+msg+`"}}`)), key), 300)
	if strings.Contains(got, key[:8]) || !utf8.ValidString(got) || len(got) > 300 {
		t.Fatalf("%q", got)
	}
	if n := clipNotes(strings.Repeat("更新说明", 100)); !utf8.ValidString(n) || !strings.HasSuffix(n, "…") {
		t.Fatalf("notes %q", n)
	}
}
