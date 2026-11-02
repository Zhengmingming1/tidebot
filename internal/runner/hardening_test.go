package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Use the test executable as a portable child; no shell or Python dependency.
func init() {
	mode := os.Getenv("TIDEBOT_RUNNER_TEST_CHILD")
	if mode == "" {
		return
	}
	switch mode {
	case "stderr":
		_, _ = io.Copy(os.Stderr, bytes.NewReader(bytes.Repeat([]byte("x"), 1<<20)))
	case "oversize":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), 11<<20))
		os.Exit(0)
	case "missing":
		os.Exit(0)
	}
	result := map[string]any{"type": "result", "result": "ok", "is_error": false}
	if mode == "secret" {
		result["is_error"] = true
		result["result"] = "failed fixture-private-api-key"
	}
	_ = json.NewEncoder(os.Stdout).Encode(result)
	os.Exit(0)
}
func helperOptions(t *testing.T, mode string) Options {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Options{ClaudeBin: self, EnvBase: append(os.Environ(), "TIDEBOT_RUNNER_TEST_CHILD="+mode), Timeout: 5 * time.Second, APIKey: "fixture-private-api-key"}
}
func TestOutputFailuresAndLargeStderr(t *testing.T) {
	for _, tc := range []struct{ mode, status string }{{"stderr", "succeeded"}, {"oversize", "failed"}, {"missing", "failed"}, {"secret", "failed"}} {
		t.Run(tc.mode, func(t *testing.T) {
			r, db, _ := drainRunner(t, 1)
			result, err := r.RunOnce(context.Background(), helperOptions(t, tc.mode))
			if result.Status != tc.status {
				t.Fatalf("%+v %v", result, err)
			}
			var stored string
			if e := db.QueryRow(`SELECT COALESCE(error_message,'') FROM runs WHERE id=?`, result.RunID).Scan(&stored); e != nil {
				t.Fatal(e)
			}
			if strings.Contains(stored+result.ErrorMessage+result.ResultText, "fixture-private-api-key") {
				t.Fatal("secret leaked")
			}
			m := r.Metrics()
			if m.Started != 1 || m.Finished != 1 || m.Active != 0 || m.Running != 0 {
				t.Fatalf("metrics %+v", m)
			}
			if tc.status == "failed" && m.Failed != 1 {
				t.Fatalf("missing failure metric %+v", m)
			}
		})
	}
}
func TestPersistenceFailuresAreNotSuccessfulExecutions(t *testing.T) {
	for _, phase := range []string{"running", "succeeded"} {
		t.Run(phase, func(t *testing.T) {
			r, db, _ := drainRunner(t, 1)
			if _, err := db.Exec(`CREATE TRIGGER reject_run_write BEFORE UPDATE ON runs WHEN NEW.status='` + phase + `' BEGIN SELECT RAISE(ABORT,'fixture write failure'); END`); err != nil {
				t.Fatal(err)
			}
			result, err := r.RunOnce(context.Background(), helperOptions(t, "good"))
			if err == nil {
				t.Fatalf("persistence failure hidden: %+v", result)
			}
			if m := r.Metrics(); m.PersistenceErrors != 1 || (phase == "running" && m.Started != 0) {
				t.Fatalf("metrics %+v", m)
			}
		})
	}
}
func TestMetricsConcurrentSnapshots(t *testing.T) {
	r := New(nil, nil, 2)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.metrics.record(Result{Status: "failed"}, nil)
				_ = r.Metrics()
			}
		}()
	}
	wg.Wait()
	if m := r.Metrics(); m.Failed != 1600 || m.Finished != 1600 {
		t.Fatal(m)
	}
}

func TestCancelBetweenInsertAndStartKeepsUserReason(t *testing.T) {
	r, d, _ := drainRunner(t, 1)
	opts := helperOptions(t, "good")
	opts.OnStart = func(id string) {
		if !r.Cancel(id) {
			t.Error("run not registered")
		}
	}
	result, err := r.RunOnce(context.Background(), opts)
	if result.Status != "canceled" || !errors.Is(err, ErrStoppedByUser) {
		t.Fatalf("%+v %v", result, err)
	}
	var status string
	if err := d.QueryRow(`SELECT status FROM runs WHERE id=?`, result.RunID).Scan(&status); err != nil || status != "canceled" {
		t.Fatalf("%s %v", status, err)
	}
	if m := r.Metrics(); m.Started != 0 || m.Skipped != 0 || m.Canceled != 1 || m.PersistenceErrors != 0 {
		t.Fatal(m)
	}
}

func TestCancelDuringAdmissionKeepsUserReason(t *testing.T) {
	r, _, _ := drainRunner(t, 1)
	opts := helperOptions(t, "good")
	opts.AtStart = func(ctx context.Context, o *Options) error {
		if o.RunID == "" || !r.Cancel(o.RunID) {
			t.Fatal("admission has no registered run")
		}
		return ctx.Err()
	}
	result, err := r.RunOnce(context.Background(), opts)
	if result.Status != "canceled" || !errors.Is(err, ErrStoppedByUser) {
		t.Fatal(result, err)
	}
}
