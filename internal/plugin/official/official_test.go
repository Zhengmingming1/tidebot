package official

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/html"
)

func TestCatalogLoadsAndVerifies(t *testing.T) {
	list, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 6 {
		t.Fatalf("want 6 official plugins, got %d", len(list))
	}
	for _, e := range list {
		if e.Hash == "" || e.Manifest.ID != e.ID {
			t.Fatalf("bad entry %+v", e)
		}
		if _, ok := meta[e.ID]; !ok {
			t.Fatalf("no meta for %s", e.ID)
		}
	}
	e, _ := Get("rss-feed")
	if !verify("rss-feed", e.Hash) || verify("rss-feed", strings.Repeat("0", 64)) || verify("evil", e.Hash) {
		t.Fatal("verifier")
	}
	for _, name := range []string{"rss", "webmon", "wecom", "dingtalk", "email"} {
		if runtimes[name] == nil {
			t.Fatalf("runtime %s not registered", name)
		}
	}
}

func verify(id, h string) bool {
	e, err := Get(id)
	return err == nil && e.Hash == h
}

const rss2 = `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>Demo</title>
<item><title>One</title><link>https://ex.com/1</link><guid>g1</guid><pubDate>Fri, 16 Oct 2026 08:00:00 +0000</pubDate><description>&lt;p&gt;Hello &lt;b&gt;world&lt;/b&gt;&lt;/p&gt;</description></item>
<item><title>Two</title><link>https://ex.com/2</link></item>
<item><title>No id</title></item></channel></rss>`

const atom = `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"><title>A</title>
<entry><id>urn:a1</id><title>Alpha</title><link rel="alternate" href="https://ex.com/a"/><updated>2026-10-16T00:00:00Z</updated><summary>S</summary></entry></feed>`

func TestParseFeed(t *testing.T) {
	f, err := ParseFeed([]byte(rss2))
	if err != nil {
		t.Fatal(err)
	}
	if f.Title != "Demo" || len(f.Items) != 3 || f.Items[0].ID != "g1" || f.Items[1].ID != "https://ex.com/2" || !strings.HasPrefix(f.Items[2].ID, "sha256:") {
		t.Fatalf("%+v", f)
	}
	if f.Items[0].Summary != "Hello world" {
		t.Fatalf("summary %q", f.Items[0].Summary)
	}
	a, err := ParseFeed([]byte(atom))
	if err != nil || a.Items[0].ID != "urn:a1" || a.Items[0].Link != "https://ex.com/a" {
		t.Fatalf("%v %+v", err, a)
	}
	if _, err := ParseFeed([]byte("<html></html>")); err == nil {
		t.Fatal("html is not a feed")
	}
}

func TestRSSPollConditionalAndDedupe(t *testing.T) {
	var mu sync.Mutex
	body, hits, cond := rss2, 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		hits++
		if r.Header.Get("If-None-Match") == `"v1"` && body == rss2 {
			cond++
			w.WriteHeader(304)
			return
		}
		if body == rss2 {
			w.Header().Set("ETag", `"v1"`)
		}
		io.WriteString(w, body)
	}))
	defer srv.Close()
	st := &State{}
	cfg := map[string]any{"url": srv.URL}
	items, err := pollRSS(context.Background(), cfg, st, true)
	if err != nil || len(items) != 0 || len(st.Seen) != 3 || st.ETag != `"v1"` {
		t.Fatalf("first poll seeds silently: %v %v %+v", err, items, st)
	}
	items, _ = pollRSS(context.Background(), cfg, st, false)
	if len(items) != 0 || cond != 1 {
		t.Fatalf("304 path: %v cond=%d", items, cond)
	}
	mu.Lock()
	body = strings.Replace(rss2, "<item><title>One", "<item><title>New</title><guid>g9</guid></item><item><title>One", 1)
	mu.Unlock()
	items, _ = pollRSS(context.Background(), cfg, st, false)
	if len(items) != 1 || items[0]["id"] != "g9" {
		t.Fatalf("new item only: %v", items)
	}
	items, _ = pollRSS(context.Background(), cfg, st, false)
	if len(items) != 0 {
		t.Fatalf("dedupe: %v", items)
	}
}

func TestRobots(t *testing.T) {
	txt := "User-agent: *\nDisallow: /private\nAllow: /private/ok\n\nUser-agent: tidebot\nDisallow: /nobots\n"
	if robotsAllow(txt, "tidebot", "/private") != true { // tidebot group overrides *
		t.Fatal("specific group should win")
	}
	if robotsAllow(txt, "tidebot", "/nobots/x") {
		t.Fatal("disallowed")
	}
	if robotsAllow(txt, "other", "/private/x") || !robotsAllow(txt, "other", "/private/ok/1") {
		t.Fatal("longest match")
	}
	if !robotsAllow("", "tidebot", "/") || robotsAllow("User-agent: *\nDisallow: /*.pdf$", "x", "/a.pdf") == true {
		t.Fatal("wildcards")
	}
}

func TestWebMonitor(t *testing.T) {
	page := `<html><head><title>t</title><script>var x=1</script></head><body><div id="price">  ¥ 100 </div><p>static</p></body></html>`
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			io.WriteString(w, "User-agent: *\nDisallow: /blocked")
			return
		}
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, page)
	}))
	defer srv.Close()
	cfg := map[string]any{"url": srv.URL + "/p", "selector": "#price", "threshold_pct": 1.0, "ignore_whitespace": true}
	st := &State{}
	if it, err := pollWeb(context.Background(), cfg, st, true); err != nil || it != nil || st.Text != "¥ 100" {
		t.Fatalf("baseline %v %v %q", it, err, st.Text)
	}
	st.Seeded = true
	mu.Lock()
	page = strings.Replace(page, "¥ 100 ", "¥  100", 1) // whitespace only
	mu.Unlock()
	if it, _ := pollWeb(context.Background(), cfg, st, false); it != nil {
		t.Fatalf("whitespace change must be ignored: %v", it)
	}
	mu.Lock()
	page = strings.Replace(page, "100", "90", 1)
	mu.Unlock()
	it, err := pollWeb(context.Background(), cfg, st, false)
	if err != nil || len(it) != 1 || it[0]["changed_pct"].(float64) != 100 {
		t.Fatalf("change %v %v", it, err)
	}
	if _, err := pollWeb(context.Background(), map[string]any{"url": srv.URL + "/blocked"}, &State{}, true); err == nil || !strings.Contains(err.Error(), "robots") {
		t.Fatalf("robots: %v", err)
	}
	doc, _ := html.Parse(strings.NewReader(page))
	if _, err := ExtractText(doc, "#nope", true); err == nil {
		t.Fatal("selector matched nothing")
	}
	if p, _, _ := ChangePct("a\nb\nc\nd", "a\nb\nc\nX"); p != 25 {
		t.Fatalf("pct %v", p)
	}
}

func TestDingTalkSignAndSend(t *testing.T) {
	var gotQ, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQ = r.URL.RawQuery
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer srv.Close()
	old := DingTalkHost
	DingTalkHost = srv.URL
	defer func() { DingTalkHost = old }()
	cfg := map[string]any{"webhook_url": srv.URL + "/robot/send?access_token=tok", "sign_secret": "SECabc"}
	if err := DingTalkSend(context.Background(), cfg, "markdown", "", "# Hi\nbody"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotQ, "access_token=tok") || !strings.Contains(gotQ, "sign=") || !strings.Contains(gotQ, "timestamp=") || !strings.Contains(gotBody, `"title":"Hi"`) {
		t.Fatalf("q=%s body=%s", gotQ, gotBody)
	}
	if err := DingTalkSend(context.Background(), map[string]any{"webhook_url": "https://evil.example/robot/send"}, "text", "", "x"); err == nil {
		t.Fatal("foreign host must be refused")
	}
}

func TestDingTalkSignVector(t *testing.T) {
	// python: base64(hmac.new(b"SEC123", b"1700000000000\\nSEC123", sha256).digest())
	if got := DingTalkSign("SEC123", 1700000000000); got != "lkcPI1uoxBY1gUnCnnPH1Kkru0Hqjo7rFpA3haIVhEQ=" {
		t.Fatalf("sign = %s", got)
	}
}

func TestWeComSendErrorsHideURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"errcode":93000,"errmsg":"invalid webhook url"}`)
	}))
	defer srv.Close()
	old := WeComHost
	WeComHost = srv.URL
	defer func() { WeComHost = old }()
	err := WeComSend(context.Background(), map[string]any{"webhook_url": srv.URL + "/cgi-bin/webhook/send?key=SECRETKEY"}, "text", "hi")
	if err == nil || strings.Contains(err.Error(), "SECRETKEY") || !strings.Contains(err.Error(), "93000") {
		t.Fatalf("%v", err)
	}
}

func TestEmailHelpers(t *testing.T) {
	m := string(BuildEmail("Bot <a@x.com>", []string{"b@y.com"}, "每日\r\nBcc: evil@z", "line1\nline2", time.Unix(0, 0)))
	if strings.Contains(m, "\r\nBcc:") || !strings.Contains(m, "=?utf-8?") || !strings.Contains(m, "line1\r\nline2") {
		t.Fatal(m)
	}
	if _, err := parseRecipients("a@x.com, nope"); err == nil {
		t.Fatal("bad address")
	}
	cfg := map[string]any{"from_contains": "boss", "keyword": "URGENT"}
	if !MailMatch(cfg, "The Boss <b@x>", "hi", "this is urgent") || MailMatch(cfg, "other", "hi", "urgent") {
		t.Fatal("filters")
	}
	if err := SendEmail(context.Background(), map[string]any{"smtp_host": "127.0.0.1", "smtp_port": 1.0, "from": "a@x.com", "default_to": "b@y.com"}, "", "s", "b"); err == nil {
		t.Fatal("no server")
	}
}

func TestMCPServer(t *testing.T) {
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"send_text","arguments":{"content":"x"}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	tools := runtimes["wecom"].Tools(map[string]any{})
	if err := ServeMCP(strings.NewReader(in), &syncW{w: &out}, "t", "1", tools); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	got := map[float64]map[string]any{}
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]any
		_ = json.Unmarshal([]byte(l), &m)
		if id, ok := m["id"].(float64); ok {
			got[id] = m
		}
	}
	if got[1]["result"] == nil || !strings.Contains(toJSON(got[2]), "send_markdown") || !strings.Contains(toJSON(got[3]), `"isError":true`) {
		t.Fatalf("%s", out.String())
	}
}

type syncW struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncW) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.w.Write(p) }
