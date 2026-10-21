package updater

import "testing"

func TestSelfBuiltIsNotifyOnly(t *testing.T) {
	t.Setenv("TIDEBOT_MANAGED", "")
	defer func(f func() bool) { OfficialBuild = f }(OfficialBuild)

	OfficialBuild = func() bool { return false }
	in := Detect("server", "/opt/tidebot/agentd")
	if in.Mode != ModeNotify || in.Kind != KindSelfBuilt {
		t.Fatalf("self-built copy must be notify-only, got %+v", in)
	}
	m := &Manifest{Version: "v9.9.9", Assets: map[string]Asset{AssetKey("server", "linux", "amd64"): {URL: "x"}}}
	c := Evaluate(m, "0.1.6", in, "linux", "amd64")
	if !c.Available || c.CanApply {
		t.Fatalf("self-built: announce, never apply: %+v", c)
	}
	if c := Evaluate(m, "dev", in, "linux", "amd64"); c.Available || c.CanApply {
		t.Fatalf("dev build: nothing to apply: %+v", c)
	}

	OfficialBuild = func() bool { return true }
	if in := Detect("server", "/opt/tidebot/agentd"); in.Mode != ModeApply {
		t.Fatalf("official tarball install should apply, got %+v", in)
	}
}
