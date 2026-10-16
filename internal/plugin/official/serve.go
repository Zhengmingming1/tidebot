package official

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// Item is one event a polling trigger found.
type Item map[string]any

// Poller checks one subscription. first is true on the first poll after subscribing:
// pollers then record a baseline and return nothing, so subscribing never floods.
type Poller func(ctx context.Context, cfg map[string]any, st *State, first bool) ([]Item, error)

// Runtime describes one builtin plugin.
type Runtime struct {
	Name     string
	Poll     Poller                                                        // nil for connector-only plugins
	Interval func(cfg map[string]any) time.Duration                        // poll interval for a binding
	Tools    func(cfg map[string]any) []Tool                               // MCP tools (connector), may be nil
	Test     func(ctx context.Context, cfg map[string]any) (string, error) // "send a test" / dry run
}

var runtimes = map[string]*Runtime{}

func register(r *Runtime) { runtimes[r.Name] = r }

// Main is the entry point for `tidebot __plugin <name> plugin|mcp`. It returns the exit code.
func Main(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: __plugin <name> plugin|mcp")
		return 2
	}
	rt := runtimes[args[0]]
	if rt == nil {
		fmt.Fprintln(os.Stderr, "unknown builtin plugin", args[0])
		return 2
	}
	switch args[1] {
	case "mcp":
		cfg := map[string]any{}
		_ = json.Unmarshal([]byte(os.Getenv("TIDEBOT_PLUGIN_CONFIG")), &cfg)
		var tools []Tool
		if rt.Tools != nil {
			tools = rt.Tools(cfg)
		}
		if err := ServeMCP(os.Stdin, os.Stdout, "tidebot-"+rt.Name, "1.0.0", tools); err != nil {
			return 1
		}
		return 0
	case "plugin":
		s := &server{rt: rt, conn: NewConn(os.Stdout), subs: map[string]context.CancelFunc{}, cfg: map[string]any{}}
		s.dataDir = os.Getenv("TIDEBOT_PLUGIN_DATA")
		_ = s.conn.Serve(os.Stdin, s.handle)
		s.stopAll()
		return 0
	}
	return 2
}

type server struct {
	rt      *Runtime
	conn    *Conn
	mu      sync.Mutex
	cfg     map[string]any
	dataDir string
	subs    map[string]context.CancelFunc
}

var reBinding = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

func (s *server) handle(method string, params json.RawMessage) (any, error) {
	switch method {
	case "plugin.activate":
		var p struct {
			Config  map[string]any `json:"config"`
			DataDir string         `json:"data_dir"`
		}
		_ = json.Unmarshal(params, &p)
		s.mu.Lock()
		if p.Config != nil {
			s.cfg = p.Config
		}
		if p.DataDir != "" {
			s.dataDir = p.DataDir
		}
		s.mu.Unlock()
		return map[string]any{"ok": true, "name": s.rt.Name, "polls": s.rt.Poll != nil}, nil
	case "plugin.describe":
		var tools []string
		if s.rt.Tools != nil {
			for _, t := range s.rt.Tools(s.config()) {
				tools = append(tools, t.Name)
			}
		}
		return map[string]any{"tools": tools, "polls": s.rt.Poll != nil}, nil
	case "plugin.shutdown":
		s.stopAll()
		go func() { time.Sleep(100 * time.Millisecond); os.Exit(0) }()
		return map[string]bool{"ok": true}, nil
	case "trigger.subscribe":
		var p struct {
			BindingID string         `json:"binding_id"`
			Config    map[string]any `json:"config"`
		}
		_ = json.Unmarshal(params, &p)
		if s.rt.Poll == nil {
			return nil, errors.New("this plugin has no triggers")
		}
		if !reBinding.MatchString(p.BindingID) {
			return nil, errors.New("bad binding id")
		}
		s.subscribe(p.BindingID, p.Config)
		return map[string]bool{"ok": true}, nil
	case "trigger.unsubscribe":
		var p struct {
			BindingID string `json:"binding_id"`
		}
		_ = json.Unmarshal(params, &p)
		s.mu.Lock()
		if c := s.subs[p.BindingID]; c != nil {
			c()
			delete(s.subs, p.BindingID)
		}
		s.mu.Unlock()
		return map[string]bool{"ok": true}, nil
	case "trigger.preview":
		// Dry run against a config without firing anything or touching saved state.
		var p struct {
			Config map[string]any `json:"config"`
		}
		_ = json.Unmarshal(params, &p)
		if s.rt.Poll == nil {
			return nil, errors.New("this plugin has no triggers")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		items, err := s.rt.Poll(ctx, merge(s.config(), p.Config), &State{preview: true}, false)
		if err != nil {
			return nil, err
		}
		if len(items) > 5 {
			items = items[:5]
		}
		return map[string]any{"items": items}, nil
	case "plugin.test":
		if s.rt.Test == nil {
			return nil, errors.New("nothing to test")
		}
		var p struct {
			Config map[string]any `json:"config"`
		}
		_ = json.Unmarshal(params, &p)
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		out, err := s.rt.Test(ctx, merge(s.config(), p.Config))
		if err != nil {
			return nil, err
		}
		return map[string]string{"result": out}, nil
	}
	return nil, errNoMethod
}

func (s *server) config() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

func merge(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func (s *server) subscribe(bid string, bcfg map[string]any) {
	s.mu.Lock()
	if c := s.subs[bid]; c != nil {
		c()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.subs[bid] = cancel
	dir := s.dataDir
	s.mu.Unlock()
	st := loadState(dir, bid)
	go func() {
		first := !st.Seeded
		for {
			cfg := merge(s.config(), bcfg)
			pctx, pcancel := context.WithTimeout(ctx, 60*time.Second)
			items, err := s.rt.Poll(pctx, cfg, st, first)
			pcancel()
			if err != nil {
				s.log("warn", fmt.Sprintf("poll %s: %v", bid, err))
			} else {
				if first {
					st.Seeded = true
				}
				first = false
				st.save()
				for _, it := range items {
					it["binding_id"] = bid
					if err := s.conn.Call("trigger.fire", map[string]any{"trigger_id": bid, "payload": it}, nil); err != nil {
						s.log("warn", "fire: "+err.Error())
					}
				}
			}
			iv := 30 * time.Minute
			if s.rt.Interval != nil {
				iv = s.rt.Interval(cfg)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(iv):
			}
		}
	}()
}

func (s *server) stopAll() {
	s.mu.Lock()
	for k, c := range s.subs {
		c()
		delete(s.subs, k)
	}
	s.mu.Unlock()
}

func (s *server) log(level, m string) {
	_ = s.conn.Notify("log.write", map[string]string{"level": level, "message": m})
}

// minutes reads an interval in minutes with a floor.
func minutes(cfg map[string]any, key string, def, min float64) time.Duration {
	v := num(cfg, key, def)
	if v < min {
		v = min
	}
	return time.Duration(v * float64(time.Minute))
}

// State is a binding's persisted poll state (seen ids, validators, last content).
type State struct {
	Seeded       bool              `json:"seeded"`
	Seen         []string          `json:"seen,omitempty"` // newest last, capped
	ETag         string            `json:"etag,omitempty"`
	LastModified string            `json:"last_modified,omitempty"`
	Text         string            `json:"text,omitempty"`
	UID          uint32            `json:"uid,omitempty"`
	UIDValidity  uint32            `json:"uid_validity,omitempty"`
	Extra        map[string]string `json:"extra,omitempty"`
	path         string
	preview      bool
}

const maxSeen = 2000

func loadState(dir, bid string) *State {
	st := &State{}
	if dir == "" {
		return st
	}
	st.path = filepath.Join(dir, "state-"+bid+".json")
	if b, err := os.ReadFile(st.path); err == nil {
		_ = json.Unmarshal(b, st)
	}
	return st
}

func (st *State) save() {
	if st.path == "" || st.preview {
		return
	}
	if len(st.Seen) > maxSeen {
		st.Seen = st.Seen[len(st.Seen)-maxSeen:]
	}
	b, _ := json.Marshal(st)
	tmp := st.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, st.path)
	}
}

func (st *State) seen(id string) bool {
	for _, x := range st.Seen {
		if x == id {
			return true
		}
	}
	return false
}

// --- HTTP ----------------------------------------------------------------

// UserAgent identifies tidebot's fetchers.
var UserAgent = "tidebot-plugin/1.0 (+https://github.com/Zhengmingming1/tidebot-releases)"

var httpClient = &http.Client{Timeout: 30 * time.Second}

const maxBody = 5 << 20

func readBody(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxBody {
		return nil, errors.New("response larger than 5 MB")
	}
	return b, nil
}
