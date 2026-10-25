package power

import (
	"context"
	"os/exec"
	"time"
)

// pmset lives in /usr/bin on every macOS; the full path avoids depending on PATH,
// which is minimal when the desktop app is started from Finder.
const pmset = "/usr/bin/pmset"

func read() State {
	st := ParsePmsetBatt(run(pmset, "-g", "batt"))
	if st.OnBattery { // Low Power Mode only matters while unplugged
		st.Saver = ParsePmsetLowPower(run(pmset, "-g"))
	}
	return st
}

func run(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}
