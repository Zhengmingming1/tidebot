package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/skillrepo"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

const capabilitySkillDiscoveryPrefix = "skill-discovery:"
const capabilityDiscoveryMaxResults = 200

// Query text is the only user data sent to public discovery services. Returned
// metadata is untrusted and never supplies executable recipes or trust grants.
type capabilitySearchView struct {
	Results  []capability.CatalogEntry `json:"results"`
	Warnings []string                  `json:"warnings"`
}

func (a *App) searchCapabilitySources(ctx context.Context, r webui.CapabilityRequest) (capabilitySearchView, error) {
	view := capabilitySearchView{Results: []capability.CatalogEntry{}, Warnings: []string{}}
	query := strings.TrimSpace(r.Query)
	if len(query) > 512 {
		return view, capError(400, "invalid_query", "search query is too long")
	}
	providers := map[string]bool{}
	for _, provider := range strings.Split(r.Providers, ",") {
		provider = strings.TrimSpace(provider)
		if provider == "" {
			continue
		}
		if provider != "skills.sh" && provider != "github" {
			return view, capError(400, "invalid_provider", "supported optional discovery providers: skills.sh, github")
		}
		providers[provider] = true
	}
	if len(providers) > 0 && query == "" {
		return view, capError(400, "invalid_query", "public discovery requires a search query")
	}
	local, err := a.searchCapabilities(ctx, query)
	if err != nil {
		return view, err
	}
	view.Results = append(view.Results, local...)
	sources, err := a.savedCapabilitySources(ctx)
	if err != nil {
		return view, err
	}
	warn := func(source, code, message string) {
		view.Warnings = append(view.Warnings, source+": "+message)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	registrySearched := false
	for _, source := range sources {
		switch source.Kind {
		case "marketplace":
			if source.RefreshedAt == "" {
				warn(source.ID, "source_not_refreshed", "Refresh this marketplace to discover its plugins.")
				continue
			}
			for _, entry := range source.cached {
				if capabilityEntryMatches(entry, query) {
					view.Results = append(view.Results, entry)
				}
			}
		case "mcp_registry":
			if registrySearched {
				continue
			}
			registrySearched = true
			entries, e := a.searchCapabilityRegistry(ctx, query)
			if e != nil {
				warn(source.ID, "source_unavailable", "The MCP registry could not be searched. Local results are still available.")
				continue
			}
			for i := range entries {
				entries[i].DiscoveryProvider = source.ID
			}
			view.Results = append(view.Results, entries...)
		}
	}
	// Deterministic provider order, independently optional and failure-isolated.
	if providers["skills.sh"] {
		entries, e := a.searchCapabilitySkillsSH(ctx, query)
		if e != nil {
			warn("skills.sh", "source_unavailable", "skills.sh discovery is unavailable. Its public API is experimental; other sources remain available.")
		} else {
			view.Results = append(view.Results, entries...)
		}
	}
	if providers["github"] {
		entries, e := a.searchCapabilityGitHub(ctx, query)
		if e != nil {
			warn("github", "source_unavailable", "GitHub discovery is unavailable or rate limited; other sources remain available.")
		} else {
			view.Results = append(view.Results, entries...)
		}
	}
	view.Results = rankCapabilitySearch(view.Results, query, time.Now())
	return view, nil
}

func capabilityEntryMatches(entry capability.CatalogEntry, query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return true
	}
	terms := append([]string{entry.ID, entry.Title, entry.Description}, entry.Aliases...)
	return strings.Contains(strings.ToLower(strings.Join(terms, " ")), q)
}
func capabilityEntryExact(entry capability.CatalogEntry, query string) bool {
	for _, term := range append([]string{entry.ID, entry.Title}, entry.Aliases...) {
		if strings.EqualFold(strings.TrimSpace(term), query) {
			return true
		}
	}
	return false
}
func capabilityEntryHasRecipe(entry capability.CatalogEntry) bool {
	for _, tool := range append(append([]capability.ToolRequirement{}, entry.Requires.Tools...), entry.Requires.Runtimes...) {
		if len(tool.Recipes) > 0 {
			return true
		}
	}
	return false
}
func capabilityTrustRank(trust string) int {
	switch trust {
	case "official":
		return 4
	case "verified":
		return 3
	case "community":
		return 2
	default:
		return 1
	}
}
func capabilitySearchIdentity(entry capability.CatalogEntry) string {
	if entry.Source.Kind == "registry" && entry.Source.Registry != nil {
		return "registry:" + entry.Source.Registry.Entry
	}
	return strings.ToLower(strings.TrimSuffix(entry.Source.URL, ".git")) + "\x00" + sourceRoot(entry.Source) + "\x00" + entry.Source.Adapter + "\x00" + entry.ID
}
func rankCapabilitySearch(entries []capability.CatalogEntry, query string, now time.Time) []capability.CatalogEntry {
	recent := func(e capability.CatalogEntry) bool {
		t, err := time.Parse(time.RFC3339, e.UpdatedAt)
		return err == nil && !t.After(now) && now.Sub(t) <= 90*24*time.Hour
	}
	popularity := func(e capability.CatalogEntry) float64 { return math.Log1p(float64(max(e.Stars, e.Installs, 0))) }
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if ta, tb := capabilityTrustRank(a.Source.Trust), capabilityTrustRank(b.Source.Trust); ta != tb {
			return ta > tb
		}
		if ea, eb := capabilityEntryExact(a, query), capabilityEntryExact(b, query); ea != eb {
			return ea
		}
		if ra, rb := capabilityEntryHasRecipe(a), capabilityEntryHasRecipe(b); ra != rb {
			return ra
		}
		if ra, rb := recent(a), recent(b); ra != rb {
			return ra
		}
		if pa, pb := popularity(a), popularity(b); pa != pb {
			return pa > pb
		}
		return false
	})
	seen := map[string]bool{}
	out := []capability.CatalogEntry{}
	for _, entry := range entries {
		identity := capabilitySearchIdentity(entry)
		if seen[identity] {
			continue
		}
		seen[identity] = true
		if len(entry.Labels) == 0 {
			entry.Labels = []string{entry.Source.Trust}
			if entry.Source.Trust != "official" {
				entry.Labels = append(entry.Labels, "unverified_publisher")
			}
		}
		out = append(out, entry)
		if len(out) >= capabilityDiscoveryMaxResults {
			break
		}
	}
	return out
}

func (a *App) fetchCapabilityDiscovery(ctx context.Context, endpoint string, out any) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || !((u.Host == "skills.sh" && u.Path == "/api/search") || (u.Host == "api.github.com" && u.Path == "/search/repositories")) {
		return capError(400, "invalid_source", "invalid public discovery endpoint")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "tidebot-capability-discovery")
	// Deliberately no token, cookies, env, private account information or redirects.
	resp, err := a.capabilityRegistryHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("public discovery request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return capError(502, "discovery_unavailable", fmt.Sprintf("public discovery returned HTTP %d", resp.StatusCode))
	}
	if resp.ContentLength > capabilityRegistryMaxBytes {
		return capError(502, "discovery_response_limit", "public discovery response exceeds size limit")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, capabilityRegistryMaxBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > capabilityRegistryMaxBytes {
		return capError(502, "discovery_response_limit", "public discovery response exceeds size limit")
	}
	if json.Unmarshal(raw, out) != nil {
		return capError(502, "invalid_discovery_response", "invalid public discovery response")
	}
	return nil
}

var capabilityDiscoveryRepo = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9_.-]{1,100}$`)
var capabilityDiscoverySkill = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

type capabilityDiscoveredSkill struct {
	Repository string `json:"repository"`
	Skill      string `json:"skill"`
}

func encodeCapabilityDiscoveredSkill(repository, skill string) string {
	raw, _ := json.Marshal(capabilityDiscoveredSkill{repository, skill})
	return capabilitySkillDiscoveryPrefix + base64.RawURLEncoding.EncodeToString(raw)
}
func (a *App) resolveCapabilityDiscoveredSkill(ctx context.Context, raw string) (capability.Source, capability.Requirements, error) {
	var hint capabilityDiscoveredSkill
	invalid := func() (capability.Source, capability.Requirements, error) {
		return capability.Source{}, capability.Requirements{}, capError(400, "invalid_source", "invalid skill discovery source")
	}
	if len(raw) > 2048 {
		return invalid()
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, capabilitySkillDiscoveryPrefix))
	if err != nil || json.Unmarshal(decoded, &hint) != nil || !capabilityDiscoveryRepo.MatchString(hint.Repository) || !capabilityDiscoverySkill.MatchString(hint.Skill) {
		return invalid()
	}
	repository := "https://github.com/" + hint.Repository
	src, requires, err := capSource(repository)
	if err != nil {
		return invalid()
	}
	snap, err := a.capGit().Scan(ctx, capKey(src.URL), src.URL, "", ".")
	if err != nil {
		return src, requires, err
	}
	if !marketplaceCommit(snap.Commit) {
		return invalid()
	}
	var matches []skillrepo.Candidate
	for _, candidate := range snap.Candidates {
		if candidate.Error == "" && (candidate.Name == hint.Skill || path.Base(candidate.Path) == hint.Skill) {
			matches = append(matches, candidate)
		}
	}
	if len(matches) != 1 {
		return src, requires, capError(400, "choose_skill", "discovered skill must match one Git skill directory; use an explicit repository directory")
	}
	root, err := skillrepo.ValidateRoot(matches[0].Path)
	if err != nil {
		return invalid()
	}
	src.Commit = snap.Commit
	src.Subpaths = []string{root}
	return src, requires, nil
}

func (a *App) searchCapabilitySkillsSH(ctx context.Context, query string) ([]capability.CatalogEntry, error) {
	var response struct {
		Skills []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Source   string `json:"source"`
			Installs int64  `json:"installs"`
		} `json:"skills"`
	}
	endpoint := "https://skills.sh/api/search?" + url.Values{"q": {query}}.Encode()
	if err := a.fetchCapabilityDiscovery(ctx, endpoint, &response); err != nil {
		return nil, err
	}
	if response.Skills == nil {
		return nil, capError(502, "invalid_discovery_response", "skills.sh returned no skills array")
	}
	out := []capability.CatalogEntry{}
	for _, skill := range response.Skills {
		if !capabilityDiscoveryRepo.MatchString(skill.Source) || !capabilityDiscoverySkill.MatchString(skill.Name) {
			continue
		}
		// API skill IDs are display identifiers, never install paths or URLs.
		src, _, err := capSource("https://github.com/" + skill.Source)
		if err != nil {
			continue
		}
		out = append(out, capability.CatalogEntry{ID: encodeCapabilityDiscoveredSkill(skill.Source, skill.Name), Title: skill.Name, Description: "Community discovery metadata. The skill directory and contents are verified from Git before review.", Source: src, Installs: max(skill.Installs, 0), DiscoveryProvider: "skills.sh", Labels: []string{"unknown", "unverified_publisher", "experimental_discovery"}})
		if len(out) >= 50 {
			break
		}
	}
	return out, nil
}

func (a *App) searchCapabilityGitHub(ctx context.Context, query string) ([]capability.CatalogEntry, error) {
	var response struct {
		Items []struct {
			FullName    string `json:"full_name"`
			Description string `json:"description"`
			Private     bool   `json:"private"`
			Fork        bool   `json:"fork"`
			Archived    bool   `json:"archived"`
			Stars       int64  `json:"stargazers_count"`
			PushedAt    string `json:"pushed_at"`
			License     *struct {
				SPDX string `json:"spdx_id"`
			} `json:"license"`
		} `json:"items"`
	}
	// Explicitly public even when callers include GitHub search syntax.
	endpoint := "https://api.github.com/search/repositories?" + url.Values{"q": {query + " is:public"}, "per_page": {"30"}, "page": {"1"}}.Encode()
	if err := a.fetchCapabilityDiscovery(ctx, endpoint, &response); err != nil {
		return nil, err
	}
	if response.Items == nil {
		return nil, capError(502, "invalid_discovery_response", "GitHub returned no items array")
	}
	out := []capability.CatalogEntry{}
	for _, item := range response.Items {
		if item.Private || !capabilityDiscoveryRepo.MatchString(item.FullName) || len(item.Description) > 4096 {
			continue
		}
		src, _, err := capSource("https://github.com/" + item.FullName)
		if err != nil {
			continue
		}
		src.Trust = "community"
		labels := []string{"community", "unverified_publisher", "repository_choose_skill"}
		if item.Fork {
			labels = append(labels, "fork")
			src.Trust = "unknown"
		}
		if item.Archived {
			labels = append(labels, "archived")
		}
		if item.License == nil || item.License.SPDX == "" || item.License.SPDX == "NOASSERTION" {
			labels = append(labels, "no_license")
			src.Trust = "unknown"
		} else {
			src.License = item.License.SPDX
		}
		updated, err := time.Parse(time.RFC3339, item.PushedAt)
		if err == nil && time.Since(updated) > 90*24*time.Hour {
			labels = append(labels, "stale")
		}
		out = append(out, capability.CatalogEntry{ID: src.URL, Title: item.FullName, Description: item.Description, Source: src, Stars: max(item.Stars, 0), UpdatedAt: item.PushedAt, DiscoveryProvider: "github", Labels: labels})
		if len(out) >= 30 {
			break
		}
	}
	return out, nil
}

const capabilitySkillSourcePrefix = "skill-source:"

// Source candidates are exact Git directories, not paths asserted by a search
// provider. Each ID includes the discovery commit; the original ref is retained
// so a later owner-requested update can find a newer version.
func (a *App) discoverCapabilitySourceSkills(ctx context.Context, raw string) (any, error) {
	src, _, err := capSource(raw)
	if err != nil {
		return nil, err
	}
	if src.Kind != "git" {
		return nil, capError(400, "invalid_source", "source skills require an HTTPS Git repository")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	snap, err := a.capGit().Scan(ctx, capKey(src.URL), src.URL, src.Ref, sourceRoot(src))
	if err != nil {
		return nil, err
	}
	if !marketplaceCommit(snap.Commit) {
		return nil, capError(502, "invalid_source", "repository did not resolve to an immutable commit")
	}
	results := []capability.CatalogEntry{}
	for _, candidate := range snap.Candidates {
		root, e := skillrepo.ValidateRoot(candidate.Path)
		if e != nil || candidate.Error != "" {
			continue
		}
		if scope := sourceRoot(src); scope != "." && root != scope && !strings.HasPrefix(root, scope+"/") {
			continue
		}
		ref := marketplaceSourceReference{URL: src.URL, Ref: src.Ref, Commit: snap.Commit, Root: root}
		encoded, _ := json.Marshal(ref)
		source := capabilitySkillSourcePrefix + base64.RawURLEncoding.EncodeToString(encoded)
		entrySource, _, e := parseCapabilitySkillSource(source)
		if e != nil {
			continue
		}
		title := candidate.Name
		if title == "" {
			title = path.Base(root)
		}
		results = append(results, capability.CatalogEntry{ID: source, Title: title, Description: candidate.Description, Source: entrySource, Labels: []string{"unknown", "unverified_publisher"}})
		if len(results) >= capabilityDiscoveryMaxResults {
			break
		}
	}
	return map[string]any{"results": results, "commit": snap.Commit}, nil
}

func parseCapabilitySkillSource(raw string) (capability.Source, capability.Requirements, error) {
	bad := func() (capability.Source, capability.Requirements, error) {
		return capability.Source{}, capability.Requirements{}, capError(400, "invalid_source", "invalid pinned skill source")
	}
	if len(raw) > 8192 || !strings.HasPrefix(raw, capabilitySkillSourcePrefix) {
		return bad()
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, capabilitySkillSourcePrefix))
	if err != nil {
		return bad()
	}
	var ref marketplaceSourceReference
	if json.Unmarshal(data, &ref) != nil || !marketplaceCommit(ref.Commit) || skillrepo.ValidateRef(ref.Ref) != nil {
		return bad()
	}
	src, _, err := capSource(ref.URL)
	if err != nil || src.Kind != "git" || sourceRoot(src) != "." {
		return bad()
	}
	root, err := skillrepo.ValidateRoot(ref.Root)
	if err != nil {
		return bad()
	}
	// Never accept source trust, adapter, recipes or publisher from encoded data.
	src.Ref = ref.Ref
	src.Commit = ref.Commit
	src.Subpaths = []string{root}
	src.Trust = "unknown"
	src.Registry = nil
	src.Publisher = ""
	return src, capability.Requirements{}, nil
}
