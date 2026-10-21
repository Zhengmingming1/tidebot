package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/diskspace"
)

const gib = uint64(1) << 30

func fakeDisk(a *App, free, total *uint64) {
	a.disk.measure = func(string) (diskspace.Info, error) { return diskspace.Info{Free: *free, Total: *total}, nil }
}

func drainDisk(ch <-chan Event) (n int, last Event) {
	for {
		select {
		case e := <-ch:
			if e.Kind == EventDiskLow {
				n++
				last = e
			}
		default:
			return
		}
	}
}

func TestDiskThresholdClamp(t *testing.T) {
	for _, c := range []struct{ total, want uint64 }{
		{8 * gib, 1 * gib},    // small VPS: floor 1 GiB
		{40 * gib, 2 * gib},   // 5%
		{256 * gib, 5 * gib},  // laptop: cap 5 GiB
		{4096 * gib, 5 * gib}, // big server
	} {
		if got := diskThreshold(c.total); got != c.want {
			t.Errorf("total %d GiB: got %d want %d", c.total/gib, got, c.want)
		}
	}
}

func TestDiskWatchWarnsOnceThrottlesAndRearms(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	free, total := 3*gib, 100*gib // threshold 5 GiB → low
	fakeDisk(a, &free, &total)
	ch := a.Events.Subscribe(16)
	t0 := time.Now()

	a.diskTick(ctx, t0)
	n, e := drainDisk(ch)
	if n != 1 || e.Detail != "3221225472/107374182400" {
		t.Fatalf("first low check should warn once, got %d %+v", n, e)
	}
	a.diskTick(ctx, t0.Add(time.Hour))
	if n, _ := drainDisk(ch); n != 0 {
		t.Fatal("must not repeat within a day")
	}
	// Hovering just above the line (5.5 GiB < 7.5 GiB recovery) keeps it disarmed.
	free = 5*gib + gib/2
	a.diskTick(ctx, t0.Add(2*time.Hour))
	free = 4 * gib
	a.diskTick(ctx, t0.Add(3*time.Hour))
	if n, _ := drainDisk(ch); n != 0 {
		t.Fatal("flapping around the threshold must not re-alert")
	}
	// Still low a day later: one reminder.
	a.diskTick(ctx, t0.Add(25*time.Hour))
	if n, _ := drainDisk(ch); n != 1 {
		t.Fatal("want a daily reminder while still low")
	}
	// Clear recovery re-arms; the next drop alerts right away.
	free = 20 * gib
	a.diskTick(ctx, t0.Add(26*time.Hour))
	free = 2 * gib
	a.diskTick(ctx, t0.Add(27*time.Hour))
	if n, _ := drainDisk(ch); n != 1 {
		t.Fatal("after recovery a new drop should alert immediately")
	}
}

func TestDiskWatchSurvivesRestart(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	free, total := uint64(500<<20), 20*gib
	fakeDisk(a, &free, &total)
	ch := a.Events.Subscribe(16)
	a.diskTick(ctx, time.Now())
	if n, _ := drainDisk(ch); n != 1 {
		t.Fatal("want a warning")
	}
	// Simulate a restart: fresh in-memory state, same database.
	a.disk = diskWatch{}
	fakeDisk(a, &free, &total)
	a.diskTick(ctx, time.Now().Add(time.Hour))
	if n, _ := drainDisk(ch); n != 0 {
		t.Fatal("restart must not repeat the warning within a day")
	}
}

func TestDiskLowMessageTrayAndStorage(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	free, total := uint64(812<<20), 64*gib
	fakeDisk(a, &free, &total)

	m, ok := a.EventToMessage(Event{Kind: EventDiskLow, Detail: "851443712/68719476736"})
	if !ok || m.Key != "disk_low" || m.Params["free"] != "812 MB" || m.Params["total"] != "64.0 GB" || !strings.Contains(m.Title, "812 MB") || m.Body == "" {
		t.Fatalf("message %+v", m)
	}
	if st := a.TrayStatus(ctx, nil); len(st.Problems) != 0 {
		t.Fatalf("tray shows nothing before the first check: %+v", st)
	}
	a.diskTick(ctx, time.Now())
	st := a.TrayStatus(ctx, nil)
	if len(st.Problems) != 1 || st.Problems[0].Key != "desktop.p.disk_low" || st.Problems[0].N != 812 || st.Icon != TrayError {
		t.Fatalf("tray %+v", st)
	}
	v, err := a.StorageUsage(ctx, time.Now())
	if err != nil || v.Disk == nil || !v.Disk.Low || v.Disk.Free != free || v.Disk.Threshold != 3435973836 {
		t.Fatalf("storage disk %+v %v", v.Disk, err)
	}
	// Recovered disk: no tray problem, storage still reports space.
	free = 30 * gib
	a.diskTick(ctx, time.Now())
	if st := a.TrayStatus(ctx, nil); len(st.Problems) != 0 {
		t.Fatalf("tray should clear: %+v", st)
	}
	// Platforms that can't measure: no disk block, no event.
	a.disk.measure = func(string) (diskspace.Info, error) { return diskspace.Info{}, diskspace.ErrUnsupported }
	if v, _ := a.StorageUsage(ctx, time.Now()); v.Disk != nil {
		t.Fatal("unknown disk must be omitted")
	}
	a.diskTick(ctx, time.Now())
	if a.lastDisk() != nil {
		t.Fatal("unknown disk must clear the cached status")
	}
}

func TestDiskWatchRealVolume(t *testing.T) {
	a := newTestApp(t)
	d, ok := a.DiskStatus()
	if !ok {
		t.Skip("disk space not measurable here")
	}
	if d.Total == 0 || d.Threshold < diskMinThreshold || d.Threshold > diskMaxThreshold {
		t.Fatalf("implausible %+v", d)
	}
}
