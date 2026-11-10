package desktophelper

import (
	"os"

	"github.com/Zhengmingming1/tidebot/internal/plugin"
	"github.com/Zhengmingming1/tidebot/internal/plugin/official"
	"github.com/Zhengmingming1/tidebot/internal/sessiontools"
)

// Dispatch handles every subprocess mode used by BuiltinExe and session tools before
// any GUI, flags, database, or single-instance setup. All executable entry
// points share this list so session MCP/header workers cannot launch a window.
// Unknown commands return handled=false for the caller's normal entry point.
func Dispatch(args []string) (code int, handled bool) {
	if len(args) == 0 {
		return 0, false
	}
	if len(args) >= 2 && args[0] == "hook" {
		// `hook pretool` from a stale hook config of an older version (hooks never
		// ran under --bare, and the config isn't written any more): allow, and never
		// open a window for it.
		_, _ = os.Stdout.Write([]byte("{}"))
		return 0, true
	}
	switch args[0] {
	case plugin.BuiltinArg:
		return official.Main(args[1:]), true
	case sessiontools.HeadersArg:
		return sessiontools.HeadersMain(args[1:]), true
	case sessiontools.Arg:
		return sessiontools.Main(args[1:]), true
	default:
		return 0, false
	}
}
