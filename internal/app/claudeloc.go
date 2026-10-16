package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/claudebin"
)

var claudeLoc claudebin.Cache

// claudePathSetting is the path picked in the UI; it overrides config claude_bin.
const claudePathSetting = "claude.path"

func (a *App) configuredClaude(ctx context.Context) string {
	if a.Auth != nil {
		if v, _ := a.settingString(ctx, claudePathSetting); v != "" {
			return v
		}
	}
	return a.Cfg.ClaudeBin
}

// ResolveClaude finds the claude CLI (cached; force re-resolves).
func (a *App) ResolveClaude(ctx context.Context, force bool) claudebin.Result {
	conf := a.configuredClaude(ctx)
	if isMockBin(conf) {
		return claudebin.Result{Path: conf, Method: "configured", Searched: []string{conf}}
	}
	return claudeLoc.Get(ctx, claudebin.Options{Configured: conf}, force)
}

// claudeExec returns the binary and environment to start claude with.
func (a *App) claudeExec(ctx context.Context) (bin string, env []string) {
	r := a.ResolveClaude(ctx, false)
	if r.Path == "" {
		return a.configuredClaude(ctx), nil // let the runner report "not found"
	}
	return r.Path, claudebin.WithPATH(os.Environ(), r.PATH)
}

// rpcSetClaudePath saves (or clears, with "") the claude path chosen in the UI.
func (a *App) rpcSetClaudePath(ctx context.Context, raw json.RawMessage) (any, error) {
	p, _ := decode[struct {
		Path string `json:"path"`
	}](raw)
	path := strings.TrimSpace(p.Path)
	if path != "" {
		if !filepath.IsAbs(path) && !strings.HasPrefix(path, "~/") {
			return nil, errors.New("use an absolute path")
		}
		r := claudebin.Resolve(ctx, claudebin.Options{Configured: path})
		if r.Path == "" {
			return nil, errors.New("that file did not answer `--version`; is it Claude Code?")
		}
	}
	b, _ := json.Marshal(path)
	if err := a.Auth.SetSetting(ctx, claudePathSetting, string(b)); err != nil {
		return nil, err
	}
	claudeLoc.Invalidate()
	return a.ClaudeStatus(ctx, true), nil
}
