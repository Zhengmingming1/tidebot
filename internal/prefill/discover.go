package prefill

import (
	"bytes"
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"

	"github.com/Zhengmingming1/tidebot/internal/plugin/official"
)

// FoundFeed is one feed found for a site.
type FoundFeed struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
	Kind  string `json:"kind,omitempty"` // rss | atom | feed
	Items int    `json:"items,omitempty"`
}

// Discovery is the result of looking up a URL the user pasted.
type Discovery struct {
	Input    string      `json:"input"`
	IsFeed   bool        `json:"is_feed"` // the URL already is a feed
	Feeds    []FoundFeed `json:"feeds"`
	Title    string      `json:"title,omitempty"`    // page <title>
	Selector string      `json:"selector,omitempty"` // suggested CSS selector for the main content
	Probed   bool        `json:"probed,omitempty"`   // feeds came from well-known paths, not <link> tags
}

// commonFeedPaths are tried when a page advertises no feed.
var commonFeedPaths = []string{"/feed", "/feed.xml", "/rss.xml", "/atom.xml", "/index.xml", "/rss"}

// looksLikeFeed sniffs the first bytes of a body.
func looksLikeFeed(b []byte, ctype string) bool {
	ct := strings.ToLower(ctype)
	if strings.Contains(ct, "rss") || strings.Contains(ct, "atom") {
		return true
	}
	head := strings.ToLower(string(b[:min(len(b), 1024)]))
	return strings.Contains(head, "<rss") || strings.Contains(head, "<feed") || strings.Contains(head, "<rdf:rdf")
}

func feedFrom(u string, b []byte) (FoundFeed, bool) {
	f, err := official.ParseFeed(b)
	if err != nil {
		return FoundFeed{}, false
	}
	k := "rss"
	if strings.Contains(strings.ToLower(string(b[:min(len(b), 1024)])), "<feed") {
		k = "atom"
	}
	return FoundFeed{URL: u, Title: f.Title, Kind: k, Items: len(f.Items)}, true
}

// Discover fetches raw (a site or feed URL). For a feed it returns the feed; for a
// page it returns the feeds advertised in <link rel="alternate">, or failing that
// the first well-known feed path that parses, plus the page title and a content
// selector hint (used by the web monitor).
func Discover(ctx context.Context, raw string, allowPrivate bool) (*Discovery, error) {
	u, err := NormalizeURL(raw)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c := Client(allowPrivate, 8*time.Second)
	b, ct, final, err := get(ctx, c, u.String(), "text/html, application/rss+xml, application/atom+xml, application/xml;q=0.9, */*;q=0.5")
	if err != nil {
		return nil, err
	}
	d := &Discovery{Input: u.String(), Feeds: []FoundFeed{}}
	if looksLikeFeed(b, ct) {
		if f, ok := feedFrom(final.String(), b); ok {
			d.IsFeed, d.Feeds, d.Title = true, []FoundFeed{f}, f.Title
			return d, nil
		}
	}
	r, err := charset.NewReader(bytes.NewReader(b), ct)
	if err != nil {
		r = bytes.NewReader(b)
	}
	doc, err := html.Parse(r)
	if err != nil {
		return d, nil
	}
	d.Title = pageTitle(doc)
	d.Selector = SuggestSelector(doc)
	seen := map[string]bool{}
	for _, l := range cascadia.QueryAll(doc, cascadia.MustCompile(`link[rel~="alternate"][href]`)) {
		typ := strings.ToLower(attr(l, "type"))
		if !strings.Contains(typ, "rss") && !strings.Contains(typ, "atom") && !strings.Contains(typ, "feed+json") {
			continue
		}
		ref, err := final.Parse(strings.TrimSpace(attr(l, "href")))
		if err != nil || (ref.Scheme != "http" && ref.Scheme != "https") || seen[ref.String()] {
			continue
		}
		seen[ref.String()] = true
		k := "rss"
		if strings.Contains(typ, "atom") {
			k = "atom"
		} else if strings.Contains(typ, "json") {
			k = "feed"
		}
		d.Feeds = append(d.Feeds, FoundFeed{URL: ref.String(), Title: strings.TrimSpace(attr(l, "title")), Kind: k})
		if len(d.Feeds) >= 8 {
			break
		}
	}
	if len(d.Feeds) > 0 {
		return d, nil
	}
	for i, p := range commonFeedPaths {
		if i >= 5 || ctx.Err() != nil {
			break
		}
		ref := &url.URL{Scheme: final.Scheme, Host: final.Host, Path: p}
		fb, fct, ffinal, err := get(ctx, c, ref.String(), "application/rss+xml, application/atom+xml, application/xml")
		if err != nil || !looksLikeFeed(fb, fct) {
			continue
		}
		if f, ok := feedFrom(ffinal.String(), fb); ok {
			d.Feeds, d.Probed = []FoundFeed{f}, true
			break
		}
	}
	return d, nil
}

func attr(n *html.Node, k string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, k) {
			return a.Val
		}
	}
	return ""
}

func pageTitle(doc *html.Node) string {
	if t := cascadia.Query(doc, cascadia.MustCompile("title")); t != nil && t.FirstChild != nil {
		s := strings.Join(strings.Fields(t.FirstChild.Data), " ")
		if r := []rune(s); len(r) > 120 {
			s = string(r[:120])
		}
		return s
	}
	return ""
}

func textLen(n *html.Node) int {
	total := 0
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.ElementNode && (x.Data == "script" || x.Data == "style" || x.Data == "noscript" || x.Data == "nav" || x.Data == "footer") {
			return
		}
		if x.Type == html.TextNode {
			total += len(strings.TrimSpace(x.Data))
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return total
}

// selectorCandidates, most specific first. A candidate is suggested when it matches
// exactly one element holding a real share of the page's text: monitoring that region
// ignores header/footer/sidebar churn (clocks, ads, "N people online").
var selectorCandidates = []string{"main article", "article", "main", "[role=main]", "#content", "#main", ".content", ".post", ".entry-content", "#readme"}

// SuggestSelector returns a content selector for the page, or "" to watch the whole page.
func SuggestSelector(doc *html.Node) string {
	body := cascadia.Query(doc, cascadia.MustCompile("body"))
	if body == nil {
		return ""
	}
	all := textLen(body)
	if all < 200 {
		return ""
	}
	for _, s := range selectorCandidates {
		ns := cascadia.QueryAll(doc, cascadia.MustCompile(s))
		if len(ns) != 1 {
			continue
		}
		if n := textLen(ns[0]); n >= 200 && n >= all*3/10 && n < all*97/100 {
			return s
		}
	}
	return ""
}
