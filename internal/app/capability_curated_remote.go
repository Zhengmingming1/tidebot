package app

import (
	"context"
	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/capability/adapters"
)

func (a *App) readCuratedRemoteCapabilitySnapshot(_ context.Context, source capability.Source) (map[string][]byte, error) {
	files, err := capability.CuratedRemoteSnapshot(source)
	if err != nil {
		return nil, capError(400, "invalid_source", err.Error())
	}
	return files, nil
}

func (a *App) inspectCuratedRemoteCapability(ctx context.Context, source capability.Source) (capability.Lock, error) {
	files, err := a.readCuratedRemoteCapabilitySnapshot(ctx, source)
	if err != nil {
		return capability.Lock{}, err
	}
	parsed, err := adapters.ParseRegistry(files["server.json"])
	if err != nil {
		return capability.Lock{}, err
	}
	for i := range parsed.Components {
		parsed.Components[i].MCP.OAuth = true
	}
	_, hash, err := capability.HashFiles(files)
	if err != nil {
		return capability.Lock{}, err
	}
	scan, err := scanCapabilitySource(source, files)
	if err != nil {
		return capability.Lock{}, err
	}
	lock := capability.Lock{Format: capability.LockFormat, ID: capKey("curated-remote:" + source.Registry.Entry), Title: parsed.Title, Source: source, FilesSHA256: hash, Components: parsed.Components, Requires: parsed.Requires, Permissions: parsed.Permissions, Scan: scan}
	if err := lock.Validate(); err != nil {
		return capability.Lock{}, err
	}
	a.cacheCapabilityInspection(lock)
	return lock, nil
}

func scanCuratedRemoteCapability(source capability.Source, files map[string][]byte) (capability.Scan, error) {
	expected, err := capability.CuratedRemoteSnapshot(source)
	if err != nil {
		return capability.Scan{}, err
	}
	_, want, err := capability.HashFiles(expected)
	if err != nil {
		return capability.Scan{}, err
	}
	_, got, err := capability.HashFiles(files)
	if err != nil {
		return capability.Scan{}, err
	}
	if want != got {
		return capability.Scan{}, capError(409, "source_changed", "curated remote configuration differs from the reviewed built-in snapshot")
	}
	scan := capability.ScanFiles(files)
	scan.Findings = append(scan.Findings, capability.Finding{ID: "hosted-service-mutable", File: "server.json", Severity: "caution", Note: "Only client configuration is pinned. Hosted provider code and tools can change. OAuth consent and exact tool grants require separate approval."})
	if scan.Verdict == "safe" {
		scan.Verdict = "caution"
	}
	return scan, nil
}
