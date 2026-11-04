package managed

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStatusHelperProcess(t *testing.T) {
	mode := os.Getenv("TIDEBOT_STATUS_FIXTURE")
	if mode == "" {
		return
	}
	switch mode {
	case "success":
		fmt.Fprint(os.Stdout, `{"success":true}`)
		fmt.Fprint(os.Stderr, "PRIVATE-STDERR")
	case "overflow":
		fmt.Fprint(os.Stdout, strings.Repeat("x", (64<<10)+1))
	case "failure":
		fmt.Fprint(os.Stderr, "PRIVATE-FAILURE")
		os.Exit(1)
	case "timeout":
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}
func TestReadStatusBoundedPrivateProcess(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"success", "overflow", "failure", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			out, err := ReadStatus(ctx, Command{Path: exe, Args: []string{"-test.run=^TestStatusHelperProcess$"}, Env: append(os.Environ(), "TIDEBOT_STATUS_FIXTURE="+mode, "GORACE=atexit_sleep_ms=0"), Dir: t.TempDir()})
			if mode == "success" {
				if err != nil || string(out) != `{"success":true}` {
					t.Fatalf("unexpected status result %q %v", out, err)
				}
			} else if err == nil || len(out) != 0 {
				t.Fatal("unsafe result was returned")
			}
			if err != nil && strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("stderr escaped")
			}
		})
	}
	if _, err := ReadStatus(context.Background(), Command{Path: "relative"}); err == nil {
		t.Fatal("relative executable allowed")
	}
}
