package webui

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestAssetsVersionedIndex(t *testing.T) {
	set, err := loadAssets()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(set.version) {
		t.Fatalf("version %q", set.version)
	}
	idx := string(set.index)
	for _, f := range []string{"styles.css", "app.js", "favicon.svg", "favicon.png", "apple-touch-icon.png"} {
		if !strings.Contains(idx, `"/static/`+f+`?v=`+set.version+`"`) {
			t.Errorf("index.html: %s not versioned", f)
		}
	}
	if regexp.MustCompile(`(?:href|src)="/static/[^"?]+"`).MatchString(idx) {
		t.Error("unversioned /static/ URL left in index.html")
	}
	// The rest of the page is untouched.
	orig, _ := Static.ReadFile("static/index.html")
	if got := regexp.MustCompile(`\?v=[0-9a-f]+`).ReplaceAllString(idx, ""); got != string(orig) {
		t.Error("index.html changed beyond ?v=")
	}
}

func TestBuildAssetsVersionFollowsContent(t *testing.T) {
	if _, err := buildAssets(os.DirFS(t.TempDir()), "missing"); err == nil {
		t.Fatal("missing dir: want error")
	}
	dir := t.TempDir()
	write := func(s string) string {
		if err := os.WriteFile(dir+"/app.js", []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		set, err := buildAssets(os.DirFS(dir), ".")
		if err != nil {
			t.Fatal(err)
		}
		return set.version
	}
	v1 := write("a\r\n")
	v2 := write("a\r\n")
	v3 := write("a\n") // CRLF vs LF are different bytes, so a different build
	if v1 != v2 || v1 == v3 {
		t.Fatalf("versions %s %s %s", v1, v2, v3)
	}
}

func staticGet(t *testing.T, h http.Handler, method, url string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestStaticCachingAndGzip(t *testing.T) {
	s, _ := testServer(t)
	h := s.routes()
	set, _ := loadAssets()
	raw, _ := Static.ReadFile("static/app.js")
	v := "?v=" + set.version

	// gzip + immutable for the current build URL
	rec := staticGet(t, h, "GET", "/static/app.js"+v, map[string]string{"Accept-Encoding": "br, gzip, deflate"})
	if rec.Code != 200 || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("code %d enc %q", rec.Code, rec.Header().Get("Content-Encoding"))
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("cache-control %q", cc)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Fatalf("content-type %q", ct)
	}
	if rec.Header().Get("Vary") != "Accept-Encoding" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing Vary / nosniff")
	}
	if rec.Body.Len()*3 > len(raw) {
		t.Fatalf("gzip only %d -> %d", len(raw), rec.Body.Len())
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	un, _ := io.ReadAll(zr)
	if !bytes.Equal(un, raw) {
		t.Fatal("gzip body differs from app.js")
	}
	gzTag := rec.Header().Get("ETag")
	if !strings.HasSuffix(gzTag, `-gz"`) {
		t.Fatalf("etag %q", gzTag)
	}

	// identity without (or with refused) gzip; old/no version: revalidate every time
	for _, ae := range []string{"", "identity", "gzip;q=0", "gzip; q=0.0"} {
		rec = staticGet(t, h, "GET", "/static/app.js?v=000000000000", map[string]string{"Accept-Encoding": ae})
		if rec.Header().Get("Content-Encoding") != "" || !bytes.Equal(rec.Body.Bytes(), raw) {
			t.Fatalf("AE %q: not identity", ae)
		}
		if rec.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("AE %q: cache-control %q", ae, rec.Header().Get("Cache-Control"))
		}
	}
	idTag := rec.Header().Get("ETag")

	// 304 for either ETag variant, weak or strong, also in a list
	for _, inm := range []string{gzTag, idTag, "W/" + idTag, `"nope", ` + gzTag, "*"} {
		rec = staticGet(t, h, "GET", "/static/app.js", map[string]string{"If-None-Match": inm, "Accept-Encoding": "gzip"})
		if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 || rec.Header().Get("Content-Encoding") != "" {
			t.Fatalf("If-None-Match %s: code %d len %d", inm, rec.Code, rec.Body.Len())
		}
	}
	rec = staticGet(t, h, "GET", "/static/app.js", map[string]string{"If-None-Match": `"other"`})
	if rec.Code != 200 {
		t.Fatalf("stale etag: %d", rec.Code)
	}

	// i18n and css are compressed too; PNG isn't
	for p, ct := range map[string]string{
		"/static/i18n/zh-CN.json": "application/json; charset=utf-8",
		"/static/styles.css":      "text/css; charset=utf-8",
		"/static/favicon.svg":     "image/svg+xml",
	} {
		rec = staticGet(t, h, "GET", p, map[string]string{"Accept-Encoding": "gzip"})
		if rec.Code != 200 || rec.Header().Get("Content-Type") != ct || rec.Header().Get("Content-Encoding") != "gzip" {
			t.Fatalf("%s: %d %q %q", p, rec.Code, rec.Header().Get("Content-Type"), rec.Header().Get("Content-Encoding"))
		}
	}
	rec = staticGet(t, h, "GET", "/static/apple-touch-icon.png", map[string]string{"Accept-Encoding": "gzip"})
	if rec.Header().Get("Content-Encoding") != "" || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatal("png should go out as is")
	}

	// HEAD: headers, no body; other methods and unknown files refused
	rec = staticGet(t, h, "HEAD", "/static/styles.css", nil)
	if rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") == "" {
		t.Fatalf("HEAD: %d %d", rec.Code, rec.Body.Len())
	}
	if rec = staticGet(t, h, "POST", "/static/app.js", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", rec.Code)
	}
	for _, p := range []string{"/static/", "/static/nope.js", "/static/i18n", "/static/..%2fserver.go"} {
		if rec = staticGet(t, h, "GET", p, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", p, rec.Code)
		}
	}

	// index.html stays no-store and points at the versioned URLs
	rec = staticGet(t, h, "GET", "/", nil)
	if rec.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rec.Body.String(), "/static/app.js"+v) {
		t.Fatal("index not versioned / cacheable")
	}
}

func TestDesktopStaticIdentity(t *testing.T) {
	s, _ := testServer(t)
	h, err := s.DesktopHandler(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rec := staticGet(t, h, "GET", "/static/app.js", map[string]string{"Accept-Encoding": "gzip"})
	raw, _ := Static.ReadFile("static/app.js")
	if rec.Code != 200 || rec.Header().Get("Content-Encoding") != "" || !bytes.Equal(rec.Body.Bytes(), raw) {
		t.Fatalf("desktop webview must get identity bytes: %d %q", rec.Code, rec.Header().Get("Content-Encoding"))
	}
}

func TestAppJSUsesAssetVersion(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`document.currentScript && document.currentScript.src`,
		`"/static/i18n/" + lang + ".json" + assetV`,
		`"/static/i18n/en.json" + assetV`,
		`"/static/favicon.svg" + assetV`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js missing %q", want)
		}
	}
	if regexp.MustCompile(`"/static/[^"]+\.(json|svg|js|css)"\)`).MatchString(src) {
		t.Error("unversioned static fetch left in app.js")
	}
}
