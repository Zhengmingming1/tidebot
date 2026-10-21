package webui

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/prefill"
)

// handlePluginPrefill: POST /api/plugins/prefill {plugin, field, value, allow_private}.
// It guesses the rest of an official plugin's settings from one value the user typed
// or pasted. Values are never logged; network lookups use prefill's guarded client.
//
//	email        from / smtp_username / imap_username / "address" → SMTP + IMAP servers
//	wecom        webhook_url (URL or bare key)            → normalized URL, masked form
//	dingtalk     webhook_url (URL, token, + SEC… secret)  → URL + sign_secret
//	rss-feed     url / feeds line                          → feed autodiscovery
//	daily-digest feeds line                                → feed autodiscovery
//	web-monitor  url                                       → page title + content selector
func (s *Server) handlePluginPrefill(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Plugin       string `json:"plugin"`
		Field        string `json:"field"`
		Value        string `json:"value"`
		AllowPrivate bool   `json:"allow_private"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&in); err != nil || len(in.Value) > 4000 {
		writeErr(w, 400, "bad json")
		return
	}
	v := strings.TrimSpace(in.Value)
	out := map[string]any{"plugin": in.Plugin, "field": in.Field}
	switch in.Plugin {
	case "email":
		srv, ok := prefill.Mail(r.Context(), v)
		if !ok {
			out["found"] = false
			break
		}
		addr := v
		if i := strings.LastIndex(addr, "<"); i >= 0 {
			addr = strings.TrimSuffix(addr[i+1:], ">")
		}
		out["found"], out["provider"], out["note"], out["via"] = true, srv.Provider, srv.Note, srv.Via
		out["values"] = map[string]any{"smtp_host": srv.SMTPHost, "smtp_port": srv.SMTPPort, "smtp_username": addr, "from": addr,
			"imap_host": srv.IMAPHost, "imap_port": srv.IMAPPort, "imap_username": addr, "default_to": addr}
	case "wecom", "dingtalk":
		wh := prefill.ParseWebhook(v, in.Plugin)
		out["found"] = wh.Kind != ""
		out["kind"], out["masked"], out["built"] = wh.Kind, wh.Masked, wh.Built
		out["mismatch"] = wh.Kind != "" && wh.Kind != in.Plugin
		vals := map[string]any{}
		if wh.Kind == in.Plugin && wh.URL != "" {
			vals["webhook_url"] = wh.URL
		}
		if in.Plugin == "dingtalk" && wh.SignSecret != "" {
			vals["sign_secret"] = wh.SignSecret
			out["secret_masked"] = prefill.Mask(wh.SignSecret)
		}
		out["values"] = vals
	case "rss-feed", "daily-digest", "web-monitor":
		d, err := prefill.Discover(r.Context(), v, in.AllowPrivate)
		if err != nil {
			code := "fetch"
			if errors.Is(err, prefill.ErrPrivate) {
				code = "private"
			}
			writeJSON(w, 200, map[string]any{"found": false, "error_code": code, "error": err.Error()})
			return
		}
		out["found"] = true
		out["discovery"] = d
	default:
		writeErr(w, 404, "no prefill for this plugin")
		return
	}
	writeJSON(w, 200, out)
}
