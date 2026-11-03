package freepool

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/netproxy"
)

const (
	MaxRequestBytes  = 8 << 20
	maxResponseBytes = 32 << 20
	requestTimeout   = 3 * time.Minute
)

// Gateway's access callback must check the current plugin version, enable state,
// network grant and data-policy acknowledgement on every request.
type Gateway struct {
	check          func(context.Context) (string, error)
	client         *http.Client
	upstream       string
	ctx            context.Context
	cancel         context.CancelFunc
	mu             sync.Mutex
	server         *http.Server
	listener       net.Listener
	token          string
	url            string
	closed         bool
	handlers       sync.WaitGroup
	catalogGate    chan struct{}
	models         []Model // protected by catalogGate
	catalogExpires time.Time
}

func New(check func(context.Context) (string, error)) *Gateway {
	ctx, cancel := context.WithCancel(context.Background())
	client := netproxy.Client(0)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Gateway{check: check, client: client, upstream: upstreamBase, ctx: ctx, cancel: cancel, catalogGate: make(chan struct{}, 1)}
}
func (g *Gateway) Start() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return errors.New("free model gateway is closed")
	}
	if g.server != nil {
		return nil
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return err
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	g.token = hex.EncodeToString(secret[:])
	g.url = "http://" + ln.Addr().String()
	g.listener = ln
	g.server = &http.Server{Handler: http.HandlerFunc(g.serve), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return g.ctx }}
	go func() { _ = g.server.Serve(ln) }()
	return nil
}
func (g *Gateway) URL() string   { g.mu.Lock(); defer g.mu.Unlock(); return g.url }
func (g *Gateway) Token() string { g.mu.Lock(); defer g.mu.Unlock(); return g.token }
func (g *Gateway) Close(ctx context.Context) error {
	g.mu.Lock()
	g.closed = true
	g.cancel()
	srv := g.server
	g.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
	}
	g.client.CloseIdleConnections()
	done := make(chan struct{})
	go func() { g.handlers.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (g *Gateway) requestContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, requestTimeout)
	stop := context.AfterFunc(g.ctx, cancel)
	if g.ctx.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}
func (g *Gateway) serve(w http.ResponseWriter, r *http.Request) {
	// Add while holding the same mutex Close uses before waiting.
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		writeError(w, 503, "overloaded_error", "free model plugin is disabled")
		return
	}
	g.handlers.Add(1)
	token, host := g.token, strings.TrimPrefix(g.url, "http://")
	g.mu.Unlock()
	defer g.handlers.Done()
	w.Header().Set("Cache-Control", "no-store")
	if r.Host != host || r.Header.Get("Origin") != "" {
		writeError(w, 403, "permission_error", "local gateway clients only")
		return
	}
	provided := r.Header.Get("X-Api-Key")
	if auth := r.Header.Get("Authorization"); auth != "" {
		if !strings.HasPrefix(auth, "Bearer ") || provided != "" && provided != strings.TrimPrefix(auth, "Bearer ") {
			writeError(w, 401, "authentication_error", "invalid local gateway token")
			return
		}
		provided = strings.TrimPrefix(auth, "Bearer ")
	}
	if token == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
		writeError(w, 401, "authentication_error", "invalid local gateway token")
		return
	}
	ctx, cancel := g.requestContext(r.Context())
	defer cancel()
	if g.check == nil {
		writeError(w, 403, "permission_error", "free model plugin is not approved")
		return
	}
	configured, err := g.check(ctx)
	if err != nil {
		writeError(w, 403, "permission_error", "free model plugin is disabled, unapproved or requires data-policy acknowledgement")
		return
	}
	if configured == "" {
		configured = DefaultModel
	}
	switch r.URL.Path {
	case "/v1/models":
		if r.Method != http.MethodGet {
			writeError(w, 405, "invalid_request_error", "GET required")
			return
		}
		models, e := g.Models(ctx, false)
		if e != nil {
			writeError(w, 503, "overloaded_error", e.Error())
			return
		}
		data := make([]map[string]any, 0, len(models))
		for _, m := range models {
			data = append(data, map[string]any{"id": m.ID, "type": "model", "display_name": m.Name, "created_at": "1970-01-01T00:00:00Z"})
		}
		writeJSON(w, 200, map[string]any{"data": data, "has_more": false, "first_id": models[0].ID, "last_id": models[len(models)-1].ID})
	case "/v1/messages":
		if r.Method != http.MethodPost {
			writeError(w, 405, "invalid_request_error", "POST required")
			return
		}
		g.messages(ctx, w, r, configured)
	case "/v1/messages/count_tokens":
		writeError(w, 404, "not_found_error", "upstream token counting is not supported; no estimated count is reported as an official count")
	default:
		writeError(w, 404, "not_found_error", "unsupported free model gateway endpoint")
	}
}
func (g *Gateway) messages(ctx context.Context, w http.ResponseWriter, r *http.Request, configured string) {
	// Reading the whole bounded body allows validation before contacting the LLM.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBytes))
	if err != nil {
		writeError(w, 413, "request_too_large", "request exceeds the free gateway limit")
		return
	}
	var req struct {
		Model     string            `json:"model"`
		MaxTokens int               `json:"max_tokens"`
		Messages  []json.RawMessage `json:"messages"`
	}
	if json.Unmarshal(body, &req) != nil || req.Model == "" || len(req.Messages) == 0 || req.MaxTokens < 1 {
		writeError(w, 400, "invalid_request_error", "model, messages and positive max_tokens are required")
		return
	}
	if req.Model != configured {
		writeError(w, 400, "invalid_request_error", "request model differs from the selected free model")
		return
	}
	// Normalize the model field so duplicate JSON keys cannot be interpreted as
	// a different model by the upstream parser. Preserve other native fields.
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(body, &fields)
	fields["model"], _ = json.Marshal(configured)
	model, err := g.selectedModel(ctx, configured)
	if err != nil {
		writeError(w, 503, "overloaded_error", err.Error())
		return
	}
	if model.MaxOutputTokens > 0 && req.MaxTokens > model.MaxOutputTokens {
		// Claude's generic output budget can exceed a free model's real limit.
		fields["max_tokens"], _ = json.Marshal(model.MaxOutputTokens)
	}
	body, _ = json.Marshal(fields)
	upstream, err := http.NewRequestWithContext(ctx, http.MethodPost, g.upstream+"/messages", bytes.NewReader(body))
	if err != nil {
		writeError(w, 500, "api_error", "cannot prepare free model request")
		return
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("Accept", "application/json, text/event-stream")
	version := r.Header.Get("Anthropic-Version")
	if version == "" {
		version = "2023-06-01"
	}
	upstream.Header.Set("Anthropic-Version", version)
	if beta := r.Header.Get("Anthropic-Beta"); beta != "" {
		upstream.Header.Set("Anthropic-Beta", beta)
	}
	// Construct a fresh request; local authentication, cookies and proxy identity
	// headers are never forwarded. Kilo rejects fabricated upstream credentials.
	res, err := g.client.Do(upstream)
	if err != nil {
		if ctx.Err() == nil {
			writeError(w, 502, "api_error", "free model upstream request failed")
		}
		return
	}
	defer res.Body.Close()
	if retry := res.Header.Get("Retry-After"); retry != "" {
		w.Header().Set("Retry-After", retry)
	}
	if res.StatusCode != http.StatusOK {
		status := res.StatusCode
		if status < 400 || status > 599 {
			status = 502
		}
		kind := "api_error"
		switch status {
		case 400:
			kind = "invalid_request_error"
		case 401:
			kind = "authentication_error"
		case 403:
			kind = "permission_error"
		case 429:
			kind = "rate_limit_error"
		case 503:
			kind = "overloaded_error"
		}
		writeError(w, status, kind, fmt.Sprintf("free model upstream returned HTTP %d", res.StatusCode))
		return
	}
	contentType, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || (contentType != "application/json" && contentType != "text/event-stream") {
		writeError(w, 502, "api_error", "free model upstream returned an unsupported response")
		return
	}
	w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
	if contentType == "application/json" {
		response, e := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
		if e != nil || len(response) > maxResponseBytes || !json.Valid(response) {
			writeError(w, 502, "api_error", "free model upstream returned an invalid or oversized response")
			return
		}
		_, _ = w.Write(response)
		return
	}
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	n, e := io.Copy(&flushWriter{w: w}, io.LimitReader(res.Body, maxResponseBytes+1))
	if e == nil && n > maxResponseBytes {
		_, _ = io.WriteString(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"free model stream exceeds response limit\"}}\n\n")
	}
}

type flushWriter struct{ w http.ResponseWriter }

func (f *flushWriter) Write(b []byte) (int, error) {
	n, err := f.w.Write(b)
	if fl, ok := f.w.(http.Flusher); ok {
		fl.Flush()
	}
	return n, err
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, kind, message string) {
	writeJSON(w, status, map[string]any{"type": "error", "error": map[string]string{"type": kind, "message": message}})
}
