package capability

// Curated binaries use publisher-owned release metadata, never a README install
// command. Inspection pins the release tag and platform checksum before review.
func curatedBinaryCatalog() []CatalogEntry {
	return []CatalogEntry{{ID: "github-mcp-server", Title: "GitHub MCP", Description: "Official GitHub MCP binary, read-only tools by default. Inspect pins the release and SHA256; token and tool grants require approval.", Aliases: []string{"github", "github mcp", "GitHub MCP 二进制"}, Source: Source{Kind: "curated-binary", URL: "https://github.com/github/github-mcp-server", Ref: "latest", Adapter: "tidebot-native", Trust: "official", Publisher: "github", Registry: &Registry{Name: "tidebot-curated", Entry: "github-mcp-server"}}}}
}
