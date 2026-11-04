package sessiontools

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Zhengmingming1/tidebot/internal/ipc"
)

const HeadersArg = "__capability-headers"
const HeadersIPCMethod = "capability.headers"

// HeadersMain emits only the current headers to Claude Code. OAuth access and
// refresh tokens are never written to the MCP config, logs or capability locks.
func HeadersMain(args []string) int {
	if len(args) != 2 {
		return 2
	}
	client, err := ipc.DialClient(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "tidebot headers service unavailable")
		return 1
	}
	defer client.Close()
	headers := map[string]string{}
	if err = client.Call(HeadersIPCMethod, map[string]string{"token": args[1]}, &headers); err != nil {
		fmt.Fprintln(os.Stderr, "tidebot capability authentication required")
		return 1
	}
	if json.NewEncoder(os.Stdout).Encode(headers) != nil {
		return 1
	}
	return 0
}
