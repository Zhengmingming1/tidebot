package adapters

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// StdioLaunch must describe an already installed, integrity-verified executable.
// Validation executes that program; callers must obtain execution approval first.
// Env is explicit and is not merged with the host environment or secret store.
type StdioLaunch struct {
	Command string
	Args    []string
	Env     []string
	Dir     string
	Timeout time.Duration
}
type MCPToolAnnotations struct {
	ReadOnlyHint *bool `json:"readOnlyHint,omitempty"`
}
type MCPTool struct {
	Annotations *MCPToolAnnotations `json:"annotations,omitempty"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	InputSchema json.RawMessage     `json:"inputSchema"`
}

var toolName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// ValidateStdio performs initialize and tools/list only. It never calls tools,
// follows sampling requests, accepts server prompts or executes install commands.
// Reads, messages, tool count, time and process shutdown are all bounded.
func ValidateStdio(ctx context.Context, launch StdioLaunch) ([]MCPTool, error) {
	if !filepath.IsAbs(launch.Command) || len(launch.Args) > 128 || len(launch.Env) > 128 {
		return nil, errors.New("validation requires an installed absolute executable")
	}
	for _, arg := range append(append([]string{}, launch.Args...), launch.Env...) {
		if len(arg) > 8192 || strings.ContainsRune(arg, 0) {
			return nil, errors.New("invalid validation launch")
		}
	}
	timeout := launch.Timeout
	if timeout <= 0 || timeout > 15*time.Second {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, launch.Command, launch.Args...)
	cmd.Env = append([]string{}, launch.Env...)
	cmd.Dir = launch.Dir
	cmd.WaitDelay = 250 * time.Millisecond
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	afterStart, cleanup, err := validationProcess(cmd)
	if err != nil {
		return nil, errors.New("MCP process ownership unavailable")
	}
	defer cleanup()
	if err := cmd.Start(); err != nil {
		return nil, errors.New("MCP validation could not start executable")
	}
	if err := afterStart(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errors.New("MCP process ownership failed")
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = stdout.Close()
		case <-done:
		}
	}()
	defer close(done)
	defer func() { stdin.Close(); cancel(); _ = cmd.Wait() }()
	scanner := bufio.NewScanner(io.LimitReader(stdout, 2<<20))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	send := func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = stdin.Write(append(b, '\n'))
		return err
	}
	receive := func(id int) (json.RawMessage, error) {
		for count := 0; count < 64; count++ {
			if !scanner.Scan() {
				if ctx.Err() != nil {
					return nil, errors.New("MCP validation timed out or was cancelled")
				}
				return nil, errors.New("MCP validation response missing or too large")
			}
			var msg struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      json.RawMessage `json:"id"`
				Method  string          `json:"method"`
				Result  json.RawMessage `json:"result"`
				Error   json.RawMessage `json:"error"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil || msg.JSONRPC != "2.0" {
				return nil, errors.New("invalid MCP JSON-RPC response")
			}
			if msg.Method != "" {
				if len(msg.ID) > 0 {
					return nil, errors.New("MCP server requests are not supported during validation")
				}
				continue
			}
			if string(msg.ID) != fmt.Sprint(id) {
				return nil, errors.New("unexpected MCP response ID")
			}
			if len(msg.Error) > 0 && string(msg.Error) != "null" {
				return nil, errors.New("MCP server rejected validation request")
			}
			if len(msg.Result) == 0 {
				return nil, errors.New("MCP response result missing")
			}
			return msg.Result, nil
		}
		return nil, errors.New("MCP validation message limit exceeded")
	}
	if err = send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "tidebot-validation", "version": "1"}}}); err != nil {
		return nil, errors.New("MCP initialize write failed")
	}
	result, err := receive(1)
	if err != nil {
		return nil, err
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools json.RawMessage `json:"tools"`
		} `json:"capabilities"`
	}
	if err = json.Unmarshal(result, &init); err != nil || !containsVersion(init.ProtocolVersion) || len(init.Capabilities.Tools) == 0 {
		return nil, errors.New("MCP server does not support tool discovery")
	}
	if err = send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		return nil, errors.New("MCP notification write failed")
	}
	if err = send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{}}); err != nil {
		return nil, errors.New("MCP tools/list write failed")
	}
	result, err = receive(2)
	if err != nil {
		return nil, err
	}
	return parseToolList(result)
}

func parseToolList(result json.RawMessage) ([]MCPTool, error) {
	var response struct {
		Tools      []MCPTool `json:"tools"`
		NextCursor string    `json:"nextCursor"`
	}
	if err := json.Unmarshal(result, &response); err != nil || len(response.Tools) > 256 || response.NextCursor != "" {
		return nil, errors.New("MCP tool inventory exceeds validation limits")
	}
	seen := map[string]bool{}
	for _, tool := range response.Tools {
		if !toolName.MatchString(tool.Name) || seen[tool.Name] || len(tool.Description) > 16384 || len(tool.InputSchema) > 65536 {
			return nil, errors.New("invalid MCP tool metadata")
		}
		seen[tool.Name] = true
		var schema struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(tool.InputSchema, &schema) != nil || schema.Type != "object" {
			return nil, errors.New("MCP tool schema must be an object")
		}
	}
	return response.Tools, nil
}
func containsVersion(version string) bool {
	return version == "2024-11-05" || version == "2025-03-26" || version == "2025-06-18" || version == "2025-11-25"
}
