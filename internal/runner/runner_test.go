package runner

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/provider"
)

func mockBin(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	bin := filepath.Join(root, "testdata", "mock-claude", "claude")
	return bin
}

func TestRunOnceWithMock(t *testing.T) {
	skipOnWindows(t)
	path := filepath.Join(t.TempDir(), "t.db")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := db.NewStore(sqlDB)
	r := New(store, nil, 2)

	ctx := context.Background()
	res, err := r.RunOnce(ctx, Options{
		ClaudeBin:    mockBin(t),
		Prompt:       "hello tidebot",
		WorkspaceDir: t.TempDir(),
		Timeout:      10 * time.Second,
		Bare:         true,
		Profile: provider.Profile{
			Name: "mock", BaseURL: "http://127.0.0.1:9", AuthEnv: "ANTHROPIC_AUTH_TOKEN", Model: "mock-model",
		},
		APIKey:      "test-key-not-real",
		TriggerKind: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "succeeded" {
		t.Fatalf("status=%s err=%s", res.Status, res.ErrorMessage)
	}
	if res.SessionID == "" || res.TotalCostUSD <= 0 {
		t.Fatalf("%+v", res)
	}
	if res.InputTokens != 12 || res.OutputTokens != 34 {
		t.Fatalf("tokens in=%d out=%d", res.InputTokens, res.OutputTokens)
	}
	runs, err := store.ListRuns(ctx, "", 5)
	if err != nil || len(runs) != 1 || runs[0].Status != "succeeded" {
		t.Fatalf("runs=%+v err=%v", runs, err)
	}
	// The final answer is kept in full for the run page.
	if txt, cut, err := store.RunResult(ctx, res.RunID); err != nil || txt != "ok: hello tidebot" || cut {
		t.Fatalf("result=%q cut=%v err=%v", txt, cut, err)
	}
}

// TestRunResultMaskedAndErrorsNotStored: the stored answer never carries the API key, and an
// error result (already the run's error message) is not stored a second time.
func TestRunResultMaskedAndErrorsNotStored(t *testing.T) {
	skipOnWindows(t)
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := db.NewStore(sqlDB)
	r := New(store, nil, 2)
	ctx := context.Background()
	opts := func(prompt string) Options {
		return Options{ClaudeBin: mockBin(t), Prompt: prompt, WorkspaceDir: t.TempDir(), Timeout: 10 * time.Second, Bare: true,
			Profile: provider.Profile{Name: "mock", BaseURL: "http://127.0.0.1:9", AuthEnv: "ANTHROPIC_AUTH_TOKEN", Model: "mock-model"},
			APIKey:  "sk-mock-secret-123", TriggerKind: "manual"}
	}
	res, err := r.RunOnce(ctx, opts("echo sk-mock-secret-123"))
	if err != nil {
		t.Fatal(err)
	}
	if txt, _, _ := store.RunResult(ctx, res.RunID); txt != "ok: echo ***" {
		t.Fatalf("masked result=%q", txt)
	}
	o := opts("x")
	o.ExtraArgs = []string{"--fail"}
	res, _ = r.RunOnce(ctx, o)
	if res.Status != "failed" {
		t.Fatalf("status=%s", res.Status)
	}
	if txt, _, _ := store.RunResult(ctx, res.RunID); txt != "" {
		t.Fatalf("error result stored: %q", txt)
	}
}

func TestConcurrencyLimit(t *testing.T) {
	skipOnWindows(t)
	path := filepath.Join(t.TempDir(), "t.db")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	r := New(db.NewStore(sqlDB), nil, 1)

	// Use --sleep via ExtraArgs: mock supports --sleep N before other parsing...
	// Our mock parses --sleep. Spawn two; second should wait on semaphore.
	started := make(chan struct{}, 2)
	done := make(chan error, 2)
	run := func() {
		started <- struct{}{}
		_, err := r.RunOnce(context.Background(), Options{
			ClaudeBin: mockBin(t),
			Prompt:    "x",
			Timeout:   15 * time.Second,
			ExtraArgs: []string{"--sleep", "1"},
			Profile:   provider.Profile{Name: "m", Model: "m"},
			APIKey:    "k",
		})
		done <- err
	}
	go run()
	go run()
	<-started
	<-started
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestTimeout(t *testing.T) {
	skipOnWindows(t)
	path := filepath.Join(t.TempDir(), "t.db")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	r := New(db.NewStore(sqlDB), nil, 2)
	_, err = r.RunOnce(context.Background(), Options{
		ClaudeBin: mockBin(t),
		Prompt:    "slow",
		Timeout:   200 * time.Millisecond,
		ExtraArgs: []string{"--sleep", "5"},
		Profile:   provider.Profile{Name: "m"},
		APIKey:    "k",
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func skipOnWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mock claude is a shell script")
	}
}
