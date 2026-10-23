package webui

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// JSON answers from /api/ are gzipped when the client accepts it and the body is at least
// gzipMinJSON bytes. Below that the gzip header and the extra CPU cost more than they save.
// Only writeJSON compresses: SSE streams, file downloads and error pages from the auth layer
// go out as before. The desktop window drops Accept-Encoding, so it always gets identity.
const gzipMinJSON = 1024

// apiGzip marks a response whose client accepts gzip. It adds nothing else: Flush and Unwrap
// pass through, so SSE handlers and http.ResponseController see the real writer.
type apiGzip struct{ http.ResponseWriter }

func (g *apiGzip) Unwrap() http.ResponseWriter { return g.ResponseWriter }

func (g *apiGzip) Flush() {
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// withAPIGzip wraps /api/ responses for clients that accept gzip.
func withAPIGzip(w http.ResponseWriter, r *http.Request) http.ResponseWriter {
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		return w
	}
	w.Header().Add("Vary", "Accept-Encoding")
	if !acceptsGzip(r) {
		return w
	}
	return &apiGzip{w}
}

var (
	jsonBufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}
	gzipPool    = sync.Pool{New: func() any {
		zw, _ := gzip.NewWriterLevel(nil, gzip.DefaultCompression)
		return zw
	}}
)

// maxPooledBuf keeps one huge answer (a big export) from pinning its buffer in the pool.
const maxPooledBuf = 256 << 10

func writeJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	buf := jsonBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer func() {
		if buf.Cap() <= maxPooledBuf {
			jsonBufPool.Put(buf)
		}
	}()
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)

	if _, ok := w.(*apiGzip); ok && buf.Len() >= gzipMinJSON && bodyAllowed(status) &&
		h.Get("Content-Encoding") == "" && !strings.Contains(h.Get("Cache-Control"), "no-transform") {
		h.Set("Content-Encoding", "gzip")
		h.Del("Content-Length")
		w.WriteHeader(status)
		zw := gzipPool.Get().(*gzip.Writer)
		zw.Reset(w)
		_, _ = zw.Write(buf.Bytes())
		_ = zw.Close()
		zw.Reset(nil)
		gzipPool.Put(zw)
		return
	}
	if bodyAllowed(status) {
		h.Set("Content-Length", strconv.Itoa(buf.Len()))
	}
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func bodyAllowed(status int) bool {
	return status >= 200 && status != http.StatusNoContent && status != http.StatusNotModified
}
