package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/capability/managed"
	"github.com/Zhengmingming1/tidebot/internal/capability/oauth"
	"github.com/Zhengmingming1/tidebot/internal/sessiontools"
	"github.com/Zhengmingming1/tidebot/internal/skillrepo"
	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/webui"
	"gopkg.in/yaml.v3"
)

type capabilityGit interface {
	Scan(context.Context, string, string, string, string) (skillrepo.Snapshot, error)
	ReadSkill(context.Context, string, string, string) (map[string][]byte, error)
}
type capabilityToken struct {
	SessionID string
	Expires   time.Time
}
type capabilityState struct {
	dependencyDetector capability.Detector
	deviceLogins       map[string]*capabilityDeviceLogin
	sessionCreateMu    sync.Mutex
	releaseClient      *http.Client
	registryClient     *http.Client
	pythonTargetProbe  func(context.Context) (*capability.RecipePythonTarget, error)
	oauthLocks         sync.Map
	oauthClient        *oauth.Client
	oauthPreparations  map[string]capabilityOAuthPreparation
	mu                 sync.Mutex
	tokens             map[string]capabilityToken
	git                capabilityGit
	inspected          map[string]capability.Lock
	headerTokens       map[string]capabilityHeaderScope
}

func (a *App) caps() *capability.Store {
	s := capability.NewStore(a.DB, a.Cfg.DataDir)
	s.ScanSource = scanCapabilitySource
	return s
}
func (a *App) capGit() capabilityGit {
	a.capState.mu.Lock()
	defer a.capState.mu.Unlock()
	if a.capState.git == nil {
		a.capState.git = skillrepo.New(filepath.Join(a.Cfg.DataDir, "cache", "capability-git"))
	}
	return a.capState.git
}
func capError(status int, code, message string) error {
	return &webui.CapabilityError{Status: status, Code: code, Message: message}
}
func capKey(s string) string {
	h := sha256.Sum256([]byte(s))
	return "cap_" + hex.EncodeToString(h[:12])
}
func capSource(raw string) (capability.Source, capability.Requirements, error) {
	for _, e := range capability.Catalog() {
		if e.ID == raw {
			return e.Source, e.Requires, nil
		}
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return capability.Source{}, capability.Requirements{}, capError(400, "invalid_source", "use a catalog ID or HTTPS Git source")
	}
	root, ref := ".", ""
	if strings.EqualFold(u.Host, "github.com") {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 5 && parts[2] == "tree" {
			ref = parts[3]
			root = strings.Join(parts[4:], "/")
			u.Path = "/" + strings.Join(parts[:2], "/")
		} else if len(parts) != 2 {
			return capability.Source{}, capability.Requirements{}, capError(400, "invalid_source", "specify a GitHub repository or tree/ref/skill-directory")
		}
	}
	clone, err := skillrepo.ValidateURL(u.String())
	if err != nil {
		return capability.Source{}, capability.Requirements{}, err
	}
	if _, err = skillrepo.ValidateRoot(root); err != nil {
		return capability.Source{}, capability.Requirements{}, err
	}
	return capability.Source{Kind: "git", URL: clone, Ref: ref, Subpaths: []string{root}, Adapter: "skill-dir", Trust: "unknown"}, capability.Requirements{}, nil
}
func sourceRoot(s capability.Source) string {
	if len(s.Subpaths) > 0 {
		return s.Subpaths[0]
	}
	return "."
}
func (a *App) inspectCapability(ctx context.Context, source string) (capability.Lock, error) {
	a.capState.mu.Lock()
	cached, ok := a.capState.inspected[source]
	a.capState.mu.Unlock()
	if ok {
		return cached, nil
	}
	if strings.HasPrefix(source, capabilityRegistryPrefix) {
		return a.inspectCapabilityRegistry(ctx, source)
	}
	src, requires, err := a.capabilitySource(ctx, source)
	if err != nil {
		return capability.Lock{}, err
	}
	return a.inspectCapabilityAtSource(ctx, src, requires)
}
func (a *App) inspectCapabilityAtSource(ctx context.Context, src capability.Source, requires capability.Requirements) (capability.Lock, error) {
	if src.Kind == "curated-binary" {
		return a.inspectCuratedBinaryCapability(ctx, src)
	}
	if src.Kind == "curated-remote" {
		return a.inspectCuratedRemoteCapability(ctx, src)
	}
	if src.Kind == "registry" && src.Registry != nil {
		return a.inspectCapabilityRegistry(ctx, capabilityRegistryPrefix+src.Registry.Entry)
	}
	scanRef := src.Ref
	if src.Commit != "" {
		scanRef = src.Commit
	}
	snap, err := a.capGit().Scan(ctx, capKey(src.URL), src.URL, scanRef, sourceRoot(src))
	if err != nil {
		return capability.Lock{}, err
	}
	if src.Commit != "" && snap.Commit != src.Commit {
		return capability.Lock{}, capError(409, "source_changed", "pinned source commit changed")
	}
	src.Commit = snap.Commit
	if lock, handled, e := a.inspectOtherAdapter(ctx, src); e != nil {
		return capability.Lock{}, e
	} else if handled {
		a.addRegistryPackageRecipes(ctx, &lock)
		a.addOfficialReleaseRecipes(ctx, &lock)
		a.cacheCapabilityInspection(lock)
		return lock, nil
	}
	var selected *skillrepo.Candidate
	for i := range snap.Candidates {
		c := &snap.Candidates[i]
		if c.Path == sourceRoot(src) || (sourceRoot(src) == "." && c.Path == "") {
			selected = c
			break
		}
	}
	if selected == nil && len(snap.Candidates) == 1 {
		selected = &snap.Candidates[0]
	}
	if selected == nil {
		return capability.Lock{}, capError(400, "choose_skill", "source must identify one skill directory")
	}
	if selected.Error != "" {
		return capability.Lock{}, capError(400, "invalid_skill", selected.Error)
	}
	src.Commit = snap.Commit
	src.Subpaths = []string{selected.Path}
	files, err := a.capGit().ReadSkill(ctx, capKey(src.URL), src.Commit, selected.Path)
	if err != nil {
		return capability.Lock{}, err
	}
	_, hash, err := capability.HashFiles(files)
	if err != nil {
		return capability.Lock{}, err
	}
	inferred := capability.InferRequirements(files)
	inferred.Tools = append(inferred.Tools, inferCapabilityMetadata(files["SKILL.md"])...)
	requires.Tools = append(requires.Tools, inferred.Tools...)
	requires.Runtimes = append(requires.Runtimes, inferred.Runtimes...)
	if raw, ok := files["tidebot.json"]; ok {
		m, e := skillstore.ValidateSkillManifest(raw)
		if e != nil {
			return capability.Lock{}, e
		}
		for _, d := range m.Environment {
			switch d.Kind {
			case "command":
				requires.Tools = append(requires.Tools, capability.ToolRequirement{Name: d.Name, Version: d.Version, Optional: d.Optional})
			case "env":
				requires.Secrets = append(requires.Secrets, capability.SecretRequirement{Name: d.Name, Optional: d.Optional, Secret: true})
			}
		}
	}
	title := selected.Name
	if title == "" {
		title = path.Base(selected.Path)
	}
	if title == "." || title == "" {
		title = path.Base(src.URL)
	}
	lock := capability.Lock{Format: capability.LockFormat, ID: capKey(src.URL + "\x00" + selected.Path), Title: title, Source: src, FilesSHA256: hash, Components: []capability.Component{{Type: "skill", Name: title, Path: selected.Path}}, Requires: requires, Permissions: capability.Permissions{}, Scan: capability.ScanFiles(files)}
	a.addOfficialReleaseRecipes(ctx, &lock)
	a.cacheCapabilityInspection(lock)
	return lock, nil
}
func (a *App) proposalFiles(ctx context.Context, p capability.Proposal) (map[string][]byte, error) {
	var files map[string][]byte
	var err error
	if p.Lock.Source.Kind == "bundle" {
		files, err = a.readImportedSnapshot(p.Lock.FilesSHA256)
	} else {
		files, err = a.readCapabilitySnapshot(ctx, p.Lock.Source)
	}
	if err != nil {
		return nil, err
	}
	_, hash, err := capability.HashFiles(files)
	if err != nil {
		return nil, err
	}
	if hash != p.Lock.FilesSHA256 {
		return nil, capError(409, "source_changed", "reviewed snapshot changed; create a new proposal")
	}
	return files, nil
}

type capabilityProposalView struct {
	capability.Proposal
	CostCapUSD     float64                    `json:"cost_cap_usd,omitempty"`
	Policy         capability.Policy          `json:"policy"`
	DependencyPlan []capabilityDependencyPlan `json:"dependency_plan"`
}

func proposalView(p capability.Proposal) capabilityProposalView {
	v := capabilityProposalView{Proposal: p, Policy: capability.InstallPolicy(p.Lock.Source.Trust, p.Lock.Scan.Verdict, false), DependencyPlan: dependencyPlan(p.Lock)}
	if p.Kind == "skill_optimize" {
		v.CostCapUSD = 0.10
		v.Policy = capability.Policy{Allowed: true}
	}
	if p.Kind == "uninstall" {
		v.Policy = capability.Policy{Allowed: true, ReviewRequired: true}
		v.DependencyPlan = nil
	}
	return v
}
func (a *App) CapabilityRequest(ctx context.Context, action string, r webui.CapabilityRequest) (any, error) {
	switch action {
	case "sources", "source_save", "source_delete", "source_refresh":
		return a.capabilitySourcesRequest(ctx, action, r)
	case "cli_login_start", "cli_login_status", "cli_login_cancel":
		return a.capabilityCLILogin(ctx, action, r)
	case "rescan":
		return a.proposeCapabilityRescan(ctx, r)
	case "source-skills":
		return a.discoverCapabilitySourceSkills(ctx, r.Source)
	case "marketplace":
		return a.DiscoverCapabilityMarketplace(ctx, r.Source)
	case "run_bindings":
		return a.capabilityRunBindings(ctx, r.RunID)
	case "default_get":
		return a.CapabilityDefaults(ctx)
	case "default_set":
		return a.SetCapabilityDefault(ctx, r.CapabilityID, r.DefaultEnabled)
	case "deactivate":
		if !r.Reviewed {
			return nil, capError(400, "review_required", "confirm capability deactivation")
		}
		return a.DeactivateCapability(ctx, r.CapabilityID)
	case "storage", "storage_gc":
		return a.capabilityStorageRequest(ctx, action, r)
	case "credentials_clear":
		return a.clearCapabilityCredentials(ctx, r)
	case "oauth_prepare", "oauth_start", "oauth_callback", "oauth_revoke":
		return a.capabilityOAuthRequest(ctx, action, r)
	case "recover":
		return a.recoverRunInSession(ctx, r.RunID)
	case "skill-optimize":
		return a.proposeSessionOptimization(ctx, r)
	case "gc", "versions", "events", "rollback", "uninstall", "export", "update", "import":
		return a.capabilityLifecycleRequest(ctx, action, r)
	case "list":
		v, e := a.caps().List(ctx)
		return map[string]any{"capabilities": v}, e
	case "registry_search":
		results, e := a.searchCapabilityRegistry(ctx, r.Query)
		return map[string]any{"results": results}, e
	case "search":
		return a.searchCapabilitySources(ctx, r)
	case "inspect":
		return a.inspectCapability(ctx, r.Source)
	case "test":
		return a.testInstalledCapability(ctx, r.Source)
	case "check":
		var lock capability.Lock
		rec, e := a.caps().Get(ctx, r.Source)
		if e == nil {
			lock = rec.Lock
		} else {
			lock, e = a.inspectCapability(ctx, r.Source)
			if e != nil {
				return nil, e
			}
		}
		return a.checkCapability(ctx, lock)
	case "propose":
		if _, e := a.chatSession(ctx, r.SessionID); e != nil {
			return nil, e
		}
		lock, e := a.inspectCapability(ctx, r.Source)
		if e != nil {
			return nil, e
		}
		if current, e := a.caps().Get(ctx, lock.ID); e == nil {
			if strings.HasPrefix(r.Source, capabilityMarketplacePrefix) {
				return a.proposeMarketplaceUpdate(ctx, r.SessionID, current, lock)
			}
			return nil, capError(409, "already_installed", "capability already installed; bind it using session settings")
		}
		checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		e = a.resolveCapabilityDependencies(checkCtx, &lock, true)
		cancel()
		if e != nil {
			return nil, e
		}
		p, e := a.caps().CreateProposal(ctx, capability.Proposal{SessionID: r.SessionID, Kind: "install", Lock: lock})
		return proposalView(p), e
	case "proposals":
		if _, e := a.chatSession(ctx, r.SessionID); e != nil {
			return nil, e
		}
		// The chat page polls this while a turn is answered; an unchanged list
		// is answered from the index alone, without reading the payloads.
		sig, e := a.caps().ProposalsSig(ctx, r.SessionID)
		if e != nil {
			return nil, e
		}
		if r.Since != "" && r.Since == sig {
			return map[string]any{"unchanged": true, "sig": sig}, nil
		}
		ps, e := a.caps().ListProposals(ctx, r.SessionID)
		out := []capabilityProposalView{}
		for _, p := range ps {
			out = append(out, proposalView(p))
		}
		return map[string]any{"proposals": out, "sig": sig}, e
	case "confirm", "dismiss", "files", "file":
		if _, e := a.chatSession(ctx, r.SessionID); e != nil {
			return nil, e
		}
		p, e := a.caps().GetProposal(ctx, r.ProposalID)
		if e != nil {
			return nil, capError(404, "not_found", "proposal not found")
		}
		if p.SessionID != r.SessionID {
			return nil, capError(404, "not_found", "proposal not found")
		}
		if p.Kind == "skill_optimize" {
			return a.decideSessionOptimization(ctx, action, r, p)
		}
		if p.Kind == "uninstall" {
			return a.decideCapabilityUninstall(ctx, action, r, p)
		}
		if action == "files" || action == "file" {
			files, e := a.proposalFiles(ctx, p)
			if e != nil {
				return nil, e
			}
			if action == "files" {
				names := []string{}
				for n := range files {
					names = append(names, n)
				}
				sort.Strings(names)
				return map[string]any{"files": names}, nil
			}
			b, ok := files[r.Path]
			if !ok {
				return nil, capError(404, "not_found", "file not found")
			}
			truncated := len(b) > 64<<10
			if truncated {
				b = b[:64<<10]
			}
			return map[string]any{"path": r.Path, "text": string(b), "truncated": truncated, "untrusted": true}, nil
		}
		if action == "confirm" {
			if e = capability.ValidateLocalChoices(p, r.LocalChoices); e != nil {
				return nil, capError(400, "local_choice_required", e.Error())
			}
			for _, state := range p.SkillReconciliation {
				if state.LocallyModified && !r.Reviewed {
					return nil, capError(400, "review_required", "review local skill changes before updating")
				}
			}
			if e = a.caps().CheckSkillReconciliation(ctx, p); e != nil {
				return nil, capError(409, "local_skills_changed", e.Error())
			}
			policy := capability.InstallPolicy(p.Lock.Source.Trust, p.Lock.Scan.Verdict, false)
			if !policy.Allowed {
				return nil, capError(403, "blocked", policy.Reason)
			}
			if policy.ReviewRequired && !r.Reviewed || policy.SecondConfirmationRequired && !r.SecondaryConfirm {
				return nil, capError(400, "review_required", "review findings and explicitly confirm the risks")
			}
		}
		p, e = a.caps().ClaimProposal(ctx, p.ID, r.SessionID, "ui")
		if e != nil {
			return nil, capError(409, "proposal_unavailable", e.Error())
		}
		if action == "dismiss" {
			e = a.caps().FinishProposal(ctx, p.ID, "ui", "dismissed")
			return map[string]bool{"ok": e == nil}, e
		}
		return a.installCapabilityProposal(ctx, p, r.InstallDependencies, r.LocalChoices)
	}
	return nil, capError(400, "unsupported", "unsupported capability action")
}
func (a *App) installCapabilityProposal(ctx context.Context, p capability.Proposal, installDependencies bool, localChoices ...map[string]string) (out any, err error) {
	defer func() {
		if err != nil {
			_ = a.caps().FinishProposal(context.Background(), p.ID, "ui", "failed")
		}
	}()
	choices := map[string]string{}
	if len(localChoices) > 0 && localChoices[0] != nil {
		choices = localChoices[0]
	}
	if err = capability.ValidateLocalChoices(p, choices); err != nil {
		return nil, err
	}
	if err = a.caps().CheckSkillReconciliation(ctx, p); err != nil {
		return nil, err
	}
	files, err := a.proposalFiles(ctx, p)
	if err != nil {
		return nil, err
	}
	// Recompute the scan at action time; a changed scanner cannot weaken the review.
	fresh, scanErr := scanCapabilitySource(p.Lock.Source, files)
	if scanErr != nil {
		return nil, scanErr
	}
	if fresh.Scanner != p.Lock.Scan.Scanner || fresh.Verdict != p.Lock.Scan.Verdict {
		return nil, capError(409, "review_changed", "scan changed; create a new proposal")
	}
	if p.Kind == "install" {
		if _, e := a.caps().Get(ctx, p.Lock.ID); e == nil {
			return nil, capError(409, "already_installed", "capability is already installed")
		}
	}
	if err = a.verifyReviewedCapabilityDependencies(ctx, p.Lock); err != nil {
		return nil, err
	}
	if installDependencies {
		cleanupDependencies, e := a.installCapabilityDependencies(ctx, &p)
		defer func() {
			if err != nil {
				cleanupDependencies()
			}
		}()
		if e != nil {
			return nil, e
		}
	}
	registered := []string{}
	defer func() {
		if err != nil {
			for _, sid := range registered {
				a.rollbackCapabilitySkill(sid)
			}
		}
	}()
	var resolutions map[string]capability.SkillResolution
	registered, resolutions, err = a.prepareCapabilitySkills(ctx, &p, files, choices)
	if err != nil {
		return nil, err
	}
	p.Lock.InstalledBy = "session:" + p.SessionID + " / ui"
	return a.caps().InstallProposalReconciled(ctx, p.ID, p.SessionID, "ui", p.Lock, files, resolutions)
}
func (a *App) capabilityTitle(ctx context.Context, id string) (string, bool) {
	r, e := a.caps().Get(ctx, id)
	return r.Title, e == nil
}
func (a *App) capabilitySkills(ctx context.Context, id string) []string {
	r, e := a.caps().Get(ctx, id)
	if e != nil || a.caps().Verify(ctx, id, r.VersionID) != nil {
		return nil
	}
	var out []string
	for _, c := range r.Lock.Components {
		if !c.Disabled {
			out = append(out, c.SkillID)
		}
	}
	return out
}
func (a *App) chatToolEnv(sid string) map[string]string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return nil
	}
	token := hex.EncodeToString(b)
	a.capState.mu.Lock()
	defer a.capState.mu.Unlock()
	if a.capState.tokens == nil {
		a.capState.tokens = map[string]capabilityToken{}
	}
	for k, v := range a.capState.tokens {
		if v.SessionID == sid || time.Now().After(v.Expires) {
			delete(a.capState.tokens, k)
		}
	}
	a.capState.tokens[token] = capabilityToken{sid, time.Now().Add(2 * time.Hour)}
	return map[string]string{sessiontools.EnvIPC: a.Cfg.IPCPath, sessiontools.EnvToken: token}
}
func (a *App) endChatToolToken(sid string) {
	a.capState.mu.Lock()
	defer a.capState.mu.Unlock()
	for k, v := range a.capState.tokens {
		if v.SessionID == sid {
			delete(a.capState.tokens, k)
		}
	}
}
func (a *App) rpcSessionTool(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		Token string            `json:"token"`
		Tool  string            `json:"tool"`
		Args  map[string]string `json:"args"`
	}
	if e := json.Unmarshal(raw, &in); e != nil {
		return nil, e
	}
	a.capState.mu.Lock()
	t, ok := a.capState.tokens[in.Token]
	a.capState.mu.Unlock()
	if !ok || time.Now().After(t.Expires) {
		return nil, errors.New("session tool token expired or invalid")
	}
	if _, e := a.chatSession(ctx, t.SessionID); e != nil {
		return nil, e
	}
	req := webui.CapabilityRequest{SessionID: t.SessionID, Source: in.Args["source"], Query: in.Args["query"], Reason: in.Args["reason"]}
	var out any
	var err error
	switch in.Tool {
	case "cap_search":
		req.Providers = in.Args["providers"]
		if in.Args["registry"] == "mcp" {
			out, err = a.CapabilityRequest(ctx, "registry_search", req)
		} else if in.Args["registry"] != "" {
			return nil, errors.New("unsupported capability registry")
		} else {
			out, err = a.CapabilityRequest(ctx, "search", req)
		}
	case "cap_inspect":
		out, err = a.CapabilityRequest(ctx, "inspect", req)
	case "cap_check":
		out, err = a.CapabilityRequest(ctx, "check", req)
	case "cap_list":
		records, e := a.caps().List(ctx)
		if e != nil {
			return nil, e
		}
		row, e := a.chatSession(ctx, t.SessionID)
		if e != nil {
			return nil, e
		}
		out = map[string]any{"capabilities": records, "bound": parseChatBindings(row.bindingsRaw).Capabilities}
	case "cap_propose":
		if action := in.Args["action"]; action == "update" || action == "uninstall" {
			out, err = a.proposeCapabilityLifecycle(ctx, action, req)
			break
		}
		if in.Args["action"] == "skill_optimize" {
			req.SkillID = in.Args["skill_id"]
			if !a.sessionHasSkill(ctx, t.SessionID, req.SkillID) {
				return nil, errors.New("skill must be bound to this session")
			}
			out, err = a.CapabilityRequest(ctx, "skill-optimize", req)
			break
		}
		if in.Args["action"] != "install" {
			return nil, errors.New("only install proposals are supported")
		}
		out, err = a.CapabilityRequest(ctx, "propose", req)
	case "cap_read_file":
		lock, e := a.inspectCapability(ctx, req.Source)
		if e != nil {
			return nil, e
		}
		files, e := a.proposalFiles(ctx, capability.Proposal{Lock: lock})
		if e != nil {
			return nil, e
		}
		b, ok := files[in.Args["path"]]
		if !ok {
			return nil, errors.New("file not found")
		}
		if len(b) > 64<<10 {
			b = b[:64<<10]
		}
		out = map[string]any{"text": string(b), "untrusted": true}
	case "cap_test":
		req.Source = in.Args["id"]
		if _, e := a.caps().Get(ctx, req.Source); e != nil {
			return nil, errors.New("installed capability required")
		}
		out, err = a.testInstalledCapability(ctx, req.Source)
	default:
		return nil, errors.New("unsupported session tool")
	}
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(out)
	return map[string]string{"text": string(b)}, err
}

func (a *App) checkCapability(ctx context.Context, lock capability.Lock) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := a.resolveCapabilityDependencies(ctx, &lock, true); err != nil {
		return nil, err
	}
	return map[string]any{"id": lock.ID, "requires": lock.Requires, "resolved": lock.Resolved, "dependency_preferences": lock.DependencyPreferences, "dependency_plan": dependencyPlan(lock), "managed_install_supported": true, "note": "Owner-configured paths and managed preferences apply to this review. Pinned managed dependencies require explicit owner confirmation. Login shell startup hooks and upstream setup scripts are not executed."}, nil
}

// Roll back only the newly allocated component, never an existing user skill.
func (a *App) rollbackCapabilitySkill(skillID string) {
	ctx := context.Background()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	for _, query := range []string{
		`DELETE FROM version_files WHERE version_id IN (SELECT id FROM versions WHERE entity_type='skill' AND entity_id=?)`,
		`DELETE FROM pointers WHERE entity_type='skill' AND entity_id=?`,
		`DELETE FROM version_tags WHERE entity_type='skill' AND entity_id=?`,
		`DELETE FROM versions WHERE entity_type='skill' AND entity_id=?`,
		`DELETE FROM skills WHERE id=?`,
	} {
		if _, err = tx.ExecContext(ctx, query, skillID); err != nil {
			return
		}
	}
	_ = tx.Commit()
}

func inferCapabilityMetadata(md []byte) []capability.ToolRequirement {
	text := strings.ReplaceAll(strings.TrimPrefix(string(md), "\ufeff"), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return nil
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 || end > 64<<10 {
		return nil
	}
	var meta struct {
		Metadata struct {
			Requires struct {
				Bins []string `yaml:"bins"`
			} `yaml:"requires"`
			CLIVersion string `yaml:"cli_version"`
		} `yaml:"metadata"`
		CLIVersion string `yaml:"cli_version"`
	}
	if yaml.Unmarshal([]byte(text[4:4+end]), &meta) != nil {
		return nil
	}
	v := meta.Metadata.CLIVersion
	if v == "" {
		v = meta.CLIVersion
	}
	if len(v) > 128 {
		return nil
	}
	out := []capability.ToolRequirement{}
	for _, name := range meta.Metadata.Requires.Bins {
		if len(out) >= 32 {
			break
		}
		if len(name) > 128 || strings.ContainsAny(name, "/\\ \t\n") {
			continue
		}
		out = append(out, capability.ToolRequirement{Name: name, Version: v})
	}
	return out
}
func (a *App) searchCapabilities(ctx context.Context, query string) ([]capability.CatalogEntry, error) {
	out := capability.SearchCatalog(query)
	repos, err := a.Skills.ListRepositories(ctx)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	for _, repo := range repos {
		var candidates []skillrepo.Candidate
		if json.Unmarshal(repo.Candidates, &candidates) != nil {
			continue
		}
		for _, c := range candidates {
			if c.Error != "" || q != "" && !strings.Contains(strings.ToLower(c.Name+" "+c.Description+" "+repo.Name), q) {
				continue
			}
			out = append(out, capability.CatalogEntry{ID: "repository:" + repo.ID + ":" + c.Path, Title: c.Name, Description: c.Description, Source: capability.Source{Kind: "git", URL: repo.URL, Ref: repo.Ref, Subpaths: []string{c.Path}, Adapter: "skill-dir", Trust: "unknown"}})
		}
	}
	return out, nil
}
func (a *App) capabilitySource(ctx context.Context, source string) (capability.Source, capability.Requirements, error) {
	if strings.HasPrefix(source, capabilitySkillSourcePrefix) {
		return parseCapabilitySkillSource(source)
	}
	if strings.HasPrefix(source, capabilitySkillDiscoveryPrefix) {
		return a.resolveCapabilityDiscoveredSkill(ctx, source)
	}
	if strings.HasPrefix(source, capabilityMarketplacePrefix) {
		return parseCapabilityMarketplaceSource(source)
	}
	if strings.HasPrefix(source, "repository:") {
		parts := strings.SplitN(source, ":", 3)
		if len(parts) != 3 {
			return capability.Source{}, capability.Requirements{}, capError(400, "invalid_source", "invalid repository source")
		}
		repo, err := a.Skills.GetRepository(ctx, parts[1])
		if err != nil {
			return capability.Source{}, capability.Requirements{}, err
		}
		root, err := skillrepo.ValidateRoot(parts[2])
		if err != nil {
			return capability.Source{}, capability.Requirements{}, err
		}
		if repo.Root != "" && root != repo.Root && !strings.HasPrefix(root, repo.Root+"/") {
			return capability.Source{}, capability.Requirements{}, capError(400, "invalid_source", "outside registered repository scope")
		}
		return capability.Source{Kind: "git", URL: repo.URL, Ref: repo.Ref, Subpaths: []string{root}, Adapter: "skill-dir", Trust: "unknown"}, capability.Requirements{}, nil
	}
	return capSource(source)
}
func (a *App) recoverCapabilityProposals(ctx context.Context) {
	if _, err := a.caps().RecoverStorage(ctx); err != nil {
		a.Log.Warn("recover capability snapshots", "error", err)
	}
	// Installs only run under an applying proposal, which a restart ends; the
	// stages and downloads an interrupted one left are never reused.
	if r, err := managed.SweepStages(ctx, a.Cfg.DataDir); err != nil {
		a.Log.Warn("remove interrupted managed installs", "error", err)
	} else if len(r.Removed) > 0 {
		a.Log.Info("removed interrupted managed installs", "count", len(r.Removed), "bytes", r.FreedBytes)
	}
	_, err := a.caps().RecoverInterruptedProposals(ctx)
	if err != nil {
		a.Log.Warn("recover capability proposals", "error", err)
	}
}

func (a *App) cacheCapabilityInspection(lock capability.Lock) {
	a.capState.mu.Lock()
	defer a.capState.mu.Unlock()
	if len(a.capState.inspected) >= 128 {
		a.capState.inspected = nil
	}
	if a.capState.inspected == nil {
		a.capState.inspected = map[string]capability.Lock{}
	}
	a.capState.inspected[lock.ID] = lock
}
