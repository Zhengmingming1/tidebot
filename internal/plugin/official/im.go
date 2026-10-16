package official

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Endpoint hosts (tests point these at httptest servers).
var (
	WeComHost    = "https://qyapi.weixin.qq.com"
	DingTalkHost = "https://oapi.dingtalk.com"
)

// checkWebhook makes sure a configured webhook goes to the declared host only.
func checkWebhook(raw, host, path string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	h, _ := url.Parse(host)
	if err != nil || u.Scheme != h.Scheme || u.Host != h.Host || !strings.HasPrefix(u.Path, path) {
		return nil, fmt.Errorf("webhook must be %s%s…", host, path)
	}
	return u, nil
}

// postJSON posts and checks the {errcode, errmsg} reply both IMs use. Errors never
// include the URL (it carries the robot key / token).
func postJSON(ctx context.Context, u string, body any) (map[string]any, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return nil, errors.New("bad request")
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("User-Agent", UserAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, errors.New("request failed (network)")
	}
	defer resp.Body.Close()
	rb, _ := readBody(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	var out map[string]any
	_ = json.Unmarshal(rb, &out)
	if c := num(out, "errcode", 0); c != 0 {
		return out, fmt.Errorf("error %d: %s", int(c), str(out, "errmsg"))
	}
	return out, nil
}

func limitRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// --- WeCom -----------------------------------------------------------------

// WeComSend sends text or markdown to a group robot.
func WeComSend(ctx context.Context, cfg map[string]any, kind, content string) error {
	u, err := checkWebhook(str(cfg, "webhook_url"), WeComHost, "/cgi-bin/webhook/send")
	if err != nil {
		return err
	}
	if strings.TrimSpace(content) == "" {
		return errors.New("empty message")
	}
	var body map[string]any
	if kind == "markdown" {
		body = map[string]any{"msgtype": "markdown", "markdown": map[string]string{"content": limitRunes(content, 4000)}}
	} else {
		body = map[string]any{"msgtype": "text", "text": map[string]string{"content": limitRunes(content, 2000)}}
	}
	_, err = postJSON(ctx, u.String(), body)
	return err
}

// WeComAppSend sends an application message (needs corp_id, agent_id, app_secret).
func WeComAppSend(ctx context.Context, cfg map[string]any, toUser, content string) error {
	if err := need(cfg, "corp_id", "agent_id", "app_secret"); err != nil {
		return errors.New("app messages need corp_id, agent_id and app_secret in the plugin settings")
	}
	q := url.Values{"corpid": {str(cfg, "corp_id")}, "corpsecret": {str(cfg, "app_secret")}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, WeComHost+"/cgi-bin/gettoken?"+q.Encode(), nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		return errors.New("gettoken failed (network)")
	}
	rb, _ := readBody(resp.Body)
	resp.Body.Close()
	var tok map[string]any
	_ = json.Unmarshal(rb, &tok)
	if c := num(tok, "errcode", 0); c != 0 || str(tok, "access_token") == "" {
		return fmt.Errorf("gettoken error %d: %s", int(c), str(tok, "errmsg"))
	}
	if toUser == "" {
		toUser = firstNonEmpty(str(cfg, "to_user"), "@all")
	}
	aid, _ := strconv.Atoi(str(cfg, "agent_id"))
	_, err = postJSON(ctx, WeComHost+"/cgi-bin/message/send?access_token="+url.QueryEscape(str(tok, "access_token")),
		map[string]any{"touser": toUser, "msgtype": "markdown", "agentid": aid, "markdown": map[string]string{"content": limitRunes(content, 2000)}})
	return err
}

// --- DingTalk -------------------------------------------------------------

// DingTalkSign returns the timestamp and signature for a robot with 加签 enabled:
// base64(HMAC-SHA256(secret, timestamp + "\n" + secret)).
func DingTalkSign(secret string, ts int64) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(strconv.FormatInt(ts, 10) + "\n" + secret))
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

// DingTalkSend sends text or markdown to a robot.
func DingTalkSend(ctx context.Context, cfg map[string]any, kind, title, content string) error {
	u, err := checkWebhook(str(cfg, "webhook_url"), DingTalkHost, "/robot/send")
	if err != nil {
		return err
	}
	if strings.TrimSpace(content) == "" {
		return errors.New("empty message")
	}
	if sec := str(cfg, "sign_secret"); sec != "" {
		ts := time.Now().UnixMilli()
		q := u.Query()
		q.Set("timestamp", strconv.FormatInt(ts, 10))
		q.Set("sign", DingTalkSign(sec, ts))
		u.RawQuery = q.Encode()
	}
	var body map[string]any
	if kind == "markdown" {
		if title == "" {
			title = limitRunes(strings.TrimLeft(strings.SplitN(content, "\n", 2)[0], "# "), 60)
		}
		body = map[string]any{"msgtype": "markdown", "markdown": map[string]string{"title": title, "text": limitRunes(content, 5000)}}
	} else {
		body = map[string]any{"msgtype": "text", "text": map[string]string{"content": limitRunes(content, 5000)}}
	}
	_, err = postJSON(ctx, u.String(), body)
	return err
}

func imTools(send func(ctx context.Context, kind, title, content string) error, extra ...Tool) []Tool {
	mk := func(kind, desc string) Tool {
		props := map[string]any{"content": map[string]any{"type": "string", "description": "Message body"}}
		if kind == "markdown" {
			props["title"] = map[string]any{"type": "string", "description": "Short title (DingTalk notification preview)"}
		}
		return Tool{Name: "send_" + kind, Description: desc, InputSchema: schema(props, "content"), Call: func(a map[string]any) (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := send(ctx, kind, str(a, "title"), str(a, "content")); err != nil {
				return "", err
			}
			return "sent", nil
		}}
	}
	return append([]Tool{mk("text", "Send a plain-text message to the configured group."), mk("markdown", "Send a Markdown message to the configured group.")}, extra...)
}

func init() {
	register(&Runtime{
		Name: "wecom",
		Tools: func(cfg map[string]any) []Tool {
			app := Tool{Name: "send_app_message", Description: "Send a WeCom application message to users (needs app credentials in the plugin settings).",
				InputSchema: schema(map[string]any{"content": map[string]any{"type": "string"}, "to_user": map[string]any{"type": "string", "description": "user IDs separated by |, default from settings"}}, "content"),
				Call: func(a map[string]any) (string, error) {
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					if err := WeComAppSend(ctx, cfg, str(a, "to_user"), str(a, "content")); err != nil {
						return "", err
					}
					return "sent", nil
				}}
			return imTools(func(ctx context.Context, kind, _, content string) error { return WeComSend(ctx, cfg, kind, content) }, app)
		},
		Test: func(ctx context.Context, cfg map[string]any) (string, error) {
			return "sent", WeComSend(ctx, cfg, "markdown", "**tidebot** 测试消息 / test message ✅")
		},
	})
	register(&Runtime{
		Name: "dingtalk",
		Tools: func(cfg map[string]any) []Tool {
			return imTools(func(ctx context.Context, kind, title, content string) error {
				return DingTalkSend(ctx, cfg, kind, title, content)
			})
		},
		Test: func(ctx context.Context, cfg map[string]any) (string, error) {
			return "sent", DingTalkSend(ctx, cfg, "markdown", "tidebot", "### tidebot\n测试消息 / test message ✅")
		},
	})
}
