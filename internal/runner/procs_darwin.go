package runner

import (
	"context"
	"os/exec"
	"time"
)

// procTable uses ps(1): no cgo, one exec per refresh (the status card caches it).
func procTable() (map[int]ProcInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/ps", "-axo", "pid=,ppid=,rss=").Output()
	if err != nil {
		return nil, err
	}
	return ParsePS(string(out)), nil
}
