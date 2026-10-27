package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/provider"
)

// Model list for the provider drawer: the model fields become pick lists instead of ids
// typed from a docs page. Asks the provider's own GET …/v1/models with the saved key. Many
// gateways put the Anthropic protocol under a sub-path ("/anthropic", "/apps/anthropic",
// "/coding") and list models only on the OpenAI-style root, so a 404/405 moves on to the next
// candidate. Idea from farion1231/cc-switch src-tauri/src/services/model_fetch.rs.

const (
	modelListTTL      = 10 * time.Minute
	modelListMax      = 500
	modelListMaxPages = 10
)

// Longest first, so "/api/anthropic" wins over "/anthropic".
var modelCompatSuffixes = []string{"/api/anthropic", "/apps/anthropic", "/api/coding", "/anthropic", "/coding", "/claude"}

type listedModel struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Owner   string `json:"owner,omitempty"`
	created int64
}

type modelListEntry struct {
	sig string
	at  time.Time
	out map[string]any
}

var (
	modelListMu    sync.Mutex
	modelListCache = map[string]modelListEntry{}
)

// modelListURLs: candidate list endpoints for a base URL, most specific first. bearer[i] says
// candidate i is an OpenAI-style root, which always takes "Authorization: Bearer".
func modelListURLs(base string) (urls []string, bearer []bool) {
	b := strings.TrimRight(strings.TrimSpace(base), "/")
	if b == "" {
		b = "https://api.anthropic.com"
	}
	add := func(u string, br bool) {
		for _, x := range urls {
			if x == u {
				return
			}
		}
		urls = append(urls, u)
		bearer = append(bearer, br)
	}
	if strings.HasSuffix(b, "/v1") {
		add(b+"/models", false)
	} else {
		add(b+"/v1/models", false)
	}
	for _, s := range modelCompatSuffixes {
		if strings.HasSuffix(b, s) {
			root := strings.TrimRight(strings.TrimSuffix(b, s), "/")
			if u, err := url.Parse(root); err != nil || u.Host == "" {
				break
			}
			add(root+"/v1/models", true)
			add(root+"/models", true)
			if s == "/apps/anthropic" { // Alibaba Model Studio lists on its OpenAI-compatible path
				add(root+"/compatible-mode/v1/models", true)
			}
			break
		}
	}
	return urls, bearer
}

// parseModelList reads {"data":[{id,display_name,owned_by,created|created_at}]} (Anthropic,
// OpenAI) or {"models":[{slug|id|name}]}.
func parseModelList(b []byte) (ms []listedModel, hasMore bool, lastID string, err error) {
	var r struct {
		Data []struct {
			ID          string          `json:"id"`
			DisplayName string          `json:"display_name"`
			OwnedBy     string          `json:"owned_by"`
			Created     json.RawMessage `json:"created"`
			CreatedAt   string          `json:"created_at"`
		} `json:"data"`
		Models  json.RawMessage `json:"models"`
		HasMore bool            `json:"has_more"`
		LastID  string          `json:"last_id"`
	}
	if err = json.Unmarshal(b, &r); err != nil {
		return nil, false, "", errors.New("the answer is not a model list")
	}
	for _, d := range r.Data {
		m := listedModel{ID: d.ID, Owner: d.OwnedBy}
		if d.DisplayName != "" && d.DisplayName != d.ID {
			m.Name = d.DisplayName
		}
		var n int64
		if json.Unmarshal(d.Created, &n) == nil && n > 0 {
			m.created = n
		} else if t, e := time.Parse(time.RFC3339, d.CreatedAt); e == nil {
			m.created = t.Unix()
		}
		ms = append(ms, m)
	}
	if r.Data == nil && len(r.Models) > 0 {
		var arr []map[string]any
		if json.Unmarshal(r.Models, &arr) == nil {
			for _, e := range arr {
				id := ""
				for _, k := range []string{"slug", "id", "name"} {
					if s, ok := e[k].(string); ok && s != "" {
						id = strings.TrimPrefix(s, "models/")
						break
					}
				}
				if id != "" {
					ms = append(ms, listedModel{ID: id})
				}
			}
		}
	}
	if r.Data == nil && r.Models == nil {
		return nil, false, "", errors.New("the answer is not a model list")
	}
	return ms, r.HasMore, r.LastID, nil
}

// cleanModels drops empty / odd ids and duplicates, caps the list, and orders it newest first
// when every entry has a date (Anthropic's own order), else by id.
func cleanModels(in []listedModel) []listedModel {
	seen := map[string]bool{}
	out := make([]listedModel, 0, len(in))
	dated := true
	for _, m := range in {
		m.ID = strings.TrimSpace(m.ID)
		if m.ID == "" || len(m.ID) > 128 || strings.ContainsAny(m.ID, " \t\r\n\"'<>") || seen[m.ID] {
			continue
		}
		if len(m.Name) > 80 {
			m.Name = m.Name[:80]
		}
		if len(m.Owner) > 40 {
			m.Owner = m.Owner[:40]
		}
		seen[m.ID] = true
		if m.created == 0 {
			dated = false
		}
		out = append(out, m)
		if len(out) >= modelListMax {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if dated && out[i].created != out[j].created {
			return out[i].created > out[j].created
		}
		if dated {
			return false
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ListModels: GET /api/providers/{name}/models. Answers {ok, models, host, path, fetched_at,
// cached} or {ok:false, status, error}; the key never appears in either.
func (pa providerAdmin) ListModels(ctx context.Context, name string, refresh bool) (map[string]any, error) {
	p, ok := pa.a.Registry.Get(name)
	if !ok {
		return nil, errors.New("provider not found")
	}
	key, err := pa.a.ResolveAPIKey(p)
	if err != nil || key == "" {
		return map[string]any{"ok": false, "error": "no API key set"}, nil
	}
	h := sha256.Sum256([]byte(p.BaseURL + "\x00" + p.AuthEnv + "\x00" + key))
	sig := hex.EncodeToString(h[:8])
	modelListMu.Lock()
	if e, ok := modelListCache[name]; ok && !refresh && e.sig == sig && time.Since(e.at) < modelListTTL {
		modelListMu.Unlock()
		out := map[string]any{}
		for k, v := range e.out {
			out[k] = v
		}
		out["cached"] = true
		return out, nil
	}
	modelListMu.Unlock()

	out := pa.fetchModels(ctx, p, key)
	if out["ok"] == true {
		modelListMu.Lock()
		modelListCache[name] = modelListEntry{sig: sig, at: time.Now(), out: out}
		modelListMu.Unlock()
	}
	return out, nil
}

func (pa providerAdmin) fetchModels(ctx context.Context, p provider.Profile, key string) map[string]any {
	urls, bearer := modelListURLs(p.BaseURL)
	var last map[string]any
	for i, u := range urls {
		var all []listedModel
		next := u
		status := 0
		var failed map[string]any
		for page := 0; page < modelListMaxPages && next != ""; page++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
			if err != nil {
				return map[string]any{"ok": false, "error": "bad base URL"}
			}
			req.Header.Set("accept", "application/json")
			req.Header.Set("anthropic-version", "2023-06-01")
			if p.AuthEnv == "ANTHROPIC_API_KEY" && !bearer[i] {
				req.Header.Set("x-api-key", key)
			} else {
				req.Header.Set("authorization", "Bearer "+key)
			}
			resp, err := providerTestClient.Do(req)
			if err != nil {
				return map[string]any{"ok": false, "error": provider.MaskSecret(err.Error(), key)}
			}
			rb, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
			resp.Body.Close()
			status = resp.StatusCode
			if status != 200 {
				failed = map[string]any{"ok": false, "status": status, "error": provider.MaskSecret(upstreamMessage(rb), key)}
				break
			}
			ms, more, lastID, perr := parseModelList(rb)
			if perr != nil {
				failed = map[string]any{"ok": false, "status": status, "error": perr.Error()}
				break
			}
			all = append(all, ms...)
			next = ""
			if more && lastID != "" && len(all) < modelListMax {
				q, _ := url.Parse(u)
				v := q.Query()
				v.Set("after_id", lastID)
				q.RawQuery = v.Encode()
				next = q.String()
			}
		}
		if failed == nil {
			pu, _ := url.Parse(u)
			return map[string]any{"ok": true, "models": cleanModels(all), "host": pu.Host, "path": pu.Path, "fetched_at": time.Now().UTC().Format(time.RFC3339), "cached": false}
		}
		last = failed
		// Only "no such endpoint" (or a page that isn't a model list) tries the next
		// candidate; a refused key or a rate limit is the answer.
		if status != 404 && status != 405 && status != 200 {
			return failed
		}
	}
	if last == nil {
		last = map[string]any{"ok": false, "error": "no model list endpoint"}
	}
	last["unsupported"] = true
	return last
}

// upstreamMessage pulls {"error":{"message"}} (or {"message"}) out of an error body, ≤ 300 bytes.
func upstreamMessage(rb []byte) string {
	var e struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	msg := strings.TrimSpace(string(rb))
	if json.Unmarshal(rb, &e) == nil {
		var inner struct {
			Message string `json:"message"`
		}
		var s string
		switch {
		case json.Unmarshal(e.Error, &inner) == nil && inner.Message != "":
			msg = inner.Message
		case json.Unmarshal(e.Error, &s) == nil && s != "":
			msg = s
		case e.Message != "":
			msg = e.Message
		}
	}
	if strings.HasPrefix(msg, "<") { // an HTML error page says nothing useful
		msg = ""
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return msg
}

// forgetModelList drops a cached list when a provider is saved, renamed or removed.
func forgetModelList(names ...string) {
	modelListMu.Lock()
	for _, n := range names {
		delete(modelListCache, n)
	}
	modelListMu.Unlock()
}
