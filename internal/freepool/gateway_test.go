package freepool

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const nativeRequest = `{"model":"kilo-auto/free","max_tokens":128,"messages":[{"role":"user","content":"Return OK using the tool."}],"tools":[{"name":"return_ok","input_schema":{"type":"object","properties":{}}}]}`
const nativeResponse = `{"id":"msg_fixture","type":"message","role":"assistant","model":"kilo-auto/free","content":[{"type":"tool_use","id":"call_1","name":"return_ok","input":{}}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":4}}`

func modelFixture(id string) map[string]any {
	return map[string]any{
		"id": id, "name": "Auto Free", "isFree": true, "mayTrainOnYourPrompts": true,
		"pricing":              map[string]any{"prompt": "0", "completion": "0", "request": "0", "image": "0", "input_cache_read": "0"},
		"supported_parameters": []string{"tools", "max_tokens"}, "context_length": 256000,
		"architecture": map[string]any{"input_modalities": []string{"text"}, "output_modalities": []string{"text"}},
		"top_provider": map[string]any{"max_completion_tokens": 32768},
	}
}
func catalogFixture(models ...map[string]any) []byte {
	b, _ := json.Marshal(map[string]any{"data": models})
	return b
}
func serveCatalog(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(catalogFixture(modelFixture(DefaultModel)))
}
func newTestGateway(t *testing.T, h http.HandlerFunc, check func(context.Context) (string, error), start bool) *Gateway {
	t.Helper()
	up := httptest.NewServer(h)
	t.Cleanup(up.Close)
	g := New(check)
	g.client = up.Client()
	g.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	g.upstream = up.URL
	if start {
		if err := g.Start(); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := g.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return g
}
func allow(context.Context) (string, error) { return DefaultModel, nil }
func localRequest(t *testing.T, g *Gateway, path, body string) *http.Request {
	t.Helper()
	method := http.MethodPost
	if path == "/v1/models" {
		method = http.MethodGet
	}
	r, err := http.NewRequest(method, g.URL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("X-Api-Key", g.Token())
	r.Header.Set("Content-Type", "application/json")
	return r
}
func doRequest(t *testing.T, r *http.Request) (*http.Response, string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, string(body)
}
func TestCatalogUsesActualFreePriceAndCapabilities(t *testing.T) {
	good := modelFixture(DefaultModel)
	paid := modelFixture("paid")
	paid["isFree"] = false
	priced := modelFixture("priced")
	priced["pricing"].(map[string]any)["request"] = "0.01"
	underflow := modelFixture("tiny-price")
	underflow["pricing"].(map[string]any)["completion"] = "1e-9999"
	noPrice := modelFixture("no-price")
	delete(noPrice["pricing"].(map[string]any), "completion")
	noTools := modelFixture("no-tools")
	noTools["supported_parameters"] = []string{"max_tokens"}
	unknownPolicy := modelFixture("unknown-policy")
	delete(unknownPolicy, "mayTrainOnYourPrompts")
	models, err := parseModels(catalogFixture(paid, priced, noPrice, underflow, noTools, unknownPolicy, good))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != DefaultModel || models[1].ID != "unknown-policy" {
		t.Fatalf("unexpected allowed models: %+v", models)
	}
	if !models[0].MayTrainOnPrompts || !models[0].DataPolicyKnown || !models[1].MayTrainOnPrompts || models[1].DataPolicyKnown {
		t.Fatalf("training policy was not preserved conservatively: %+v", models)
	}
	if models[0].MaxOutputTokens != 32768 || models[0].ContextLength != 256000 {
		t.Fatalf("real catalog schema not parsed: %+v", models[0])
	}
	for _, price := range []string{`"0"`, `0`, `"0.00e-20"`, `"-0.0"`} {
		if !zeroPricing(map[string]json.RawMessage{"prompt": json.RawMessage(price), "completion": json.RawMessage(`"0"`)}) {
			t.Errorf("zero price rejected: %s", price)
		}
	}
	for _, price := range []string{`null`, `false`, `"NaN"`, `"0/1"`, `"1e-9999"`, `"-0.01"`, `""`} {
		if zeroPricing(map[string]json.RawMessage{"prompt": json.RawMessage(price), "completion": json.RawMessage(`"0"`)}) {
			t.Errorf("unknown/nonzero price allowed: %s", price)
		}
	}
	if _, err := parseModels(catalogFixture(good, good)); err == nil {
		t.Fatal("duplicate model identifiers must fail closed")
	}
}
func TestCatalogCacheRefreshFailureAndPriceChangeFailClosed(t *testing.T) {
	var calls, version atomic.Int32
	g := newTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch version.Load() {
		case 1:
			w.WriteHeader(503)
		case 2:
			m := modelFixture(DefaultModel)
			m["pricing"].(map[string]any)["prompt"] = "0.1"
			_, _ = w.Write(catalogFixture(m))
		default:
			serveCatalog(w)
		}
	}, nil, false)
	models, err := g.Models(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	models[0].InputModalities[0] = "changed"
	cached, err := g.Models(context.Background(), false)
	if err != nil || calls.Load() != 1 || cached[0].InputModalities[0] != "text" {
		t.Fatalf("cache is not independent: calls=%d models=%+v err=%v", calls.Load(), cached, err)
	}
	version.Store(1)
	if _, err := g.Models(context.Background(), true); err == nil {
		t.Fatal("forced failed refresh unexpectedly succeeded")
	}
	if _, err := g.Models(context.Background(), false); err == nil || calls.Load() != 3 {
		t.Fatal("failed refresh reused previously free models")
	}
	version.Store(0)
	if _, err := g.Models(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	version.Store(2)
	g.catalogExpires = time.Now().Add(-time.Second)
	if _, err := g.Models(context.Background(), false); err == nil {
		t.Fatal("price change did not close access after expiration")
	}
	if err := g.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Models(context.Background(), false); err == nil {
		t.Fatal("closed gateway must reject catalog requests")
	}
	if err := g.Start(); err == nil {
		t.Fatal("closed gateway must not be restarted")
	}
}
func TestCatalogConcurrentRefreshAndCancellation(t *testing.T) {
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	g := newTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			serveCatalog(w)
		case <-r.Context().Done():
		}
	}, nil, false)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := g.Models(context.Background(), false); err != nil {
				t.Error(err)
			}
		}()
	}
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.Models(ctx, false); err == nil {
		t.Error("canceled waiter succeeded")
	}
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("concurrent requests fetched catalog %d times", calls.Load())
	}
}
func TestNativeMessagesAndCredentialIsolation(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "global-provider-secret")
	t.Setenv("OPENAI_API_KEY", "global-other-secret")
	var received http.Header
	var upstreamBody string
	g := newTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
		for _, h := range []string{"Authorization", "X-Api-Key", "Cookie", "Proxy-Authorization", "X-Forwarded-For", "X-Kilo-Session-Id"} {
			if r.Header.Get(h) != "" {
				t.Errorf("private header %s reached upstream", h)
			}
		}
		if r.URL.Path == "/models" {
			serveCatalog(w)
			return
		}
		if r.URL.Path != "/messages" {
			t.Errorf("wrong native endpoint %s", r.URL.Path)
		}
		received = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		upstreamBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, nativeResponse)
	}, allow, true)
	r := localRequest(t, g, "/v1/messages", nativeRequest)
	r.Header.Set("Authorization", "Bearer "+g.Token())
	r.Header.Set("Cookie", "private=value")
	r.Header.Set("Proxy-Authorization", "Basic private")
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	r.Header.Set("X-Kilo-Session-Id", "spoofed-session")
	r.Header.Set("Anthropic-Beta", "fine-grained-tool-streaming-2025-05-14")
	res, body := doRequest(t, r)
	if res.StatusCode != 200 || body != nativeResponse {
		t.Fatalf("native tool response changed: status=%d body=%s", res.StatusCode, body)
	}
	if received.Get("Anthropic-Version") != "2023-06-01" || received.Get("Anthropic-Beta") != r.Header.Get("Anthropic-Beta") {
		t.Fatalf("native Anthropic headers lost: %+v", received)
	}
	var decoded map[string]json.RawMessage
	if json.Unmarshal([]byte(upstreamBody), &decoded) != nil || len(decoded["tools"]) == 0 {
		t.Fatal("native tools were lost")
	}
	res, body = doRequest(t, localRequest(t, g, "/v1/models", ""))
	if res.StatusCode != 200 || !strings.Contains(body, `"display_name":"Auto Free"`) || !strings.Contains(body, `"has_more":false`) {
		t.Fatalf("unexpected Anthropic model listing: %d %s", res.StatusCode, body)
	}
}
func TestLocalAuthenticationAndApproval(t *testing.T) {
	var calls atomic.Int32
	g := newTestGateway(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); serveCatalog(w) }, allow, true)
	for _, tc := range []struct {
		name   string
		mutate func(*http.Request)
		want   int
	}{
		{"missing-token", func(r *http.Request) { r.Header.Del("X-Api-Key") }, 401},
		{"invalid-token", func(r *http.Request) { r.Header.Set("X-Api-Key", "wrong") }, 401},
		{"mismatched-tokens", func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }, 401},
		{"remote-host", func(r *http.Request) { r.Host = "malicious.example" }, 403},
		{"browser-origin", func(r *http.Request) { r.Header.Set("Origin", "https://malicious.example") }, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := localRequest(t, g, "/v1/messages", nativeRequest)
			tc.mutate(r)
			res, _ := doRequest(t, r)
			if res.StatusCode != tc.want {
				t.Fatalf("status=%d want=%d", res.StatusCode, tc.want)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("unauthorized request contacted upstream")
	}
	denied := newTestGateway(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unapproved upstream access") }, func(context.Context) (string, error) { return "", errors.New("secret permission details") }, true)
	res, body := doRequest(t, localRequest(t, denied, "/v1/messages", nativeRequest))
	if res.StatusCode != 403 || strings.Contains(body, "secret") {
		t.Fatalf("permission check bypassed or leaked details: %d %s", res.StatusCode, body)
	}
}
func TestValidationLimitsAndUnsupportedEndpoints(t *testing.T) {
	var messageCalls atomic.Int32
	g := newTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			messageCalls.Add(1)
		}
		serveCatalog(w)
	}, allow, true)
	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{"/v1/messages", strings.Replace(nativeRequest, DefaultModel, "paid/model", 1), 400},
		{"/v1/messages", `{"model":"kilo-auto/free","max_tokens":1,"messages":null}`, 400},
		{"/v1/messages", strings.Repeat("x", MaxRequestBytes+1), 413},
		{"/v1/messages/count_tokens", nativeRequest, 404},
		{"/v1/chat/completions", nativeRequest, 404},
	} {
		res, body := doRequest(t, localRequest(t, g, tc.path, tc.body))
		if res.StatusCode != tc.status {
			t.Errorf("%s status=%d want=%d body=%s", tc.path, res.StatusCode, tc.status, body)
		}
	}
	if messageCalls.Load() != 0 {
		t.Fatal("invalid request was forwarded")
	}
}
func TestRateLimitsAndRedirectsDoNotRetryOrLeak(t *testing.T) {
	var redirected, messageCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer target.Close()
	var status atomic.Int32
	status.Store(429)
	g := newTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			serveCatalog(w)
			return
		}
		messageCalls.Add(1)
		w.Header().Set("Retry-After", "60")
		w.Header().Set("Location", target.URL)
		w.WriteHeader(int(status.Load()))
		_, _ = io.WriteString(w, `{"error":"provider-secret"}`)
	}, allow, true)
	for _, code := range []int32{429, 503, 307} {
		status.Store(code)
		res, body := doRequest(t, localRequest(t, g, "/v1/messages", nativeRequest))
		want := int(code)
		if code == 307 {
			want = 502
		}
		if res.StatusCode != want || res.Header.Get("Retry-After") != "60" || strings.Contains(body, "provider-secret") {
			t.Fatalf("error handling status=%d body=%s", res.StatusCode, body)
		}
	}
	if redirected.Load() != 0 || messageCalls.Load() != 3 {
		t.Fatalf("unexpected redirect or retry: redirected=%d requests=%d", redirected.Load(), messageCalls.Load())
	}
}
func TestSSEFlushesAndPreservesNativeToolEvents(t *testing.T) {
	first := "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_1\",\"name\":\"return_ok\",\"input\":{}}}\n\n"
	last := "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	release := make(chan struct{})
	g := newTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			serveCatalog(w)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, first)
		w.(http.Flusher).Flush()
		select {
		case <-release:
			_, _ = io.WriteString(w, last)
		case <-r.Context().Done():
		}
	}, allow, true)
	r := localRequest(t, g, "/v1/messages", strings.TrimSuffix(nativeRequest, "}")+`,"stream":true}`)
	res, err := (&http.Client{Timeout: 4 * time.Second}).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	reader := bufio.NewReader(res.Body)
	var received strings.Builder
	for i := 0; i < 3; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal("SSE buffered until upstream completed:", err)
		}
		received.WriteString(line)
	}
	close(release)
	rest, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	received.Write(rest)
	if received.String() != first+last || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("native stream changed: %s", received.String())
	}
}
func TestClientCancellationAndDisableCancelUpstream(t *testing.T) {
	for _, disable := range []bool{false, true} {
		t.Run(map[bool]string{false: "client-cancel", true: "plugin-disable"}[disable], func(t *testing.T) {
			upstreamCanceled := make(chan struct{})
			g := newTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/models" {
					serveCatalog(w)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "event: ping\ndata: {}\n\n")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(upstreamCanceled)
			}, allow, true)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := localRequest(t, g, "/v1/messages", nativeRequest).WithContext(ctx)
			res, err := (&http.Client{Timeout: 4 * time.Second}).Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if disable {
				closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
				defer closeCancel()
				if err := g.Close(closeCtx); err != nil {
					t.Fatal("disable deadlocked:", err)
				}
			} else {
				cancel()
			}
			select {
			case <-upstreamCanceled:
			case <-time.After(2 * time.Second):
				t.Fatal("upstream request survived cancellation")
			}
		})
	}
}

func TestNativeOutputBudgetClampedToCatalogLimit(t *testing.T) {
	forwarded := make(chan map[string]json.RawMessage, 1)
	g := newTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			model := modelFixture(DefaultModel)
			model["top_provider"].(map[string]any)["max_completion_tokens"] = 8192
			_, _ = w.Write(catalogFixture(model))
			return
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		forwarded <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, nativeResponse)
	}, allow, true)
	request := strings.Replace(nativeRequest, `"max_tokens":128`, `"max_tokens":32000`, 1)
	request = strings.TrimSuffix(request, "}") + `,"stream":false,"metadata":{"user_id":"fixture"}}`
	res, _ := doRequest(t, localRequest(t, g, "/v1/messages", request))
	if res.StatusCode != 200 {
		t.Fatalf("output budget was rejected: %d", res.StatusCode)
	}
	body := <-forwarded
	if string(body["max_tokens"]) != "8192" || string(body["model"]) != `"kilo-auto/free"` {
		t.Fatalf("incorrect model/budget: %+v", body)
	}
	if string(body["stream"]) != "false" || string(body["metadata"]) != `{"user_id":"fixture"}` || len(body["tools"]) == 0 || len(body["messages"]) == 0 {
		t.Fatalf("native fields lost: %+v", body)
	}
}
