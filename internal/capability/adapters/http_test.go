package adapters

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/capability/oauth"
)

type discoveryTransport func(*http.Request) (*http.Response, error)

func (f discoveryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestValidateHTTPOnlyDiscovery(t *testing.T) {
	for _, sse := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sse"}[sse], func(t *testing.T) {
			methods := []string{}
			client := oauth.NewClient(&http.Client{Transport: discoveryTransport(func(req *http.Request) (*http.Response, error) {
				var message struct {
					Method string `json:"method"`
				}
				if err := json.NewDecoder(req.Body).Decode(&message); err != nil {
					t.Fatal(err)
				}
				methods = append(methods, message.Method)
				if req.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Fatal("credential omitted")
				}
				body := ""
				status := 200
				switch message.Method {
				case "initialize":
					body = `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}}}}`
				case "notifications/initialized":
					status = 202
				case "tools/list":
					body = `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"read","inputSchema":{"type":"object"}}]}}`
				default:
					t.Fatalf("unexpected method %s", message.Method)
				}
				if message.Method != "initialize" && req.Header.Get("Mcp-Session-Id") != "fixture-session" {
					t.Fatal("session not propagated")
				}
				contentType := "application/json"
				if sse && body != "" {
					body = "data: " + body + "\n\n"
					contentType = "text/event-stream"
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}, "Mcp-Session-Id": {"fixture-session"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			})}, nil)
			client.LookupIP = func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
			}
			tools, err := ValidateHTTP(context.Background(), HTTPLaunch{URL: "https://mcp.example.com/mcp", Headers: map[string]string{"Authorization": "Bearer fixture-token"}, Client: client})
			if err != nil {
				t.Fatal(err)
			}
			if len(tools) != 1 || tools[0].Name != "read" {
				t.Fatal("invalid inventory")
			}
			if !reflect.DeepEqual(methods, []string{"initialize", "notifications/initialized", "tools/list"}) {
				t.Fatal(methods)
			}
		})
	}
}
func TestValidateHTTPRejectsPrivateEndpoint(t *testing.T) {
	if _, err := ValidateHTTP(context.Background(), HTTPLaunch{URL: "https://127.0.0.1/mcp"}); err == nil {
		t.Fatal("private endpoint accepted")
	}
}
