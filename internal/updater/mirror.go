package updater

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// ReleasesPage is where users can always download by hand.
var ReleasesPage = "https://github.com/" + ReleasesRepo + "/releases/latest"

// Source is one place to get latest.json(.sig) and the release files.
//
// A Prefix source is a "GitHub proxy" that serves any github.com URL behind its
// own host (https://proxy.example/https://github.com/...). Otherwise Manifest is
// a directory holding latest.json + latest.json.sig and Assets a directory
// holding the files ({version} is replaced, e.g. https://mirror/tidebot/v{version}).
// Every source is safe to use: the manifest is ed25519-signed and each file's
// sha256 comes from that signed manifest.
type Source struct {
	Name     string `json:"name"`
	Prefix   string `json:"prefix,omitempty"`
	Manifest string `json:"manifest,omitempty"`
	Assets   string `json:"assets,omitempty"`
}

// Direct is GitHub itself.
var Direct = Source{Name: "github"}

// Presets are offered in Settings → 网络 but NOT enabled by default: they are
// third-party GitHub proxies (they see your IP and what you download) and may
// vanish without notice. Checked 2026-10 to serve both latest.json and the
// release archives byte-identically.
var Presets = []Source{
	{Name: "gh-proxy.com", Prefix: "https://gh-proxy.com/"},
	{Name: "ghfast.top", Prefix: "https://ghfast.top/"},
}

// Validate checks a user-entered source.
func (s Source) Validate() error {
	chk := func(v string) error {
		if v == "" {
			return nil
		}
		u, err := url.Parse(strings.ReplaceAll(v, "{version}", "0"))
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			return fmt.Errorf("mirror URL %q must be http(s)://", v)
		}
		return nil
	}
	if s.Prefix == "" && s.Manifest == "" {
		return errors.New("mirror needs a prefix or a manifest URL")
	}
	for _, v := range []string{s.Prefix, s.Manifest, s.Assets} {
		if err := chk(v); err != nil {
			return err
		}
	}
	return nil
}

// ManifestURL is the directory holding latest.json for this source.
func (s Source) ManifestURL() string {
	switch {
	case s.Manifest != "":
		return strings.TrimRight(s.Manifest, "/")
	case s.Prefix != "":
		return s.Prefix + ManifestBase()
	}
	return ManifestBase()
}

// AssetURL maps the manifest's asset URL onto this source.
func (s Source) AssetURL(orig, version string) string {
	switch {
	case s.Assets != "":
		name := orig[strings.LastIndex(orig, "/")+1:]
		return strings.TrimRight(strings.ReplaceAll(s.Assets, "{version}", strings.TrimPrefix(version, "v")), "/") + "/" + name
	case s.Prefix != "":
		return s.Prefix + orig
	}
	return orig
}

// Sources returns the order to try: TIDEBOT_UPDATE_URL alone when set
// (tests, private mirrors), else GitHub then the user's mirrors.
func Sources(mirrors []Source) []Source {
	if os.Getenv("TIDEBOT_UPDATE_URL") != "" {
		return []Source{{Name: "env", Manifest: ManifestBase()}}
	}
	out := []Source{Direct}
	for _, m := range mirrors {
		if m.Validate() == nil {
			out = append(out, m)
		}
	}
	return out
}

// Attempt records one try.
type Attempt struct {
	Source string `json:"source"`
	Code   string `json:"code,omitempty"`
	Error  string `json:"error,omitempty"`
}

// FetchError is returned when every source failed.
type FetchError struct{ Attempts []Attempt }

func (e *FetchError) Error() string {
	var b strings.Builder
	for i, a := range e.Attempts {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(a.Source + ": " + a.Error)
	}
	return b.String()
}

// Code is the classification of the first (GitHub) attempt, which is the one
// the user's fix (proxy) is about; signature trumps everything.
func (e *FetchError) Code() string {
	for _, a := range e.Attempts {
		if a.Code == "signature" {
			return a.Code
		}
	}
	if len(e.Attempts) > 0 {
		return e.Attempts[0].Code
	}
	return "unknown"
}

// FetchFrom tries each source in order and returns the first verified manifest
// with the index of the source that served it.
func FetchFrom(ctx context.Context, hc *http.Client, srcs []Source, pub string) (*Manifest, int, error) {
	fe := &FetchError{}
	for i, s := range srcs {
		m, err := Fetch(ctx, hc, s.ManifestURL(), pub)
		if err == nil {
			return m, i, nil
		}
		fe.Attempts = append(fe.Attempts, Attempt{Source: s.Name, Code: Classify(err), Error: err.Error()})
		if ctx.Err() != nil {
			break
		}
	}
	return nil, -1, fe
}

// HTTPError is a non-200 response.
type HTTPError struct {
	URL    string
	Status int
	Text   string
}

func (e *HTTPError) Error() string { return "GET " + e.URL + ": " + e.Text }

// Classify maps an error to timeout | dns | tls | proxy | refused | http_404 |
// http | signature | checksum | unknown.
func Classify(err error) string {
	if err == nil {
		return ""
	}
	var he *HTTPError
	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	var uaErr x509.UnknownAuthorityError
	var hnErr x509.HostnameError
	var recErr tls.RecordHeaderError
	var ne net.Error
	var opErr *net.OpError
	switch {
	case errors.Is(err, ErrBadSignature):
		return "signature"
	case errors.Is(err, ErrChecksum):
		return "checksum"
	case errors.As(err, &he):
		if he.Status == 404 {
			return "http_404"
		}
		if he.Status == 407 {
			return "proxy"
		}
		return "http"
	case errors.As(err, &dnsErr):
		return "dns"
	case errors.As(err, &certErr), errors.As(err, &uaErr), errors.As(err, &hnErr), errors.As(err, &recErr):
		return "tls"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "proxyconnect"), strings.Contains(s, "socks connect"):
		return "proxy"
	case strings.Contains(s, "tls:"), strings.Contains(s, "x509:"):
		return "tls"
	case strings.Contains(s, "i/o timeout"), strings.Contains(s, "deadline exceeded"):
		return "timeout"
	case strings.Contains(s, "no such host"):
		return "dns"
	case errors.As(err, &opErr), strings.Contains(s, "connection refused"), strings.Contains(s, "connection reset"):
		return "refused"
	}
	return "unknown"
}
