package workflow

import (
	"strings"
	"testing"
)

func BenchmarkRenderFanIn(b *testing.B) {
	input := strings.Repeat("x", 64<<10)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := render("{{input}}|{{input}}|{{input}}", input, nil, "all"); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkRenderRejectOversize(b *testing.B) {
	input := strings.Repeat("x", MaxOutputBytes)
	template := strings.Repeat("{{input}}", 1024)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := render(template, input, nil, "all"); err == nil {
			b.Fatal("oversize accepted")
		}
	}
}
