package app

import (
	"context"
	"errors"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func capabilityMCPReferencesSecret(lock capability.Lock, name string) bool {
	ref := "$" + "{secret:" + name + "}"
	for _, c := range lock.Components {
		if c.MCP == nil {
			continue
		}
		for _, v := range c.MCP.Env {
			if v == ref {
				return true
			}
		}
		for _, v := range c.MCP.Headers {
			if v == ref {
				return true
			}
		}
	}
	return false
}
func (a *App) CapabilitySecretStatus(ctx context.Context, id string) ([]webui.CapabilitySecretStatus, error) {
	r, e := a.caps().Get(ctx, id)
	if e != nil {
		return nil, e
	}
	out := []webui.CapabilitySecretStatus{}
	for _, s := range r.Lock.Requires.Secrets {
		set := false
		if a.Secrets != nil {
			v, e := a.Secrets.Get("cap:" + id + ":" + s.Name)
			set = e == nil && v != ""
		}
		out = append(out, webui.CapabilitySecretStatus{Name: s.Name, Optional: s.Optional, Set: set})
	}
	return out, nil
}
func (a *App) SetCapabilitySecret(ctx context.Context, id, name, value string) error {
	r, e := a.caps().Get(ctx, id)
	if e != nil {
		return e
	}
	if name == "" || strings.ContainsAny(name, ":\r\n\x00") || !declaredCapabilitySecret(r.Lock, name) {
		return errors.New("secret is not declared by this installed capability")
	}
	if len(value) == 0 || len(value) > 16384 || strings.ContainsAny(value, "\r\n\x00") {
		return errors.New("secret must be a nonempty single-line value up to 16384 bytes")
	}
	if a.Secrets == nil {
		return errors.New("secret store unavailable")
	}
	if e = a.rememberCapabilityCredential(ctx, id, "secret", name); e != nil {
		return e
	}
	return a.Secrets.Set("cap:"+id+":"+name, value)
}
