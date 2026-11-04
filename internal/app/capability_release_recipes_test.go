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

type recipeTransport func(*http.Request) (*http.Response, error)

func (f recipeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func releaseFixtureClient(t *testing.T, tool, goos, arch string, change func(*capabilityRelease, *string)) (*http.Client, *int) {
	t.Helper()
	repo := officialToolRepository(tool)
	name, checksums, _, _ := releasePlatformAsset(tool, "1.2.3", goos, arch)
	base := "https://github.com/" + repo + "/releases/download/v1.2.3/"
	hash := strings.Repeat("a", 64)
	release := capabilityRelease{Tag: "v1.2.3", Assets: []capabilityReleaseAsset{{Name: name, URL: base + name, Digest: "sha256:" + hash}, {Name: checksums, URL: base + checksums}}}
	body := hash + "  " + name + "\n"
	if change != nil {
		change(&release, &body)
	}
	calls := 0
	client := &http.Client{Transport: recipeTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" {
			t.Fatalf("unexpected request %s", r.Method)
		}
		content := ""
		switch r.URL.String() {
		case "https://api.github.com/repos/" + repo + "/releases/latest":
			b, _ := json.Marshal(release)
			content = string(b)
		case base + checksums:
			content = body
		default:
			t.Fatalf("unexpected request (must not fetch binaries/scripts): %s", r.URL.String())
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(content)), Header: http.Header{}, Request: r}, nil
	})}
	return client, &calls
}
func TestOfficialReleaseRecipePinsMetadataOnly(t *testing.T) {
	for _, tool := range []string{"dws", "github-mcp-server", "uv"} {
		for _, platform := range []struct{ os, arch string }{{"linux", "amd64"}, {"darwin", "arm64"}, {"windows", "amd64"}, {"windows", "arm64"}} {
			t.Run(tool+platform.os+platform.arch, func(t *testing.T) {
				a := newTestApp(t)
				client, calls := releaseFixtureClient(t, tool, platform.os, platform.arch, nil)
				a.capState.releaseClient = client
				recipe, err := a.resolveOfficialReleaseRecipe(context.Background(), capability.ToolRequirement{Name: tool, Version: ">=1.0.0"}, platform.os, platform.arch)
				if err != nil {
					t.Fatal(err)
				}
				if *calls != 2 || recipe.Version != "1.2.3" || recipe.Backend != "github-release" || recipe.Executable == "" || len(recipe.Assets) != 1 {
					t.Fatalf("%d %+v", *calls, recipe)
				}
				asset := recipe.Assets[platform.os+"-"+platform.arch]
				if asset.SHA256 != strings.Repeat("a", 64) || asset.Signature != nil {
					t.Fatalf("asset %+v", asset)
				}
			})
		}
	}
}
func TestOfficialReleaseRecipeFailsClosed(t *testing.T) {
	for name, change := range map[string]func(*capabilityRelease, *string){
		"prerelease":         func(r *capabilityRelease, _ *string) { r.Prerelease = true },
		"range tag":          func(r *capabilityRelease, _ *string) { r.Tag = "latest" },
		"wrong host":         func(r *capabilityRelease, _ *string) { r.Assets[0].URL = "https://evil.example/dws.tar.gz" },
		"wrong checksum url": func(r *capabilityRelease, _ *string) { r.Assets[1].URL = "http://127.0.0.1/checksums" },
		"digest mismatch":    func(r *capabilityRelease, _ *string) { r.Assets[0].Digest = "sha256:" + strings.Repeat("b", 64) },
		"missing checksum":   func(_ *capabilityRelease, s *string) { *s = strings.Repeat("a", 64) + "  different.tar.gz\n" },
		"duplicate checksum": func(_ *capabilityRelease, s *string) { *s += *s },
		"duplicate asset":    func(r *capabilityRelease, _ *string) { r.Assets = append(r.Assets, r.Assets[0]) },
	} {
		t.Run(name, func(t *testing.T) {
			a := newTestApp(t)
			client, _ := releaseFixtureClient(t, "dws", "linux", "amd64", change)
			a.capState.releaseClient = client
			if _, err := a.resolveOfficialReleaseRecipe(context.Background(), capability.ToolRequirement{Name: "dws"}, "linux", "amd64"); err == nil {
				t.Fatal("unsafe recipe accepted")
			}
		})
	}
}
func TestReleaseRecipePreservesExplicitRecipesAndRejectsRedirects(t *testing.T) {
	a := newTestApp(t)
	a.capState.releaseClient = &http.Client{Transport: recipeTransport(func(r *http.Request) (*http.Response, error) { t.Fatal("unexpected metadata request"); return nil, nil })}
	lock := capability.Lock{Requires: capability.Requirements{Tools: []capability.ToolRequirement{{Name: "dws", Recipes: []capability.Recipe{{Version: "1.0.0"}}}, {Name: "unrecognized-tool"}}}}
	a.addOfficialReleaseRecipes(context.Background(), &lock)
	if lock.Requires.Tools[0].Recipes[0].Version != "1.0.0" {
		t.Fatal("existing pin changed")
	}
	for _, url := range []string{"http://release-assets.githubusercontent.com/file", "https://evil.example/file", "https://github.com/other/repo"} {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		if a.capabilityReleaseHTTPClient().CheckRedirect(req, nil) == nil {
			t.Fatal("unsafe redirect accepted")
		}
	}
}

func TestCuratedDWSInspectionPinsReleaseRecipe(t *testing.T) {
	a, _, sid := capFixture(t)
	client, calls := releaseFixtureClient(t, "dws", runtime.GOOS, runtime.GOARCH, nil)
	a.capState.releaseClient = client
	lock, err := a.inspectCapability(context.Background(), "dingtalk-dws")
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 2 || len(lock.Requires.Tools) == 0 || len(lock.Requires.Tools[0].Recipes) != 1 {
		t.Fatalf("recipe not inspected: calls=%d %+v", *calls, lock.Requires)
	}
	recipe := lock.Requires.Tools[0].Recipes[0]
	if recipe.Version != "1.2.3" {
		t.Fatal(recipe)
	}
	// Referencing the inspected identity must retain the exact reviewed release.
	a.capState.releaseClient = &http.Client{Transport: recipeTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("pinned inspection unexpectedly refreshed")
		return nil, nil
	})}
	out, err := a.CapabilityRequest(context.Background(), "propose", webui.CapabilityRequest{Source: lock.ID, SessionID: sid})
	if err != nil {
		t.Fatal(err)
	}
	got := out.(capabilityProposalView)
	if got.Lock.Requires.Tools[0].Recipes[0].Version != "1.2.3" {
		t.Fatal("release pin changed")
	}
}
