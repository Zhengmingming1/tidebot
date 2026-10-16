package runner

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseRealKimiCodingFixture(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "testdata", "streamjson", "kimi-coding-pong.ndjson")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var (
		sawInit   bool
		sawResult bool
		result    Event
	)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ev, err := ParseEvent(sc.Bytes())
		if err != nil {
			t.Fatalf("parse: %v line=%s", err, sc.Text()[:min(80, len(sc.Text()))])
		}
		if ev.IsInit() {
			sawInit = true
			if ev.Model == "" {
				t.Fatal("init missing model")
			}
		}
		if ev.IsResult() {
			sawResult = true
			result = ev
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if !sawInit || !sawResult {
		t.Fatalf("init=%v result=%v", sawInit, sawResult)
	}
	if result.IsError {
		t.Fatal("result is_error")
	}
	if result.Result != "pong" {
		t.Fatalf("result text=%q", result.Result)
	}
	if result.TotalCostUSD <= 0 {
		t.Fatalf("cost=%v", result.TotalCostUSD)
	}
	if result.Usage == nil || result.Usage.InputTokens < 1 || result.Usage.OutputTokens < 1 {
		t.Fatalf("usage=%+v", result.Usage)
	}
	if result.SessionID == "" {
		t.Fatal("missing session_id")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
