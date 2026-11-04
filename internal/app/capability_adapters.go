package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/capability/adapters"
)

type capabilitySnapshotGit interface {
	ReadSnapshot(context.Context, string, string, string) (map[string][]byte, error)
}

func (a *App) readCapabilitySnapshot(ctx context.Context, source capability.Source) (map[string][]byte, error) {
	if source.Kind == "curated-binary" {
		return a.readCuratedBinaryCapabilitySnapshot(ctx, source)
	}
	if source.Kind == "curated-remote" {
		return a.readCuratedRemoteCapabilitySnapshot(ctx, source)
	}
	if source.Kind == "registry" {
		return a.readCapabilityRegistrySnapshot(ctx, source)
	}
	if source.Adapter == "skill-dir" || source.Adapter == "" {
		return a.capGit().ReadSkill(ctx, capKey(source.URL), source.Commit, sourceRoot(source))
	}
	if git, ok := a.capGit().(capabilitySnapshotGit); ok {
		return git.ReadSnapshot(ctx, capKey(source.URL), source.Commit, sourceRoot(source))
	}
	return nil, errors.New("source adapter unavailable")
}
func (a *App) inspectOtherAdapter(ctx context.Context, source capability.Source) (capability.Lock, bool, error) {
	git, ok := a.capGit().(capabilitySnapshotGit)
	if !ok {
		return capability.Lock{}, false, nil
	}
	files, err := git.ReadSnapshot(ctx, capKey(source.URL), source.Commit, sourceRoot(source))
	if err != nil {
		return capability.Lock{}, false, err
	}
	var result adapters.Result
	if raw, ok := files["tidebot-capability.json"]; ok {
		source.Adapter = "tidebot-native"
		var native capability.Lock
		if err = json.Unmarshal(raw, &native); err == nil {
			result = adapters.Result{Title: native.Title, Components: native.Components, Requires: native.Requires, Permissions: native.Permissions}
			for i := range result.Components {
				result.Components[i].SkillID = ""
				result.Components[i].VersionID = ""
				result.Components[i].FilesSHA256 = ""
			}
		}
	} else if _, ok := files["plugin.yaml"]; ok {
		source.Adapter = "tidebot-plugin"
		result, err = adapters.ParseTidebotPlugin(files)
	} else if _, ok := files[".claude-plugin/plugin.json"]; ok {
		source.Adapter = "claude-plugin"
		result, err = adapters.ParseClaudePlugin(files, "")
	} else if raw, ok := files["server.json"]; ok {
		source.Adapter = "mcp-registry"
		result, err = adapters.ParseRegistry(raw)
	} else {
		return capability.Lock{}, false, nil
	}
	if err != nil {
		return capability.Lock{}, true, err
	}
	_, hash, err := capability.HashFiles(files)
	if err != nil {
		return capability.Lock{}, true, err
	}
	scan := capability.ScanFiles(files)
	scan.Findings = append(scan.Findings, result.Findings...)
	for _, f := range result.Findings {
		if f.Severity == "high" {
			scan.Verdict = "high"
		} else if f.Severity == "caution" && scan.Verdict == "safe" {
			scan.Verdict = "caution"
		}
	}
	lock := capability.Lock{Format: capability.LockFormat, ID: capKey(source.URL + "\x00" + sourceRoot(source)), Title: result.Title, Source: source, FilesSHA256: hash, Components: result.Components, Requires: result.Requires, Permissions: result.Permissions, Scan: scan}
	if strings.TrimSpace(lock.Title) == "" {
		return capability.Lock{}, true, errors.New("adapter has no title")
	}
	return lock, true, nil
}

func scanCapabilitySource(source capability.Source, files map[string][]byte) (capability.Scan, error) {
	if source.Kind == "curated-binary" {
		return scanCuratedBinaryCapability(source, files)
	}
	if source.Kind == "curated-remote" {
		return scanCuratedRemoteCapability(source, files)
	}
	scan := capability.ScanFiles(files)
	if source.Kind == "registry" {
		scan.Findings = append(scan.Findings, capability.Finding{ID: "registry-identity-only", File: "server.json", Severity: "caution", Note: "Registry namespace ownership verifies publisher identity, not a security audit of server code or packages."})
		if scan.Verdict == "safe" {
			scan.Verdict = "caution"
		}
	}
	var result adapters.Result
	var err error
	switch source.Adapter {
	case "tidebot-plugin":
		result, err = adapters.ParseTidebotPlugin(files)
	case "claude-plugin":
		result, err = adapters.ParseClaudePlugin(files, "")
	case "mcp-registry":
		result, err = adapters.ParseRegistry(files["server.json"])
	}
	if err != nil {
		return scan, err
	}
	scan.Findings = append(scan.Findings, result.Findings...)
	for _, f := range result.Findings {
		if f.Severity == "high" {
			scan.Verdict = "high"
		} else if f.Severity == "caution" && scan.Verdict == "safe" {
			scan.Verdict = "caution"
		}
	}
	return scan, nil
}
func capabilityComponentFiles(lock capability.Lock, component capability.Component, files map[string][]byte) (map[string][]byte, error) {
	if lock.Source.Adapter == "skill-dir" {
		return files, nil
	}
	if component.Type == "command" {
		b, ok := files[component.Path]
		if !ok {
			return nil, errors.New("command file missing")
		}
		return map[string][]byte{"SKILL.md": b}, nil
	}
	out := map[string][]byte{}
	componentPath := component.Path
	if strings.HasSuffix(componentPath, "/SKILL.md") {
		componentPath = strings.TrimSuffix(componentPath, "/SKILL.md")
	} else if componentPath == "SKILL.md" {
		componentPath = "."
	}
	prefix := strings.TrimSuffix(componentPath, "/") + "/"
	if componentPath == "." || componentPath == "" {
		prefix = ""
	}
	for p, b := range files {
		if strings.HasPrefix(p, prefix) {
			out[strings.TrimPrefix(p, prefix)] = b
		}
	}
	if _, ok := out["SKILL.md"]; !ok {
		return nil, errors.New("skill component has no SKILL.md")
	}
	return out, nil
}
func (a *App) registerCapabilitySkill(ctx context.Context, proposalID string, index int, name string, files map[string][]byte) (skillstore.SkillView, error) {
	dir, err := os.MkdirTemp(a.Cfg.DataDir, ".cap-skill-")
	if err != nil {
		return skillstore.SkillView{}, err
	}
	defer os.RemoveAll(dir)
	for name, b := range files {
		dest := filepath.Join(dir, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return skillstore.SkillView{}, err
		}
		if err = os.WriteFile(dest, b, 0600); err != nil {
			return skillstore.SkillView{}, err
		}
	}
	skillName := fmt.Sprintf("%s %s %d", name, proposalID, index)
	skill, err := a.Skills.ImportDir(ctx, skillName, dir, "capability:ui")
	if err != nil {
		var sid string
		if a.DB.QueryRowContext(context.Background(), `SELECT id FROM skills WHERE name=?`, skillName).Scan(&sid) == nil {
			a.rollbackCapabilitySkill(sid)
		}
	}
	return skill, err
}
func (a *App) verifySessionCapabilities(ctx context.Context, row chatRow) error {
	for _, id := range parseChatBindings(row.bindingsRaw).Capabilities {
		rec, err := a.caps().Get(ctx, id)
		if err != nil {
			return fmt.Errorf("bound capability unavailable: %s", id)
		}
		if err = a.caps().Verify(ctx, id, rec.VersionID); err != nil {
			return fmt.Errorf("capability integrity check: %w", err)
		}
		for _, c := range rec.Lock.Components {
			if c.Disabled || c.Type != "skill" && c.Type != "command" {
				continue
			}
			files, e := a.Skills.Files(ctx, c.VersionID)
			if e != nil {
				return e
			}
			_, hash, e := capability.HashFiles(files)
			want := c.FilesSHA256
			if want == "" && rec.Lock.Source.Adapter == "skill-dir" {
				want = rec.Lock.FilesSHA256
			}
			if e != nil || want == "" || hash != want {
				return errors.New("bound capability skill differs from reviewed files")
			}
		}
	}
	return nil
}
