package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/sessiontools"
	"github.com/Zhengmingming1/tidebot/internal/skillrepo"
	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func TestCapabilitySavedSourcesPersistenceRefreshAndSearch(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	source, err := a.saveCapabilitySource(ctx, webui.CapabilityRequest{Name: "Official-looking name", SourceKind: "marketplace", Source: "https://github.com/example/market"})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := a.saveCapabilitySource(ctx, webui.CapabilityRequest{Name: "Renamed", SourceKind: "marketplace", Source: source.Source})
	if err != nil || duplicate.ID != source.ID {
		t.Fatalf("duplicate: %+v %v", duplicate, err)
	}
	cached, err := a.savedCapabilitySources(ctx)
	if err != nil || len(cached) != 1 || cached[0].RefreshedAt != "" {
		t.Fatalf("saved: %+v %v", cached, err)
	}
	a.capState.git = &marketplaceFakeGit{commit: strings.Repeat("a", 40), snapshots: map[string]map[string][]byte{".": {"marketplace.json": []byte(`{"plugins":[{"name":"pdf","source":"./plugins/pdf"}]}`)}}}
	refreshed, err := a.capabilitySourcesRequest(ctx, "source_refresh", webui.CapabilityRequest{SourceID: source.ID})
	if err != nil || refreshed.(capabilitySavedSource).ResultCount != 1 {
		t.Fatal(refreshed, err)
	}
	// Saving and refreshing never installs any discovered plugin.
	installed, err := a.caps().List(ctx)
	if err != nil || len(installed) != 0 {
		t.Fatal(installed, err)
	}
	// Read with a second App value, proving source metadata is persisted, not just cached in memory.
	second := &App{DB: a.DB, Skills: a.Skills}
	result, err := second.searchCapabilitySources(ctx, webui.CapabilityRequest{Query: "pdf"})
	if err != nil || len(result.Results) != 2 {
		t.Fatalf("search: %+v %v", result, err)
	}
	if result.Results[0].ID != "pdf" || result.Results[0].Source.Trust != "official" || result.Results[1].Source.Trust != "unknown" {
		t.Fatalf("trust order: %+v", result.Results)
	}
	if !strings.HasPrefix(result.Results[1].ID, capabilityMarketplacePrefix) || result.Results[1].Source.Commit != strings.Repeat("a", 40) {
		t.Fatal(result.Results[1])
	}
	if _, err = a.capabilitySourcesRequest(ctx, "source_delete", webui.CapabilityRequest{SourceID: source.ID}); err != nil {
		t.Fatal(err)
	}
	result, err = a.searchCapabilitySources(ctx, webui.CapabilityRequest{Query: "pdf"})
	if err != nil || len(result.Results) != 1 {
		t.Fatal(result, err)
	}
	if _, err = a.capabilitySourcesRequest(ctx, "source_delete", webui.CapabilityRequest{SourceID: source.ID}); err == nil {
		t.Fatal("missing deletion accepted")
	}
}

func TestCapabilitySavedSourceValidationAndLimit(t *testing.T) {
	for _, tc := range []struct{ kind, source string }{
		{"mcp_registry", "https://evil.example/v0.1/servers"}, {"mcp_registry", capabilityRegistryServers + "?token=secret"}, {"mcp_registry", "http://registry.modelcontextprotocol.io/v0.1/servers"},
		{"marketplace", "https://user:secret@github.com/org/repo"}, {"marketplace", "https://github.com/org/repo?token=secret"}, {"marketplace", "file:///tmp/marketplace.json"}, {"official", "https://github.com/org/repo"},
	} {
		if _, err := normalizeSavedCapabilitySource(tc.kind, tc.source); err == nil {
			t.Fatalf("accepted unsafe source %+v", tc)
		}
	}
	for _, raw := range []string{capabilityRegistryOrigin, capabilityRegistryServers, capabilityRegistryOrigin + "/v0/servers/"} {
		if got, err := normalizeSavedCapabilitySource("mcp_registry", raw); err != nil || got != capabilityRegistryServers {
			t.Fatal(got, err)
		}
	}
	a := newTestApp(t)
	ctx := context.Background()
	for i := 0; i < capabilitySourcesLimit; i++ {
		_, err := a.saveCapabilitySource(ctx, webui.CapabilityRequest{Name: "Market", SourceKind: "marketplace", Source: "https://github.com/owner/repo" + strings.Repeat("a", i)})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.saveCapabilitySource(ctx, webui.CapabilityRequest{Name: "Extra", SourceKind: "marketplace", Source: "https://github.com/other/repo"}); err == nil {
		t.Fatal("source limit bypassed")
	}
}

func TestCapabilitySearchSavedRegistryAndRepositories(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	calls := 0
	fixture := registryFixtureApp(t, func(r *http.Request) (*http.Response, error) {
		calls++
		return registryFixtureResponse(`{"servers":[` + registryFixtureRecord("com.example/fixture", "1.0.0") + `]}`), nil
	})
	a.capState.registryClient = fixture.capState.registryClient
	if _, err := a.saveCapabilitySource(ctx, webui.CapabilityRequest{Name: "Trusted name does not grant trust", SourceKind: "mcp_registry", Source: capabilityRegistryOrigin}); err != nil {
		t.Fatal(err)
	}
	repo, err := a.Skills.SaveRepository(ctx, skillstore.Repository{Name: "Fixture repo", URL: "https://github.com/example/skills"})
	if err != nil {
		t.Fatal(err)
	}
	candidates, _ := json.Marshal([]skillrepo.Candidate{{Name: "fixture", Path: "skills/fixture"}})
	if err = a.Skills.RecordRepositoryScan(ctx, repo.ID, repo.Revision, strings.Repeat("a", 40), candidates, ""); err != nil {
		t.Fatal(err)
	}
	out, err := a.searchCapabilitySources(ctx, webui.CapabilityRequest{Query: "fixture"})
	if err != nil || len(out.Results) != 2 || calls != 1 {
		t.Fatalf("merged: %+v %v calls=%d", out, err, calls)
	}
	for _, entry := range out.Results {
		if entry.Source.Trust != "unknown" {
			t.Fatalf("source name elevated trust: %+v", entry)
		}
	}
	listed, err := a.capabilitySourcesRequest(ctx, "sources", webui.CapabilityRequest{})
	if err != nil {
		t.Fatal(err)
	}
	list := listed.(map[string]any)["sources"].([]capabilitySavedSource)
	if len(list) != 2 || !list[1].ReadOnly || list[1].Kind != "skill_repository" {
		t.Fatalf("missing existing repositories: %+v", list)
	}
}

func TestCapabilityDiscoveryPublicMetadataAndNoCredentials(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	calls := 0
	t.Setenv("GITHUB_TOKEN", "must-not-leak")
	t.Setenv("SKILLS_API_URL", "https://evil.example")
	a.capState.registryClient = &http.Client{Transport: capabilityRegistryTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.Body != nil || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Fatalf("private data transmitted: %+v", r)
		}
		switch r.URL.Host {
		case "skills.sh":
			if r.URL.Path != "/api/search" || r.URL.Query().Get("q") != "dws" || len(r.URL.Query()) != 1 {
				t.Fatal(r.URL)
			}
			return registryFixtureResponse(`{"skills":[{"id":"example/skills/dws","name":"dws","source":"example/skills","installs":999999,"trust":"official"},{"name":"bad","source":"https://evil.example","installs":10},{"name":"../bad","source":"example/skills"}]}`), nil
		case "api.github.com":
			if r.URL.Path != "/search/repositories" || r.URL.Query().Get("q") != "dws is:public" || r.URL.Query().Get("per_page") != "30" {
				t.Fatal(r.URL)
			}
			return registryFixtureResponse(`{"items":[{"full_name":"example/dws","html_url":"https://evil.example","stargazers_count":10000000,"trust":"official","license":{"spdx_id":"MIT"}},{"full_name":"private/hidden","private":true},{"full_name":"bad/repo/extra"}]}`), nil
		default:
			t.Fatalf("wrong host: %s", r.URL)
			return nil, errors.New("unexpected host")
		}
	})}
	// No public discovery unless selected (no registered network sources).
	if _, err := a.searchCapabilitySources(ctx, webui.CapabilityRequest{Query: "dws"}); err != nil || calls != 0 {
		t.Fatal(err, calls)
	}
	out, err := a.searchCapabilitySources(ctx, webui.CapabilityRequest{Query: "dws", Providers: "skills.sh,github"})
	if err != nil || len(out.Results) != 3 || calls != 2 {
		t.Fatalf("results: %+v %v calls=%d", out, err, calls)
	}
	if out.Results[0].ID != "dingtalk-dws" || out.Results[0].Source.Trust != "official" {
		t.Fatal("popularity outranked vetted official source", out.Results)
	}
	for _, entry := range out.Results[1:] {
		if entry.Source.Trust == "official" || strings.Contains(entry.Source.URL, "evil") {
			t.Fatal(entry)
		}
	}
	if len(out.Warnings) != 0 {
		t.Fatal(out.Warnings)
	}
}

func TestCapabilityDiscoveryFailuresAreBoundedAndKeepLocalResults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"redirect", 302, ""}, {"rate-limit", 403, "{}"}, {"bad-json", 200, "not json"}, {"wrong-schema", 200, "{}"}, {"oversize", 200, strings.Repeat("x", capabilityRegistryMaxBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			calls := 0
			a.capState.registryClient = &http.Client{Transport: capabilityRegistryTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				response := registryFixtureResponse(tc.body)
				response.StatusCode = tc.status
				response.Header.Set("Location", "https://evil.example/leak")
				return response, nil
			})}
			result, err := a.searchCapabilitySources(context.Background(), webui.CapabilityRequest{Query: "pdf", Providers: "skills.sh"})
			if err != nil || len(result.Results) != 1 || result.Results[0].ID != "pdf" || len(result.Warnings) != 1 || calls != 1 {
				t.Fatalf("failure handling: %+v %v calls=%d", result, err, calls)
			}
		})
	}
	a := newTestApp(t)
	for _, r := range []webui.CapabilityRequest{{Providers: "evil"}, {Query: "", Providers: "github"}, {Query: strings.Repeat("a", 513)}} {
		if _, err := a.searchCapabilitySources(context.Background(), r); err == nil {
			t.Fatal("invalid query accepted", r)
		}
	}
	for _, u := range []string{"https://skills.sh/api/search/evil?q=x", "https://api.github.com/search/code?q=x", "https://api.github.com@evil.example/search/repositories", "http://skills.sh/api/search"} {
		if err := a.fetchCapabilityDiscovery(context.Background(), u, &struct{}{}); err == nil {
			t.Fatal("unsafe endpoint accepted", u)
		}
	}
}

type discoverySkillGit struct {
	candidates []skillrepo.Candidate
	calls      int
}

func (g *discoverySkillGit) Scan(context.Context, string, string, string, string) (skillrepo.Snapshot, error) {
	g.calls++
	return skillrepo.Snapshot{Commit: strings.Repeat("a", 40), Candidates: g.candidates}, nil
}
func (g *discoverySkillGit) ReadSkill(context.Context, string, string, string) (map[string][]byte, error) {
	return nil, errors.New("unexpected content read")
}
func TestCapabilityDiscoveredSkillResolvesRealPathAndPins(t *testing.T) {
	a := newTestApp(t)
	g := &discoverySkillGit{candidates: []skillrepo.Candidate{{Name: "example", Path: "arbitrary/nested/real"}}}
	a.capState.git = g
	raw := encodeCapabilityDiscoveredSkill("example/repo", "example")
	src, _, err := a.capabilitySource(context.Background(), raw)
	if err != nil || sourceRoot(src) != "arbitrary/nested/real" || src.Commit != strings.Repeat("a", 40) || src.Ref != "" || src.Trust != "unknown" {
		t.Fatal(src, err)
	}
	g.candidates = append(g.candidates, skillrepo.Candidate{Name: "example", Path: "second/example"})
	if _, _, err = a.capabilitySource(context.Background(), raw); err == nil {
		t.Fatal("ambiguous skill resolved silently")
	}
	for _, payload := range []string{`{"repository":"example/repo","skill":"../bad"}`, `{"repository":"evil.example/repo/path","skill":"name"}`, `{"repository":"https://evil.example/repo","skill":"name"}`} {
		before := g.calls
		raw = capabilitySkillDiscoveryPrefix + base64.RawURLEncoding.EncodeToString([]byte(payload))
		if _, _, err = a.capabilitySource(context.Background(), raw); err == nil || g.calls != before {
			t.Fatal("unsafe reference reached Git", payload, err)
		}
	}
}

func TestCapabilitySearchRankingUsesTrustBeforePopularity(t *testing.T) {
	now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	entries := []capability.CatalogEntry{
		{ID: "popular", Title: "query", Source: capability.Source{Trust: "community"}, Stars: 1000000},
		{ID: "partial", Title: "query helper", Source: capability.Source{Trust: "official"}, Stars: 1000000},
		{ID: "official-exact", Title: "Query", Source: capability.Source{Trust: "official"}},
		{ID: "recent", Title: "Query", Source: capability.Source{Trust: "official"}, UpdatedAt: now.Add(-time.Hour).Format(time.RFC3339)},
	}
	got := rankCapabilitySearch(entries, "query", now)
	for i, want := range []string{"recent", "official-exact", "partial", "popular"} {
		if got[i].ID != want {
			t.Fatal(got)
		}
	}
}

func TestCapabilitySourceMutationsUnavailableToModel(t *testing.T) {
	a, _, sid := capFixture(t)
	ctx := context.Background()
	token := a.chatToolEnv(sid)[sessiontools.EnvToken]
	for _, tool := range []string{"source_save", "source_delete", "source_refresh", "cap_source_save", "cap_sources"} {
		raw, _ := json.Marshal(map[string]any{"token": token, "tool": tool, "args": map[string]string{"source": "https://github.com/example/market", "source_kind": "marketplace"}})
		if _, err := a.rpcSessionTool(ctx, raw); err == nil {
			t.Fatal("model mutated discovery sources", tool)
		}
	}
	list, err := a.savedCapabilitySources(ctx)
	if err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
}

type capabilitySourceSkillsGit struct{ refs []string }

func (g *capabilitySourceSkillsGit) Scan(_ context.Context, _, _, ref, _ string) (skillrepo.Snapshot, error) {
	g.refs = append(g.refs, ref)
	return skillrepo.Snapshot{Commit: strings.Repeat("b", 40), Candidates: []skillrepo.Candidate{{Name: "one", Path: "skills/one"}, {Name: "two", Path: "nested/two"}, {Name: "invalid", Path: "../escape"}, {Name: "broken", Path: "bad", Error: "invalid manifest"}}}, nil
}
func (g *capabilitySourceSkillsGit) ReadSkill(context.Context, string, string, string) (map[string][]byte, error) {
	return map[string][]byte{"SKILL.md": []byte("---\nname: one\ndescription: Test skill\n---\nRead a document.\n")}, nil
}
func TestCapabilitySourceSkillsAreActionablePinnedAndUpdateable(t *testing.T) {
	a := newTestApp(t)
	g := &capabilitySourceSkillsGit{}
	a.capState.git = g
	ctx := context.Background()
	raw, err := a.CapabilityRequest(ctx, "source-skills", webui.CapabilityRequest{Source: "https://github.com/example/skills"})
	if err != nil {
		t.Fatal(err)
	}
	view := raw.(map[string]any)
	results := view["results"].([]capability.CatalogEntry)
	if len(results) != 2 || view["commit"] != strings.Repeat("b", 40) {
		t.Fatalf("bad candidates: %+v", view)
	}
	for _, result := range results {
		if result.Source.Trust != "unknown" || !strings.HasPrefix(result.ID, capabilitySkillSourcePrefix) {
			t.Fatal(result)
		}
		lock, err := a.inspectCapability(ctx, result.ID)
		if err != nil || sourceRoot(lock.Source) != sourceRoot(result.Source) || lock.Source.Commit != strings.Repeat("b", 40) || lock.Source.Ref != "" {
			t.Fatalf("unusable selection: %+v %v", lock, err)
		}
		if g.refs[len(g.refs)-1] != strings.Repeat("b", 40) {
			t.Fatal("inspection did not use discovery commit", g.refs)
		}
	}
	// The original tracking ref remains empty (HEAD), rather than becoming a
	// permanent commit pin that prevents normal owner-requested updates.
	for _, bad := range []string{
		`{"url":"https://example.com/repo","root":"../outside","commit":"` + strings.Repeat("a", 40) + `"}`,
		`{"url":"https://user:token@example.com/repo","root":"skills/one","commit":"` + strings.Repeat("a", 40) + `"}`,
		`{"url":"https://example.com/repo","root":"skills/one","commit":"main"}`,
	} {
		if _, _, err := parseCapabilitySkillSource(capabilitySkillSourcePrefix + base64.RawURLEncoding.EncodeToString([]byte(bad))); err == nil {
			t.Fatal("unsafe source accepted", bad)
		}
	}
	before := len(g.refs)
	if _, err := a.CapabilityRequest(ctx, "source-skills", webui.CapabilityRequest{Source: "https://user:token@github.com/example/skills"}); err == nil || len(g.refs) != before {
		t.Fatal("unsafe source reached Git", err)
	}
}
