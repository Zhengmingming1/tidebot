package webui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/Zhengmingming1/tidebot/internal/skillpkg"
)

// ---------- skill upload ----------

var stageIDRe = regexp.MustCompile(`^[a-f0-9]{16}$`)

func (s *Server) stagingDir() string { return filepath.Join(s.deps.Cfg.DataDir, "skill-staging") }

// cleanStaging removes uploads older than an hour.
func (s *Server) cleanStaging() {
	ents, _ := os.ReadDir(s.stagingDir())
	for _, e := range ents {
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > time.Hour {
			_ = os.RemoveAll(filepath.Join(s.stagingDir(), e.Name()))
		}
	}
}

type skillPreview struct {
	StageID     string         `json:"stage_id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Files       int            `json:"files"`
	Existing    *skillExisting `json:"existing,omitempty"`
}

type skillExisting struct {
	ID        string `json:"id"`
	VersionNo int    `json:"version_no"`
}

// handleSkillUpload accepts one archive in "file" (.zip/.skill/.tar.gz) or a folder upload as
// several "files" parts with matching "paths" (relative paths from webkitRelativePath).
func (s *Server) handleSkillUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "POST only")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, skillpkg.MaxTotalBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeErr(w, 400, "upload too large or malformed")
		return
	}
	defer r.MultipartForm.RemoveAll()
	s.cleanStaging()
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	sid := hex.EncodeToString(buf)
	dest := filepath.Join(s.stagingDir(), sid)
	if err := os.MkdirAll(dest, 0o700); err != nil {
		writeErr(w, 500, "cannot stage upload")
		return
	}
	fail := func(code int, msg string) {
		_ = os.RemoveAll(dest)
		writeErr(w, code, msg)
	}
	if fhs := r.MultipartForm.File["file"]; len(fhs) == 1 {
		f, err := fhs[0].Open()
		if err != nil {
			fail(400, "cannot read upload")
			return
		}
		data, err := io.ReadAll(io.LimitReader(f, skillpkg.MaxArchiveBytes+1))
		f.Close()
		if err != nil {
			fail(400, "cannot read upload")
			return
		}
		if err := skillpkg.Extract(fhs[0].Filename, data, dest); err != nil {
			fail(400, err.Error())
			return
		}
	} else if fhs := r.MultipartForm.File["files"]; len(fhs) > 0 {
		paths := r.MultipartForm.Value["paths"]
		if len(paths) != len(fhs) {
			fail(400, "paths and files do not match")
			return
		}
		var entries []skillpkg.FileEntry
		var closers []io.Closer
		for i, fh := range fhs {
			f, err := fh.Open()
			if err != nil {
				fail(400, "cannot read upload")
				return
			}
			closers = append(closers, f)
			entries = append(entries, skillpkg.FileEntry{Path: paths[i], Data: f})
		}
		err := skillpkg.WriteFiles(entries, dest)
		for _, c := range closers {
			c.Close()
		}
		if err != nil {
			fail(400, err.Error())
			return
		}
	} else {
		fail(400, "choose a .zip, .skill or .tar.gz file, or a folder")
		return
	}
	root, err := skillpkg.FindRoot(dest)
	if err != nil {
		fail(400, err.Error())
		return
	}
	md, _ := os.ReadFile(filepath.Join(root, "SKILL.md"))
	meta, err := skillpkg.ParseFrontmatter(md)
	if err != nil {
		fail(400, err.Error())
		return
	}
	p := skillPreview{StageID: sid, Name: meta.Name, Description: meta.Description}
	if p.Name == "" && root != dest {
		p.Name = filepath.Base(root)
	}
	_ = filepath.Walk(root, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() {
			p.Files++
		}
		return nil
	})
	p.Existing = s.findSkill(r.Context(), p.Name)
	_ = os.WriteFile(filepath.Join(dest, ".root"), []byte(root), 0o600)
	writeJSON(w, 200, p)
}

func (s *Server) findSkill(ctx context.Context, name string) *skillExisting {
	if name == "" {
		return nil
	}
	list, err := s.deps.Skills.List(ctx)
	if err != nil {
		return nil
	}
	for _, sk := range list {
		if sk.Name == name {
			e := &skillExisting{ID: sk.ID}
			if sk.CurrentVersion != nil {
				e.VersionNo = sk.CurrentVersion.VersionNo
			}
			return e
		}
	}
	return nil
}

var skillNameRe = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} ._-]{0,63}$`)

// importStaged commits a staged upload. mode "version" is required when the name exists.
func (s *Server) importStaged(w http.ResponseWriter, r *http.Request, stageID, name, mode string) {
	if !stageIDRe.MatchString(stageID) {
		writeErr(w, 400, "bad stage id")
		return
	}
	name = strings.TrimSpace(name)
	if !skillNameRe.MatchString(name) {
		writeErr(w, 400, "skill name: 1-64 letters, digits, spaces, . _ -")
		return
	}
	dest := filepath.Join(s.stagingDir(), stageID)
	rootB, err := os.ReadFile(filepath.Join(dest, ".root"))
	if err != nil {
		writeErr(w, 404, "upload expired; choose the file again")
		return
	}
	root := filepath.Clean(string(rootB))
	if !strings.HasPrefix(root, dest) {
		writeErr(w, 400, "bad stage")
		return
	}
	if ex := s.findSkill(r.Context(), name); ex != nil && mode != "version" {
		writeJSON(w, 409, map[string]any{"error": "a skill with this name exists", "code": "exists", "existing": ex})
		return
	}
	_ = os.Remove(filepath.Join(dest, ".root"))
	sk, err := s.deps.Skills.ImportDir(r.Context(), name, root, "user")
	_ = os.RemoveAll(dest)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"id": sk.ID, "name": sk.Name})
}

// ---------- button ids ----------

// slugify makes an ASCII id from a label; non-ASCII labels (e.g. Chinese) fall back to "btn".
func slugify(label string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(label) {
		switch {
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 24 {
		s = strings.Trim(s[:24], "-")
	}
	if s == "" {
		s = "btn"
	}
	return s
}

// NewButtonID returns a unique id: slug of the label, plus a short random suffix if taken.
func NewButtonID(label string, taken map[string]bool) string {
	base := slugify(label)
	if !taken[base] && base != "btn" {
		return base
	}
	for {
		buf := make([]byte, 3)
		_, _ = rand.Read(buf)
		id := base + "-" + hex.EncodeToString(buf)
		if !taken[id] {
			return id
		}
	}
}

// ---------- providers ----------

// ProviderView is what the WebUI sees. The API key is never included, only whether it is
// set and its last 4 characters.
type ProviderView struct {
	Name           string            `json:"name"`
	Preset         string            `json:"preset,omitempty"`
	BaseURL        string            `json:"base_url"`
	AuthEnv        string            `json:"auth_env"`
	Model          string            `json:"model"`
	SmallFastModel string            `json:"small_fast_model"`
	OpusModel      string            `json:"opus_model"`
	SonnetModel    string            `json:"sonnet_model"`
	HaikuModel     string            `json:"haiku_model"`
	ExtraEnv       map[string]string `json:"extra_env"`
	KeySet         bool              `json:"key_set"`
	KeyLast4       string            `json:"key_last4,omitempty"`
	KeySource      string            `json:"key_source,omitempty"` // store | env
	Builtin        bool              `json:"builtin"`
	Default        bool              `json:"default"`
	UsedBy         int               `json:"used_by"`
}

// ProviderInput is a create/update request. APIKey nil = keep; "" with ClearKey = remove.
type ProviderInput struct {
	OriginalName   string            `json:"original_name"`
	Name           string            `json:"name"`
	Preset         string            `json:"preset"`
	BaseURL        string            `json:"base_url"`
	AuthEnv        string            `json:"auth_env"`
	Model          string            `json:"model"`
	SmallFastModel string            `json:"small_fast_model"`
	OpusModel      string            `json:"opus_model"`
	SonnetModel    string            `json:"sonnet_model"`
	HaikuModel     string            `json:"haiku_model"`
	ExtraEnv       map[string]string `json:"extra_env"`
	APIKey         *string           `json:"api_key,omitempty"`
	ClearKey       bool              `json:"clear_key"`
}

// ProviderAdmin is implemented by the app.
type ProviderAdmin interface {
	ListProviders(ctx context.Context) []ProviderView
	Presets() []map[string]any
	SaveProvider(ctx context.Context, in ProviderInput) (ProviderView, error)
	DeleteProvider(ctx context.Context, name string) error
	SetDefaultProvider(ctx context.Context, name string) error
	TestProvider(ctx context.Context, name string) (map[string]any, error)
}

// ValidateBaseURL: empty (provider default) or https; plain http only for localhost.
func ValidateBaseURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return errors.New("base URL must be a full URL like https://api.example.com/anthropic")
	}
	if u.Scheme == "https" {
		return nil
	}
	h := u.Hostname()
	if u.Scheme == "http" && (h == "localhost" || h == "127.0.0.1" || h == "::1") {
		return nil
	}
	return errors.New("base URL must use https (plain http is allowed only for localhost)")
}

var envKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// ValidateExtraEnv refuses secrets-looking or auth/base-url keys in extra env.
func ValidateExtraEnv(m map[string]string) error {
	for k, v := range m {
		if !envKeyRe.MatchString(k) {
			return fmt.Errorf("extra env name %q: use UPPER_CASE", k)
		}
		uk := strings.ToUpper(k)
		if strings.Contains(uk, "KEY") || strings.Contains(uk, "TOKEN") || strings.Contains(uk, "SECRET") || strings.Contains(uk, "PASSWORD") || uk == "ANTHROPIC_BASE_URL" {
			return fmt.Errorf("%s can't go in extra env; use the API key field / base URL", k)
		}
		if len(v) > 512 {
			return fmt.Errorf("%s is too long", k)
		}
	}
	return nil
}

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	pa := s.deps.Providers
	if pa == nil {
		writeErr(w, 404, "not available")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, map[string]any{"providers": pa.ListProviders(r.Context()), "presets": pa.Presets()})
	case http.MethodPost:
		var in ProviderInput
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		v, err := pa.SaveProvider(r.Context(), in)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, v)
	default:
		writeErr(w, 405, "method")
	}
}

func (s *Server) handleProviderSub(w http.ResponseWriter, r *http.Request) {
	pa := s.deps.Providers
	if pa == nil {
		writeErr(w, 404, "not available")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/providers/"), "/")
	name, _ := url.PathUnescape(parts[0])
	var err error
	var out any = map[string]bool{"ok": true}
	switch {
	case len(parts) == 1 && r.Method == http.MethodDelete:
		err = pa.DeleteProvider(r.Context(), name)
	case len(parts) == 2 && parts[1] == "default" && r.Method == http.MethodPost:
		err = pa.SetDefaultProvider(r.Context(), name)
	case len(parts) == 2 && parts[1] == "test" && r.Method == http.MethodPost:
		out, err = pa.TestProvider(r.Context(), name)
	default:
		writeErr(w, 404, "not found")
		return
	}
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, out)
}

// handlePluginUpload: POST /api/plugins/upload (multipart "file": .zip/.tar.gz with plugin.yaml).
// The package is staged as an unapproved version; the response lists requested permissions.
func (s *Server) handlePluginUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || s.deps.Ext == nil {
		writeErr(w, 405, "POST only")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, skillpkg.MaxArchiveBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeErr(w, 400, "upload too large or malformed")
		return
	}
	defer r.MultipartForm.RemoveAll()
	fhs := r.MultipartForm.File["file"]
	if len(fhs) != 1 {
		writeErr(w, 400, "choose one .zip or .tar.gz plugin package")
		return
	}
	f, err := fhs[0].Open()
	if err != nil {
		writeErr(w, 400, "cannot read upload")
		return
	}
	data, _ := io.ReadAll(io.LimitReader(f, skillpkg.MaxArchiveBytes+1))
	f.Close()
	tmp, err := os.MkdirTemp("", "tidebot-plugin-")
	if err != nil {
		writeErr(w, 500, "cannot stage upload")
		return
	}
	defer os.RemoveAll(tmp)
	if err := skillpkg.Extract(fhs[0].Filename, data, tmp); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	root, err := skillpkg.FindFile(tmp, "plugin.yaml")
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	raw, _ := json.Marshal(map[string]string{"dir": root})
	out, err := s.deps.Ext(r.Context(), "plugins.stage", raw)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, out)
}
