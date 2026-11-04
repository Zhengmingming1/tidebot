package skillpkg

import (
	"strings"
	"testing"
)

func TestParseFrontmatterDelimiterLines(t *testing.T) {
	tests := []struct {
		name    string
		md      string
		want    Meta
		wantErr bool
	}{
		{name: "empty", md: "---\n---\n# body"},
		{name: "empty at EOF", md: "---\n---"},
		{name: "empty CRLF with BOM", md: "\ufeff---\r\n---\r\n# body"},
		{name: "closing at EOF", md: "---\nname: sample\n---", want: Meta{Name: "sample"}},
		{name: "closing with horizontal whitespace", md: "---\nname: sample\n--- \t\n# body", want: Meta{Name: "sample"}},
		{name: "prefix is a YAML key", md: "---\nname: sample\n---extra: ignored\ndescription: Keep this description\n---\n# body", want: Meta{Name: "sample", Description: "Keep this description"}},
		{name: "prefix alone is not closed", md: "---\nname: sample\n---extra", wantErr: true},
		{name: "comment suffix is not a delimiter", md: "---\nname: sample\n--- # comment\n", wantErr: true},
		{name: "four dashes are not closed", md: "---\nname: sample\n----\n", wantErr: true},
		{name: "suffix with whitespace is not closed", md: "---\nname: sample\n--- invalid\n", wantErr: true},
		{name: "indented content is not a delimiter", md: "---\nname: sample\ndescription: |\n  ---\n  Keep this line\n---\n", want: Meta{Name: "sample", Description: "---\nKeep this line"}},
		{name: "body is not YAML", md: "---\nname: sample\n---\n[not YAML\n---extra", want: Meta{Name: "sample"}},
		{name: "invalid YAML still rejected", md: "---\nname: [\n---\n", wantErr: true},
		{name: "no frontmatter", md: "# body\n---\nname: sample\n---\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseFrontmatter([]byte(tt.md))
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, want error = %v", err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Fatalf("metadata = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseFrontmatterLongLine(t *testing.T) {
	// Parsing must not inherit bufio.Scanner's 64 KiB line limit.
	got, err := ParseFrontmatter([]byte("---\nname: sample\ndescription: " + strings.Repeat("x", 70<<10) + "\n---\n"))
	if err != nil || got.Name != "sample" || got.Description != strings.Repeat("x", 1024) {
		t.Fatalf("name = %q, description length = %d, error = %v", got.Name, len(got.Description), err)
	}
}
