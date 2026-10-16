//go:build !windows

package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSetClaudePath(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	bin := filepath.Join(t.TempDir(), "claude")
	os.WriteFile(bin, []byte("#!/bin/sh\necho '2.1.3 (Claude Code)'\n"), 0o755)
	if _, err := a.rpcSetClaudePath(ctx, json.RawMessage(`{"path":"relative/claude"}`)); err == nil {
		t.Fatal("relative path accepted")
	}
	if _, err := a.rpcSetClaudePath(ctx, json.RawMessage(`{"path":"/nonexistent/claude"}`)); err == nil {
		t.Fatal("missing file accepted")
	}
	b, _ := json.Marshal(map[string]string{"path": bin})
	out, err := a.rpcSetClaudePath(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	st := out.(CCStatus)
	if !st.Found || st.Path != bin || st.Method != "configured" || !st.Custom || st.Version != "2.1.3" {
		t.Fatalf("%+v", st)
	}
	if got, _ := a.claudeExec(ctx); got != bin {
		t.Fatalf("runner would use %q", got)
	}
}
