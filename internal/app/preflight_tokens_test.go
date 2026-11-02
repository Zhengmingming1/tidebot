package app

import "testing"

func TestEstimateSkillTokens(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"empty", "", 0},
		{"ascii rounding", "abcde", 2},
		{"ascii exact", "abcdefgh", 2},
		{"unicode", "你好", 2},
		{"mixed", "abcd你好", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := estimateSkillTokens([]byte(tt.input)); got != tt.want {
				t.Fatalf("estimateSkillTokens(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestEstimateSkillTokensMalformedAndNonBMP(t *testing.T) {
	for _, tc := range []struct {
		data []byte
		want int
	}{
		{[]byte{0xff, 0xfe, 'a'}, 3},
		{[]byte("abcd😀好"), 3},
		{[]byte("a\x00\nb"), 1},
	} {
		if got := estimateSkillTokens(tc.data); got != tc.want {
			t.Fatalf("%x: %d want %d", tc.data, got, tc.want)
		}
	}
}
