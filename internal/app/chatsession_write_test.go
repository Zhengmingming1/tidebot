package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// shortChatWrites shrinks the retry pauses of a turn's bookkeeping writes.
func shortChatWrites(t *testing.T) {
	t.Helper()
	wait, retry, total := chatWriteWait, chatWriteRetry, chatWriteFor
	chatWriteWait, chatWriteRetry, chatWriteFor = time.Second, 10*time.Millisecond, 20*time.Second
	t.Cleanup(func() { chatWriteWait, chatWriteRetry, chatWriteFor = wait, retry, total })
}

// The turn's row could not be written when its run started (tidebot's one
// database connection busy past the try): the run went ahead and cost money, but
// the session never showed it. The row must still be written.
func TestChatTurnRowWrittenWhenStartWriteFails(t *testing.T) {
	chatHome(t)
	shortChatWrites(t)
	a := newTestApp(t)
	ctx := context.Background()
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	// Every write of the row fails while its run is still going.
	if _, err := a.DB.Exec(`CREATE TRIGGER test_turn_busy BEFORE INSERT ON chat_session_turns
WHEN (SELECT status FROM runs WHERE id=NEW.run_id) NOT IN ('succeeded','failed','canceled')
BEGIN SELECT RAISE(ABORT, 'database is locked'); END`); err != nil {
		t.Fatal(err)
	}
	started, err := a.SendChatTurn(ctx, ses.ID, "hello")
	if err != nil {
		t.Fatal(err)
	}
	d := waitTurn(t, a, ses.ID, 1)
	if len(d.TurnList) != 1 || d.TurnList[0].Index != 1 || d.TurnList[0].RunID == "" || d.TurnList[0].Message != "hello" {
		t.Fatalf("turn missing from the session: %+v", d.TurnList)
	}
	if started.RunID != "" && d.TurnList[0].RunID != started.RunID {
		t.Fatalf("turn run %s, started %s", d.TurnList[0].RunID, started.RunID)
	}
	// the next turn goes on as usual
	if _, err := a.DB.Exec(`DROP TRIGGER test_turn_busy`); err != nil {
		t.Fatal(err)
	}
	next, err := a.SendChatTurn(ctx, ses.ID, "again")
	if err != nil || next.Index != 2 {
		t.Fatalf("next turn %+v %v", next, err)
	}
	if d = waitTurn(t, a, ses.ID, 2); len(d.TurnList) != 2 {
		t.Fatalf("turns %+v", d.TurnList)
	}
}

// The end-of-turn write failed once: the session stayed "running" and refused
// every message (and could not be deleted) until tidebot restarted. It must be
// retried, and keep the Claude Code session for the next turn.
func TestChatTurnEndRetriedWhenDatabaseBusy(t *testing.T) {
	chatHome(t)
	shortChatWrites(t)
	a := newTestApp(t)
	ctx := context.Background()
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`CREATE TABLE test_block_idle(x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO test_block_idle VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`CREATE TRIGGER test_idle_busy BEFORE UPDATE ON chat_sessions
WHEN NEW.status='idle' AND EXISTS (SELECT 1 FROM test_block_idle)
BEGIN SELECT RAISE(ABORT, 'database is locked'); END`); err != nil {
		t.Fatal(err)
	}
	started, err := a.SendChatTurn(ctx, ses.ID, "hello")
	if err != nil {
		t.Fatal(err)
	}
	// the run ends, the session can't be released yet
	waitFor(t, 20*time.Second, func() bool {
		var n int
		_ = a.DB.QueryRow(`SELECT COUNT(*) FROM runs r JOIN chat_session_turns t ON t.run_id=r.id WHERE t.session_id=? AND r.status IN ('succeeded','failed','canceled')`, ses.ID).Scan(&n)
		return n == 1
	})
	time.Sleep(100 * time.Millisecond)
	var status string
	if err := a.DB.QueryRow(`SELECT status FROM chat_sessions WHERE id=?`, ses.ID).Scan(&status); err != nil || status != "running" {
		t.Fatalf("status %q %v, want running while the write fails", status, err)
	}
	if _, err := a.DB.Exec(`DELETE FROM test_block_idle`); err != nil {
		t.Fatal(err)
	}
	d := waitTurn(t, a, ses.ID, 1)
	if len(d.TurnList) != 1 || d.TurnList[0].RunID != started.RunID && started.RunID != "" {
		t.Fatalf("turns %+v", d.TurnList)
	}
	var csid string
	if err := a.DB.QueryRow(`SELECT claude_session_id FROM chat_sessions WHERE id=?`, ses.ID).Scan(&csid); err != nil || csid == "" {
		t.Fatalf("claude session not kept: %q %v", csid, err)
	}
	if _, err := a.SendChatTurn(ctx, ses.ID, "again"); err != nil {
		t.Fatalf("session refuses the next message: %v", err)
	}
	waitTurn(t, a, ses.ID, 2)
}

func TestChatWriteRetriesThenGivesUp(t *testing.T) {
	a := newTestApp(t)
	wait, retry, total := chatWriteWait, chatWriteRetry, chatWriteFor
	t.Cleanup(func() { chatWriteWait, chatWriteRetry, chatWriteFor = wait, retry, total })
	chatWriteWait, chatWriteRetry, chatWriteFor = time.Second, time.Millisecond, time.Second

	calls := 0
	busy := errors.New("database is locked")
	if err := a.chatWrite("test", "s", func(context.Context) error {
		if calls++; calls < 3 {
			return busy
		}
		return nil
	}); err != nil || calls != 3 {
		t.Fatalf("calls %d err %v", calls, err)
	}

	chatWriteFor = 50 * time.Millisecond
	begin := time.Now()
	if err := a.chatWrite("test", "s", func(context.Context) error { return busy }); !errors.Is(err, busy) {
		t.Fatalf("err %v", err)
	}
	if time.Since(begin) > 2*time.Second {
		t.Fatalf("gave up after %s", time.Since(begin))
	}
}
