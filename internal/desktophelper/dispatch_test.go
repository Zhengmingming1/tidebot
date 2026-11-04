package desktophelper

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/plugin"
	"github.com/Zhengmingming1/tidebot/internal/sessiontools"
)

func TestDispatchProcess(t *testing.T) {
	if os.Getenv("TIDEBOT_TEST_WORKER") != "1" {
		return
	}
	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	code, handled := Dispatch(args)
	if !handled {
		os.Exit(99)
	}
	os.Exit(code)
}

func TestWorkerProtocols(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		input string
		code  int
		json  bool
	}{
		{"guard", []string{"hook", "pretool"}, `{}`, 0, true},
		{"session MCP", []string{sessiontools.Arg}, "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{}}\n", 0, true},
		{"capability headers", []string{sessiontools.HeadersArg}, "", 2, false},
		{"builtin", []string{plugin.BuiltinArg}, "", 2, false},
		{"unknown", []string{"not-a-worker"}, "", 99, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestDispatchProcess$", "--"}, tc.args...)...)
			cmd.Env = append(os.Environ(), "TIDEBOT_TEST_WORKER=1", sessiontools.EnvIPC+"=", sessiontools.EnvToken+"=", "TIDEBOT_IPC=")
			cmd.Stdin = strings.NewReader(tc.input)
			out, err := cmd.Output()
			code := 0
			if err != nil {
				if e, ok := err.(*exec.ExitError); ok {
					code = e.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			if code != tc.code {
				t.Fatalf("code=%d want=%d output=%s error=%v", code, tc.code, out, err)
			}
			if tc.json && !json.Valid(out) {
				t.Fatalf("worker did not return JSON: %q", out)
			}
			if tc.name == "session MCP" && !strings.Contains(string(out), `"serverInfo"`) {
				t.Fatalf("session MCP handshake missing: %s", out)
			}
		})
	}
}
