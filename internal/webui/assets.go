package webui

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Embedded static assets are served from memory with a content-hash ETag, a gzip copy made
// once at first use and a build version (?v=) for long-lived caching. embed.FS has no
// modification times, so http.FileServer sent neither Last-Modified nor ETag: every page
// load re-downloaded ~640 KB of JS/CSS/i18n uncompressed.
//
// Content types come from a fixed table rather than mime.TypeByExtension, which reads the
// Windows registry and can answer text/plain for .js there; the same bytes and headers go
// out on macOS, Windows and Linux.

type asset struct {
	body  []byte
	gz    []byte // nil when gzip doesn't make it smaller (PNG) or the type isn't text
	ctype string
	etag  string // strong ETag of the identity bytes; the gzip copy gets a "-gz" suffix
}

type assetSet struct {
	files   map[string]*asset // key: path under static/, forward slashes
	version string            // short hash over every file: changes with any asset
	index   []byte            // index.html with ?v=<version> on /static/ URLs
}

var assetTypes = map[string]string{
	".js":   "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".json": "application/json; charset=utf-8",
	".html": "text/html; charset=utf-8",
	".svg":  "image/svg+xml",
	".png":  "image/png",
}

var (
	assetsOnce sync.Once
	assetsVal  *assetSet
	assetsErr  error
)

func loadAssets() (*assetSet, error) {
	assetsOnce.Do(func() { assetsVal, assetsErr = buildAssets(Static, "static") })
	return assetsVal, assetsErr
}

var staticRef = regexp.MustCompile(`((?:href|src)="/static/[^"?#]+)"`)

func buildAssets(fsys fs.FS, root string) (*assetSet, error) {
	set := &assetSet{files: map[string]*asset{}}
	all := sha256.New()
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(p, root+"/")
		sum := sha256.Sum256(b)
		h := hex.EncodeToString(sum[:8])
		ext := strings.ToLower(path.Ext(name))
		ct, ok := assetTypes[ext]
		if !ok {
			ct = "application/octet-stream"
		}
		a := &asset{body: b, ctype: ct, etag: `"` + h + `"`}
		if ext != ".png" && len(b) > 512 {
			var buf bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
			_, _ = zw.Write(b)
			_ = zw.Close()
			if buf.Len() < len(b)*9/10 {
				a.gz = buf.Bytes()
			}
		}
		set.files[name] = a
		all.Write([]byte(name))
		all.Write(sum[:])
		return nil
	})
	if err != nil {
		return nil, err
	}
	set.version = hex.EncodeToString(all.Sum(nil))[:12]
	if a := set.files["index.html"]; a != nil {
		set.index = staticRef.ReplaceAll(a.body, []byte(`$1?v=`+set.version+`"`))
	}
	return set, nil
}

// acceptsGzip reports whether the client listed gzip without q=0.
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		f := strings.Split(part, ";")
		if !strings.EqualFold(strings.TrimSpace(f[0]), "gzip") {
			continue
		}
		for _, p := range f[1:] {
			p = strings.ReplaceAll(strings.TrimSpace(p), " ", "")
			if strings.HasPrefix(p, "q=") {
				if q, err := strconv.ParseFloat(p[2:], 64); err == nil && q == 0 {
					return false
				}
			}
		}
		return true
	}
	return false
}

// etagMatch is the weak comparison of If-None-Match (either variant of the file counts).
func etagMatch(inm string, a *asset) bool {
	if inm == "" {
		return false
	}
	base := strings.Trim(a.etag, `"`)
	for _, t := range strings.Split(inm, ",") {
		t = strings.TrimSpace(t)
		if t == "*" {
			return true
		}
		t = strings.Trim(strings.TrimPrefix(t, "W/"), `"`)
		if t == base || t == base+"-gz" {
			return true
		}
	}
	return false
}

func (set *assetSet) serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/static/")
	a := set.files[name]
	if a == nil || name == "" {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	if r.URL.Query().Get("v") == set.version {
		// The URL changes with every build, so this exact URL never changes.
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	h.Set("Content-Type", a.ctype)
	body, etag := a.body, a.etag
	if a.gz != nil {
		h.Set("Vary", "Accept-Encoding")
		if acceptsGzip(r) {
			body, etag = a.gz, strings.TrimSuffix(a.etag, `"`)+`-gz"`
			h.Set("Content-Encoding", "gzip")
		}
	}
	h.Set("ETag", etag)
	if etagMatch(r.Header.Get("If-None-Match"), a) {
		h.Del("Content-Encoding")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}
