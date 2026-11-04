package adapters

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStdioFixture(t *testing.T) {
	if os.Getenv("TIDEBOT_MCP_FIXTURE") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, `"method":"initialize"`) {
			fmt.Println(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}}}}`)
		} else if strings.Contains(line, `"method":"tools/list"`) {
			if os.Getenv("TIDEBOT_MCP_BAD") == "1" {
				fmt.Println(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"bad","inputSchema":{"type":"string"}}]}}`)
			} else {
				fmt.Println(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"read","description":"Read only test fixture","inputSchema":{"type":"object"}}]}}`)
			}
		}
	}
	os.Exit(0)
}
func TestValidateStdio(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	launch := StdioLaunch{Command: executable, Args: []string{"-test.run=^TestStdioFixture$"}, Env: []string{"TIDEBOT_MCP_FIXTURE=1"}, Timeout: time.Second}
	tools, err := ValidateStdio(context.Background(), launch)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "read" {
		t.Fatal("invalid tools")
	}
	launch.Env = append(launch.Env, "TIDEBOT_MCP_BAD=1")
	if _, err := ValidateStdio(context.Background(), launch); err == nil {
		t.Fatal("bad schema accepted")
	}
	launch.Command = "npx"
	if _, err := ValidateStdio(context.Background(), launch); err == nil {
		t.Fatal("unresolved command accepted")
	}
}
