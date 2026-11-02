package webui

import (
	"encoding/json"

	"io"

	"net/http"

	"regexp"

	"strings"
)

var (
	reUSDCap   = regexp.MustCompile(`daily (USD|CNY) cap reached \(([\d.]+)/([\d.]+)\)`)
	reTokenCap = regexp.MustCompile(`daily token cap reached \((\d+)/(\d+)\)`)
	reUSDHeld  = regexp.MustCompile(`daily (USD|CNY) cap held by (\d+) running \(([\d.]+)\+([\d.]+)/([\d.]+)\)`)
)

// parseHeldDetail reads a "held by runs going" refusal: how much they hold and how many.
func parseHeldDetail(d string) (held, n string) {
	if m := reUSDHeld.FindStringSubmatch(d); m != nil {
		return curSym(m[1]) + m[4], m[2]
	}
	return "", ""
}

func curSym(c string) string {
	if c == "CNY" {
		return "¥"
	}
	return "$"
}

// parseBudgetDetail turns the stored English breach text into a code + numbers.
func parseBudgetDetail(d string) (code, used, limit string) {
	if m := reUSDCap.FindStringSubmatch(d); m != nil {
		sym := "$"
		if m[1] == "CNY" {
			sym = "¥"
		}
		return "usd_cap", sym + m[2], sym + m[3]
	}
	if m := reTokenCap.FindStringSubmatch(d); m != nil {
		return "token_cap", m[1], m[2]
	}
	if m := reUSDHeld.FindStringSubmatch(d); m != nil {
		return "held", curSym(m[1]) + m[3], curSym(m[1]) + m[5]
	}
	if strings.Contains(d, "remaining daily budget is zero") {
		return "zero", "", ""
	}
	return "", "", ""
}

func (s *Server) handleExt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.deps.Ext == nil {
		writeErr(w, http.StatusNotFound, "not available")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	out, err := s.deps.Ext(r.Context(), strings.TrimPrefix(r.URL.Path, "/api/rpc/"), json.RawMessage(raw))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": out})
}

// answerSameDTO points the run page at the earlier run with the same final answer.
type answerSameDTO struct {
	RunID string `json:"run_id"`
	At    string `json:"at"`
}
