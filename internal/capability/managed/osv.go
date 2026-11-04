package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OSVClient rejects known malicious packages (MAL-*). Ordinary vulnerability
// advisories are not a malware verdict; the application may display them through
// a separate vulnerability review. Network/parse failures fail installation shut.
type OSVClient struct {
	Client   HTTPDoer
	Endpoint string
}

func (c *OSVClient) Check(ctx context.Context, ecosystem, name, version string) error {
	if ecosystem != "npm" && ecosystem != "PyPI" {
		return errors.New("unsupported OSV ecosystem")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = "https://api.osv.dev/v1/query"
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.User != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"))) {
		return errors.New("invalid OSV endpoint")
	}
	payload, _ := json.Marshal(map[string]any{"package": map[string]string{"ecosystem": ecosystem, "name": name}, "version": version})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("OSV redirect refused") }}
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("OSV malware check unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OSV malware check status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(b) > 2<<20 {
		return errors.New("invalid OSV response size")
	}
	var result struct {
		Vulns []struct {
			ID      string   `json:"id"`
			Aliases []string `json:"aliases"`
		} `json:"vulns"`
	}
	if err = json.Unmarshal(b, &result); err != nil {
		return errors.New("invalid OSV response")
	}
	for _, v := range result.Vulns {
		for _, id := range append(v.Aliases, v.ID) {
			if strings.HasPrefix(strings.ToUpper(id), "MAL-") {
				return fmt.Errorf("malicious package blocked by OSV: %s", id)
			}
		}
	}
	return nil
}
