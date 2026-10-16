package guard

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/ipc"
)

// HookMain implements `<binary> hook pretool`: read the hook JSON on stdin, ask the daemon
// (which knows approvals and records pending confirmations), fall back to local rules.
// It always exits 0; the decision is in the JSON on stdout.
func HookMain(stdin io.Reader, stdout io.Writer) {
	raw, _ := io.ReadAll(io.LimitReader(stdin, 1<<20))
	var c Call
	_ = json.Unmarshal(raw, &c)
	ws := os.Getenv("TIDEBOT_GUARD_WORKSPACE")
	extra := filepath.SplitList(os.Getenv("TIDEBOT_GUARD_EXTRA_DIRS"))
	v := Check(c, ws, extra)
	if v.Deny {
		if sock := os.Getenv("TIDEBOT_IPC"); sock != "" {
			if cli, err := ipc.DialClient(sock); err == nil {
				var out Verdict
				err := cli.Call("guard.check", map[string]any{
					"run_id": os.Getenv("TIDEBOT_RUN_ID"), "task_id": os.Getenv("TIDEBOT_TASK_ID"),
					"tool": c.Tool, "key": v.Key, "rule": v.RuleID, "reason": v.Reason,
				}, &out)
				cli.Close()
				if err == nil {
					v.Deny = out.Deny
				}
			}
		}
	}
	_, _ = stdout.Write(HookOutput(v))
}

// IsHookInvocation reports whether args are `hook pretool`.
func IsHookInvocation(args []string) bool {
	return len(args) >= 2 && args[0] == "hook" && strings.EqualFold(args[1], "pretool")
}
