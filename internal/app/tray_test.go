package app

import (
	"context"
	"testing"
	"time"
)

func TestTrayStatusStates(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	st := a.TrayStatus(ctx, nil)
	if st.Icon != TrayIdle || len(st.Problems) != 0 || st.Running != 0 {
		t.Fatalf("fresh app should be idle: %+v", st)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := a.DB.Exec(`INSERT INTO runs(id, status, created_at) VALUES('r1','running',?)`, now); err != nil {
		t.Fatal(err)
	}
	if st = a.TrayStatus(ctx, nil); st.Icon != TrayRunning || st.Running != 1 {
		t.Fatalf("want running: %+v", st)
	}
	_, _ = a.DB.Exec(`INSERT INTO runs(id, status, created_at, finished_at) VALUES('r2','failed',?,?)`, now, now)
	st = a.TrayStatus(ctx, nil)
	if st.Icon != TrayError || len(st.Problems) != 1 || st.Problems[0].Key != "desktop.p.failed" || st.Problems[0].N != 1 {
		t.Fatalf("want failed problem: %+v", st)
	}
	sig := st.Signature()
	st = a.TrayStatus(ctx, context.DeadlineExceeded)
	if st.Problems[0].Key != "desktop.p.daemon_down" || st.Signature() == sig {
		t.Fatalf("daemon problem must come first and change the signature: %+v", st)
	}
}

func TestTrayDetectsDaemonDownQuickly(t *testing.T) {
	a := newTestApp(t)
	if err := a.PingIPC(time.Second); err == nil {
		t.Fatal("no daemon yet, ping should fail")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d, err := a.StartDaemon(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return a.PingIPC(time.Second) == nil })
	d.Stop()
	start := time.Now()
	waitFor(t, 5*time.Second, func() bool { return a.PingIPC(time.Second) != nil })
	if time.Since(start) > 5*time.Second {
		t.Fatal("daemon down not detected within 5s")
	}
}
