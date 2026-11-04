package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func TestCuratedGitHubBinaryPinsReleaseSnapshot(t *testing.T) {
	a := &App{}
	repo := "github/github-mcp-server"
	name, checksums, _, _ := releasePlatformAsset("github-mcp-server", "1.2.3", runtime.GOOS, runtime.GOARCH)
	if name == "" {
		t.Skip("unsupported host platform")
	}
	hash := strings.Repeat("a", 64)
	base := "https://github.com/" + repo + "/releases/download/v1.2.3/"
	latest := true
	calls := 0
	a.capState.releaseClient = &http.Client{Transport: recipeTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var body string
		switch r.URL.String() {
		case "https://api.github.com/repos/" + repo + "/releases/latest":
			if !latest {
				t.Fatal("confirmation re-resolved latest")
			}
			fallthrough
		case "https://api.github.com/repos/" + repo + "/releases/tags/v1.2.3":
			b, _ := json.Marshal(capabilityRelease{Tag: "v1.2.3", Assets: []capabilityReleaseAsset{{Name: name, URL: base + name, Digest: "sha256:" + hash}, {Name: checksums, URL: base + checksums}}})
			body = string(b)
		case base + checksums:
			body = hash + "  " + name + "\n"
		default:
			t.Fatalf("unexpected request or artifact download: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	source, _, e := capSource("github-mcp-server")
	if e != nil {
		t.Fatal(e)
	}
	lock, e := a.inspectCuratedBinaryCapability(context.Background(), source)
	if e != nil {
		t.Fatal(e)
	}
	if lock.Source.Ref != "v1.2.3" || lock.Requires.Tools[0].Recipes[0].Version != "1.2.3" || lock.Components[0].MCP.Args[1] != "--read-only" {
		t.Fatalf("wrong pin: %+v", lock)
	}
	if lock.Scan.Verdict != "caution" || len(lock.Requires.Secrets) != 1 {
		t.Fatal("missing integrity disclosure/secret requirement")
	}
	latest = false
	files, e := a.readCuratedBinaryCapabilitySnapshot(context.Background(), lock.Source)
	if e != nil {
		t.Fatal(e)
	}
	_, got, _ := capability.HashFiles(files)
	if got != lock.FilesSHA256 || calls != 4 {
		t.Fatalf("snapshot differs: %s %s calls=%d", got, lock.FilesSHA256, calls)
	}
	hash = strings.Repeat("b", 64)
	files, e = a.readCuratedBinaryCapabilitySnapshot(context.Background(), lock.Source)
	if e != nil {
		t.Fatal(e)
	}
	_, got, _ = capability.HashFiles(files)
	if got == lock.FilesSHA256 {
		t.Fatal("mutated checksum did not change snapshot")
	}
	for _, change := range []func(*capability.Source){func(s *capability.Source) { s.Ref = "latest" }, func(s *capability.Source) { s.URL = "https://github.com/other/server" }, func(s *capability.Source) { s.Trust = "unknown" }, func(s *capability.Source) { s.Ref = "v1.2.3/../../latest" }} {
		bad := lock.Source
		change(&bad)
		if _, e = a.readCuratedBinaryCapabilitySnapshot(context.Background(), bad); e == nil {
			t.Fatalf("accepted forged source: %+v", bad)
		}
	}
}
func TestCuratedGitHubBinaryRejectsWrongPinnedTag(t *testing.T) {
	a := &App{}
	a.capState.releaseClient = &http.Client{Transport: recipeTransport(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/tags/v1.2.3") {
			t.Fatalf("unexpected endpoint %s", r.URL)
		}
		return registryFixtureResponse(`{"tag_name":"v1.2.4","assets":[]}`), nil
	})}
	if _, e := a.resolveOfficialReleaseRecipeAt(context.Background(), capability.ToolRequirement{Name: "github-mcp-server"}, "linux", "amd64", "v1.2.3"); e == nil {
		t.Fatal("wrong pinned tag accepted")
	}
}

func TestCuratedGitHubBinaryProposalConfirmKeepsScan(t *testing.T) {
	a, _, sid := capFixture(t)
	client, calls := releaseFixtureClient(t, "github-mcp-server", runtime.GOOS, runtime.GOARCH, nil)
	baseTransport := client.Transport
	inspected := false
	client.Transport = recipeTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") && inspected {
			t.Fatal("confirmation resolved latest")
		}
		if strings.HasSuffix(r.URL.Path, "/releases/tags/v1.2.3") {
			clone := r.Clone(r.Context())
			u := *r.URL
			u.Path = strings.TrimSuffix(u.Path, "/tags/v1.2.3") + "/latest"
			clone.URL = &u
			return baseTransport.RoundTrip(clone)
		}
		return baseTransport.RoundTrip(r)
	})
	a.capState.releaseClient = client
	value, err := a.CapabilityRequest(context.Background(), "propose", webui.CapabilityRequest{SessionID: sid, Source: "github-mcp-server"})
	if err != nil {
		t.Fatal(err)
	}
	inspected = true
	proposal := value.(capabilityProposalView)
	if proposal.Lock.Scan.Verdict != "caution" {
		t.Fatalf("missing review disclosure: %+v", proposal.Lock.Scan)
	}
	value, err = a.CapabilityRequest(context.Background(), "confirm", webui.CapabilityRequest{SessionID: sid, ProposalID: proposal.ID, InstallDependencies: false})
	if err != nil {
		t.Fatal(err)
	}
	installed := value.(capability.Record)
	if installed.Lock.Scan.Verdict != proposal.Lock.Scan.Verdict || installed.Lock.Source.Ref != "v1.2.3" || *calls != 4 {
		t.Fatalf("unexpected confirmation: %+v calls=%d", installed.Lock, *calls)
	}
	if err := a.caps().Verify(context.Background(), installed.ID, installed.VersionID); err != nil {
		t.Fatal(err)
	}
}
