//go:build !darwin && !windows

package netproxy

import (
	"context"
	"os/exec"
	"strings"
)

// Linux / BSD: env is handled by the caller; here we read GNOME's settings when present.
func lookupSystem(ctx context.Context) *System {
	if _, err := exec.LookPath("gsettings"); err != nil {
		return nil
	}
	return ParseGsettings(func(schema, key string) string { return strings.TrimSpace(run(ctx, "gsettings", "get", schema, key)) })
}
