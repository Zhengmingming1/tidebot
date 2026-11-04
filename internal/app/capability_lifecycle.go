package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func (a *App) capabilityLifecycleRequest(ctx context.Context, action string, r webui.CapabilityRequest) (any, error) {
	switch action {
	case "gc":
		if !r.Reviewed {
			return nil, capError(400, "review_required", "confirm old snapshot cleanup")
		}
		return a.caps().GC(ctx, r.CapabilityID, "ui")
	case "versions":
		v, e := a.caps().Versions(ctx, r.CapabilityID)
		return map[string]any{"versions": v}, e
	case "events":
		v, e := a.caps().Events(ctx, r.CapabilityID)
		return map[string]any{"events": v}, e
	case "export":
		if r.VersionID == "" {
			current, err := a.caps().Get(ctx, r.CapabilityID)
			if err != nil {
				return nil, err
			}
			r.VersionID = current.VersionID
		}
		return a.caps().ExportBundle(ctx, r.CapabilityID, r.VersionID)
	case "rollback":
		return a.caps().Rollback(ctx, r.CapabilityID, r.VersionID, "ui")
	case "uninstall":
		result, e := a.caps().UninstallWithReport(ctx, r.CapabilityID, "ui")
		if errors.Is(e, capability.ErrReferenced) {
			return nil, capError(409, "referenced", "remove capability bindings before uninstalling")
		}
		return result, e
	case "update":
		if _, e := a.chatSession(ctx, r.SessionID); e != nil {
			return nil, e
		}
		old, e := a.caps().Get(ctx, r.CapabilityID)
		if e != nil {
			return nil, e
		}
		if old.Lock.Source.Kind != "git" && old.Lock.Source.Kind != "registry" && old.Lock.Source.Kind != "curated-remote" && old.Lock.Source.Kind != "curated-binary" {
			return nil, capError(400, "manual_update", "this source must be imported again")
		}
		// Re-fetch the registered source's current ref, never the inspection cache.
		requires := capability.Requirements{}
		source := old.Lock.Source
		if source.Kind == "git" && source.Ref != source.Commit {
			source.Commit = ""
		}
		if old.Lock.Source.Registry != nil {
			for _, entry := range capability.Catalog() {
				if entry.ID == old.Lock.Source.Registry.Entry {
					requires = entry.Requires
					if source.Kind == "curated-remote" || source.Kind == "curated-binary" {
						source = entry.Source
					}
				}
			}
		}
		var next capability.Lock
		if old.Lock.Source.Kind == "registry" {
			if old.Lock.Source.Registry == nil || old.Lock.Source.Registry.Name != "mcp-registry" {
				return nil, capError(400, "invalid_source", "unknown registry source")
			}
			next, e = a.inspectCapabilityRegistry(ctx, capabilityRegistryPrefix+old.Lock.Source.Registry.Entry)
		} else {
			next, e = a.inspectCapabilityAtSource(ctx, source, requires)
		}
		if e != nil {
			return nil, e
		}
		if next.ID != old.ID {
			return nil, capError(409, "source_changed", "source no longer identifies the same capability")
		}
		checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		e = a.resolveCapabilityDependencies(checkCtx, &next, true)
		cancel()
		if e != nil {
			return nil, e
		}
		files, e := a.readCapabilitySnapshot(ctx, next.Source)
		if e != nil {
			return nil, e
		}
		p, diff, e := a.caps().CreateUpdateProposal(ctx, r.SessionID, "", next, files)
		if e != nil {
			return nil, e
		}
		return struct {
			capabilityProposalView
			Diff capability.Diff `json:"diff"`
		}{proposalView(p), diff}, nil
	case "import":
		if _, e := a.chatSession(ctx, r.SessionID); e != nil {
			return nil, e
		}
		lock, files, e := capability.ParseBundle(r.Bundle)
		if e != nil {
			return nil, capError(400, "invalid_bundle", e.Error())
		}
		if _, e = a.caps().Get(ctx, lock.ID); e == nil {
			return nil, capError(409, "already_installed", "capability already installed")
		}
		lock.Source.Kind = "bundle"
		lock.Scan, e = scanCapabilitySource(lock.Source, files)
		if e != nil {
			return nil, e
		}
		if e = capability.ValidatePermissions(lock); e != nil {
			return nil, e
		}
		if e = a.cacheImportedSnapshot(lock.FilesSHA256, files); e != nil {
			return nil, e
		}
		if e = a.resolveCapabilityDependencies(ctx, &lock, true); e != nil {
			return nil, e
		}
		p, e := a.caps().CreateProposal(ctx, capability.Proposal{SessionID: r.SessionID, Kind: "install", Lock: lock})
		return proposalView(p), e
	}
	return nil, capError(400, "unsupported", "unsupported lifecycle action")
}
func (a *App) cacheImportedSnapshot(hash string, files map[string][]byte) error {
	if !capabilitySHA256.MatchString(hash) {
		return errors.New("invalid snapshot hash")
	}
	dir := filepath.Join(a.Cfg.DataDir, "cache", "capability-imports")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	raw, e := json.Marshal(files)
	if e != nil {
		return e
	}
	tmp, e := os.CreateTemp(dir, ".stage-")
	if e != nil {
		return e
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, e = tmp.Write(raw); e != nil {
		tmp.Close()
		return e
	}
	if e = tmp.Close(); e != nil {
		return e
	}
	return os.Rename(name, filepath.Join(dir, hash+".json"))
}
func (a *App) readImportedSnapshot(hash string) (map[string][]byte, error) {
	if !capabilitySHA256.MatchString(hash) {
		return nil, errors.New("invalid snapshot hash")
	}
	raw, e := os.ReadFile(filepath.Join(a.Cfg.DataDir, "cache", "capability-imports", hash+".json"))
	if e != nil {
		return nil, e
	}
	if len(raw) > 96<<20 {
		return nil, errors.New("imported snapshot too large")
	}
	files := map[string][]byte{}
	if e = json.Unmarshal(raw, &files); e != nil {
		return nil, e
	}
	_, got, e := capability.HashFiles(files)
	if e != nil {
		return nil, e
	}
	if got != hash {
		return nil, errors.New("imported snapshot changed")
	}
	return files, nil
}
