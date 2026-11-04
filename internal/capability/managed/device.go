package managed

import (
	"bufio"
	"context"
	"errors"
	"io"
	"path/filepath"
	"time"
)

// RunDeviceLogin runs an explicitly owner-approved fixed CLI device flow. It
// never retains raw output; the caller extracts only safe verification fields.
// The command is isolated in a killable process tree and output is bounded.
func RunDeviceLogin(ctx context.Context, c Command, line func(string)) error {
	if !filepath.IsAbs(c.Path) {
		return errors.New("device login requires resolved command")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd, err := executableCommand(ctx, c.Path, c.Args)
	if err != nil {
		return err
	}
	cmd.Env = c.Env
	cmd.Dir = c.Dir
	cmd.Stdin = nil
	cmd.WaitDelay = 250 * time.Millisecond
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// Some CLIs print verification instructions on stderr. Merge both into a
	// single bounded stream; never log or return arbitrary CLI output.
	cmd.Stderr = cmd.Stdout
	after, cleanup, err := managedProcess(cmd)
	if err != nil {
		return err
	}
	defer cleanup()
	if err = cmd.Start(); err != nil {
		return errors.New("device login could not start")
	}
	if err = after(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return errors.New("device process ownership failed")
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = output.Close()
		case <-done:
		}
	}()
	scanner := bufio.NewScanner(io.LimitReader(output, 64<<10))
	scanner.Buffer(make([]byte, 4096), 16<<10)
	bytes := 0
	for scanner.Scan() {
		bytes += len(scanner.Bytes()) + 1
		if bytes >= 64<<10 {
			cancel()
			break
		}
		line(scanner.Text())
	}
	if scanner.Err() != nil || bytes >= 64<<10 {
		cancel()
		_ = cmd.Wait()
		return errors.New("device login output exceeded limits")
	}
	if err = cmd.Wait(); err != nil {
		return errors.New("device login failed or cancelled")
	}
	return ctx.Err()
}
