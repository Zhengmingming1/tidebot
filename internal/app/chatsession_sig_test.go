package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/pricing"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func addChatTurnRow(t testing.TB, a *App, sid string, idx int, status, answer string) string {
	t.Helper()
	rid := fmt.Sprintf("run_sig_%s_%d", sid, idx)
	if _, err := a.DB.Exec(`INSERT INTO runs(id,status,prompt,result_text,cost_amount,cost_currency,created_at) VALUES(?,?,?,?,0.01,'USD',?)`, rid, status, fmt.Sprint("question ", idx), answer, nowUTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO chat_session_turns(session_id,turn_index,run_id,created_at) VALUES(?,?,?,?)`, sid, idx, rid, nowUTC()); err != nil {
		t.Fatal(err)
	}
	return rid
}

func chatSig(t testing.TB, a *App, sid string) string {
	t.Helper()
	s, err := a.ChatSessionSig(context.Background(), sid)
	if err != nil || s == "" {
		t.Fatalf("sig: %q %v", s, err)
	}
	return s
}

// Every change GetChatSession would show moves the fingerprint, including the
// answer stored after the run's status (runner: UpdateRunFinish, then SetRunResult).
func TestChatSessionSigTracksDetail(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	s0 := chatSig(t, a, ses.ID)
	if again := chatSig(t, a, ses.ID); again != s0 {
		t.Fatal("unchanged session gave a new sig")
	}
	rid := addChatTurnRow(t, a, ses.ID, 1, "running", "")
	s1 := chatSig(t, a, ses.ID)
	if s1 == s0 {
		t.Fatal("new turn not seen")
	}
	steps := []struct {
		name, q string
		args    []any
	}{
		{"status", `UPDATE runs SET status='succeeded', finished_at=? WHERE id=?`, []any{nowUTC(), rid}},
		{"answer", `UPDATE runs SET result_text='the answer' WHERE id=?`, []any{rid}},
		{"cost", `UPDATE runs SET cost_amount=0.02 WHERE id=?`, []any{rid}},
		{"error", `UPDATE runs SET error_message='boom' WHERE id=?`, []any{rid}},
		{"truncated", `UPDATE runs SET result_truncated=1 WHERE id=?`, []any{rid}},
		{"session row", `UPDATE chat_sessions SET turns=1, status='idle', updated_at=? WHERE id=?`, []any{time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano), ses.ID}},
		{"title", `UPDATE chat_sessions SET title='renamed' WHERE id=?`, []any{ses.ID}},
		{"run deleted", `DELETE FROM runs WHERE id=?`, []any{rid}},
	}
	prev := s1
	for _, st := range steps {
		if _, err := a.DB.Exec(st.q, st.args...); err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		next := chatSig(t, a, ses.ID)
		if next == prev {
			t.Fatalf("%s: sig did not change", st.name)
		}
		prev = next
	}
	// the display currency converts every cost on the page
	if err := a.SetFX(ctx, pricing.FX{Display: pricing.CNY, USDCNY: 7}); err != nil {
		t.Fatal(err)
	}
	if next := chatSig(t, a, ses.ID); next == prev {
		t.Fatal("display currency change not seen")
	}
	// another session never shares a sig; a missing one is not found
	other, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if chatSig(t, a, other.ID) == chatSig(t, a, ses.ID) {
		t.Fatal("sig shared across sessions")
	}
	if _, err := a.ChatSessionSig(ctx, "chat_nope"); err != webui.ErrChatSessionNotFound {
		t.Fatalf("missing session: %v", err)
	}
}

// The fingerprint uses the answers' byte length (octet_length, from the record
// header) instead of the text: multi-byte answers and an edited answer count.
func TestChatSessionSigAnswerLength(t *testing.T) {
	a := newTestApp(t)
	ses, err := a.CreateChatSession(context.Background(), webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("答案 answer ", 10000) // ~140 KB
	for i := 1; i <= 3; i++ {
		addChatTurnRow(t, a, ses.ID, i, "succeeded", big)
	}
	var n int64
	if err := a.DB.QueryRow(`SELECT sum(octet_length(result_text)) FROM runs`).Scan(&n); err != nil || n != int64(3*len(big)) {
		t.Fatalf("octet_length: %d %v", n, err)
	}
	before := chatSig(t, a, ses.ID)
	if _, err := a.DB.Exec(`UPDATE runs SET result_text=? WHERE id=?`, big+"x", fmt.Sprintf("run_sig_%s_%d", ses.ID, 2)); err != nil {
		t.Fatal(err)
	}
	if chatSig(t, a, ses.ID) == before {
		t.Fatal("changed answer not seen")
	}
}

func TestChatSessionPollPerf(t *testing.T) {
	if os.Getenv("TIDEBOT_PERF") == "" {
		t.Skip("TIDEBOT_PERF=1")
	}
	a := newTestApp(t)
	ctx := context.Background()
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	var answer strings.Builder
	for answer.Len() < 16<<10 {
		answer.WriteString("## 小结\n\n- 第一点：检查 `internal/app` 的配置。\n- Second point with **bold** and a [link](https://example.com).\n\n```go\nfmt.Println(\"hi\")\n```\n\n")
	}
	for i := 1; i <= 20; i++ {
		addChatTurnRow(t, a, ses.ID, i, "succeeded", answer.String())
	}
	addChatTurnRow(t, a, ses.ID, 21, "running", "")
	cases := []struct {
		name string
		fn   func() int
	}{
		{"full (GetChatSession + JSON)", func() int {
			d, err := a.GetChatSession(ctx, ses.ID)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(d)
			return len(b)
		}},
		{"unchanged (ChatSessionSig + short reply)", func() int {
			s, err := a.ChatSessionSig(ctx, ses.ID)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(map[string]any{"unchanged": true, "sig": s})
			return len(b)
		}},
	}
	for _, c := range cases {
		best := time.Hour
		size := 0
		for r := 0; r < 5; r++ {
			start := time.Now()
			for i := 0; i < 50; i++ {
				size = c.fn()
			}
			if d := time.Since(start) / 50; d < best {
				best = d
			}
		}
		t.Logf("%s: %v per poll, %d bytes", c.name, best, size)
	}
}
