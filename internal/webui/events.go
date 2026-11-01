package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/auth"
)

// GET /api/events: one small server-sent stream per browser that says when something
// changed, so open pages stop asking /api/rev every few seconds and the bell, tab icon and
// sidebar status follow within a second instead of on the next 30–60 s poll.
//
// Each message is the whole state as three short tokens: rev (the /api/rev token: data
// written + current minute), notif (what the bell shows: notifications, pending
// confirmations, failing channels, read mark) and busy (queued / running runs). The page
// compares them with what it has and reloads only what moved. Nothing else is sent, so the
// stream never carries data a GET couldn't — it is a doorbell, not a feed.
//
// One shared watcher reads SQLite's total_changes() once a second while at least one stream
// is open (an in-memory counter: no table read) and only then counts the bell / busy rows.
// With no stream open it doesn't run at all.

// Package-level knobs (tests shorten them).
var (
	eventsTick      = time.Second
	eventsHeartbeat = 25 * time.Second
)

const (
	// eventsMax caps open streams. The page shares one stream across its tabs (one tab holds
	// it, the others listen), so this is only reached by many browsers or a stuck client; the
	// next one is refused and that page keeps polling as before.
	eventsMax = 16
)

type evState struct {
	Rev   string `json:"rev"`
	Notif string `json:"notif"`
	Busy  string `json:"busy"`
}

type evHub struct {
	mu      sync.Mutex
	subs    map[chan struct{}]struct{}
	stop    chan struct{} // closes the watcher when the last stream leaves
	quit    chan struct{} // closed on server shutdown: every stream ends
	cur     evState
	changes int64
	minute  int64
}

func (h *evHub) quitCh() chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.quit == nil {
		h.quit = make(chan struct{})
	}
	return h.quit
}

// shutdown ends all open streams (http.Server.Shutdown doesn't wait for them then).
func (h *evHub) shutdown() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.quit != nil {
		close(h.quit)
		h.quit = nil
	}
}

// readState computes the three tokens. changes is total_changes() at the time of reading.
func (s *Server) readState(ctx context.Context) (evState, int64, error) {
	var n int64
	if err := s.deps.DB.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&n); err != nil {
		return evState{}, 0, err
	}
	var nCount, pCount, failing, busy int64
	var nLast, pLast string
	_ = s.deps.DB.QueryRowContext(ctx, `SELECT COUNT(1), COALESCE(MAX(created_at),'') FROM notifications WHERE cleared_at=''`).Scan(&nCount, &nLast)
	_ = s.deps.DB.QueryRowContext(ctx, `SELECT COUNT(1), COALESCE(MAX(created_at),'') FROM requirement_items WHERE status='pending'`).Scan(&pCount, &pLast)
	_ = s.deps.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM notify_channels WHERE enabled=1 AND last_at IS NOT NULL AND last_ok=0`).Scan(&failing)
	_ = s.deps.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM runs WHERE status IN ('queued','running')`).Scan(&busy)
	notif := fmt.Sprintf("%d|%s|%d|%s|%d|%s", nCount, nLast, pCount, pLast, failing, s.notifSeenAt(ctx))
	return evState{
		Rev:   revToken(n, time.Now()),
		Notif: shortHash(notif),
		Busy:  strconv.FormatInt(busy, 10),
	}, n, nil
}

func revToken(changes int64, now time.Time) string {
	return strconv.FormatInt(changes, 10) + "." + strconv.FormatInt(now.Unix()/60, 10)
}

// shortHash keeps the bell token short and opaque (FNV-1a, 64 bit).
func shortHash(s string) string {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return strconv.FormatUint(h, 36)
}

// subscribe adds a stream; ok=false when eventsMax are open. The first one starts the watcher.
func (s *Server) subscribe() (chan struct{}, bool) {
	h := &s.events
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) >= eventsMax {
		return nil, false
	}
	if h.subs == nil {
		h.subs = map[chan struct{}]struct{}{}
	}
	ch := make(chan struct{}, 1)
	h.subs[ch] = struct{}{}
	if h.stop == nil {
		h.stop = make(chan struct{})
		h.changes, h.minute = -1, -1
		go s.watch(h.stop, eventsTick)
	}
	return ch, true
}

func (s *Server) unsubscribe(ch chan struct{}) {
	h := &s.events
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, ch)
	if len(h.subs) == 0 && h.stop != nil {
		close(h.stop)
		h.stop = nil
		h.changes, h.minute = -1, -1
	}
}

// current returns the latest state the watcher saw.
func (s *Server) current() evState {
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	return s.events.cur
}

// watch: once per tick, total_changes() (free); the bell / busy counts only when it moved.
func (s *Server) watch(stop chan struct{}, tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()
	h := &s.events
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var n int64
		err := s.deps.DB.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&n)
		min := time.Now().Unix() / 60
		h.mu.Lock()
		same := err == nil && n == h.changes && min == h.minute
		onlyMinute := err == nil && n == h.changes && h.cur.Rev != ""
		prev := h.cur
		h.mu.Unlock()
		if err != nil || same {
			cancel()
			continue
		}
		st := prev
		if onlyMinute {
			// only the minute turned: the counts can't have moved
			st.Rev = revToken(n, time.Now())
		} else if st, n, err = s.readState(ctx); err != nil {
			cancel()
			continue
		}
		cancel()
		h.mu.Lock()
		h.changes, h.minute = n, min
		if st != h.cur {
			h.cur = st
			for ch := range h.subs {
				select {
				case ch <- struct{}{}:
				default: // already signalled; the stream reads the latest state anyway
				}
			}
		}
		h.mu.Unlock()
	}
}

// sessionAlive: a read-only check (GetSession may write last_seen_at, which would move rev).
func (s *Server) sessionAlive(ctx context.Context, id string) bool {
	var exp string
	if s.deps.DB.QueryRowContext(ctx, `SELECT expires_at FROM sessions WHERE id=?`, id).Scan(&exp) != nil {
		return false
	}
	t, err := time.Parse(time.RFC3339, exp)
	return err == nil && time.Now().UTC().Before(t)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method")
		return
	}
	if _, ok := w.(http.Flusher); !ok {
		writeErr(w, 500, "no flush")
		return
	}
	ctx := r.Context()
	first, _, err := s.readState(ctx)
	if err != nil {
		writeErr(w, 500, "events")
		return
	}
	ch, ok := s.subscribe()
	if !ok {
		w.Header().Set("Retry-After", "60")
		writeErr(w, 429, "too many streams")
		return
	}
	defer s.unsubscribe(ch)
	quit := s.events.quitCh()
	sessID := ""
	if sess := auth.SessionFromContext(ctx); sess != nil {
		sessID = sess.ID
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	sent := evState{}
	// The heartbeat repeats the state (a few dozen bytes) rather than an SSE comment: the
	// page can't see comments, and it needs to know the stream is still alive after the
	// machine slept (a half-open connection raises no error). A failed write ends the
	// stream too: some in-process webview bridges report a closed page that way rather than
	// by cancelling the request.
	send := func(st evState, always bool) error {
		if (st == sent && !always) || st.Rev == "" {
			return nil
		}
		b, _ := json.Marshal(st)
		if _, err := fmt.Fprintf(w, "retry: 3000\ndata: %s\n\n", b); err != nil {
			return err
		}
		sent = st
		return http.NewResponseController(w).Flush()
	}
	if send(first, true) != nil {
		return
	}
	beat := time.NewTicker(eventsHeartbeat)
	defer beat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-quit:
			return
		case <-ch:
			if send(s.current(), false) != nil {
				return
			}
		case <-beat.C:
			// signed out elsewhere (Settings › Security) or expired: end the stream; the
			// reconnect gets a 401 and the page shows the sign-in screen.
			if sessID != "" && !s.sessionAlive(ctx, sessID) {
				return
			}
			st := s.current()
			if st.Rev == "" {
				st = sent
			}
			if send(st, true) != nil {
				return
			}
		}
	}
}
