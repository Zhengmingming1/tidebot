package runner

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetProcessMemoryInfo = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

// processMemoryCounters mirrors PROCESS_MEMORY_COUNTERS.
type processMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

func workingSet(pid uint32) int64 {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(h)
	var c processMemoryCounters
	c.CB = uint32(unsafe.Sizeof(c))
	r, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(c.CB))
	if r == 0 {
		return 0
	}
	return int64(c.WorkingSetSize)
}

// procTable walks a Toolhelp snapshot for parent links; RSS (working set) is
// only queried for processes we end up summing, via procRSS → TreeRSS.
func procTable() (map[int]ProcInfo, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	parents := map[int]int{}
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		parents[int(e.ProcessID)] = int(e.ParentProcessID)
	}
	tab := map[int]ProcInfo{}
	for pid, pp := range parents {
		tab[pid] = ProcInfo{PPID: pp}
	}
	// Fill RSS lazily for the subtrees we track (cheap: few processes).
	for pid := range tab {
		if isTrackedTree(pid, parents) {
			p := tab[pid]
			p.RSS = workingSet(uint32(pid))
			tab[pid] = p
		}
	}
	return tab, nil
}

func isTrackedTree(pid int, parents map[int]int) bool {
	procs.Lock()
	defer procs.Unlock()
	for i := 0; i < 64 && pid > 0; i++ {
		if _, ok := procs.m[pid]; ok {
			return true
		}
		next, ok := parents[pid]
		if !ok || next == pid {
			return false
		}
		pid = next
	}
	return false
}
