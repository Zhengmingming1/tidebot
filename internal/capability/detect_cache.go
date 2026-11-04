package capability

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Detector caches explicit dependency checks, not runtime authorization. A UI
// "check again" action should call Invalidate before Detect. Each result is
// copied so callers cannot mutate cached machine-resolution records.
type Detector struct {
	mu    sync.Mutex
	TTL   time.Duration
	cache map[string]detectionEntry
}
type detectionEntry struct {
	until  time.Time
	result map[string]Resolved
}

func (d *Detector) Invalidate() { d.mu.Lock(); defer d.mu.Unlock(); d.cache = nil }
func (d *Detector) Detect(ctx context.Context, requirements []ToolRequirement, options DetectOptions) map[string]Resolved {
	if options.PATH == "" {
		options.PATH = os.Getenv("PATH")
	}
	raw, _ := json.Marshal(struct {
		Requirements []ToolRequirement
		Options      DetectOptions
	}{requirements, options})
	key := string(raw)
	d.mu.Lock()
	entry, ok := d.cache[key]
	ttl := d.TTL
	d.mu.Unlock()
	if ok && time.Now().Before(entry.until) {
		return copyResolution(entry.result)
	}
	result := Detect(ctx, requirements, options)
	if ctx.Err() != nil {
		return result
	}
	if ttl <= 0 {
		ttl = time.Minute
	}
	if ttl > 10*time.Minute {
		ttl = 10 * time.Minute
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.cache) >= 128 {
		d.cache = nil
	}
	if d.cache == nil {
		d.cache = map[string]detectionEntry{}
	}
	d.cache[key] = detectionEntry{until: time.Now().Add(ttl), result: copyResolution(result)}
	return result
}
func copyResolution(in map[string]Resolved) map[string]Resolved {
	out := make(map[string]Resolved, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
