package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/sessiontools"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func dependencyPreferenceTool(t *testing.T, dir, name, version string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho '"+version+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestDependencyPreferenceOwnerCRUDAndValidation(t *testing.T) {
	a, _, sid := capFixture(t)
	ctx := context.Background()
	path := dependencyPreferenceTool(t, t.TempDir(), "pref-command", "1.2.3")
	request := webui.CapabilityDependencyPreferenceRequest{Name: "pref-command", Path: path}
	if _, err := a.CapabilityDependencyPreferenceRequest(ctx, http.MethodPut, request); err != nil {
		t.Fatal(err)
	}
	prefs, err := a.dependencyPreferences(ctx)
	if err != nil || prefs[request.Name].Path != path {
		t.Fatal(prefs, err)
	}
	for _, bad := range []webui.CapabilityDependencyPreferenceRequest{{Name: "../escape"}, {Name: "sh -c"}, {Name: "x", Path: "relative"}, {Name: "x", Path: "/tmp/a\n"}} {
		if _, err := a.CapabilityDependencyPreferenceRequest(ctx, http.MethodPut, bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	token := a.chatToolEnv(sid)[sessiontools.EnvToken]
	raw, _ := json.Marshal(map[string]any{"token": token, "tool": "cap_dependency_preferences", "args": map[string]string{"name": "pref-command", "path": "/untrusted"}})
	if _, err = a.rpcSessionTool(ctx, raw); err == nil {
		t.Fatal("model preference mutation exposed")
	}
	if _, err = a.CapabilityDependencyPreferenceRequest(ctx, http.MethodDelete, request); err != nil {
		t.Fatal(err)
	}
	prefs, err = a.dependencyPreferences(ctx)
	if err != nil || len(prefs) != 0 {
		t.Fatal(prefs, err)
	}
}

func TestDependencyPreferenceProposalFreezesPathAndRevalidates(t *testing.T) {
	a, _, sid := capFixture(t)
	ctx := context.Background()
	first := dependencyPreferenceTool(t, t.TempDir(), "pref-command", "1.2.3")
	second := dependencyPreferenceTool(t, t.TempDir(), "pref-command", "9.8.7")
	save := func(path string) {
		t.Helper()
		if _, err := a.CapabilityDependencyPreferenceRequest(ctx, http.MethodPut, webui.CapabilityDependencyPreferenceRequest{Name: "pref-command", Path: path}); err != nil {
			t.Fatal(err)
		}
	}
	save(first)
	lock, err := a.inspectCapability(ctx, "pdf")
	if err != nil {
		t.Fatal(err)
	}
	lock.Requires = capability.Requirements{Tools: []capability.ToolRequirement{{Name: "pref-command", Version: ">=1.0.0"}}}
	a.capState.inspected["preference-fixture"] = lock
	value, err := a.CapabilityRequest(ctx, "propose", webui.CapabilityRequest{SessionID: sid, Source: "preference-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	proposal := value.(capabilityProposalView)
	if proposal.Lock.Resolved["pref-command"].Path != first || proposal.Lock.DependencyPreferences["pref-command"].Path != first {
		t.Fatal(proposal)
	}
	save(second)
	value, err = a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{SessionID: sid, ProposalID: proposal.ID})
	if err != nil {
		t.Fatal(err)
	}
	record := value.(capability.Record)
	if record.Lock.Resolved["pref-command"].Path != first {
		t.Fatal("mutable preferences replaced reviewed executable")
	}
	opts := capabilityDependencyDetectOptions(record.Lock)
	opts.PATH = filepath.Dir(second)
	got := capability.Detect(ctx, record.Lock.Requires.Tools, opts)["pref-command"]
	if got.Path != first {
		t.Fatal("current PATH replaced reviewed executable", got)
	}
	if err = os.WriteFile(first, []byte("#!/bin/sh\necho 0.1.0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = a.verifyReviewedCapabilityDependencies(ctx, record.Lock); err == nil {
		t.Fatal("changed reviewed tool remained ready")
	}
}

func TestDependencyManagedPreferenceDoesNotAuthorizeDownload(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	path := dependencyPreferenceTool(t, t.TempDir(), "pref-command", "1.2.3")
	if _, err := a.CapabilityDependencyPreferenceRequest(ctx, http.MethodPut, webui.CapabilityDependencyPreferenceRequest{Name: "pref-command", Path: path, PreferManaged: true}); err != nil {
		t.Fatal(err)
	}
	lock := capability.Lock{Requires: capability.Requirements{Tools: []capability.ToolRequirement{{Name: "pref-command", Recipes: []capability.Recipe{{Backend: "http-archive", Version: "1.2.3", URL: "https://example.com/tool", SHA256: strings.Repeat("a", 64), Format: "binary", Executable: "pref-command"}}}}}}
	if err := a.resolveCapabilityDependencies(ctx, &lock, true); err != nil {
		t.Fatal(err)
	}
	if lock.Resolved["pref-command"].Status != "missing" || lock.Resolved["pref-command"].Source != "managed" {
		t.Fatal(lock.Resolved)
	}
	plan := dependencyPlan(lock)
	if plan[0].Status != "download" || !plan[0].Preference.PreferManaged {
		t.Fatal(plan)
	}
	if _, err := os.Stat(filepath.Join(a.Cfg.DataDir, "tools", "pref-command")); !os.IsNotExist(err) {
		t.Fatal("preference downloaded tool", err)
	}
}

func TestDependencyManagedPreferenceVerifiesAndReusesInstalled(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	name := "pref-command"
	directory := filepath.Join(a.Cfg.DataDir, "tools", name, "1.2.3")
	path := dependencyPreferenceTool(t, directory, name, "1.2.3")
	hash, err := managedExecutableDigest(ctx, a.Cfg.DataDir, path)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := managedTreeDigest(ctx, a.Cfg.DataDir, directory)
	if err != nil {
		t.Fatal(err)
	}
	installed := capability.Lock{Format: capability.LockFormat, ID: "managed-pref", Title: "Managed preference", Source: capability.Source{Kind: "bundle", Trust: "unknown", Adapter: "skill-dir"}, Components: []capability.Component{{Type: "agent", Name: "fixture", Path: "README.md"}}, Requires: capability.Requirements{Tools: []capability.ToolRequirement{{Name: name, Version: ">=1.0.0"}}}, Resolved: map[string]capability.Resolved{name: {Source: "managed", Status: "ready", Version: "1.2.3", Path: path, Directory: directory, SHA256: hash, TreeSHA256: tree}}}
	if _, err = a.caps().Install(ctx, installed, map[string][]byte{"README.md": []byte("fixture")}); err != nil {
		t.Fatal(err)
	}
	system := dependencyPreferenceTool(t, t.TempDir(), name, "1.2.4")
	if _, err = a.CapabilityDependencyPreferenceRequest(ctx, http.MethodPut, webui.CapabilityDependencyPreferenceRequest{Name: name, Path: system}); err != nil {
		t.Fatal(err)
	}
	target := capability.Lock{ID: "other", Requires: installed.Requires}
	if err = a.resolveCapabilityDependencies(ctx, &target, true); err != nil {
		t.Fatal(err)
	}
	if target.Resolved[name].Path != system {
		t.Fatal("system no longer default", target.Resolved)
	}
	if _, err = a.CapabilityDependencyPreferenceRequest(ctx, http.MethodPut, webui.CapabilityDependencyPreferenceRequest{Name: name, Path: system, PreferManaged: true}); err != nil {
		t.Fatal(err)
	}
	if err = a.resolveCapabilityDependencies(ctx, &target, true); err != nil {
		t.Fatal(err)
	}
	if target.Resolved[name].Path != path {
		t.Fatal("managed preference ignored", target.Resolved)
	}
	if err = os.WriteFile(path, []byte("tampered"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = a.resolveCapabilityDependencies(ctx, &target, true); err != nil {
		t.Fatal(err)
	}
	if target.Resolved[name].Status == "ready" {
		t.Fatal("adopted modified managed tree")
	}
}

func TestDependencyManualDWSFallbackAllPlatforms(t *testing.T) {
	instructions := dependencyManualInstructions(capability.ToolRequirement{Name: "dws"})
	if len(instructions) != 3 {
		t.Fatal(instructions)
	}
	for _, item := range instructions {
		if !strings.HasPrefix(item.Docs, "https://github.com/DingTalk-Real-AI/") || !strings.Contains(strings.Join(item.Commands, "\n"), "DWS_NO_SKILLS") {
			t.Fatal(item)
		}
		if strings.Contains(strings.Join(item.Commands, "\n"), "skill setup") {
			t.Fatal("unsafe command", item)
		}
	}
	if got := dependencyManualInstructions(capability.ToolRequirement{Name: "unknown", Recipes: []capability.Recipe{{Docs: "javascript:alert(1)"}}}); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestDependencyManagedReuseRequiresReviewedPackageClosure(t *testing.T) {
	old := capability.Recipe{Backend: "npm", Package: "example", Version: "1.2.3", LockJSON: json.RawMessage(`{"version":1}`)}
	req := capability.ToolRequirement{Name: "example", Recipes: []capability.Recipe{old}}
	installed := capability.Lock{Requires: capability.Requirements{Tools: []capability.ToolRequirement{req}}}
	if !managedDependencyRecipeMatches(req, installed) {
		t.Fatal("same closure not reusable")
	}
	changed := req
	changed.Recipes = append([]capability.Recipe{}, req.Recipes...)
	changed.Recipes[0].LockJSON = json.RawMessage(`{"version":2}`)
	if managedDependencyRecipeMatches(changed, installed) {
		t.Fatal("new closure silently reused old environment")
	}
}

func TestDependencyPinnedDetectionNeverFallsBackForMissingManagedTool(t *testing.T) {
	dir := t.TempDir()
	dependencyPreferenceTool(t, dir, "pref-command", "1.2.3")
	lock := capability.Lock{Resolved: map[string]capability.Resolved{"pref-command": {Source: "managed", Status: "missing"}}, DependencyPreferences: map[string]capability.DependencyPreference{"pref-command": {PreferManaged: true}}}
	options := capabilityDependencyDetectOptions(lock)
	options.PATH = dir
	got := capability.Detect(context.Background(), []capability.ToolRequirement{{Name: "pref-command"}}, options)["pref-command"]
	if got.Status == "ready" || got.Path != "" {
		t.Fatal("missing reviewed managed tool silently fell back to PATH", got)
	}
}
