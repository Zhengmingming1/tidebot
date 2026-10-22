package webui

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// maxSaveBytes caps what the page may hand the native save dialog (CSV export, run log).
const maxSaveBytes = 64 << 20

// handleDesktopSave lets the desktop app save a file through the native "Save as" dialog.
// The webviews differ: WebView2 downloads blobs itself, WebKitGTK drops them silently in
// the download folder, and WKWebView (macOS) ignores them without a download delegate,
// which Wails v3 doesn't install. GET → {"available": bool}; POST ?name=… with the raw
// bytes → {"path": …} or {"canceled": true}. Outside the desktop app (SaveFile nil) the
// server never writes files for the page: GET says unavailable, POST is 501.
func (s *Server) handleDesktopSave(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, map[string]bool{"available": s.SaveFile != nil})
		return
	case http.MethodPost:
	default:
		writeErr(w, 405, "method")
		return
	}
	if s.SaveFile == nil {
		writeErr(w, 501, "not available in the browser")
		return
	}
	name := saveName(r.URL.Query().Get("name"))
	data, err := io.ReadAll(io.LimitReader(r.Body, maxSaveBytes+1))
	if err != nil {
		writeErr(w, 400, "read body")
		return
	}
	if len(data) > maxSaveBytes {
		writeErr(w, 413, "file too large")
		return
	}
	p, err := s.SaveFile(name)
	if err != nil {
		writeErr(w, 500, "dialog failed")
		return
	}
	if p == "" {
		writeJSON(w, 200, map[string]bool{"canceled": true})
		return
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		writeErr(w, 500, "write failed")
		return
	}
	writeJSON(w, 200, map[string]string{"path": p, "name": filepath.Base(p), "reveal": s.saved.add(p)})
}

// saveName keeps only a plain file name for the dialog's suggestion.
func saveName(n string) string {
	n = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, filepath.Base(strings.ReplaceAll(n, `\`, "/")))
	n = strings.Trim(n, ". ")
	if r := []rune(n); len(r) > 120 {
		n = string(r[len(r)-120:])
	}
	if n == "" {
		n = "tidebot-export"
	}
	return n
}
