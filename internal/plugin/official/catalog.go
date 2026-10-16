// Package official holds tidebot's built-in plugin catalog and the runtimes for those
// plugins (RSS, web monitor, WeCom, DingTalk, email, daily digest pack). The runtimes are
// compiled into the tidebot binary and started as `tidebot __plugin <name> plugin|mcp`,
// speaking the same JSON-RPC stdio protocol (or MCP) as any third-party plugin.
package official

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"sync"

	"github.com/Zhengmingming1/tidebot/internal/plugin"
)

//go:embed all:catalog
var catalogFS embed.FS

// Entry is one catalog card.
type Entry struct {
	ID       string           `json:"id"`
	Category string           `json:"category"` // trigger | connector | pack
	Icon     string           `json:"icon"`
	Hash     string           `json:"hash"`
	Manifest *plugin.Manifest `json:"manifest"`
}

var meta = map[string][2]string{
	"rss-feed":     {"trigger", "rss"},
	"web-monitor":  {"trigger", "eye"},
	"email":        {"connector", "mail"},
	"wecom":        {"connector", "chat"},
	"dingtalk":     {"connector", "bell"},
	"daily-digest": {"pack", "book"},
}

// Order is the display order.
var Order = []string{"rss-feed", "web-monitor", "wecom", "dingtalk", "email", "daily-digest"}

var (
	once    sync.Once
	entries map[string]*Entry
	loadErr error
)

func load() {
	entries = map[string]*Entry{}
	for _, id := range Order {
		dir, err := extract(id)
		if err != nil {
			loadErr = err
			return
		}
		m, err := plugin.ParseManifest(dir)
		if err != nil {
			os.RemoveAll(dir)
			loadErr = errors.New(id + ": " + err.Error())
			return
		}
		h, err := plugin.HashDir(dir)
		os.RemoveAll(dir)
		if err != nil {
			loadErr = err
			return
		}
		entries[id] = &Entry{ID: id, Category: meta[id][0], Icon: meta[id][1], Hash: h, Manifest: m}
	}
}

func init() {
	plugin.OfficialVerifier = func(id, hash string) bool {
		e, err := Get(id)
		return err == nil && e.Hash == hash
	}
}

// List returns the catalog in display order.
func List() ([]Entry, error) {
	once.Do(load)
	if loadErr != nil {
		return nil, loadErr
	}
	out := make([]Entry, 0, len(Order))
	for _, id := range Order {
		out = append(out, *entries[id])
	}
	return out, nil
}

// Get returns one catalog entry.
func Get(id string) (*Entry, error) {
	once.Do(load)
	if loadErr != nil {
		return nil, loadErr
	}
	e := entries[id]
	if e == nil {
		return nil, errors.New("not an official plugin")
	}
	return e, nil
}

// extract writes one catalog plugin to a fresh temp dir (caller removes it).
func extract(id string) (string, error) {
	root := path.Join("catalog", id)
	if _, err := fs.Stat(catalogFS, root); err != nil {
		return "", errors.New("not an official plugin")
	}
	dir, err := os.MkdirTemp("", "tidebot-official-")
	if err != nil {
		return "", err
	}
	var files []string
	err = fs.WalkDir(catalogFS, root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return err
	})
	sort.Strings(files)
	for _, f := range files {
		if err != nil {
			break
		}
		var b []byte
		b, err = catalogFS.ReadFile(f)
		if err != nil {
			break
		}
		rel, _ := filepath.Rel(filepath.FromSlash(root), filepath.FromSlash(f))
		dst := filepath.Join(dir, rel)
		if err = os.MkdirAll(filepath.Dir(dst), 0o755); err == nil {
			err = os.WriteFile(dst, b, 0o644)
		}
	}
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// Stage copies an official plugin into the store as an unapproved version. Permissions
// still go through the normal item-by-item approval; nothing is granted here.
func Stage(ctx context.Context, st *plugin.Store, id string) (*plugin.VersionInfo, error) {
	e, err := Get(id)
	if err != nil {
		return nil, err
	}
	dir, err := extract(id)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	return st.StageSource(ctx, dir, e.Hash, plugin.SourceOfficial)
}

// PackFiles returns the workflow pack files of an official pack plugin (rel path → bytes).
func PackFiles(id string) (map[string][]byte, error) {
	root := path.Join("catalog", id, "pack")
	out := map[string][]byte{}
	err := fs.WalkDir(catalogFS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := catalogFS.ReadFile(p)
		if err == nil {
			out[p[len(root)+1:]] = b
		}
		return err
	})
	return out, err
}

func sha(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
