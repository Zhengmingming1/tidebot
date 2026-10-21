package webui

import (
	"context"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/skillpkg"
)

// skillDescription returns the SKILL.md frontmatter description of a version,
// whitespace-collapsed and cut to 200 characters ("" when absent).
func (s *Server) skillDescription(ctx context.Context, versionID string) string {
	if s.deps.Skills == nil || versionID == "" {
		return ""
	}
	fs, err := s.deps.Skills.Files(ctx, versionID)
	if err != nil {
		return ""
	}
	md, ok := fs["SKILL.md"]
	if !ok {
		return ""
	}
	m, err := skillpkg.ParseFrontmatter(md)
	if err != nil {
		return ""
	}
	d := strings.Join(strings.Fields(m.Description), " ")
	if r := []rune(d); len(r) > 200 {
		d = string(r[:200])
	}
	return d
}
