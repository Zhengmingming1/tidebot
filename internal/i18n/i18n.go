// Package i18n provides tiny key-based message dictionaries for agentctl (en, zh-CN).
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

//go:embed locales/*.json
var files embed.FS

// Supported lists the available locales.
var Supported = []string{"en", "zh-CN"}

var (
	mu    sync.Mutex
	lang  string
	dicts = map[string]map[string]string{}
)

// Load returns the dictionary for a locale.
func Load(l string) (map[string]string, error) {
	mu.Lock()
	defer mu.Unlock()
	if d, ok := dicts[l]; ok {
		return d, nil
	}
	b, err := files.ReadFile("locales/" + l + ".json")
	if err != nil {
		return nil, err
	}
	d := map[string]string{}
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	dicts[l] = d
	return d, nil
}

// Normalize maps a locale string (zh_CN.UTF-8, zh-Hans, en_US…) or "auto" to a supported one.
func Normalize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "auto" || s == "C" || s == "POSIX" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(s), "zh") {
		return "zh-CN"
	}
	return "en"
}

// Detect picks the locale: TIDEBOT_LANG, then LC_ALL, LC_MESSAGES, LANG; fallback en.
func Detect() string {
	for _, k := range []string{"TIDEBOT_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := Normalize(os.Getenv(k)); v != "" {
			return v
		}
	}
	return "en"
}

// SetLang forces a locale ("" or "auto" = Detect()).
func SetLang(l string) {
	n := Normalize(l)
	if n == "" {
		n = Detect()
	}
	mu.Lock()
	lang = n
	mu.Unlock()
}

func current() string {
	mu.Lock()
	l := lang
	mu.Unlock()
	if l == "" {
		l = Detect()
		mu.Lock()
		lang = l
		mu.Unlock()
	}
	return l
}

// T formats the message for key in the current locale (fmt verbs), falling back to en, then the key.
func T(key string, args ...any) string {
	msg := key
	if d, err := Load(current()); err == nil && d[key] != "" {
		msg = d[key]
	} else if d, err := Load("en"); err == nil && d[key] != "" {
		msg = d[key]
	}
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}
