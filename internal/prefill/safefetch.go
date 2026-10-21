// Package prefill guesses plugin settings from what the user already has: an email
// address, a pasted robot webhook, a site URL. Network lookups (feed autodiscovery,
// page hints) go through a guarded client: http(s) only, public IPs only unless the
// user explicitly allows private ones, short timeouts, small bodies, few redirects.
package prefill

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/netproxy"
)

// ErrPrivate is returned when a URL resolves to a loopback/private/link-local address
// and private addresses were not allowed.
var ErrPrivate = errors.New("address is on a private or local network")

// MaxBody caps how much of a page or feed is read.
const MaxBody = 1 << 20

// UserAgent identifies tidebot's lookups.
var UserAgent = "tidebot-prefill/1.0 (+https://github.com/Zhengmingming1/tidebot-releases)"

// blocked reports whether ip must not be dialed when private addresses are off.
// 198.18.0.0/15 is deliberately allowed: proxy tools in fake-IP / TUN mode (Clash,
// Surge, sing-box) answer every DNS query from that range, and it never reaches a LAN.
func blocked(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
		// 0.0.0.0/8, 100.64.0.0/10 (CGNAT, Tailscale), 192.0.0.0/24
		if ip[0] == 0 || (ip[0] == 100 && ip[1]&0xc0 == 64) || (ip[0] == 192 && ip[1] == 0 && ip[2] == 0) {
			return true
		}
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
}

// checkHost resolves host and refuses it if any address is blocked.
func checkHost(ctx context.Context, host string) error {
	if ip := net.ParseIP(host); ip != nil {
		if blocked(ip) {
			return ErrPrivate
		}
		return nil
	}
	if h := strings.ToLower(host); h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") {
		return ErrPrivate
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}
	for _, a := range ips {
		if blocked(a.IP) {
			return ErrPrivate
		}
	}
	return nil
}

// ProxyFor is the proxy for a URL (nil = direct); netproxy.ProxyFor in production.
var ProxyFor = netproxy.ProxyFor

type guardRT struct {
	base         http.RoundTripper
	allowPrivate bool
}

// RoundTrip checks every request, including each redirect hop. When the request goes
// through the user's proxy, the dial-time check can't see the target, so the target
// host is resolved and checked here first.
func (g guardRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return nil, errors.New("URL must be http(s)://")
	}
	if !g.allowPrivate && ProxyFor(req.URL.String()) != nil {
		if err := checkHost(req.Context(), req.URL.Hostname()); err != nil {
			return nil, err
		}
	}
	return g.base.RoundTrip(req)
}

// Client returns a guarded HTTP client. Direct connections are checked on the dialed
// address (after DNS), so redirects and DNS rebinding to internal hosts are refused
// too; proxied requests are checked by resolving the target first.
func Client(allowPrivate bool, timeout time.Duration) *http.Client {
	d := &net.Dialer{Timeout: 5 * time.Second, Control: func(network, address string, _ syscall.RawConn) error {
		if allowPrivate {
			return nil
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil || blocked(ip) {
			return ErrPrivate
		}
		return nil
	}}
	plain := &net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{
		Proxy: func(r *http.Request) (*url.URL, error) { return ProxyFor(r.URL.String()), nil },
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// The user's own proxy may live on localhost: dial it unguarded (the target
			// was checked in RoundTrip). Everything else gets the address check.
			if isProxyAddr(addr) {
				return plain.DialContext(ctx, network, addr)
			}
			return d.DialContext(ctx, network, addr)
		},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 6 * time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       10 * time.Second,
	}
	return &http.Client{Timeout: timeout, Transport: guardRT{base: tr, allowPrivate: allowPrivate}, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		return nil
	}}
}

// isProxyAddr reports whether addr (host:port) is the configured HTTP(S) proxy.
func isProxyAddr(addr string) bool {
	for _, probe := range []string{"https://example.com/", "http://example.com/"} {
		if p := ProxyFor(probe); p != nil {
			port := p.Port()
			if port == "" {
				port = map[string]string{"http": "80", "https": "443", "socks5": "1080", "socks5h": "1080"}[p.Scheme]
			}
			if net.JoinHostPort(p.Hostname(), port) == addr {
				return true
			}
		}
	}
	return false
}

// NormalizeURL trims, adds https:// to a bare host, and rejects non-http(s) URLs
// and URLs with credentials.
func NormalizeURL(raw string) (*url.URL, error) {
	s := strings.Trim(strings.TrimSpace(raw), `"'<>`)
	if s == "" {
		return nil, errors.New("empty URL")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("URL must be http(s)://")
	}
	if u.User != nil {
		return nil, errors.New("URL must not contain a user name or password")
	}
	u.Fragment = ""
	return u, nil
}

// get fetches u with the guarded client and returns at most MaxBody bytes.
func get(ctx context.Context, c *http.Client, u, accept string) (body []byte, ctype string, final *url.URL, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", accept)
	resp, err := c.Do(req)
	if err != nil {
		if errors.Is(err, ErrPrivate) {
			return nil, "", nil, ErrPrivate
		}
		return nil, "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, "", nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	return b, resp.Header.Get("Content-Type"), resp.Request.URL, err
}
