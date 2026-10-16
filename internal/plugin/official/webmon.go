package official

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// RobotsAllowed fetches /robots.txt and checks path for our user agent ("tidebot") or "*".
// Missing or unreadable robots.txt allows everything.
func RobotsAllowed(ctx context.Context, rawURL string) (bool, error) {
	u, err := checkURL(rawURL)
	if err != nil {
		return false, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.Scheme+"://"+u.Host+"/robots.txt", nil)
	req.Header.Set("User-Agent", UserAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return true, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return true, nil
	}
	b, err := readBody(resp.Body)
	if err != nil {
		return true, nil
	}
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return robotsAllow(string(b), "tidebot", p), nil
}

func robotsAllow(txt, agent, path string) bool {
	type group struct {
		agents []string
		rules  [][2]string // allow|disallow, prefix
	}
	var groups []*group
	var cur *group
	lastWasAgent := false
	sc := bufio.NewScanner(strings.NewReader(txt))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "user-agent":
			if cur == nil || !lastWasAgent {
				cur = &group{}
				groups = append(groups, cur)
			}
			cur.agents = append(cur.agents, strings.ToLower(v))
			lastWasAgent = true
		case "allow", "disallow":
			lastWasAgent = false
			if cur != nil {
				cur.rules = append(cur.rules, [2]string{k, v})
			}
		default:
			lastWasAgent = false
		}
	}
	pick := func(match func(string) bool) *group {
		for _, g := range groups {
			for _, a := range g.agents {
				if match(a) {
					return g
				}
			}
		}
		return nil
	}
	g := pick(func(a string) bool { return a != "*" && strings.Contains(agent, a) })
	if g == nil {
		g = pick(func(a string) bool { return a == "*" })
	}
	if g == nil {
		return true
	}
	best, allow := -1, true
	for _, r := range g.rules {
		if r[1] == "" {
			continue
		}
		if robotsMatch(r[1], path) && len(r[1]) > best {
			best, allow = len(r[1]), r[0] == "allow"
		} else if robotsMatch(r[1], path) && len(r[1]) == best && r[0] == "allow" {
			allow = true
		}
	}
	return allow
}

// robotsMatch supports * wildcards and a trailing $.
func robotsMatch(pat, path string) bool {
	anchored := strings.HasSuffix(pat, "$")
	pat = strings.TrimSuffix(pat, "$")
	parts := strings.Split(pat, "*")
	if !strings.HasPrefix(path, parts[0]) {
		return false
	}
	rest := path[len(parts[0]):]
	for _, p := range parts[1:] {
		i := strings.Index(rest, p)
		if i < 0 {
			return false
		}
		rest = rest[i+len(p):]
	}
	return !anchored || rest == "" || strings.HasSuffix(pat, "*")
}

// PageText fetches a page and returns the visible text (optionally of a CSS selection).
func PageText(ctx context.Context, rawURL, selector string, ignoreWS bool) (string, error) {
	if _, err := checkURL(rawURL); err != nil {
		return "", err
	}
	ok, err := RobotsAllowed(ctx, rawURL)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("robots.txt disallows this page")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(rawURL), nil)
	req.Header.Set("User-Agent", UserAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	b, err := readBody(resp.Body)
	if err != nil {
		return "", err
	}
	r, err := charset.NewReader(bytes.NewReader(b), resp.Header.Get("Content-Type"))
	if err != nil {
		r = bytes.NewReader(b)
	}
	doc, err := html.Parse(r)
	if err != nil {
		return "", err
	}
	return ExtractText(doc, selector, ignoreWS)
}

// ExtractText returns the visible text of doc (or of the nodes matching selector).
func ExtractText(doc *html.Node, selector string, ignoreWS bool) (string, error) {
	nodes := []*html.Node{doc}
	if s := strings.TrimSpace(selector); s != "" {
		sel, err := cascadia.Compile(s)
		if err != nil {
			return "", fmt.Errorf("bad CSS selector: %v", err)
		}
		nodes = sel.MatchAll(doc)
		if len(nodes) == 0 {
			return "", errors.New("selector matched nothing")
		}
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "template", "svg", "head":
				return
			case "br", "p", "div", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6", "section", "article", "td", "th":
				b.WriteByte('\n')
			}
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
		b.WriteByte('\n')
	}
	var lines []string
	for _, l := range strings.Split(b.String(), "\n") {
		if ignoreWS {
			l = strings.Join(strings.Fields(l), " ")
		} else {
			l = strings.TrimRight(l, " \t\r")
		}
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n"), nil
}

// ChangePct is the share of lines (multiset) that differ between a and b, 0–100.
func ChangePct(a, b string) (pct float64, added, removed []string) {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	if a == "" {
		la = nil
	}
	if b == "" {
		lb = nil
	}
	cnt := map[string]int{}
	for _, l := range la {
		cnt[l]++
	}
	for _, l := range lb {
		if cnt[l] > 0 {
			cnt[l]--
		} else {
			added = append(added, l)
		}
	}
	for _, l := range la {
		if cnt[l] > 0 {
			cnt[l]--
			removed = append(removed, l)
		}
	}
	tot := len(la) + len(lb)
	if tot == 0 {
		return 0, nil, nil
	}
	return float64(len(added)+len(removed)) * 100 / float64(tot), added, removed
}

func capLines(l []string, n int) []string {
	if len(l) > n {
		return append(l[:n:n], fmt.Sprintf("… (+%d)", len(l)-n))
	}
	return l
}

func pollWeb(ctx context.Context, cfg map[string]any, st *State, first bool) ([]Item, error) {
	txt, err := PageText(ctx, str(cfg, "url"), str(cfg, "selector"), boolean(cfg, "ignore_whitespace", true))
	if err != nil {
		return nil, err
	}
	if len(txt) > 200<<10 {
		txt = txt[:200<<10]
	}
	if st.preview {
		return []Item{{"url": str(cfg, "url"), "text": snippet(txt, 800)}}, nil
	}
	if first || !st.Seeded {
		st.Text = txt
		return nil, nil
	}
	pct, add, rem := ChangePct(st.Text, txt)
	if pct == 0 || pct < num(cfg, "threshold_pct", 1) {
		return nil, nil
	}
	st.Text = txt
	return []Item{{"url": str(cfg, "url"), "selector": str(cfg, "selector"), "changed_pct": float64(int(pct*10)) / 10,
		"added": capLines(add, 30), "removed": capLines(rem, 30), "checked_at": time.Now().UTC().Format(time.RFC3339)}}, nil
}

func init() {
	register(&Runtime{
		Name:     "webmon",
		Poll:     pollWeb,
		Interval: func(cfg map[string]any) time.Duration { return minutes(cfg, "interval_minutes", 60, 5) },
	})
}
