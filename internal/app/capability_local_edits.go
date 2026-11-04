package app

import (
	"context"
	"errors"

	"github.com/Zhengmingming1/tidebot/internal/capability"
)

// prepareCapabilitySkills creates immutable candidate versions without changing
// existing current pointers. Store publication owns the final compare-and-swap.
func (a *App) prepareCapabilitySkills(ctx context.Context, p *capability.Proposal, files map[string][]byte, choices map[string]string) (registered []string, resolutions map[string]capability.SkillResolution, err error) {
	resolutions = map[string]capability.SkillResolution{}
	states := map[string]capability.SkillReconciliation{}
	for _, state := range p.SkillReconciliation {
		key := capability.ComponentIdentity(capability.Component{Type: state.Type, Path: state.Path})
		if _, ok := states[key]; ok {
			return nil, nil, errors.New("ambiguous existing skill identity")
		}
		states[key] = state
		action := choices[state.SkillID]
		if action == "" {
			action = "adopt_upstream"
		}
		resolution := capability.SkillResolution{Action: action}
		if action == "copy" {
			localFiles, e := a.Skills.Files(ctx, state.CurrentVersionID)
			if e != nil {
				return registered, nil, e
			}
			_, hash, e := capability.HashFiles(localFiles)
			if e != nil {
				return registered, nil, e
			}
			if hash != state.FilesSHA256 {
				return registered, nil, capability.ErrLocalSkillsChanged
			}
			copied, e := a.registerCapabilitySkill(ctx, p.ID, len(p.Lock.Components)+len(registered), state.Name+" (local copy)", localFiles)
			if e != nil {
				return registered, nil, e
			}
			registered = append(registered, copied.ID)
			resolution.CopySkillID = copied.ID
		}
		resolutions[state.SkillID] = resolution
	}
	for index := range p.Lock.Components {
		component := &p.Lock.Components[index]
		if component.Disabled || component.Type != "skill" && component.Type != "command" {
			continue
		}
		state, existing := states[capability.ComponentIdentity(*component)]
		if existing && resolutions[state.SkillID].Action == "keep_local" {
			localFiles, e := a.Skills.Files(ctx, state.CurrentVersionID)
			if e != nil {
				return registered, nil, e
			}
			_, hash, e := capability.HashFiles(localFiles)
			if e != nil {
				return registered, nil, e
			}
			if hash != state.FilesSHA256 {
				return registered, nil, capability.ErrLocalSkillsChanged
			}
			component.SkillID = state.SkillID
			component.VersionID = state.CurrentVersionID
			component.FilesSHA256 = state.FilesSHA256
			component.LocallyModified = true
			continue
		}
		componentFiles, e := capabilityComponentFiles(p.Lock, *component, files)
		if e != nil {
			return registered, nil, e
		}
		component.LocallyModified = false
		if existing {
			next, _, e := a.Skills.CommitFiles(ctx, state.SkillID, state.Name, componentFiles, "capability_update", "capability:ui", true)
			if e != nil {
				return registered, nil, e
			}
			component.SkillID = state.SkillID
			component.VersionID = next.ID
		} else {
			skill, e := a.registerCapabilitySkill(ctx, p.ID, index, component.Name, componentFiles)
			if e != nil {
				return registered, nil, e
			}
			registered = append(registered, skill.ID)
			component.SkillID = skill.ID
			component.VersionID = skill.CurrentVersion.ID
		}
		_, component.FilesSHA256, e = capability.HashFiles(componentFiles)
		if e != nil {
			return registered, nil, e
		}
	}
	return registered, resolutions, nil
}
