package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/capability/adapters"
	"github.com/Zhengmingming1/tidebot/internal/skillrepo"
)

const capabilityMarketplacePrefix = "claude-marketplace:"

// These are discovery results, not installation authority. Source is an opaque,
// validated concrete Git source consumed by the normal inspect/propose flow.
type capabilityMarketplaceEntry struct {
	Name       string `json:"name"`
	Source     string `json:"source"`
	Repository string `json:"repository"`
	Ref        string `json:"ref,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Path       string `json:"path"`
	Trust      string `json:"trust"`
}
type capabilityMarketplaceView struct {
	Source   string                       `json:"source"`
	Commit   string                       `json:"commit"`
	Manifest string                       `json:"manifest"`
	Entries  []capabilityMarketplaceEntry `json:"entries"`
}
type marketplaceSourceReference struct {
	URL    string `json:"url"`
	Ref    string `json:"ref,omitempty"`
	Commit string `json:"commit,omitempty"`
	Root   string `json:"root"`
}

func marketplaceCommit(commit string) bool {
	return len(commit) == 40 && strings.Trim(commit, "0123456789abcdefABCDEF") == ""
}
func parseCapabilityMarketplaceSource(raw string) (capability.Source, capability.Requirements, error) {
	var ref marketplaceSourceReference
	bad := func() (capability.Source, capability.Requirements, error) {
		return capability.Source{}, capability.Requirements{}, capError(400, "invalid_source", "invalid marketplace Git source")
	}
	if len(raw) > 8192 || !strings.HasPrefix(raw, capabilityMarketplacePrefix) {
		return bad()
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, capabilityMarketplacePrefix))
	if err != nil || json.Unmarshal(b, &ref) != nil {
		return bad()
	}
	u, err := url.Parse(ref.URL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return bad()
	}
	clone, err := skillrepo.ValidateURL(ref.URL)
	if err != nil {
		return bad()
	}
	root, err := skillrepo.ValidateRoot(ref.Root)
	if err != nil || skillrepo.ValidateRef(ref.Ref) != nil {
		return bad()
	}
	if ref.Commit != "" {
		if !marketplaceCommit(ref.Commit) {
			return bad()
		}
		// Scan must fetch the pinned commit, never the marketplace's moving branch.
		ref.Ref = ref.Commit
	}
	// Never accept a trust label from the encoded metadata, nor inherit the
	// marketplace publisher's reputation for an independently hosted plugin.
	return capability.Source{Kind: "git", URL: clone, Ref: ref.Ref, Commit: ref.Commit, Subpaths: []string{root}, Adapter: "claude-plugin", Trust: "unknown"}, capability.Requirements{}, nil
}

// marketplaceRepository normalizes only repository Git URLs and GitHub's
// documented tree/blob forms. It never downloads an arbitrary manifest URL.
func marketplaceRepository(raw string) (clone, ref, root, manifest string, err error) {
	root = "."
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		err = errors.New("marketplace requires an HTTPS Git repository")
		return
	}
	if strings.EqualFold(u.Host, "github.com") {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			err = errors.New("invalid GitHub marketplace repository")
			return
		}
		if len(parts) > 2 {
			if len(parts) < 4 || (parts[2] != "blob" && parts[2] != "tree") {
				err = errors.New("use a GitHub repository, tree, or marketplace.json blob URL")
				return
			}
			ref = parts[3]
			if len(parts) > 4 {
				root = strings.Join(parts[4:], "/")
			}
			if parts[2] == "blob" {
				if path.Base(root) != "marketplace.json" {
					err = errors.New("marketplace blob must name marketplace.json")
					return
				}
				manifest = "marketplace.json"
				root = path.Dir(root)
				if path.Base(root) == ".claude-plugin" {
					root = path.Dir(root)
					manifest = ".claude-plugin/marketplace.json"
				}
			}
			if parts[2] == "tree" && path.Base(root) == ".claude-plugin" {
				root = path.Dir(root)
				manifest = ".claude-plugin/marketplace.json"
			}
			u.Path = "/" + strings.Join(parts[:2], "/")
			u.RawPath = ""
		}
	}
	if e = skillrepo.ValidateRef(ref); e != nil {
		err = e
		return
	}
	if root, e = skillrepo.ValidateRoot(root); e != nil {
		err = e
		return
	}
	clone, err = skillrepo.ValidateURL(u.String())
	return
}

// DiscoverCapabilityMarketplace only reads a bounded immutable Git snapshot.
// It does not inspect remote entries, install components, or execute hooks.
func (a *App) DiscoverCapabilityMarketplace(ctx context.Context, raw string) (capabilityMarketplaceView, error) {
	var out capabilityMarketplaceView
	clone, ref, root, manifest, err := marketplaceRepository(raw)
	if err != nil {
		return out, err
	}
	git, ok := a.capGit().(capabilitySnapshotGit)
	if !ok {
		return out, errors.New("marketplace snapshot reader unavailable")
	}
	snap, err := a.capGit().Scan(ctx, capKey(clone), clone, ref, root)
	if err != nil {
		return out, err
	}
	if !marketplaceCommit(snap.Commit) {
		return out, errors.New("marketplace did not resolve to an immutable Git commit")
	}
	files, err := git.ReadSnapshot(ctx, capKey(clone), snap.Commit, root)
	if err != nil {
		return out, err
	}
	if manifest == "" {
		manifest = ".claude-plugin/marketplace.json"
		if _, ok := files[manifest]; !ok {
			manifest = "marketplace.json"
		}
	}
	data, ok := files[manifest]
	if !ok {
		return out, capError(404, "marketplace_not_found", "repository has no marketplace.json")
	}
	entries, err := adapters.ParseMarketplace(data)
	if err != nil {
		return out, err
	}
	out = capabilityMarketplaceView{Source: clone, Commit: snap.Commit, Manifest: path.Join(root, manifest), Entries: []capabilityMarketplaceEntry{}}
	for _, entry := range entries {
		r := marketplaceSourceReference{URL: clone, Ref: snap.Commit, Commit: snap.Commit, Root: path.Join(root, entry.Source)}
		if strings.HasPrefix(entry.Source, "https://") {
			remote, remoteRef, remoteRoot, remoteManifest, e := marketplaceRepository(entry.Source)
			if e != nil || remoteManifest != "" {
				return out, errors.New("invalid remote marketplace Git source")
			}
			if entry.Ref != "" {
				remoteRef = entry.Ref
			}
			r = marketplaceSourceReference{URL: remote, Ref: remoteRef, Commit: entry.Commit, Root: remoteRoot}
		}
		b, _ := json.Marshal(r)
		source := capabilityMarketplacePrefix + base64.RawURLEncoding.EncodeToString(b)
		s, _, e := parseCapabilityMarketplaceSource(source)
		if e != nil {
			return out, e
		}
		out.Entries = append(out.Entries, capabilityMarketplaceEntry{Name: entry.Name, Source: source, Repository: s.URL, Ref: s.Ref, Commit: s.Commit, Path: sourceRoot(s), Trust: s.Trust})
	}
	return out, nil
}

// Re-discovery is the explicit update path for a local marketplace entry whose
// source ref is itself pinned. Never mutate the installed pointer before review.
func (a *App) proposeMarketplaceUpdate(ctx context.Context, sessionID string, current capability.Record, next capability.Lock) (any, error) {
	old := current.Lock.Source
	source := next.Source
	if current.ID != next.ID || old.Kind != "git" || source.Kind != "git" || old.URL != source.URL || sourceRoot(old) != sourceRoot(source) || old.Adapter != source.Adapter || source.Adapter != "claude-plugin" {
		return nil, capError(409, "source_changed", "marketplace update must identify the same Git repository, plugin path, and adapter")
	}
	if old.Commit == source.Commit && current.Lock.FilesSHA256 == next.FilesSHA256 {
		return nil, capError(409, "already_installed", "this marketplace plugin snapshot is already installed")
	}
	if !marketplaceCommit(source.Commit) {
		return nil, capError(400, "invalid_source", "marketplace update requires a pinned commit")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err := a.resolveCapabilityDependencies(checkCtx, &next, true)
	cancel()
	if err != nil {
		return nil, err
	}
	files, err := a.readCapabilitySnapshot(ctx, next.Source)
	if err != nil {
		return nil, err
	}
	proposal, _, err := a.caps().CreateUpdateProposal(ctx, sessionID, "", next, files)
	return proposalView(proposal), err
}
