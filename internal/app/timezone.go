package app

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/config"
)

// Settings › General › Scheduling timezone.
//
// One zone decides when cron fires, when daily budgets reset, quiet hours and how
// every time in the WebUI and tray is shown. Empty setting = automatic (config file,
// $TIDEBOT_TIMEZONE, $TZ, then the system zone). Changing it applies at once and
// moves upcoming scheduled runs (Scheduler.Realign); past-due runs keep following
// the missed-run setting.
//
// Idea from Uptime Kuma (src/components/settings/General.vue + src/util-frontend.js
// timezoneList: an "Auto: <guessed>" first option, zones labelled and sorted by their
// current UTC offset, a server timezone separate from the browser's). The search,
// local-time preview and realigning runs are tidebot's own.

const setScheduleTZ = "schedule.timezone"

// Loc is the scheduling timezone in effect.
func (a *App) Loc() *time.Location {
	if l := a.loc.Load(); l != nil {
		return l
	}
	return a.Cfg.Location()
}

// storedZone is the zone chosen in the WebUI ("" = automatic).
func (a *App) storedZone(ctx context.Context) string {
	z := ""
	a.loadJSONSetting(ctx, setScheduleTZ, &z)
	if !config.KnownZone(z) {
		return ""
	}
	return z
}

// resolveLoc is the stored zone, else the automatic one.
func (a *App) resolveLoc(ctx context.Context) *time.Location {
	if z := a.storedZone(ctx); z != "" {
		if l, err := time.LoadLocation(z); err == nil {
			return l
		}
	}
	return a.Cfg.Location()
}

// applyLoc makes loc the zone for the scheduler, budgets, notifications and the WebUI.
func (a *App) applyLoc(loc *time.Location) {
	a.loc.Store(loc)
	if a.Sched != nil {
		a.Sched.SetLocation(loc)
	}
}

// TZZone is one picker entry: the IANA name and its UTC offset right now (minutes).
type TZZone struct {
	ID     string `json:"id"`
	Offset int    `json:"offset"`
}

// TZView is what Settings › General shows.
type TZView struct {
	Zone       string   `json:"zone"`        // chosen in the WebUI, "" = automatic
	Active     string   `json:"active"`      // in effect
	Auto       string   `json:"auto"`        // what automatic resolves to
	AutoOffset int      `json:"auto_offset"` // its UTC offset now (minutes)
	Now        string   `json:"now"`         // server time (RFC3339, UTC) for the preview
	Zones      []TZZone `json:"zones,omitempty"`
	Moved      int      `json:"moved,omitempty"` // upcoming runs moved by this change
}

func (a *App) tzView(ctx context.Context, withZones bool) TZView {
	now := time.Now()
	v := TZView{Zone: a.storedZone(ctx), Active: a.Loc().String(), Now: now.UTC().Format(time.RFC3339)}
	auto := a.Cfg.Location()
	_, off := now.In(auto).Zone()
	v.Auto, v.AutoOffset = auto.String(), off/60
	if withZones {
		v.Zones = tzZones(now)
	}
	return v
}

// tzZones lists config.Zones with their current offsets, west to east, then by name.
func tzZones(now time.Time) []TZZone {
	out := make([]TZZone, 0, len(config.Zones))
	for _, z := range config.Zones {
		l, err := time.LoadLocation(z)
		if err != nil {
			continue
		}
		_, off := now.In(l).Zone()
		out = append(out, TZZone{ID: z, Offset: off / 60})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Offset != out[j].Offset {
			return out[i].Offset < out[j].Offset
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// setZone stores zone ("" = automatic), applies it and realigns upcoming runs.
func (a *App) setZone(ctx context.Context, zone string) (int, error) {
	if zone != "" && !config.KnownZone(zone) {
		return 0, errors.New("unknown timezone")
	}
	if err := a.saveJSONSetting(ctx, setScheduleTZ, zone); err != nil {
		return 0, err
	}
	before := a.Loc().String()
	loc := a.resolveLoc(ctx)
	a.applyLoc(loc)
	if before == loc.String() {
		return 0, nil
	}
	n, err := a.Sched.Realign(ctx, time.Now())
	if err != nil {
		return 0, err
	}
	a.Log.Info("scheduling timezone changed", "from", before, "to", loc.String(), "moved", n)
	return n, nil
}

func (a *App) timezoneRPC() map[string]rpcFunc {
	return map[string]rpcFunc{
		"timezone.get": func(ctx context.Context, _ json.RawMessage) (any, error) { return a.tzView(ctx, true), nil },
		"timezone.set": func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[struct {
				Zone *string `json:"zone"`
			}](raw)
			if err != nil || in.Zone == nil {
				return nil, errors.New("zone is required (empty = automatic)")
			}
			n, err := a.setZone(ctx, *in.Zone)
			if err != nil {
				return nil, err
			}
			v := a.tzView(ctx, false)
			v.Moved = n
			return v, nil
		},
	}
}
