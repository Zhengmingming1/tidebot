package capability

import "strings"

type CatalogEntry struct {
	Labels            []string     `json:"labels,omitempty"`
	DiscoveryProvider string       `json:"discovery_provider,omitempty"`
	Stars             int64        `json:"stars,omitempty"`
	Installs          int64        `json:"installs,omitempty"`
	UpdatedAt         string       `json:"updated_at,omitempty"`
	ID                string       `json:"id"`
	Title             string       `json:"title"`
	Description       string       `json:"description"`
	Aliases           []string     `json:"aliases"`
	Source            Source       `json:"source"`
	Requires          Requirements `json:"requires"`
}

// Catalog contains discovery hints, not install authority: inspect must resolve
// Git refs to commits or curated remote configuration hashes, scan actual files,
// and generate a fresh confirmation.
func Catalog() []CatalogEntry {
	out := []CatalogEntry{}
	for _, v := range []struct {
		id, title string
		aliases   []string
	}{
		{"pdf", "PDF", []string{"document", "文档"}}, {"docx", "Word documents", []string{"word", "文档"}}, {"xlsx", "Spreadsheets", []string{"excel", "表格"}}, {"pptx", "Presentations", []string{"slides", "幻灯片"}}, {"brand-guidelines", "Brand guidelines", []string{"brand", "品牌"}}, {"canvas-design", "Canvas design", []string{"design", "设计"}}, {"frontend-design", "Frontend design", []string{"frontend", "前端"}}, {"skill-creator", "Skill creator", []string{"skills", "技能"}}, {"webapp-testing", "Web application testing", []string{"playwright", "testing", "测试"}},
	} {
		out = append(out, CatalogEntry{ID: v.id, Title: v.title, Description: "Anthropic skill; dependencies and license are checked from the pinned source.", Aliases: v.aliases, Source: Source{Kind: "git", URL: "https://github.com/anthropics/skills", Ref: "main", Subpaths: []string{"skills/" + v.id}, Adapter: "skill-dir", Trust: "official", Publisher: "anthropics", Registry: &Registry{Name: "tidebot-curated", Entry: v.id}}})
	}
	out = append(out, CatalogEntry{ID: "dingtalk-dws", Title: "钉钉 DWS", Description: "DingTalk workspace CLI skill; inspect pins an official release recipe when checksums are available.", Aliases: []string{"钉钉", "dingtalk", "dws"}, Source: Source{Kind: "git", URL: "https://github.com/DingTalk-Real-AI/dingtalk-workspace-cli", Ref: "main", Subpaths: []string{"skills/mono"}, Adapter: "skill-dir", Trust: "official", Publisher: "DingTalk-Real-AI", Registry: &Registry{Name: "tidebot-curated", Entry: "dingtalk-dws"}}, Requires: Requirements{Tools: []ToolRequirement{{Name: "dws", Version: ">=1.0.61", Check: []string{"--version"}}}}})
	out = append(out, curatedRemoteCatalog()...)
	out = append(out, curatedBinaryCatalog()...)
	return out
}
func SearchCatalog(query string) []CatalogEntry {
	q := strings.ToLower(strings.TrimSpace(query))
	exact, partial := []CatalogEntry{}, []CatalogEntry{}
	for _, e := range Catalog() {
		terms := append([]string{e.ID, e.Title}, e.Aliases...)
		match, isExact := q == "", false
		for _, s := range terms {
			s = strings.ToLower(s)
			if s == q {
				isExact = true
			}
			if strings.Contains(s, q) {
				match = true
			}
		}
		if isExact {
			exact = append(exact, e)
		} else if match {
			partial = append(partial, e)
		}
	}
	return append(exact, partial...)
}
