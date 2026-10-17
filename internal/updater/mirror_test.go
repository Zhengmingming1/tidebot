package updater

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestSourceURLs(t *testing.T) {
	t.Setenv("TIDEBOT_UPDATE_URL", "")
	p := Source{Name: "p", Prefix: "https://gh-proxy.example/"}
	if got := p.ManifestURL(); got != "https://gh-proxy.example/https://github.com/Zhengmingming1/tidebot-releases/releases/latest/download" {
		t.Fatal(got)
	}
	orig := "https://github.com/Zhengmingming1/tidebot-releases/releases/download/v0.2.0/tidebot-server_0.2.0_linux_amd64.tar.gz"
	if got := p.AssetURL(orig, "0.2.0"); got != "https://gh-proxy.example/"+orig {
		t.Fatal(got)
	}
	d := Source{Name: "d", Manifest: "https://m.example/tidebot/", Assets: "https://m.example/tidebot/v{version}"}
	if d.ManifestURL() != "https://m.example/tidebot" || d.AssetURL(orig, "0.2.0") != "https://m.example/tidebot/v0.2.0/tidebot-server_0.2.0_linux_amd64.tar.gz" {
		t.Fatal(d.ManifestURL(), d.AssetURL(orig, "0.2.0"))
	}
	if (Source{Name: "x"}).Validate() == nil || (Source{Prefix: "ftp://x/"}).Validate() == nil || p.Validate() != nil {
		t.Fatal("validate")
	}
	s := Sources([]Source{p, {Name: "bad"}})
	if len(s) != 2 || s[0].Name != "github" || s[1].Name != "p" {
		t.Fatalf("%+v", s)
	}
	for _, pr := range Presets {
		if pr.Validate() != nil {
			t.Fatal(pr)
		}
	}
}

func TestMirrorFallback(t *testing.T) {
	good := newFixture(t, nil)
	bad := newFixture(t, nil) // different key -> signature mismatch against good.pub
	notFound := httptest.NewServer(http.NotFoundHandler())
	defer notFound.Close()
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	srcs := []Source{
		{Name: "dead", Manifest: deadURL},
		{Name: "404", Manifest: notFound.URL},
		{Name: "forged", Manifest: bad.srv.URL},
		{Name: "good", Manifest: good.srv.URL},
	}
	m, used, err := FetchFrom(context.Background(), Client(), srcs, good.pub)
	if err != nil || used != 3 || m.Version != "0.2.0" {
		t.Fatal(used, err)
	}
	_, _, err = FetchFrom(context.Background(), Client(), srcs[:3], good.pub)
	var fe *FetchError
	if !errors.As(err, &fe) || len(fe.Attempts) != 3 {
		t.Fatal(err)
	}
	if codes := []string{fe.Attempts[0].Code, fe.Attempts[1].Code, fe.Attempts[2].Code}; codes[0] != "refused" || codes[1] != "http_404" || codes[2] != "signature" {
		t.Fatal(codes)
	}
	if fe.Code() != "signature" {
		t.Fatal(fe.Code())
	}

	// Asset download: 404 mirror, then a mirror serving the wrong bytes, then the real one.
	corrupt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("evil")) }))
	defer corrupt.Close()
	dir, in := setupInstall(t)
	c := Evaluate(m, "0.1.2", in, "linux", "amd64")
	assetSrcs := []Source{{Name: "404", Manifest: "x", Assets: notFound.URL}, {Name: "corrupt", Manifest: "x", Assets: corrupt.URL}}
	if _, err := ApplyFrom(context.Background(), Client(), c, t.TempDir(), assetSrcs); !errors.As(err, &fe) || fe.Attempts[0].Code != "http_404" || fe.Attempts[1].Code != "checksum" {
		t.Fatal(err)
	}
	if read(t, filepath.Join(dir, "agentd")) != "OLD-agentd" {
		t.Fatal("touched files after failed downloads")
	}
	assetSrcs = append(assetSrcs, Source{Name: "good", Manifest: "x", Assets: good.srv.URL})
	if _, err := ApplyFrom(context.Background(), Client(), c, t.TempDir(), assetSrcs); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(dir, "agentd")) != "NEW-agentd" {
		t.Fatal("not installed from the good mirror")
	}
}

func TestClassify(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(2 * time.Second) }))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := get(ctx, Client(), slow.URL, 10)
	if Classify(err) != "timeout" {
		t.Fatal(err, Classify(err))
	}
	tlsSrv := httptest.NewTLSServer(http.NotFoundHandler())
	defer tlsSrv.Close()
	if _, err := get(context.Background(), Client(), tlsSrv.URL, 10); Classify(err) != "tls" {
		t.Fatal(err, Classify(err))
	}
	if Classify(&HTTPError{Status: 407}) != "proxy" || Classify(ErrChecksum) != "checksum" || Classify(errors.New("dial tcp 20.205.243.166:443: i/o timeout")) != "timeout" {
		t.Fatal("classify")
	}
	if Classify(errors.New("lookup github.com: no such host")) != "dns" {
		t.Fatal("dns")
	}
}
