package app

import (
	"context"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// A core that stopped while a session turn ran (force quit, crash, power loss)
// left the turn's row but never counted it. The next turn must still show up in
// the session, under a new index, and the count must match the turn list.
func TestChatSessionTurnAfterInterruptedTurn(t *testing.T) {
	chatHome(t)
	a := newTestApp(t)
	ctx := context.Background()

	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SendChatTurn(ctx, ses.ID, "first"); err != nil {
		t.Fatal(err)
	}
	waitTurn(t, a, ses.ID, 1)

	// What the stopped core left: turn 2's row (its run closed at startup by
	// recoverInterruptedRuns), the session still "running", turns still 1.
	lost := originRun(t, a, runner.Options{Prompt: "second"})
	if _, err := a.DB.ExecContext(ctx, `UPDATE runs SET status='failed', error_message='interrupted' WHERE id=?`, lost); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.ExecContext(ctx, `INSERT INTO chat_session_turns(session_id, turn_index, run_id, created_at) VALUES(?,?,?,?)`, ses.ID, 2, lost, nowUTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.ExecContext(ctx, `UPDATE chat_sessions SET status='running' WHERE id=?`, ses.ID); err != nil {
		t.Fatal(err)
	}

	a.recoverChatSessions(ctx)
	d, err := a.GetChatSession(ctx, ses.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "idle" || d.Turns != 2 || len(d.TurnList) != 2 {
		t.Fatalf("after recovery: status %s turns %d list %d", d.Status, d.Turns, len(d.TurnList))
	}

	started, err := a.SendChatTurn(ctx, ses.ID, "third")
	if err != nil {
		t.Fatal(err)
	}
	if started.Index != 3 {
		t.Fatalf("next turn index %d, want 3", started.Index)
	}
	d = waitTurn(t, a, ses.ID, 3)
	if len(d.TurnList) != 3 {
		t.Fatalf("turn list %+v", d.TurnList)
	}
	last := d.TurnList[2]
	if last.Index != 3 || last.RunID != started.RunID || last.RunID == "" || last.Message != "third" {
		t.Fatalf("new turn missing from the session: %+v", d.TurnList)
	}
	if d.TurnList[1].RunID != lost || d.TurnList[1].Status != "failed" {
		t.Fatalf("interrupted turn %+v", d.TurnList[1])
	}
}

// A turn whose row could not be written still counts toward the limit (the run
// happened and cost money); the count never goes down.
func TestChatSessionTurnCountNeverDrops(t *testing.T) {
	chatHome(t)
	a := newTestApp(t)
	ctx := context.Background()
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.ExecContext(ctx, `UPDATE chat_sessions SET turns=4 WHERE id=?`, ses.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SendChatTurn(ctx, ses.ID, "hello"); err != nil {
		t.Fatal(err)
	}
	d := waitTurn(t, a, ses.ID, 5)
	if len(d.TurnList) != 1 || d.TurnList[0].Index != 1 {
		t.Fatalf("turn list %+v", d.TurnList)
	}
}
