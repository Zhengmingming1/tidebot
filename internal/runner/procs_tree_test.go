package runner

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestTreeRSSAndParsePS(t *testing.T) {
	tab := ParsePS("  10 1 100\n 11 10 50\n 12 11 25\n 13 1 999\n bad line\n")
	if got := TreeRSS(10, tab); got != 175*1024 {
		t.Fatalf("tree rss %d", got)
	}
	if TreeRSS(42, tab) != 0 {
		t.Fatal("missing pid")
	}
	tab[10] = ProcInfo{PPID: 12, RSS: 1} // cycle must not loop
	_ = TreeRSS(10, tab)
}

// Live check on every OS CI runs: a tracked child must report nonzero memory.
func TestProcRSSLiveChild(t *testing.T) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-NoProfile", "-Command", "Start-Sleep -Seconds 20")
	} else {
		cmd = exec.Command("sleep", "20")
	}
	if err := cmd.Start(); err != nil {
		t.Skip("cannot start child:", err)
	}
	defer cmd.Process.Kill()
	done := trackProc(cmd.Process.Pid, "r1")
	defer done()
	var rss int64
	for i := 0; i < 20 && rss == 0; i++ {
		rss = procRSS(cmd.Process.Pid)
		time.Sleep(100 * time.Millisecond)
	}
	if rss <= 0 {
		t.Fatalf("rss for pid %d on %s = %d", cmd.Process.Pid, runtime.GOOS, rss)
	}
	// Our own process (parent of the child) includes the child in its tree.
	if self := procRSS(os.Getpid()); self < rss {
		t.Fatalf("self tree %d < child %d", self, rss)
	}
}
