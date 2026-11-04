package capability

import (
	"encoding/json"
	"errors"
	"reflect"
)

// Curated remotes pin only client configuration, not the provider's running code.
// Endpoints are reviewed against publisher documentation; no remote manifest is
// fetched and no provider-controlled trust assertions are consumed.
func curatedRemoteCatalog() []CatalogEntry {
	out := []CatalogEntry{}
	for _, v := range []struct {
		id, title, publisher, endpoint, docs string
		aliases                              []string
	}{
		{"notion", "Notion MCP", "Notion", "https://mcp.notion.com/mcp", "https://developers.notion.com/guides/mcp/get-started-with-mcp", []string{"notion", "notes", "笔记"}},
		{"linear", "Linear MCP", "Linear", "https://mcp.linear.app/mcp", "https://linear.app/docs/mcp", []string{"linear", "issues", "项目"}},
		{"linear-readonly", "Linear MCP (read-only)", "Linear", "https://mcp.linear.app/mcp/readonly", "https://linear.app/docs/mcp", []string{"linear read-only", "只读"}},
	} {
		source := Source{Kind: "curated-remote", URL: v.endpoint, Ref: "2026-11-04.1", Adapter: "mcp-registry", Trust: "official", Publisher: v.publisher, Registry: &Registry{Name: "tidebot-curated", Entry: v.id}}
		entry := CatalogEntry{ID: v.id, Title: v.title, Description: "Official hosted MCP. OAuth and exact tool grants require separate approval. Configuration is pinned; provider code and available tools can change. Documentation: " + v.docs, Aliases: v.aliases, Source: source}
		files := curatedRemoteFiles(entry)
		_, entry.Source.Commit, _ = HashFiles(files)
		out = append(out, entry)
	}
	return out
}

func curatedRemoteFiles(entry CatalogEntry) map[string][]byte {
	raw, _ := json.Marshal(struct {
		Name    string              `json:"name"`
		Title   string              `json:"title"`
		Version string              `json:"version"`
		Remotes []map[string]string `json:"remotes"`
	}{entry.ID, entry.Title, entry.Source.Ref, []map[string]string{{"type": "streamable-http", "url": entry.Source.URL}}})
	return map[string][]byte{"server.json": raw}
}

// CuratedRemoteSnapshot accepts only an exact built-in source, including its
// configuration hash. Callers cannot claim official trust by supplying a URL,
// registry name, publisher, or imported manifest that resembles a known entry.
func CuratedRemoteSnapshot(source Source) (map[string][]byte, error) {
	for _, entry := range curatedRemoteCatalog() {
		if reflect.DeepEqual(source, entry.Source) {
			return curatedRemoteFiles(entry), nil
		}
	}
	return nil, errors.New("curated remote requires an exact pinned built-in source")
}
