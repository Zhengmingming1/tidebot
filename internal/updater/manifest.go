// Package updater implements tidebot's self-update: a signed manifest
// (latest.json + ed25519 signature) published to the public releases repo,
// sha256-checked downloads, rename-based atomic swaps that keep one backup,
// and a boot-time marker that rolls back when the new build doesn't come up.
//
// Nothing is sent to anyone except plain HTTPS GETs for the manifest and the
// asset (no telemetry, no identifiers). The default HTTP client follows tidebot's
// proxy setting (see internal/netproxy).
package updater

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/netproxy"
)

// ReleasesRepo is the public repo that carries release files and latest.json
// (the update channel). A var so forks can point builds at their own channel with
// -ldflags "-X github.com/Zhengmingming1/tidebot/internal/updater.ReleasesRepo=...".
var ReleasesRepo = "Zhengmingming1/tidebot-releases"

// PublicKey verifies latest.json. The private half lives only in the source
// repo's Actions secret UPDATE_SIGNING_KEY (environment "release"). Replace both
// together if rotated. Forks override it with -X ...updater.PublicKey=<base64>.
var PublicKey = "r9j+SIJVqWgrff/AGa6+GXBIHY0ekRSkjF253CjNPPU="

// Asset is one downloadable file for one target.
type Asset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size,omitempty"`
	Kind   string `json:"kind"` // server-tar | server-zip | appimage | app-tar | nsis
}

// Manifest is latest.json.
type Manifest struct {
	Version  string           `json:"version"`
	Date     string           `json:"date"`
	Channel  string           `json:"channel"`
	Notes    string           `json:"notes,omitempty"`
	NotesURL string           `json:"notes_url,omitempty"`
	Assets   map[string]Asset `json:"assets"` // key: <product>-<os>-<arch>, product server|desktop
}

// AssetKey names the manifest entry for a product on an OS/arch.
func AssetKey(product, goos, goarch string) string { return product + "-" + goos + "-" + goarch }

var (
	ErrBadSignature = errors.New("update manifest signature is invalid")
	ErrChecksum     = errors.New("downloaded file does not match the manifest sha256")
)

// Sign returns the base64 signature over the exact manifest bytes.
func Sign(priv ed25519.PrivateKey, body []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, body))
}

// Verify checks body against a base64 signature with a base64 public key.
func Verify(pubB64 string, body []byte, sigB64 string) error {
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubB64))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("update public key is not configured")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sigB64))
	if err != nil || !ed25519.Verify(ed25519.PublicKey(pub), body, sig) {
		return ErrBadSignature
	}
	return nil
}

// ManifestBase returns where latest.json lives; TIDEBOT_UPDATE_URL overrides it
// (mirrors, tests). The URL must point at a directory holding latest.json(.sig).
func ManifestBase() string {
	if v := strings.TrimRight(os.Getenv("TIDEBOT_UPDATE_URL"), "/"); v != "" {
		return v
	}
	return "https://github.com/" + ReleasesRepo + "/releases/latest/download"
}

// Client uses tidebot's proxy choice (Settings → 网络 > env > OS settings) with
// sane timeouts. Downloads use the request context for the overall deadline.
func Client() *http.Client { return netproxy.Client(0) }

// Fetch downloads and verifies latest.json.
func Fetch(ctx context.Context, hc *http.Client, base, pub string) (*Manifest, error) {
	body, err := get(ctx, hc, base+"/latest.json", 1<<20)
	if err != nil {
		return nil, err
	}
	sig, err := get(ctx, hc, base+"/latest.json.sig", 4096)
	if err != nil {
		return nil, err
	}
	if err := Verify(pub, body, string(sig)); err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if m.Version == "" || len(m.Assets) == 0 {
		return nil, errors.New("manifest is empty")
	}
	return &m, nil
}

func get(ctx context.Context, hc *http.Client, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tidebot-updater") // no version or machine id on purpose
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, &HTTPError{URL: url, Status: resp.StatusCode, Text: resp.Status}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("GET %s: response too large", url)
	}
	return b, nil
}

// Get downloads one small file (same client headers and size cap as the manifest).
func Get(ctx context.Context, hc *http.Client, url string, max int64) ([]byte, error) {
	return get(ctx, hc, url, max)
}
