package plugin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// Field is one property of a plugin config_schema.
type Field struct {
	Key         string   `json:"key"`
	Type        string   `json:"type"` // string|number|integer|boolean|array
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Enum        []any    `json:"enum,omitempty"`
	ItemsEnum   []any    `json:"items_enum,omitempty"` // array of enum → multi-select
	Default     any      `json:"default,omitempty"`
	Required    bool     `json:"required,omitempty"`
	WriteOnly   bool     `json:"write_only,omitempty"` // stored as a secret, never returned
	MaxLength   int      `json:"max_length,omitempty"`
	Minimum     *float64 `json:"minimum,omitempty"`
	Maximum     *float64 `json:"maximum,omitempty"`
	Order       int      `json:"-"`
}

var reFieldKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// ParseSchema validates the supported JSON Schema subset and returns fields in declaration
// order when "x-order" is given, otherwise sorted by key (YAML maps lose order).
func ParseSchema(sc map[string]any) ([]Field, error) {
	if t, _ := sc["type"].(string); t != "" && t != "object" {
		return nil, errors.New("type must be object")
	}
	props, _ := sc["properties"].(map[string]any)
	if len(props) > 50 {
		return nil, errors.New("at most 50 properties")
	}
	req := map[string]bool{}
	if rl, ok := sc["required"].([]any); ok {
		for _, r := range rl {
			if s, ok := r.(string); ok {
				req[s] = true
			}
		}
	}
	var out []Field
	for k, raw := range props {
		if !reFieldKey.MatchString(k) {
			return nil, fmt.Errorf("property %q: keys are lower_snake_case", k)
		}
		p, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("property %q must be an object", k)
		}
		f := Field{Key: k, Required: req[k], Order: math.MaxInt32}
		f.Type, _ = p["type"].(string)
		f.Title, _ = p["title"].(string)
		f.Description, _ = p["description"].(string)
		f.Enum, _ = p["enum"].([]any)
		f.Default = p["default"]
		f.WriteOnly, _ = p["writeOnly"].(bool)
		if fm, _ := p["format"].(string); fm == "password" {
			f.WriteOnly = true
		}
		if n, ok := num(p["maxLength"]); ok {
			f.MaxLength = int(n)
		}
		if n, ok := num(p["minimum"]); ok {
			f.Minimum = &n
		}
		if n, ok := num(p["maximum"]); ok {
			f.Maximum = &n
		}
		if n, ok := num(p["x-order"]); ok {
			f.Order = int(n)
		}
		switch f.Type {
		case "string", "number", "integer", "boolean":
		case "array":
			it, _ := p["items"].(map[string]any)
			f.ItemsEnum, _ = it["enum"].([]any)
			if len(f.ItemsEnum) == 0 {
				return nil, fmt.Errorf("property %q: arrays need items.enum", k)
			}
		default:
			return nil, fmt.Errorf("property %q: unsupported type %q", k, f.Type)
		}
		if f.WriteOnly && f.Type != "string" {
			return nil, fmt.Errorf("property %q: only strings can be writeOnly", k)
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint64:
		return float64(x), true
	}
	return 0, false
}

func inEnum(v any, enum []any) bool {
	for _, e := range enum {
		if fmt.Sprint(e) == fmt.Sprint(v) {
			return true
		}
	}
	return false
}

// ValidateConfig checks values against fields. Write-only fields may be omitted (keep the stored
// secret) and are returned separately so they can go to the secret store.
func ValidateConfig(fields []Field, values map[string]any) (plain map[string]any, secrets map[string]string, err error) {
	plain, secrets = map[string]any{}, map[string]string{}
	known := map[string]bool{}
	for _, f := range fields {
		known[f.Key] = true
		v, has := values[f.Key]
		if !has || v == nil || v == "" {
			if f.Required && !f.WriteOnly {
				return nil, nil, fmt.Errorf("%s is required", label(f))
			}
			continue
		}
		switch f.Type {
		case "string":
			s, ok := v.(string)
			if !ok {
				return nil, nil, fmt.Errorf("%s must be text", label(f))
			}
			max := f.MaxLength
			if max <= 0 || max > 4000 {
				max = 4000
			}
			if len(s) > max {
				return nil, nil, fmt.Errorf("%s is longer than %d characters", label(f), max)
			}
			if len(f.Enum) > 0 && !inEnum(s, f.Enum) {
				return nil, nil, fmt.Errorf("%s must be one of the listed values", label(f))
			}
			if f.WriteOnly {
				secrets[f.Key] = s
				continue
			}
			plain[f.Key] = s
		case "number", "integer":
			n, ok := num(v)
			if !ok {
				return nil, nil, fmt.Errorf("%s must be a number", label(f))
			}
			if f.Type == "integer" && n != math.Trunc(n) {
				return nil, nil, fmt.Errorf("%s must be a whole number", label(f))
			}
			if (f.Minimum != nil && n < *f.Minimum) || (f.Maximum != nil && n > *f.Maximum) {
				return nil, nil, fmt.Errorf("%s is out of range", label(f))
			}
			if len(f.Enum) > 0 && !inEnum(n, f.Enum) {
				return nil, nil, fmt.Errorf("%s must be one of the listed values", label(f))
			}
			plain[f.Key] = n
		case "boolean":
			b, ok := v.(bool)
			if !ok {
				return nil, nil, fmt.Errorf("%s must be on or off", label(f))
			}
			plain[f.Key] = b
		case "array":
			arr, ok := v.([]any)
			if !ok {
				return nil, nil, fmt.Errorf("%s must be a list", label(f))
			}
			for _, x := range arr {
				if !inEnum(x, f.ItemsEnum) {
					return nil, nil, fmt.Errorf("%s has an unknown choice", label(f))
				}
			}
			plain[f.Key] = arr
		}
	}
	for k := range values {
		if !known[k] {
			return nil, nil, fmt.Errorf("unknown setting %q", k)
		}
	}
	return plain, secrets, nil
}

func label(f Field) string {
	if f.Title != "" {
		return f.Title
	}
	return f.Key
}

func cfgSecretName(key string) string { return "cfg_" + key }

// ConfigView is what the WebUI sees: fields, current plain values and which secrets are set.
type ConfigView struct {
	Fields     []Field         `json:"fields"`
	Values     map[string]any  `json:"values"`
	SecretsSet map[string]bool `json:"secrets_set"`
}

// GetConfig returns the form for the current version. Secret values are never included.
func (s *Store) GetConfig(ctx context.Context, pluginID string) (*ConfigView, error) {
	in, err := s.Get(ctx, pluginID)
	if err != nil || in.Current == nil {
		return nil, errors.New("plugin not installed")
	}
	fields, err := ParseSchema(in.Current.Manifest.ConfigSchema)
	if err != nil {
		return nil, err
	}
	v := &ConfigView{Fields: fields, Values: map[string]any{}, SecretsSet: map[string]bool{}}
	var cj sql.NullString
	_ = s.DB.QueryRowContext(ctx, `SELECT config_json FROM plugins WHERE id=?`, pluginID).Scan(&cj)
	if cj.Valid && cj.String != "" {
		_ = json.Unmarshal([]byte(cj.String), &v.Values)
	}
	for _, f := range fields {
		if f.WriteOnly {
			delete(v.Values, f.Key)
			if s.Secrets != nil {
				if x, err := s.Secrets.Get(secretKey(pluginID, cfgSecretName(f.Key))); err == nil && x != "" {
					v.SecretsSet[f.Key] = true
				}
			}
		}
	}
	return v, nil
}

// SetConfig validates and stores values; write-only values go to the secret store.
func (s *Store) SetConfig(ctx context.Context, pluginID string, values map[string]any) error {
	in, err := s.Get(ctx, pluginID)
	if err != nil || in.Current == nil {
		return errors.New("plugin not installed")
	}
	fields, err := ParseSchema(in.Current.Manifest.ConfigSchema)
	if err != nil {
		return err
	}
	plain, secrets, err := ValidateConfig(fields, values)
	if err != nil {
		return err
	}
	for _, f := range fields {
		if f.WriteOnly && f.Required {
			if _, now := secrets[f.Key]; !now {
				if x, e := s.Secrets.Get(secretKey(pluginID, cfgSecretName(f.Key))); e != nil || x == "" {
					return fmt.Errorf("%s is required", label(f))
				}
			}
		}
	}
	for k, v := range secrets {
		// config secrets are declared by the schema (writeOnly), not by permissions.secrets
		if err := s.Secrets.Set(secretKey(pluginID, cfgSecretName(k)), v); err != nil {
			return err
		}
	}
	b, _ := json.Marshal(plain)
	_, err = s.DB.ExecContext(ctx, `UPDATE plugins SET config_json=? WHERE id=?`, string(b), pluginID)
	return err
}

// ActivationConfig is what the plugin process receives: plain values plus write-only secrets.
func (s *Store) ActivationConfig(ctx context.Context, pluginID string) map[string]any {
	cfg := map[string]any{}
	var cj sql.NullString
	_ = s.DB.QueryRowContext(ctx, `SELECT config_json FROM plugins WHERE id=?`, pluginID).Scan(&cj)
	if cj.Valid {
		_ = json.Unmarshal([]byte(cj.String), &cfg)
	}
	if in, err := s.Get(ctx, pluginID); err == nil && in.Current != nil && s.Secrets != nil {
		if fields, err := ParseSchema(in.Current.Manifest.ConfigSchema); err == nil {
			for _, f := range fields {
				if f.WriteOnly {
					if x, err := s.Secrets.Get(secretKey(pluginID, cfgSecretName(f.Key))); err == nil && x != "" {
						cfg[f.Key] = x
					}
				}
			}
		}
	}
	return cfg
}

// Grant records consent for a subset of the requested permissions (declined ones stay off)
// and makes the version current.
func (s *Store) Grant(ctx context.Context, versionID string, granted []string) error {
	v, err := s.version(ctx, versionID)
	if err != nil {
		return err
	}
	if extra := NewPermissions(v.Requested, granted); len(extra) > 0 {
		return fmt.Errorf("not requested by the plugin: %s", strings.Join(extra, ", "))
	}
	return s.setApproved(ctx, v, granted, true)
}

func (s *Store) setApproved(ctx context.Context, v *VersionInfo, list []string, makeCurrent bool) error {
	if list == nil {
		list = []string{}
	}
	sort.Strings(list)
	b, _ := json.Marshal(list)
	if _, err := s.DB.ExecContext(ctx, `UPDATE plugin_versions SET approved_permissions_json=? WHERE id=?`, string(b), v.ID); err != nil {
		return err
	}
	if makeCurrent {
		_, err := s.DB.ExecContext(ctx, `UPDATE plugins SET current_version_id=? WHERE id=?`, v.ID, v.Manifest.ID)
		return err
	}
	return nil
}

// SetPermission grants or revokes one permission on the current version.
func (s *Store) SetPermission(ctx context.Context, pluginID, perm string, on bool) error {
	in, err := s.Get(ctx, pluginID)
	if err != nil || in.Current == nil {
		return errors.New("plugin not installed")
	}
	v := in.Current
	if !inList(v.Requested, perm) {
		return errors.New("the plugin does not request this permission")
	}
	next := []string{}
	for _, p := range v.Approved {
		if p != perm {
			next = append(next, p)
		}
	}
	if on {
		next = append(next, perm)
	}
	return s.setApproved(ctx, v, next, false)
}

func inList(l []string, x string) bool {
	for _, y := range l {
		if y == x {
			return true
		}
	}
	return false
}

// Restrict returns a copy of the manifest that only carries granted permissions.
func (m *Manifest) Restrict(granted []string) *Manifest {
	c := *m
	g := map[string]bool{}
	for _, p := range granted {
		g[p] = true
	}
	keep := func(prefix string, in []string) []string {
		var out []string
		for _, x := range in {
			if g[prefix+x] {
				out = append(out, x)
			}
		}
		return out
	}
	c.Permissions.Network = keep("network:", m.Permissions.Network)
	c.Permissions.FS.Read = keep("fs.read:", m.Permissions.FS.Read)
	c.Permissions.FS.Write = keep("fs.write:", m.Permissions.FS.Write)
	c.Permissions.HostAPI = keep("host_api:", m.Permissions.HostAPI)
	c.Permissions.Secrets = keep("secret:", m.Permissions.Secrets)
	return &c
}
