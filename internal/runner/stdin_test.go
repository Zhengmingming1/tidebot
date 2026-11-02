package runner

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestComposedPromptStaysOutOfArgv(t *testing.T) {
	prompt := strings.Repeat("长输出\n", 16000)
	args := claudeArgs(Options{Prompt: prompt, PromptStdin: true, Bare: true, ContextArgs: []string{"--plugin-dir", "stage"}})
	if args[0] != "-p" || strings.Contains(strings.Join(args, " "), "长输出") || !strings.Contains(strings.Join(args, " "), "--plugin-dir stage") {
		t.Fatalf("invalid stdin arguments: %v", args)
	}
	legacy := claudeArgs(Options{Prompt: "normal prompt"})
	if legacy[1] != "normal prompt" {
		t.Fatalf("legacy arguments changed: %v", legacy)
	}
}

func TestPromptStdinPreservesLongInput(t *testing.T) {
	skipOnWindows(t)
	prompt := strings.Repeat("a:'中文' $not-a-shell\n", 2200) + "final line\n\n"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := execute(ctx, Options{ClaudeBin: mockBin(t), Prompt: prompt, PromptStdin: true}, io.Discard, 0, nil)
	if err != nil || res.Status != "succeeded" || res.ResultText != "ok: "+prompt {
		t.Fatalf("stdin round trip: status=%s error=%v input=%d output=%d", res.Status, err, len(prompt), len(res.ResultText))
	}
}
