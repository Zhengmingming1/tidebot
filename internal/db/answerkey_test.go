package db

import "testing"

func TestAnswerKey(t *testing.T) {
	for _, c := range [][2]string{
		{"a\r\n\n  b\t c\n", "a b c"},
		{"  \n\t", ""},
		{"今日\u3000要闻", "今日 要闻"},
	} {
		if got := AnswerKey(c[0]); got != c[1] {
			t.Errorf("AnswerKey(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}
