package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os/exec"
)

// Self-update lives in internal/updater (signed latest.json on the public
// tidebot-releases repo); see selfupdate.go. The old GitHub-API check against
// the source repo was removed in 0.1.6: that repo is private, so it 404'd.

func execOutput(ctx context.Context, bin string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, bin, args...).Output()
	return string(out), err
}

func execOutputEnv(ctx context.Context, env []string, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	out, err := cmd.Output()
	return string(out), err
}

func randomToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
