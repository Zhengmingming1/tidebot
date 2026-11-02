package webui

import (
	"bufio"

	"errors"
	"fmt"

	"io"

	"net/http"

	"os"

	"strconv"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/runner"
)

// streamRunLog tails the run's log file over SSE until the run ends,
// the client disconnects, or the UI goes idle.
func (s *Server) streamRunLog(w http.ResponseWriter, r *http.Request, runID string) {
	ctx := r.Context()
	st := s.runStatus(ctx, runID)
	if st == "" {
		writeErr(w, 404, "run not found")
		return
	}
	if !s.sseAllowed() {
		writeErr(w, 429, "sse idle; interact with UI to resume")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "no flush")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	// Each log line carries its end offset as the SSE id, so a reconnect (Last-Event-ID or ?from=)
	// continues where the client left off instead of replaying the whole log.
	var offset int64
	// Lines are written as they are read and flushed once per batch, so a burst of
	// output is one network write rather than one per line.
	send := func(event, data string) {
		for _, line := range strings.Split(data, "\n") {
			if event != "" {
				fmt.Fprintf(w, "event: %s\n", event)
			} else {
				fmt.Fprintf(w, "id: %d\n", offset)
			}
			fmt.Fprintf(w, "data: %s\n\n", line)
		}
	}
	end := func(status string) {
		send("end", status)
		flusher.Flush()
	}
	path := ""
	if s.deps.LogPath != nil {
		path = s.deps.LogPath(runID)
	}
	if from := r.Header.Get("Last-Event-ID"); from != "" || r.URL.Query().Get("from") != "" {
		if from == "" {
			from = r.URL.Query().Get("from")
		}
		if n, err := strconv.ParseInt(from, 10, 64); err == nil && n > 0 {
			if fi, err := os.Stat(path); err == nil && n <= fi.Size() {
				offset = n
			} else if path != "" {
				if raw, ok := runner.GzipRawSize(path + ".gz"); ok && n <= raw {
					offset = n
				}
			}
		}
	}
	// A finished run's log may have been gzipped (runner.CompressFinishedLogs): it is
	// complete, so it is read once from offset to the end and the stream closes.
	readPacked := func() bool {
		rc, packed, err := runner.OpenLogFile(path)
		if err != nil || !packed {
			if rc != nil {
				rc.Close()
			}
			return false
		}
		defer rc.Close()
		if _, err := io.CopyN(io.Discard, rc, offset); err != nil {
			return true
		}
		br := bufio.NewReader(rc)
		sent := false
		for {
			line, err := br.ReadString('\n')
			if len(line) > 0 {
				offset += int64(len(line))
				if line = strings.TrimRight(line, "\r\n"); line != "[[end]]" {
					send("", line)
					sent = true
				}
			}
			if err != nil {
				break
			}
		}
		if sent {
			flusher.Flush()
		}
		return true
	}
	readMore := func() bool {
		if path == "" {
			return false
		}
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) && readPacked() {
			return true
		}
		// Stat first (like Cronicle's fstat before read): nothing new means no open.
		if fi, err := os.Stat(path); err != nil || fi.Size() <= offset {
			return false
		}
		f, err := os.Open(path)
		if err != nil {
			return false
		}
		defer f.Close()
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return false
		}
		br := bufio.NewReader(f)
		ended, sent := false, false
		for {
			line, err := br.ReadString('\n')
			if len(line) > 0 && strings.HasSuffix(line, "\n") {
				offset += int64(len(line))
				line = strings.TrimRight(line, "\r\n")
				if line == "[[end]]" {
					ended = true
					continue
				}
				send("", line)
				sent = true
			}
			if err != nil {
				break
			}
		}
		if sent {
			flusher.Flush()
		}
		return ended
	}
	if readMore() {
		end(s.runStatus(ctx, runID))
		return
	}
	if path == "" && isTerminal(st) {
		send("", "(no log captured for this run)")
		end(st)
		return
	}
	// With LogWait the stream sleeps until the run writes (in-process push, like Uptime
	// Kuma emitting each heartbeat as it happens); the slow timer only catches a status
	// change without a final line and the idle cut-off. Without it, poll as before.
	poll := s.logPoll
	if s.deps.LogWait == nil || poll <= 0 {
		poll = 500 * time.Millisecond
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	var lastRead time.Time
	for {
		var bell <-chan struct{}
		done := func() {}
		if s.deps.LogWait != nil {
			bell, done = s.deps.LogWait(runID)
			// Output may have landed between the last read and taking the bell.
			if readMore() {
				done()
				end(s.runStatus(ctx, runID))
				return
			}
		}
		select {
		case <-ctx.Done():
			done()
			return
		case <-heartbeat.C:
			done()
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-bell:
			done()
			// Coalesce bursts: at most one read per logGap.
			if gap := s.logGap - time.Since(lastRead); gap > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(gap):
				}
			}
			lastRead = time.Now()
			if readMore() {
				end(s.runStatus(ctx, runID))
				return
			}
		case <-ticker.C:
			done()
			lastRead = time.Now()
			if readMore() {
				end(s.runStatus(ctx, runID))
				return
			}
			if cur := s.runStatus(ctx, runID); isTerminal(cur) {
				readMore()
				end(cur)
				return
			}
			if !s.sseAllowed() {
				send("idle", "stream paused (idle)")
				flusher.Flush()
				return
			}
		}
	}
}
