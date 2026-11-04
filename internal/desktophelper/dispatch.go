package desktophelper

import (
	"os"

	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/plugin"
	"github.com/Zhengmingming1/tidebot/internal/plugin/official"
	"github.com/Zhengmingming1/tidebot/internal/sessiontools"
)

// Dispatch handles every subprocess mode used by HookExe and BuiltinExe before
// any GUI, flags, database, or single-instance setup. All executable entry
// points share this list so session MCP/header workers cannot launch a window.
// Unknown commands return handled=false for the caller's normal entry point.
func Dispatch(args []string) (code int, handled bool) {
	if guard.IsHookInvocation(args) {
		guard.HookMain(os.Stdin, os.Stdout)
		return 0, true
	}
	if len(args) == 0 {
		return 0, false
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
