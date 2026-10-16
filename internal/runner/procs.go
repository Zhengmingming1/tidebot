package runner

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Proc is one claude child process started by this core.
type Proc struct {
	PID      int       `json:"pid"`
	RunID    string    `json:"run_id"`
	Started  time.Time `json:"started_at"`
	RSSBytes int64     `json:"rss_bytes"` // 0 where we can't read it cheaply
}

var procs = struct {
	sync.Mutex
	m map[int]Proc
}{m: map[int]Proc{}}

func trackProc(pid int, runID string) func() {
	procs.Lock()
	procs.m[pid] = Proc{PID: pid, RunID: runID, Started: time.Now()}
	procs.Unlock()
	return func() {
		procs.Lock()
		delete(procs.m, pid)
		procs.Unlock()
	}
}

// ActiveProcs lists live claude processes (oldest first) with their resident memory.
func ActiveProcs() []Proc {
	procs.Lock()
	out := make([]Proc, 0, len(procs.m))
	for _, p := range procs.m {
		out = append(out, p)
	}
	procs.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	for i := range out {
		out[i].RSSBytes = procRSS(out[i].PID)
	}
	return out
}

// procRSS is the resident memory of pid plus all its descendants (claude runs
// node, which may spawn helpers). 0 when the OS won't tell us.
func procRSS(pid int) int64 {
	tab, err := procTable()
	if err != nil {
		return 0
	}
	return TreeRSS(pid, tab)
}

// ProcInfo is one row of the OS process table.
type ProcInfo struct {
	PPID int
	RSS  int64 // bytes
}

// TreeRSS sums RSS for root and every descendant in tab.
func TreeRSS(root int, tab map[int]ProcInfo) int64 {
	if _, ok := tab[root]; !ok {
		return 0
	}
	kids := map[int][]int{}
	for pid, p := range tab {
		if pid != root {
			kids[p.PPID] = append(kids[p.PPID], pid)
		}
	}
	var total int64
	seen := map[int]bool{}
	stack := []int{root}
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		total += tab[pid].RSS
		stack = append(stack, kids[pid]...)
	}
	return total
}

// ParseVmRSS extracts "VmRSS:  12345 kB" from a /proc/<pid>/status body, in bytes.
func ParseVmRSS(status string) int64 {
	for _, line := range strings.Split(status, "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				n, _ := strconv.ParseInt(f[1], 10, 64)
				return n * 1024
			}
		}
	}
	return 0
}
