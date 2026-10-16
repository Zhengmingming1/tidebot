package runner

import (
	"os"
	"strconv"
	"strings"
)

func procTable() (map[int]ProcInfo, error) {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	tab := map[int]ProcInfo{}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/status")
		if err != nil {
			continue
		}
		st := string(b)
		tab[pid] = ProcInfo{PPID: parsePPid(st), RSS: ParseVmRSS(st)}
	}
	return tab, nil
}

func parsePPid(status string) int {
	for _, line := range strings.Split(status, "\n") {
		if strings.HasPrefix(line, "PPid:") {
			n, _ := strconv.Atoi(strings.TrimSpace(line[5:]))
			return n
		}
	}
	return 0
}
