package adapters

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/plugin"
	"gopkg.in/yaml.v3"
)

// ParseTidebotPlugin exposes legacy native extensions without changing their
// separate per-permission approval mechanism. Importing this inert capability
// never starts a runtime, connector, trigger, notifier or workflow pack.
func ParseTidebotPlugin(files map[string][]byte) (Result, error) {
	var out Result
	if _, _, err := capability.HashFiles(files); err != nil {
		return out, err
	}
	raw, ok := files["plugin.yaml"]
	if !ok {
		return out, errors.New("plugin.yaml missing")
	}
	var m plugin.Manifest
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return out, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return out, errors.New("plugin manifest must contain one document")
	}
	// Validate only schema and relative names; Validate never reads the directory.
	if err := m.Validate(filepath.Join(string(filepath.Separator), "capability-review")); err != nil {
		return out, err
	}
	out.Title = m.Name
	out.Permissions.Network = append([]string(nil), m.Permissions.Network...)
	out.Permissions.SideEffects = len(m.Contributes.Triggers) > 0 || len(m.Contributes.Notifiers) > 0 || len(m.Permissions.FS.Write) > 0 || len(m.Permissions.HostAPI) > 0
	add := func(kind, name string) {
		out.Components = append(out.Components, capability.Component{Type: "tidebot-plugin", Name: kind + ":" + name, Path: "plugin.yaml", Disabled: true})
	}
	add("runtime", m.ID)
	for _, c := range m.Contributes.Connectors {
		add("connector", c.ID)
		out.Permissions.SideEffects = out.Permissions.SideEffects || c.SideEffects
	}
	for _, c := range m.Contributes.Triggers {
		add("trigger", c.ID)
	}
	for _, c := range m.Contributes.Notifiers {
		add("notifier", c.ID)
	}
	for _, c := range m.Contributes.WorkflowPacks {
		add("workflow-pack", c.Path)
	}
	if len(out.Components) > 128 {
		return Result{}, errors.New("too many plugin components")
	}
	out.Findings = []capability.Finding{{ID: "legacy-plugin-approval", File: "plugin.yaml", Severity: "caution", Note: fmt.Sprintf("Native tidebot plugin %s %s remains disabled here. Use the tidebot plugins manager to install and approve individual permissions; capability confirmation does not authorize native plugin execution.", m.ID, m.Version)}}
	return out, nil
}
