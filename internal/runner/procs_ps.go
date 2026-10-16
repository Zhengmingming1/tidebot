package runner

import (
	"strconv"
	"strings"
)

// ParsePS parses `ps -axo pid=,ppid=,rss=` output (rss in KiB).
func ParsePS(out string) map[int]ProcInfo {
	tab := map[int]ProcInfo{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		pid, e1 := strconv.Atoi(f[0])
		ppid, e2 := strconv.Atoi(f[1])
		rss, e3 := strconv.ParseInt(f[2], 10, 64)
		if e1 != nil || e2 != nil || e3 != nil {
			continue
		}
		tab[pid] = ProcInfo{PPID: ppid, RSS: rss * 1024}
	}
	return tab
}
