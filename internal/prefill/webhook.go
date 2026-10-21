package prefill

import (
	"net/url"
	"regexp"
	"strings"
)

// Webhook is what a pasted robot webhook (or a bare key/token) turned out to be.
type Webhook struct {
	Kind       string `json:"kind"`                  // wecom | dingtalk | feishu | slack | ""
	URL        string `json:"url,omitempty"`         // full, normalized webhook URL
	Masked     string `json:"masked,omitempty"`      // URL with the key/token masked, for display
	SignSecret string `json:"sign_secret,omitempty"` // DingTalk "SEC…" found in the same paste
	Built      bool   `json:"built,omitempty"`       // URL was built from a bare key/token
}

var (
	reUUID    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	reHex64   = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	reSEC     = regexp.MustCompile(`\bSEC[0-9a-fA-F]{40,80}\b`)
	reURLFind = regexp.MustCompile(`https?://[^\s"'<>]+`)
)

// Mask keeps the first 4 and last 4 characters of a secret.
func Mask(s string) string {
	if len(s) <= 10 {
		return strings.Repeat("•", len(s))
	}
	return s[:4] + "••••" + s[len(s)-4:]
}

// ParseWebhook recognizes WeCom / DingTalk / Feishu / Slack robot webhooks in a paste.
// want is the plugin being configured ("wecom" or "dingtalk"): a bare key/token is
// turned into that plugin's URL.
func ParseWebhook(paste, want string) Webhook {
	p := strings.TrimSpace(paste)
	var w Webhook
	if m := reSEC.FindString(p); m != "" {
		w.SignSecret = m
	}
	raw := reURLFind.FindString(p)
	if raw == "" {
		tok := strings.Trim(strings.TrimSpace(reSEC.ReplaceAllString(p, "")), `"'`)
		switch {
		case want == "wecom" && reUUID.MatchString(tok):
			raw, w.Built = "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key="+tok, true
		case want == "dingtalk" && reHex64.MatchString(tok):
			raw, w.Built = "https://oapi.dingtalk.com/robot/send?access_token="+tok, true
		default:
			return w
		}
	}
	u, err := url.Parse(strings.TrimRight(raw, ".,;)，。"))
	if err != nil || u.Host == "" {
		return w
	}
	host := strings.ToLower(u.Hostname())
	q := u.Query()
	switch {
	case host == "qyapi.weixin.qq.com" && strings.HasPrefix(u.Path, "/cgi-bin/webhook/send") && q.Get("key") != "":
		w.Kind = "wecom"
		u.Scheme, u.RawQuery = "https", url.Values{"key": {q.Get("key")}}.Encode()
		w.URL = u.String()
		w.Masked = "key " + Mask(q.Get("key"))
	case host == "oapi.dingtalk.com" && strings.HasPrefix(u.Path, "/robot/send") && q.Get("access_token") != "":
		w.Kind = "dingtalk"
		u.Scheme, u.RawQuery = "https", url.Values{"access_token": {q.Get("access_token")}}.Encode()
		w.URL = u.String()
		w.Masked = "access_token " + Mask(q.Get("access_token"))
	case (host == "open.feishu.cn" || host == "open.larksuite.com") && strings.Contains(u.Path, "/bot/v2/hook/"):
		w.Kind = "feishu"
	case host == "hooks.slack.com":
		w.Kind = "slack"
	}
	return w
}
