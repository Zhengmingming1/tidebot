package webui

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Zhengmingming1/tidebot/internal/skillpkg"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

// exportSlug turns a skill name into a portable folder / file name: letters and digits
// (any script) stay, everything else becomes "-". Windows-reserved characters, spaces and
// dots never survive, so the name unpacks the same on macOS, Windows and Linux.
func exportSlug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.TrimSpace(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.TrimRight(b.String(), "-")
	if rs := []rune(s); len(rs) > 60 {
		s = strings.TrimRight(string(rs[:60]), "-")
	}
	if s == "" {
		s = "skill"
	}
	if winReserved[strings.ToUpper(s)] {
		s += "-skill" // CON, NUL, COM1… can't be folder names on Windows
	}
	return s
}

var winReserved = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true}

// asciiName is the Content-Disposition fallback for clients without filename*.
func asciiName(s string) string {
	out := strings.Map(func(r rune) rune {
		if r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_.", r)) {
			return r
		}
		return '_'
	}, s)
	return out
}

// skillZip packs files under one top folder, sorted, with a fixed time, so the same
// version always gives the same bytes. Contents are stored as they are (no line-ending
// conversion): what was imported on Windows comes back byte for byte.
func skillZip(folder string, files map[string][]byte, mod time.Time) ([]byte, error) {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range paths {
		rel, err := skillpkg.CleanRel(p)
		if err != nil {
			continue // never write an entry the importer would refuse
		}
		h := &zip.FileHeader{Name: folder + "/" + rel, Method: zip.Deflate, Modified: mod}
		h.SetMode(0o644)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(files[p]); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// GET /api/skills/<id>/export[?v=<version>] → <name>-v<n>.zip of that version (current by
// default). The archive re-imports through Skills › Import as is.
func (s *Server) handleSkillExport(w http.ResponseWriter, r *http.Request, skillID string) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET only")
		return
	}
	ctx := r.Context()
	var name string
	if err := s.deps.DB.QueryRowContext(ctx, `SELECT name FROM skills WHERE id=?`, skillID).Scan(&name); err != nil {
		writeErr(w, 404, "skill not found")
		return
	}
	var ver *version.Version
	if v := r.URL.Query().Get("v"); v != "" {
		if !s.skillVersionOf(r, skillID, v) {
			writeErr(w, 400, "v must be a version of this skill")
			return
		}
		ver, _ = s.deps.Ver.GetVersion(ctx, v)
	} else {
		ver, _ = s.deps.Ver.CurrentVersion(ctx, version.EntitySkill, skillID)
	}
	if ver == nil {
		writeErr(w, 404, "skill has no version")
		return
	}
	files, err := s.deps.Skills.Files(ctx, ver.ID)
	if err != nil {
		writeErr(w, 500, "cannot read version files")
		return
	}
	mod, err := time.Parse(time.RFC3339, ver.CreatedAt)
	if err != nil || mod.Year() < 1980 {
		mod = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	slug := exportSlug(name)
	data, err := skillZip(slug, files, mod.UTC())
	if err != nil {
		writeErr(w, 500, "cannot build archive")
		return
	}
	if len(data) > maxSaveBytes {
		writeErr(w, 413, "skill too large to export")
		return
	}
	file := slug + "-v" + strconv.Itoa(ver.VersionNo) + ".zip"
	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", `attachment; filename="`+asciiName(file)+`"; filename*=UTF-8''`+url.PathEscape(file))
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(200)
	_, _ = w.Write(data)
}
