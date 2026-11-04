// Command tidebot-helper runs desktop subprocess work without linking a GUI.
// The macOS bundle uses it for guard, plugin, session MCP, and capability-header
// workers so those processes cannot register another app or bounce a Dock icon.
package main

import (
	"fmt"
	"os"

	"github.com/Zhengmingming1/tidebot/internal/desktophelper"
)

func main() {
	if code, handled := desktophelper.Dispatch(os.Args[1:]); handled {
		os.Exit(code)
	}
	fmt.Fprintln(os.Stderr, "tidebot-helper only supports internal worker commands")
	os.Exit(2)
}
