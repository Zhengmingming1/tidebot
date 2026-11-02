package app

import "testing"

func TestEstimateSkillTokens(t *testing.T) {
	tests := []struct {
		name string
		input string
		want int
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
