package app

import "context"

// capabilityTitle / capabilitySkills: capability packs arrive with P1; until
// then no capability exists.
func (a *App) capabilityTitle(ctx context.Context, id string) (string, bool) { return "", false }
func (a *App) capabilitySkills(ctx context.Context, id string) []string      { return nil }

// chatToolEnv / endChatToolToken: the capability tools' one-time token (P1).
func (a *App) chatToolEnv(sid string) map[string]string { return nil }
func (a *App) endChatToolToken(sid string)              {}
