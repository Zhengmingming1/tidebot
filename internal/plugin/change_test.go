package plugin

import (
	"context"
	"testing"
)

func TestStoreLifecycleChangeNotifications(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	changes := 0
	s.OnChange = func(ctx context.Context, id string) {
		if id != "demo-plugin" {
			t.Fatalf("unexpected plugin %q", id)
		}
		changes++
		// Hooks may query committed state, including during a version switch.
		_, _ = s.Get(ctx, id)
	}
	v, err := s.Stage(ctx, writePlugin(t, "1.0.0", "kv", false), "")
	if err != nil || changes != 1 {
		t.Fatalf("stage: %d %v", changes, err)
	}
	if err := s.Grant(ctx, v.ID, []string{"network:undeclared.invalid"}); err == nil || changes != 1 {
		t.Fatal("failed grant notified a change")
	}
	steps := []func() error{
		func() error { return s.Grant(ctx, v.ID, v.Requested) },
		func() error { return s.SetPermission(ctx, "demo-plugin", "host_api:kv", false) },
		func() error { return s.SetConfig(ctx, "demo-plugin", nil) },
		func() error { return s.SetEnabled(ctx, "demo-plugin", false) },
		func() error { return s.SetCurrent(ctx, "demo-plugin", v.ID) },
		func() error { return s.Approve(ctx, v.ID, v.Requested) },
		func() error { return s.Uninstall(ctx, "demo-plugin", false) },
	}
	for i, step := range steps {
		if err := step(); err != nil || changes != i+2 {
			t.Fatalf("step %d: changes=%d err=%v", i, changes, err)
		}
	}
	if err := s.SetEnabled(ctx, "missing", true); err == nil || changes != len(steps)+1 {
		t.Fatal("enabling a nonexistent plugin reported success or a change")
	}
}
