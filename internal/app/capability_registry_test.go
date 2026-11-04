package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/sessiontools"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

type capabilityRegistryTransport func(*http.Request) (*http.Response, error)

func (f capabilityRegistryTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func registryFixtureApp(t *testing.T, respond func(*http.Request) (*http.Response, error)) *App {
	t.Helper()
	a := &App{}
	a.capState.registryClient = &http.Client{Transport: capabilityRegistryTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "registry.modelcontextprotocol.io" {
			t.Fatalf("unexpected registry request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Body != nil {
			t.Fatal("registry discovery transmitted credentials or body")
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Fatal("missing JSON accept header")
		}
		return respond(r)
	})}
	return a
}

func registryFixtureResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: -1}
}

func registryFixtureRecord(name, version string) string {
	raw, _ := json.Marshal(map[string]any{
		"server": map[string]any{
			"name": name, "version": version, "title": "Fixture MCP", "description": "An inert test server",
			"remotes": []map[string]string{{"type": "streamable-http", "url": "https://mcp.example.com/mcp"}},
		},
		"_meta": map[string]any{"io.modelcontextprotocol.registry/official": map[string]any{"status": "active", "isLatest": true}},
	})
	return string(raw)
}

func registryErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var capErr *webui.CapabilityError
	if !errors.As(err, &capErr) || capErr.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestCapabilityRegistrySearchUsesFixedOriginAndNamespaceTrust(t *testing.T) {
	unknown := registryFixtureRecord("com.example/fixture", "1.0.0")
	// Upstream assertions are not trust authority, including misleading nested
	// registry metadata copied into the publisher-controlled server document.
	unknown = strings.Replace(unknown, `"description":`, `"trust":"official","publisher":"anthropics","_meta":{"io.modelcontextprotocol.registry/official":{"verified":true}},"description":`, 1)
	verified := registryFixtureRecord("io.github.anthropics/fixture", "1.0.0")
	lookalike := registryFixtureRecord("io.github.anthropics.evil/fixture", "1.0.0")
	deleted := strings.Replace(registryFixtureRecord("com.example/deleted", "1.0.0"), `"active"`, `"deleted"`, 1)
	stale := strings.Replace(registryFixtureRecord("com.example/stale", "1.0.0"), `"isLatest":true`, `"isLatest":false`, 1)
	malformed := registryFixtureRecord("com.example/bad", "latest")
	unsupported := strings.Replace(registryFixtureRecord("com.example/unsafe", "1.0.0"), `https://mcp.example.com/mcp`, `http://127.0.0.1/mcp`, 1)
	calls := 0
	a := registryFixtureApp(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/v0.1/servers" || r.URL.Query().Get("search") != "a &b? /中文" || r.URL.Query().Get("version") != "latest" || r.URL.Query().Get("limit") != "50" {
			t.Fatalf("unexpected query: %s", r.URL)
		}
		return registryFixtureResponse(`{"servers":[` + strings.Join([]string{unknown, verified, lookalike, verified, deleted, stale, malformed, unsupported}, ",") + `],"metadata":{"nextCursor":"https://evil.example/ignored"}}`), nil
	})
	results, err := a.searchCapabilityRegistry(context.Background(), " a &b? /中文 ")
	if err != nil || len(results) != 3 || calls != 1 {
		t.Fatalf("search: %+v, calls=%d, err=%v", results, calls, err)
	}
	for i, want := range []string{"unknown", "verified", "unknown"} {
		if results[i].Source.Trust != want || results[i].Source.Trust == "official" || results[i].Source.Ref != "1.0.0" || results[i].Source.Adapter != "mcp-registry" || results[i].Source.Kind != "registry" {
			t.Fatalf("bad source: %+v", results[i])
		}
	}
	if results[0].ID != "mcp-registry:com.example/fixture" || results[0].Source.Registry.Entry != "com.example/fixture" || results[0].Source.Publisher != "com.example" {
		t.Fatalf("bad registry identity: %+v", results[0])
	}
}

func TestCapabilityRegistryInspectionPinsVersionAndSnapshot(t *testing.T) {
	const name = "com.example/fixture"
	const version = "1.2.3+build.4"
	latest := registryFixtureRecord(name, version)
	pinned := latest
	var paths []string
	a := registryFixtureApp(t, func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.EscapedPath())
		switch r.URL.String() {
		case capabilityRegistryURL(name, "latest"):
			return registryFixtureResponse(latest), nil
		case capabilityRegistryURL(name, version):
			return registryFixtureResponse(pinned), nil
		default:
			t.Fatalf("unpinned or unexpected request: %s", r.URL)
			return nil, errors.New("unexpected request")
		}
	})
	lock, err := a.inspectCapabilityRegistry(context.Background(), capabilityRegistryPrefix+name)
	if err != nil {
		t.Fatal(err)
	}
	if lock.Source.Ref != version || lock.Source.URL != capabilityRegistryURL(name, version) || lock.Source.Commit != "" || len(lock.Components) != 1 || lock.Components[0].MCP.Type != "http" {
		t.Fatalf("bad lock: %+v", lock)
	}
	if len(paths) != 1 || !strings.Contains(paths[0], "com.example%2Ffixture") {
		t.Fatalf("server name was not escaped: %v", paths)
	}
	if lock.FilesSHA256 == "" || a.capState.inspected[lock.ID].FilesSHA256 != lock.FilesSHA256 || lock.Scan.Verdict != "caution" {
		t.Fatalf("lock not cached/scanned: %+v", lock)
	}
	found := false
	for _, finding := range lock.Scan.Findings {
		found = found || finding.ID == "registry-identity-only" && strings.Contains(finding.Note, "not a security audit")
	}
	if !found {
		t.Fatal("missing identity-versus-code-audit warning")
	}
	// Moving latest and registry-owned metadata cannot change the inspected pin.
	latest = registryFixtureRecord(name, "2.0.0")
	pinned = strings.Replace(pinned, `"isLatest":true`, `"isLatest":false`, 1)
	var document any
	if err := json.Unmarshal([]byte(pinned), &document); err != nil {
		t.Fatal(err)
	}
	pretty, _ := json.MarshalIndent(document, "", "  ")
	pinned = string(pretty)
	files, err := a.readCapabilityRegistrySnapshot(context.Background(), lock.Source)
	if err != nil {
		t.Fatal(err)
	}
	_, hash, err := capability.HashFiles(files)
	if err != nil || hash != lock.FilesSHA256 || len(files) != 1 {
		t.Fatalf("snapshot hash changed with formatting: %s %v", hash, err)
	}
	if _, err := a.proposalFiles(context.Background(), capability.Proposal{Lock: lock}); err != nil {
		t.Fatalf("proposal failed to re-read pinned registry snapshot: %v", err)
	}
	pinned = strings.Replace(pinned, "An inert test server", "Changed after review", 1)
	files, err = a.readCapabilityRegistrySnapshot(context.Background(), lock.Source)
	if err != nil {
		t.Fatal(err)
	}
	_, changed, _ := capability.HashFiles(files)
	if changed == lock.FilesSHA256 {
		t.Fatal("reviewed file mutation did not change hash")
	}
	if _, err := a.proposalFiles(context.Background(), capability.Proposal{Lock: lock}); err == nil {
		t.Fatal("proposal accepted mutation of reviewed registry snapshot")
	} else {
		registryErrorCode(t, err, "source_changed")
	}
	for _, path := range paths[1:] {
		if !strings.Contains(path, "/versions/"+version) {
			t.Fatalf("snapshot re-read latest: %v", paths)
		}
	}
}

func TestCapabilityRegistryCanonicalSnapshotPreservesUnknownFields(t *testing.T) {
	response := `{"server":{"version":"1.0.0","name":"com.example/fixture","_meta":{"publisher":{"build":9007199254740993}},"remotes":[{"url":"https://mcp.example.com/mcp","type":"streamable-http"}]},"_meta":{"unrelated-extension":"not an object"}}`
	a := registryFixtureApp(t, func(*http.Request) (*http.Response, error) { return registryFixtureResponse(response), nil })
	lock, err := a.inspectCapabilityRegistry(context.Background(), "mcp-registry:com.example/fixture")
	if err != nil {
		t.Fatal(err)
	}
	response = `{"server":{"_meta":{"publisher":{"build":9007199254740993}},"name":"com.example/fixture","remotes":[{"type":"streamable-http","url":"https://mcp.example.com/mcp"}],"version":"1.0.0"}}`
	files, err := a.proposalFiles(context.Background(), capability.Proposal{Lock: lock})
	if err != nil || !strings.Contains(string(files["server.json"]), "9007199254740993") {
		t.Fatalf("canonicalization lost publisher data or numeric precision: %s %v", files["server.json"], err)
	}
}

func TestCapabilityRegistryProposalConfirmationRequiresReviewAndVerifiesPin(t *testing.T) {
	for _, mutate := range []bool{false, true} {
		name := "install"
		if mutate {
			name = "changed_snapshot"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			a := newTestApp(t)
			calls := 0
			pinned := registryFixtureRecord("com.example/fixture", "1.0.0")
			fixture := registryFixtureApp(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					if r.URL.String() != capabilityRegistryURL("com.example/fixture", "latest") {
						t.Fatalf("inspection did not resolve latest: %s", r.URL)
					}
				} else if r.URL.String() != capabilityRegistryURL("com.example/fixture", "1.0.0") {
					t.Fatalf("confirmation did not use reviewed version: %s", r.URL)
				}
				return registryFixtureResponse(pinned), nil
			})
			a.capState.registryClient = fixture.capState.registryClient
			session, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
			if err != nil {
				t.Fatal(err)
			}
			value, err := a.CapabilityRequest(ctx, "propose", webui.CapabilityRequest{Source: "mcp-registry:com.example/fixture", SessionID: session.ID})
			if err != nil {
				t.Fatal(err)
			}
			proposal := value.(capabilityProposalView)
			if !proposal.Policy.ReviewRequired || !proposal.Policy.SecondConfirmationRequired || proposal.Lock.Source.Ref != "1.0.0" {
				t.Fatalf("registry proposal bypassed review: %+v", proposal)
			}
			request := webui.CapabilityRequest{ProposalID: proposal.ID, SessionID: session.ID}
			_, err = a.CapabilityRequest(ctx, "confirm", request)
			registryErrorCode(t, err, "review_required")
			request.Reviewed = true
			_, err = a.CapabilityRequest(ctx, "confirm", request)
			registryErrorCode(t, err, "review_required")
			if calls != 1 {
				t.Fatal("confirmation read snapshot before required review")
			}
			request.SecondaryConfirm = true
			if mutate {
				pinned = strings.Replace(pinned, "An inert test server", "Modified after review", 1)
			}
			value, err = a.CapabilityRequest(ctx, "confirm", request)
			if mutate {
				registryErrorCode(t, err, "source_changed")
				records, err := a.caps().List(ctx)
				if err != nil || len(records) != 0 {
					t.Fatalf("tampered metadata was installed: %+v %v", records, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			record := value.(capability.Record)
			if record.Lock.Source.Ref != "1.0.0" || record.Lock.FilesSHA256 != proposal.Lock.FilesSHA256 || record.Lock.Components[0].MCP.URL != "https://mcp.example.com/mcp" || calls != 2 {
				t.Fatalf("incorrect installed snapshot: %+v, requests=%d", record, calls)
			}
			if err := a.caps().Verify(ctx, record.ID, record.VersionID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCapabilityRegistryRejectsInvalidIdentityAndVersions(t *testing.T) {
	for _, source := range []string{"https://evil.example/fixture", "mcp-registry:", "mcp-registry:com.example/../other", "mcp-registry:com.example/%2Fother", "mcp-registry:com.example/fixture?evil=1", "mcp-registry:com..example/fixture", "mcp-registry:com.example/fixture\n"} {
		t.Run(source, func(t *testing.T) {
			a := registryFixtureApp(t, func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid source made a network request")
				return nil, errors.New("unexpected request")
			})
			// Leading/trailing whitespace is accepted by the public inspection
			// entry point, but control characters within a name are not.
			if strings.HasSuffix(source, "\n") {
				source += "x"
			}
			_, err := a.inspectCapabilityRegistry(context.Background(), source)
			registryErrorCode(t, err, "invalid_source")
		})
	}
	for _, version := range []string{"", "latest", "LATEST", "^1.0.0", ">=1.0.0", "1.x", "1.*", "../latest"} {
		t.Run("version_"+version, func(t *testing.T) {
			a := registryFixtureApp(t, func(*http.Request) (*http.Response, error) {
				return registryFixtureResponse(registryFixtureRecord("com.example/fixture", version)), nil
			})
			_, err := a.inspectCapabilityRegistry(context.Background(), "mcp-registry:com.example/fixture")
			registryErrorCode(t, err, "invalid_registry_response")
		})
	}
}

func TestCapabilityRegistrySnapshotRejectsSourceAndResponseMismatch(t *testing.T) {
	for _, mutate := range []func(*capability.Source){
		func(s *capability.Source) { s.URL = "https://evil.example/metadata" },
		func(s *capability.Source) { s.URL = strings.Replace(s.URL, "https:", "http:", 1) },
		func(s *capability.Source) { s.Ref = "latest" },
		func(s *capability.Source) { s.Registry = nil },
		func(s *capability.Source) { s.Registry.Name = "custom" },
		func(s *capability.Source) { s.Registry.Entry = "com.other/server" },
		func(s *capability.Source) { s.Kind = "git" },
		func(s *capability.Source) { s.Adapter = "skill-dir" },
	} {
		source := capabilityRegistrySource("com.example/fixture", "1.0.0")
		mutate(&source)
		a := registryFixtureApp(t, func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid source made a network request")
			return nil, errors.New("unexpected request")
		})
		_, err := a.readCapabilityRegistrySnapshot(context.Background(), source)
		registryErrorCode(t, err, "invalid_source")
	}
	for _, response := range []string{registryFixtureRecord("com.other/fixture", "1.0.0"), registryFixtureRecord("com.example/fixture", "2.0.0")} {
		a := registryFixtureApp(t, func(*http.Request) (*http.Response, error) { return registryFixtureResponse(response), nil })
		_, err := a.readCapabilityRegistrySnapshot(context.Background(), capabilityRegistrySource("com.example/fixture", "1.0.0"))
		registryErrorCode(t, err, "source_changed")
	}
	a := registryFixtureApp(t, func(*http.Request) (*http.Response, error) {
		return registryFixtureResponse(registryFixtureRecord("com.other/fixture", "1.0.0")), nil
	})
	_, err := a.inspectCapabilityRegistry(context.Background(), "mcp-registry:com.example/fixture")
	registryErrorCode(t, err, "invalid_registry_response")
}

func TestCapabilityRegistryHTTPFailuresAreBoundedAndDoNotRedirect(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		status           int
		length           int64
	}{
		{name: "redirect", status: http.StatusFound, code: "registry_unavailable"},
		{name: "not_found", status: http.StatusNotFound, code: "not_found"},
		{name: "unavailable", status: http.StatusServiceUnavailable, code: "registry_unavailable"},
		{name: "content_length", status: http.StatusOK, length: capabilityRegistryMaxBytes + 1, code: "registry_response_limit"},
		{name: "chunked_body", status: http.StatusOK, body: strings.Repeat(" ", capabilityRegistryMaxBytes+1), length: -1, code: "registry_response_limit"},
		{name: "malformed_json", status: http.StatusOK, body: `{"servers":`, code: "invalid_registry_response"},
		{name: "trailing_json", status: http.StatusOK, body: `{"servers":[]} {}`, code: "invalid_registry_response"},
		{name: "missing_servers", status: http.StatusOK, body: `{}`, code: "invalid_registry_response"},
		{name: "too_many_results", status: http.StatusOK, body: `{"servers":[` + strings.Repeat(`{},`, capabilityRegistryLimit) + `{}` + `]}`, code: "invalid_registry_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			a := registryFixtureApp(t, func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": {"https://evil.example/redirect"}}, Body: io.NopCloser(strings.NewReader(tc.body)), ContentLength: tc.length}, nil
			})
			a.capState.registryClient.CheckRedirect = func(*http.Request, []*http.Request) error {
				t.Fatal("used an injected permissive redirect policy")
				return nil
			}
			_, err := a.searchCapabilityRegistry(context.Background(), "fixture")
			registryErrorCode(t, err, tc.code)
			if calls != 1 {
				t.Fatalf("unexpected HTTP calls: %d", calls)
			}
		})
	}
}

func TestCapabilityRegistryHonorsCancellationAndClientTimeout(t *testing.T) {
	a := registryFixtureApp(t, func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.searchCapabilityRegistry(ctx, "fixture"); !errors.Is(err, context.Canceled) {
		t.Fatalf("context cancellation lost: %v", err)
	}
	a.capState.registryClient.Timeout = time.Hour
	if client := a.capabilityRegistryHTTPClient(); client.Timeout != 15*time.Second || a.capState.registryClient.Timeout != time.Hour {
		t.Fatal("client timeout was not bounded independently of injected client")
	}
}

func TestCapabilityRegistryEmptySearchAndInvalidEndpoint(t *testing.T) {
	calls := 0
	a := registryFixtureApp(t, func(*http.Request) (*http.Response, error) {
		calls++
		return registryFixtureResponse(`{"servers":[]}`), nil
	})
	results, err := a.searchCapabilityRegistry(context.Background(), "")
	if err != nil || results == nil || len(results) != 0 {
		t.Fatalf("empty search: %#v %v", results, err)
	}
	_, err = a.searchCapabilityRegistry(context.Background(), strings.Repeat("x", 513))
	registryErrorCode(t, err, "invalid_query")
	for _, endpoint := range []string{"https://evil.example/v0.1/servers", "http://registry.modelcontextprotocol.io/v0.1/servers", "https://registry.modelcontextprotocol.io:443/v0.1/servers", "https://user@registry.modelcontextprotocol.io/v0.1/servers", "https://registry.modelcontextprotocol.io/v0.1/servers#fragment", "https://registry.modelcontextprotocol.io/other"} {
		err = a.fetchCapabilityRegistry(context.Background(), endpoint, &struct{}{})
		registryErrorCode(t, err, "invalid_source")
	}
	if calls != 1 {
		t.Fatal("invalid query or endpoint made a request")
	}
}

func TestCapabilityRegistrySessionSearchRequiresExplicitRegistry(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	calls := 0
	fixture := registryFixtureApp(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/v0.1/servers" || r.URL.Query().Get("search") != "fixture" {
			t.Fatalf("unexpected registry search: %s", r.URL)
		}
		return registryFixtureResponse(`{"servers":[` + registryFixtureRecord("com.example/fixture", "1.0.0") + `]}`), nil
	})
	a.capState.registryClient = fixture.capState.registryClient
	session, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	token := a.chatToolEnv(session.ID)[sessiontools.EnvToken]
	search := func(registry string) (any, error) {
		raw, err := json.Marshal(map[string]any{"token": token, "tool": "cap_search", "args": map[string]string{"query": "fixture", "registry": registry}})
		if err != nil {
			t.Fatal(err)
		}
		return a.rpcSessionTool(ctx, raw)
	}
	if _, err := search(""); err != nil || calls != 0 {
		t.Fatalf("default search contacted the public registry: requests=%d err=%v", calls, err)
	}
	value, err := search("mcp")
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Results []capability.CatalogEntry `json:"results"`
	}
	if err := json.Unmarshal([]byte(value.(map[string]string)["text"]), &response); err != nil || len(response.Results) != 1 || response.Results[0].ID != "mcp-registry:com.example/fixture" || calls != 1 {
		t.Fatalf("registry tool results: %+v requests=%d err=%v", response, calls, err)
	}
	if _, err := search("https://evil.example/registry"); err == nil || calls != 1 {
		t.Fatalf("accepted unknown registry: requests=%d err=%v", calls, err)
	}
	if installed, err := a.caps().List(ctx); err != nil || len(installed) != 0 {
		t.Fatalf("search mutated installed capabilities: %+v %v", installed, err)
	}
	a.endChatToolToken(session.ID)
	if _, err := search("mcp"); err == nil || calls != 1 {
		t.Fatalf("expired token searched registry: requests=%d err=%v", calls, err)
	}
}

func TestCapabilityRegistryUpdateUsesFreshReviewedVersion(t *testing.T) {
	ctx := context.Background()
	a, _, sid := capFixture(t)
	latest := "1.0.0"
	fixture := registryFixtureApp(t, func(r *http.Request) (*http.Response, error) {
		version := latest
		if !strings.HasSuffix(r.URL.Path, "/latest") {
			version = r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		}
		return registryFixtureResponse(registryFixtureRecord("com.example/fixture", version)), nil
	})
	a.capState.registryClient = fixture.capState.registryClient
	lock, err := a.inspectCapabilityRegistry(ctx, "mcp-registry:com.example/fixture")
	if err != nil {
		t.Fatal(err)
	}
	files, err := a.readCapabilitySnapshot(ctx, lock.Source)
	if err != nil {
		t.Fatal(err)
	}
	old, err := a.caps().Install(ctx, lock, files)
	if err != nil {
		t.Fatal(err)
	}
	latest = "2.0.0"
	value, err := a.capabilityLifecycleRequest(ctx, "update", webui.CapabilityRequest{CapabilityID: old.ID, SessionID: sid})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var proposal capabilityProposalView
	if err = json.Unmarshal(raw, &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.Lock.Source.Ref != "2.0.0" || proposal.Kind != "update" || !proposal.Policy.ReviewRequired {
		t.Fatalf("bad update: %+v", proposal)
	}
	current, err := a.caps().Get(ctx, old.ID)
	if err != nil || current.VersionID != old.VersionID {
		t.Fatal("update changed installed version before confirmation", err)
	}
	// A later latest release does not change the reviewed update pin.
	latest = "3.0.0"
	value, err = a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{SessionID: sid, ProposalID: proposal.ID, Reviewed: true, SecondaryConfirm: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := value.(capability.Record).Lock.Source.Ref; got != "2.0.0" {
		t.Fatal("adopted unreviewed version", got)
	}
}
