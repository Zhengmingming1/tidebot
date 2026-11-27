package guard

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// End to end: real Claude Code, real OS sandbox (bubblewrap / Seatbelt), the
// --settings document SandboxSettings writes, and a scripted fake Messages API
// (ANTHROPIC_BASE_URL) that asks for Bash commands. No model key, no network
// beyond loopback. Opt-in: TIDEBOT_SANDBOX_E2E=1 (CI sets it on Linux after
// installing bubblewrap, socat and a pinned Claude Code); missing parts then
// fail instead of skipping. TIDEBOT_E2E_CLAUDE picks the claude binary.
//
// The idea of a canned model that returns scripted tool calls follows LiteLLM's
// mock_completion / mock_tool_calls (BerriAI/litellm, litellm/main.py); only
// the idea, no code taken.

// e2eStep is one Bash call the fake model makes.
type e2eStep struct {
	name    string
	command string
	disable bool // dangerouslyDisableSandbox: the run must still be sandboxed
}

// fakeMessagesAPI answers /v1/messages: the main conversation (the request that
// offers Bash) gets steps[i] as a streamed tool_use while tool results come
// back, then a final text; anything else (title, summaries) gets a short text.
// Results are the tool_result text of each step, by step index.
type fakeMessagesAPI struct {
	steps   []e2eStep
	mu      sync.Mutex
	results map[int]string
	main    int // main-conversation requests seen
}

func (f *fakeMessagesAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/v1/messages") || strings.Contains(r.URL.Path, "count_tokens") {
		if strings.Contains(r.URL.Path, "count_tokens") {
			_, _ = io.WriteString(w, `{"input_tokens":10}`)
			return
		}
		http.NotFound(w, r)
		return
	}
	var req struct {
		Stream   bool                    `json:"stream"`
		Tools    []struct{ Name string } `json:"tools"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	hasBash := false
	for _, t := range req.Tools {
		hasBash = hasBash || t.Name == "Bash"
	}
	next := -1 // -1: answer with text
	if hasBash {
		f.mu.Lock()
		f.main++
		done := 0
		for _, m := range req.Messages {
			var blocks []struct {
				Type      string          `json:"type"`
				ToolUseID string          `json:"tool_use_id"`
				Content   json.RawMessage `json:"content"`
			}
			if m.Role != "user" || json.Unmarshal(m.Content, &blocks) != nil {
				continue
			}
			for _, b := range blocks {
				var i int
				if b.Type != "tool_result" {
					continue
				}
				if _, err := fmt.Sscanf(b.ToolUseID, "toolu_e2e_%d", &i); err == nil {
					f.results[i] = toolResultText(b.Content)
					done++
				}
			}
		}
		if done < len(f.steps) {
			next = done
		}
		f.mu.Unlock()
	}
	if !req.Stream {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_e2e","type":"message","role":"assistant","model":"claude-e2e","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(ev string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev, b)
	}
	usage := map[string]int{"input_tokens": 1, "output_tokens": 1}
	send("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": fmt.Sprintf("msg_e2e_%d", next+1), "type": "message", "role": "assistant", "model": "claude-e2e",
		"content": []any{}, "stop_reason": nil, "usage": usage}})
	stop := "end_turn"
	if next >= 0 {
		s := f.steps[next]
		in := map[string]any{"command": s.command, "description": s.name}
		if s.disable {
			in["dangerouslyDisableSandbox"] = true
		}
		js, _ := json.Marshal(in)
		send("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{
			"type": "tool_use", "id": fmt.Sprintf("toolu_e2e_%d", next), "name": "Bash", "input": map[string]any{}}})
		send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(js)}})
		stop = "tool_use"
	} else {
		send("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
		send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "done"}})
	}
	send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	send("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 1}})
	send("message_stop", map[string]any{"type": "message_stop"})
}

// toolResultText flattens a tool_result content (a string or text blocks).
func toolResultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct{ Text string }
	_ = json.Unmarshal(raw, &blocks)
	var out []string
	for _, b := range blocks {
		out = append(out, b.Text)
	}
	return strings.Join(out, "\n")
}

func TestSandboxEndToEnd(t *testing.T) {
	if os.Getenv("TIDEBOT_SANDBOX_E2E") != "1" {
		t.Skip("set TIDEBOT_SANDBOX_E2E=1 (needs Claude Code and the OS sandbox)")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no system sandbox on " + runtime.GOOS)
	}
	if st := DetectSandboxProbe(runtime.GOOS, exec.LookPath, ProbeBwrap); st.Status != SandboxOK {
		t.Fatalf("sandbox not usable here: %+v", st)
	}
	claude := os.Getenv("TIDEBOT_E2E_CLAUDE")
	if claude == "" {
		p, err := exec.LookPath("claude")
		if err != nil {
			t.Fatal("claude not on PATH (TIDEBOT_E2E_CLAUDE)")
		}
		claude = p
	}

	const secret = "tidebot-e2e-canary-7c1f"
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r // macOS: /var → /private/var, as the sandbox sees it
	}
	home := filepath.Join(root, "home")
	work := filepath.Join(root, "work")
	own := filepath.Join(root, "tidebot-data")
	for _, d := range []string{filepath.Join(home, ".ssh"), filepath.Join(home, ".claude"), work, own} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for p, body := range map[string]string{
		filepath.Join(home, ".ssh", "id_e2e"):           secret,
		filepath.Join(own, "tidebot.db"):                secret,
		filepath.Join(home, ".claude", "settings.json"): "{}",
	} {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	settings := filepath.Join(own, "run-sandbox.json")
	doc := SandboxSettings(SandboxSpec{Home: home, WriteDirs: []string{work}, Private: []string{own}, Web: true, SecretEnv: []string{"TIDEBOT_E2E_SECRET"}})
	if err := os.WriteFile(settings, doc, 0o600); err != nil {
		t.Fatal(err)
	}

	q := func(p string) string { return "'" + p + "'" }
	steps := []e2eStep{
		{name: "write in the working folder", command: "echo inside > " + q(filepath.Join(work, "in.txt")) + " && echo wrote"},
		{name: "write outside", command: "echo pwn > " + q(filepath.Join(home, "outside.txt"))},
		{name: "write outside, unsandboxed retry", command: "echo pwn > " + q(filepath.Join(home, "outside-retry.txt")), disable: true},
		{name: "read a key", command: "cat " + q(filepath.Join(home, ".ssh", "id_e2e")) + " 2>&1; echo read-done"},
		{name: "read tidebot's database", command: "cat " + q(filepath.Join(own, "tidebot.db")) + " 2>&1; echo read-done"},
		{name: "read a key, unsandboxed retry", command: "cat " + q(filepath.Join(home, ".ssh", "id_e2e")) + " 2>&1; echo read-done", disable: true},
		{name: "environment", command: `echo "key=[$ANTHROPIC_API_KEY] secret=[$TIDEBOT_E2E_SECRET]"`},
		{name: "agent config in the working folder", command: "mkdir -p " + q(filepath.Join(work, ".claude")) + "; echo pwn > " + q(filepath.Join(work, ".claude", "settings.json")) + "; echo pwn > " + q(filepath.Join(work, "CLAUDE.md")) + "; echo cfg-done"},
		{name: "owner's Claude Code config", command: "echo pwn > " + q(filepath.Join(home, ".claude", "settings.json"))},
	}
	results, stdout := runClaudeE2E(t, claude, work, home, settings, secret, steps)
	for i, s := range steps {
		t.Logf("%d %s: %q", i, s.name, tail(results[i], 300))
	}
	// The final result line says the run finished normally.
	if !strings.Contains(lastResultLine(stdout), `"subtype":"success"`) {
		t.Errorf("no success result: %s", tail(stdout, 2000))
	}

	read := func(p string) string { b, _ := os.ReadFile(p); return string(b) }
	if got := read(filepath.Join(work, "in.txt")); got != "inside\n" {
		t.Errorf("write in the working folder: %q (result %q)", got, results[0])
	}
	for _, p := range []string{"outside.txt", "outside-retry.txt"} {
		if _, err := os.Stat(filepath.Join(home, p)); err == nil {
			t.Errorf("command wrote outside its folders: %s", p)
		}
	}
	for _, i := range []int{3, 4, 5} {
		if strings.Contains(results[i], secret) {
			t.Errorf("%s: secret readable: %q", steps[i].name, results[i])
		}
		if !strings.Contains(results[i], "read-done") {
			t.Errorf("%s: command did not run: %q", steps[i].name, results[i])
		}
	}
	if r := results[6]; strings.Contains(r, secret) || !strings.Contains(r, "key=[") {
		t.Errorf("environment: %q", r)
	}
	if !strings.Contains(results[7], "cfg-done") {
		t.Errorf("agent config step did not run: %q", results[7])
	}
	for _, p := range []string{filepath.Join(work, ".claude", "settings.json"), filepath.Join(work, "CLAUDE.md")} {
		if strings.Contains(read(p), "pwn") {
			t.Errorf("agent config written: %s", p)
		}
	}
	if got := read(filepath.Join(home, ".claude", "settings.json")); got != "{}" {
		t.Errorf("owner's ~/.claude/settings.json changed: %q", got)
	}

	// Control: the same steps with the sandbox off do escape, so the checks
	// above can tell (a harness that never reached the files would pass them).
	off := filepath.Join(own, "no-sandbox.json")
	if err := os.WriteFile(off, []byte(`{"sandbox":{"enabled":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctl, _ := runClaudeE2E(t, claude, work, home, off, secret, steps[:4])
	if _, err := os.Stat(filepath.Join(home, "outside.txt")); err != nil {
		t.Errorf("control: unsandboxed write outside did not land (%q)", ctl[1])
	}
	if !strings.Contains(ctl[3], secret) {
		t.Errorf("control: unsandboxed read did not see the key: %q", ctl[3])
	}
}

// runClaudeE2E runs Claude Code once against a fake Messages API that asks for
// steps, with the given --settings file; it returns each step's tool result
// and the stream-json output.
func runClaudeE2E(t *testing.T, claude, work, home, settings, secret string, steps []e2eStep) (map[int]string, string) {
	t.Helper()
	api := &fakeMessagesAPI{steps: steps, results: map[int]string{}}
	srv := httptest.NewServer(api)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, claude, "-p", "run the e2e steps", "--output-format", "stream-json", "--verbose", "--bare",
		"--permission-mode", "dontAsk", "--permission-prompts", "none", "--allowedTools", "Bash",
		"--disallowedTools", strings.Join(DangerDeny(), ","), "--settings", settings, "--model", "claude-sonnet-4-5")
	cmd.Dir = work
	cmd.Env = []string{
		"HOME=" + home, "PATH=" + os.Getenv("PATH"), "TMPDIR=" + os.TempDir(), "LANG=C.UTF-8",
		"ANTHROPIC_BASE_URL=" + srv.URL, "ANTHROPIC_API_KEY=sk-ant-e2e-" + secret,
		"TIDEBOT_E2E_SECRET=" + secret,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "DISABLE_TELEMETRY=1",
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	api.mu.Lock()
	results := api.results
	api.mu.Unlock()
	if err != nil || len(results) != len(steps) {
		t.Fatalf("claude: %v; %d/%d steps answered\nstderr: %s\nstdout tail: %s", err, len(results), len(steps), stderr.String(), tail(stdout.String(), 4000))
	}
	return results, stdout.String()
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

func lastResultLine(out string) string {
	last := ""
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		if strings.Contains(sc.Text(), `"type":"result"`) {
			last = sc.Text()
		}
	}
	return last
}
