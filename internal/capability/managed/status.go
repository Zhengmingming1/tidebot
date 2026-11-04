package managed

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"time"
)

// ReadStatus runs a fixed, caller-reviewed status command without stdin or a
// terminal. Only bounded stdout is returned in memory; stderr is discarded.
// Callers must reduce the output to a safe schema, never log or return it.
func ReadStatus(ctx context.Context, c Command) ([]byte, error) {
	if !filepath.IsAbs(c.Path) {
		return nil, errors.New("status requires resolved executable")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Path, c.Args...)
	cmd.Env, cmd.Dir = c.Env, c.Dir
	cmd.WaitDelay = 250 * time.Millisecond
	out := &statusOutput{}
	cmd.Stdout = out
	after, cleanup, err := managedProcess(cmd)
	if err != nil {
		return nil, errors.New("status process ownership unavailable")
	}
	defer cleanup()
	if err = cmd.Start(); err != nil {
		return nil, errors.New("status could not start")
	}
	if err = after(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errors.New("status process ownership failed")
	}
	if err = cmd.Wait(); err != nil || ctx.Err() != nil || out.exceeded {
		return nil, errors.New("status failed, exceeded limits, or timed out")
	}
	return out.data, nil
}

type statusOutput struct {
	data     []byte
	exceeded bool
}

func (b *statusOutput) Write(p []byte) (int, error) {
	n := len(p)
	if len(b.data)+n > 64<<10 {
		b.exceeded = true
		return n, nil
	}
	if !b.exceeded {
		b.data = append(b.data, p...)
	}
	return n, nil
}
