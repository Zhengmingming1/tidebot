package prefill

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestBlocked(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "::1", "fe80::1", "fc00::1", "0.0.0.0", "100.64.1.1", "224.0.0.1"} {
		if !blocked(net.ParseIP(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "140.82.112.3", "2606:4700::1111", "198.18.0.1"} {
		if blocked(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestNormalizeURL(t *testing.T) {
	if u, err := NormalizeURL(" example.com/blog "); err != nil || u.String() != "https://example.com/blog" {
		t.Fatal(u, err)
	}
	for _, bad := range []string{"", "file:///etc/passwd", "ftp://x.com", "https://u:p@x.com", "javascript:alert(1)"} {
		if _, err := NormalizeURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

const page = `<!doctype html><html><head><title> My  Blog </title>
<link rel="alternate" type="application/rss+xml" title="Posts" href="/feed.xml">
<link rel="alternate" type="application/atom+xml" href="https://other.example/atom">
<link rel="stylesheet" href="/s.css"></head><body><nav>home about contact</nav>
<main><h1>Hello</h1><p>` + "lorem ipsum dolor sit amet " + `</p><p>` + "x" + `</p></main><footer>© 2026</footer></body></html>`

const rss = `<?xml version="1.0"?><rss version="2.0"><channel><title>Posts</title><item><title>A</title><link>https://x/a</link></item><item><title>B</title></item></channel></rss>`

func TestDiscover(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(page)) })
	mux.HandleFunc("/feed.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(rss))
	})
	mux.HandleFunc("/bare/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body>no feeds here</body></html>"))
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat("a", MaxBody*2))) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx := context.Background()

	// Local test server = private address: refused unless allowed.
	if _, err := Discover(ctx, srv.URL, false); !errors.Is(err, ErrPrivate) {
		t.Fatalf("private not refused: %v", err)
	}
	d, err := Discover(ctx, srv.URL, true)
	if err != nil || d.IsFeed || d.Title != "My Blog" || len(d.Feeds) != 2 || d.Feeds[0].URL != srv.URL+"/feed.xml" || d.Feeds[0].Title != "Posts" || d.Feeds[1].Kind != "atom" {
		t.Fatalf("page %+v %v", d, err)
	}
	d, err = Discover(ctx, srv.URL+"/feed.xml", true)
	if err != nil || !d.IsFeed || d.Feeds[0].Items != 2 || d.Feeds[0].Kind != "rss" {
		t.Fatalf("feed %+v %v", d, err)
	}
	// No <link>: well-known paths are probed (/feed.xml parses).
	d, err = Discover(ctx, srv.URL+"/bare/", true)
	if err != nil || len(d.Feeds) != 1 || !d.Probed || !strings.HasSuffix(d.Feeds[0].URL, "/feed.xml") {
		t.Fatalf("probe %+v %v", d, err)
	}
	// Bodies are capped, not read whole.
	if d, err = Discover(ctx, srv.URL+"/big", true); err != nil || len(d.Feeds) != 1 { // /big has no links → probe finds /feed.xml
		t.Fatalf("big %+v %v", d, err)
	}
	if _, err := Discover(ctx, "file:///etc/passwd", true); err == nil {
		t.Fatal("file URL")
	}
}

func TestSuggestSelector(t *testing.T) {
	long := strings.Repeat("word ", 80)
	doc, _ := html.Parse(strings.NewReader(`<html><body><header>` + strings.Repeat("menu ", 20) + `</header><main><article>` + long + `</article></main><aside>` + strings.Repeat("ad ", 40) + `</aside></body></html>`))
	if s := SuggestSelector(doc); s != "main article" {
		t.Fatalf("got %q", s)
	}
	doc, _ = html.Parse(strings.NewReader(`<html><body><div>` + long + `</div></body></html>`))
	if s := SuggestSelector(doc); s != "" {
		t.Fatalf("plain page %q", s)
	}
}

func TestParseWebhook(t *testing.T) {
	key := "693a91f6-7a1b-4bc4-97a0-0ec2a1fa5aaa"
	w := ParseWebhook("  https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key="+key+"&debug=1 ", "wecom")
	if w.Kind != "wecom" || w.URL != "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key="+key || strings.Contains(w.Masked, key) || !strings.HasSuffix(w.Masked, "5aaa") {
		t.Fatalf("wecom %+v", w)
	}
	if w = ParseWebhook(key, "wecom"); w.Kind != "wecom" || !w.Built {
		t.Fatalf("bare key %+v", w)
	}
	tok := strings.Repeat("ab12", 16)
	sec := "SEC" + strings.Repeat("0f", 32)
	w = ParseWebhook("Webhook: https://oapi.dingtalk.com/robot/send?access_token="+tok+"\n加签: "+sec, "dingtalk")
	if w.Kind != "dingtalk" || w.SignSecret != sec || !strings.HasSuffix(w.URL, tok) || strings.Contains(w.Masked, tok) {
		t.Fatalf("dingtalk %+v", w)
	}
	if w = ParseWebhook(tok, "dingtalk"); !w.Built || w.Kind != "dingtalk" {
		t.Fatalf("bare token %+v", w)
	}
	if w = ParseWebhook("https://open.feishu.cn/open-apis/bot/v2/hook/abc", "wecom"); w.Kind != "feishu" || w.URL != "" {
		t.Fatalf("feishu %+v", w)
	}
	if w = ParseWebhook("hello", "wecom"); w.Kind != "" {
		t.Fatalf("junk %+v", w)
	}
}

func TestMail(t *testing.T) {
	ctx := context.Background()
	old := LookupMX
	defer func() { LookupMX = old }()
	LookupMX = func(_ context.Context, d string) ([]string, error) {
		switch d {
		case "acme.cn":
			return []string{"mxbiz1.qq.com"}, nil
		case "startup.io":
			return []string{"aspmx.l.google.com"}, nil
		case "bytes.com":
			return []string{"mx1.feishu.cn"}, nil
		}
		return nil, errors.New("nx")
	}
	cases := map[string]string{"Me <ME@QQ.com>": "smtp.qq.com", "a@gmail.com": "smtp.gmail.com", "x@163.com": "smtp.163.com", "y@outlook.com": "smtp-mail.outlook.com",
		"z@icloud.com": "smtp.mail.me.com", "w@aliyun.com": "smtp.aliyun.com", "boss@acme.cn": "smtp.exmail.qq.com", "dev@startup.io": "smtp.gmail.com", "pm@bytes.com": "smtp.feishu.cn"}
	for in, host := range cases {
		s, ok := Mail(ctx, in)
		if !ok || s.SMTPHost != host || s.IMAPPort != 993 {
			t.Errorf("%s → %+v %v", in, s, ok)
		}
	}
	if s, _ := Mail(ctx, "dev@startup.io"); s.Via != "mx" {
		t.Fatal("via")
	}
	if s, _ := Mail(ctx, "y@outlook.com"); s.SMTPPort != 587 || s.Note != "oauth" {
		t.Fatalf("outlook %+v", s)
	}
	for _, bad := range []string{"nobody@unknown.example", "not-an-email", "a@b", "a b@c.com"} {
		if _, ok := Mail(ctx, bad); ok {
			t.Errorf("%q matched", bad)
		}
	}
}

func TestProxiedTargetChecked(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(rss)) }))
	defer proxy.Close()
	pu, _ := url.Parse(proxy.URL)
	old := ProxyFor
	defer func() { ProxyFor = old }()
	ProxyFor = func(string) *url.URL { return pu }
	ctx := context.Background()
	// The proxy itself is on 127.0.0.1 and may be dialed; a private target behind it is refused.
	if _, err := Discover(ctx, "http://10.0.0.5/feed", false); !errors.Is(err, ErrPrivate) {
		t.Fatalf("private target via proxy: %v", err)
	}
	if _, err := Discover(ctx, "http://localhost/feed", false); !errors.Is(err, ErrPrivate) {
		t.Fatalf("localhost via proxy: %v", err)
	}
	// A public literal IP goes through the proxy (which answers with a feed).
	d, err := Discover(ctx, "http://1.1.1.1/feed", false)
	if err != nil || !d.IsFeed {
		t.Fatalf("public via proxy %+v %v", d, err)
	}
}
