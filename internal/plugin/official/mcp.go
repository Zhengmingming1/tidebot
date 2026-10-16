package official

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Tool is one MCP tool.
type Tool struct {
	Name        string                                    `json:"name"`
	Description string                                    `json:"description"`
	InputSchema map[string]any                            `json:"inputSchema"`
	Call        func(args map[string]any) (string, error) `json:"-"`
}

// ServeMCP runs a minimal MCP server (stdio, newline-delimited JSON-RPC) exposing tools.
func ServeMCP(r io.Reader, w io.Writer, name, version string, tools []Tool) error {
	c := NewConn(w)
	byName := map[string]Tool{}
	for _, t := range tools {
		byName[t.Name] = t
	}
	return c.Serve(r, func(method string, params json.RawMessage) (any, error) {
		switch method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(params, &p)
			if p.ProtocolVersion == "" {
				p.ProtocolVersion = "2024-11-05"
			}
			return map[string]any{"protocolVersion": p.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}},
				"serverInfo": map[string]any{"name": name, "version": version}}, nil
		case "notifications/initialized", "initialized":
			return nil, nil
		case "ping":
			return map[string]any{}, nil
		case "tools/list":
			return map[string]any{"tools": tools}, nil
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, err
			}
			t, ok := byName[p.Name]
			if !ok {
				return nil, fmt.Errorf("unknown tool %q", p.Name)
			}
			if p.Arguments == nil {
				p.Arguments = map[string]any{}
			}
			out, err := t.Call(p.Arguments)
			if err != nil {
				return map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": err.Error()}}}, nil
			}
			return map[string]any{"content": []map[string]string{{"type": "text", "text": out}}}, nil
		}
		return nil, errNoMethod
	})
}

func str(m map[string]any, k string) string {
	if v, ok := m[k]; ok && v != nil {
		switch x := v.(type) {
		case string:
			return x
		case float64:
			if x == float64(int64(x)) {
				return fmt.Sprintf("%d", int64(x))
			}
			return fmt.Sprintf("%g", x)
		case bool:
			return fmt.Sprintf("%t", x)
		}
	}
	return ""
}

func num(m map[string]any, k string, def float64) float64 {
	switch x := m[k].(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case string:
		var f float64
		if _, err := fmt.Sscanf(x, "%g", &f); err == nil {
			return f
		}
	}
	return def
}

func boolean(m map[string]any, k string, def bool) bool {
	if b, ok := m[k].(bool); ok {
		return b
	}
	return def
}

func need(m map[string]any, keys ...string) error {
	for _, k := range keys {
		if str(m, k) == "" {
			return errors.New("missing " + k)
		}
	}
	return nil
}

func schema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}
