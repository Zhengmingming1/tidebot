// Package textclip cuts text to a byte budget without splitting a UTF-8 character.
//
// s[:n] on a string with Chinese (3 bytes a character) or emoji (4) can end in the middle of a
// character; JSON encoding then turns the broken tail into U+FFFD and SQLite stores the
// invalid bytes as they are. Everything that shortens user or upstream text goes through here.
package textclip

import "unicode/utf8"

// Head returns the longest prefix of s that is at most n bytes and ends on a character boundary.
func Head(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	i := n
	// back off continuation bytes (10xxxxxx) to the start of the cut character
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i]
}

// Ellipsis is Head plus "…" when something was cut.
func Ellipsis(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return Head(s, n) + "…"
}
