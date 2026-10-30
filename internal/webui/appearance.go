package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
)

// Appearance is the WebUI look: one of three themes, each in a light and a dark palette, and
// whether the palette follows the OS ("system") or is fixed. It is stored server-side (setting
// "appearance") rather than in localStorage: the desktop window's origin can change between
// launches, and the same choice should hold in the browser and in the app.
//
// The page gets it as <html data-theme data-mode> in the index response itself, so the right
// colours are in the first paint with no script (CSP allows none inline anyway); "system" is
// resolved by a prefers-color-scheme media query in styles.css.
type Appearance struct {
	Theme string `json:"theme"`
	Mode  string `json:"mode"`
}

var (
	themes      = map[string]bool{"graphite": true, "tide": true, "midnight": true}
	colorModes  = map[string]bool{"system": true, "light": true, "dark": true}
	defaultLook = Appearance{Theme: "graphite", Mode: "system"}
)

func (a Appearance) valid() bool { return themes[a.Theme] && colorModes[a.Mode] }

// Appearance returns the saved look, or the default (graphite, follow the system).
func (s *Server) Appearance(ctx context.Context) Appearance {
	if s.deps.Auth == nil {
		return defaultLook
	}
	raw, err := s.deps.Auth.GetSetting(ctx, "appearance")
	if err != nil || raw == "" {
		return defaultLook
	}
	var a Appearance
	if json.Unmarshal([]byte(raw), &a) != nil || !a.valid() {
		return defaultLook
	}
	return a
}

func (s *Server) handleAppearance(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, s.Appearance(r.Context()))
	case http.MethodPut, http.MethodPost:
		var a Appearance
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&a); err != nil || !a.valid() {
			writeErr(w, 400, "theme must be graphite, tide or midnight; mode must be system, light or dark")
			return
		}
		bs, _ := json.Marshal(a)
		if err := s.deps.Auth.SetSetting(r.Context(), "appearance", string(bs)); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, a)
	default:
		writeErr(w, 405, "method not allowed")
	}
}

var htmlOpen = []byte(`<html lang="en">`)

// withAppearance puts the look on the <html> tag of index.html. Values come from the
// whitelist above and are escaped all the same.
func withAppearance(index []byte, a Appearance) []byte {
	if !a.valid() {
		a = defaultLook
	}
	attr := []byte(`<html lang="en" data-theme="` + html.EscapeString(a.Theme) + `" data-mode="` + html.EscapeString(a.Mode) + `">`)
	return bytes.Replace(index, htmlOpen, attr, 1)
}

// windowBG is each theme's page background (--bg in styles.css), light and dark, for the
// desktop window behind the webview, so it never flashes white before the page paints.
var windowBG = map[string][2][3]uint8{
	"graphite": {{0xff, 0xff, 0xff}, {0x11, 0x11, 0x13}},
	"tide":     {{0xf4, 0xf7, 0xf8}, {0x0b, 0x17, 0x1e}},
	"midnight": {{0xf7, 0xf7, 0xf8}, {0x0d, 0x0d, 0x10}},
}

// WindowBackground is the colour the desktop app paints its window with before the page
// loads: the saved theme's background in the saved mode ("system" uses osDark).
func (s *Server) WindowBackground(ctx context.Context, osDark bool) (r, g, b uint8) {
	a := s.Appearance(ctx)
	dark := a.Mode == "dark" || (a.Mode == "system" && osDark)
	c := windowBG[a.Theme]
	i := 0
	if dark {
		i = 1
	}
	return c[i][0], c[i][1], c[i][2]
}
