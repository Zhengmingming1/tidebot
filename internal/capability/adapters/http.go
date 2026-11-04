package adapters

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability/oauth"
)

type HTTPLaunch struct {
	URL     string
	Headers map[string]string
	Client  *oauth.Client
}

// ValidateHTTP performs bounded Streamable HTTP discovery only. Legacy SSE
// endpoint negotiation is intentionally unsupported; no tool calls are sent.
func ValidateHTTP(ctx context.Context, launch HTTPLaunch) ([]MCPTool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client := launch.Client
	if client == nil {
		client = oauth.NewClient(nil, nil)
	}
	session := ""
	protocol := "2025-06-18"
	request := func(id int, method string, params any) (json.RawMessage, error) {
		message := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
		if id != 0 {
			message["id"] = id
		}
		b, _ := json.Marshal(message)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, launch.URL, bytes.NewReader(b))
		if err != nil {
			return nil, errors.New("invalid MCP endpoint")
		}
		for key, value := range launch.Headers {
			if strings.ContainsAny(value, "\r\n") || len(value) > 16384 {
				return nil, errors.New("invalid MCP header")
			}
			req.Header.Set(key, value)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if id != 1 {
			req.Header.Set("MCP-Protocol-Version", protocol)
		}
		if session != "" {
			req.Header.Set("Mcp-Session-Id", session)
		}
		resp, err := client.DoResourceRequest(req)
		if err != nil {
			return nil, errors.New("MCP discovery request failed")
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("MCP discovery HTTP status %d", resp.StatusCode)
		}
		if value := resp.Header.Get("Mcp-Session-Id"); value != "" {
			if len(value) > 512 || strings.ContainsAny(value, "\r\n") {
				return nil, errors.New("invalid MCP session")
			}
			session = value
		}
		if id == 0 {
			return nil, nil
		}
		reader := io.LimitReader(resp.Body, (2<<20)+1)
		var data []byte
		if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			scanner := bufio.NewScanner(reader)
			scanner.Buffer(make([]byte, 4096), 1<<20)
			for count := 0; scanner.Scan() && count < 128; count++ {
				line := scanner.Text()
				if strings.HasPrefix(line, "data:") {
					data = append(data, []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))...)
					data = append(data, '\n')
				}
				if line == "" && len(data) > 0 {
					break
				}
			}
		} else {
			data, err = io.ReadAll(reader)
			if err != nil {
				return nil, errors.New("MCP response read failed")
			}
		}
		if len(data) > 2<<20 {
			return nil, errors.New("MCP response exceeds limit")
		}
		var response struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int             `json:"id"`
			Result  json.RawMessage `json:"result"`
			Error   json.RawMessage `json:"error"`
			Method  string          `json:"method"`
		}
		if json.Unmarshal(data, &response) != nil || response.JSONRPC != "2.0" || response.ID != id || response.Method != "" || len(response.Result) == 0 || len(response.Error) > 0 && string(response.Error) != "null" {
			return nil, errors.New("invalid MCP discovery response")
		}
		return response.Result, nil
	}
	initialized, err := request(1, "initialize", map[string]any{"protocolVersion": protocol, "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "tidebot-validation", "version": "1"}})
	if err != nil {
		return nil, err
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools json.RawMessage `json:"tools"`
		} `json:"capabilities"`
	}
	if json.Unmarshal(initialized, &init) != nil || !containsVersion(init.ProtocolVersion) || len(init.Capabilities.Tools) == 0 {
		return nil, errors.New("MCP server does not support tool discovery")
	}
	protocol = init.ProtocolVersion
	if _, err = request(0, "notifications/initialized", map[string]any{}); err != nil {
		return nil, err
	}
	result, err := request(2, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	return parseToolList(result)
}
