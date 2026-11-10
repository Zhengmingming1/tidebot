package app

import (
	"context"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/pricing"
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

func sessionCostUSD(t *testing.T, a *App, sid string) float64 {
	t.Helper()
	var total float64
	if err := a.DB.QueryRow(`SELECT cost_total FROM chat_sessions WHERE id=?`, sid).Scan(&total); err != nil {
		t.Fatal(err)
	}
	return total
}

func nearCost(x, y float64) bool { return x-y < 1e-9 && y-x < 1e-9 }

// An interrupted turn often already has mid-run usage on its run row, but
// release() never added it to the session. Recovery must pick that cost up
// without double-counting it on the next finished turn, and without adding
// the origin run (turn 0).
func TestChatSessionCostAfterInterruptedTurn(t *testing.T) {
	chatHome(t)
	a := newTestApp(t)
	ctx := context.Background()

	origin := originRun(t, a, runner.Options{Prompt: "notes"})
	if _, err := a.DB.ExecContext(ctx, `UPDATE runs SET cost_amount=9, cost_currency='USD' WHERE id=?`, origin); err != nil {
		t.Fatal(err)
	}
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{RunID: origin})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SendChatTurn(ctx, ses.ID, "first"); err != nil {
		t.Fatal(err)
	}
	d := waitTurn(t, a, ses.ID, 1)
	if _, err := a.DB.ExecContext(ctx, `UPDATE runs SET cost_amount=0.5, cost_currency='USD' WHERE id=?`, d.TurnList[1].RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.ExecContext(ctx, `UPDATE chat_sessions SET cost_total=0.5 WHERE id=?`, ses.ID); err != nil {
		t.Fatal(err)
	}

	// What the stopped core left: turn 2's row already has the mid-run cost,
	// the session is still "running", cost_total still only has turn 1.
	lost := originRun(t, a, runner.Options{Prompt: "second"})
	if _, err := a.DB.ExecContext(ctx, `UPDATE runs SET status='canceled', error_message='interrupted', cost_amount=0.25, cost_currency='USD' WHERE id=?`, lost); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.ExecContext(ctx, `INSERT INTO chat_session_turns(session_id, turn_index, run_id, created_at) VALUES(?,?,?,?)`, ses.ID, 2, lost, nowUTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.ExecContext(ctx, `UPDATE chat_sessions SET status='running' WHERE id=?`, ses.ID); err != nil {
		t.Fatal(err)
	}
	if got := sessionCostUSD(t, a, ses.ID); !nearCost(got, 0.5) {
		t.Fatalf("before recovery cost_total=%v, want 0.5", got)
	}

	a.recoverChatSessions(ctx)
	if got := sessionCostUSD(t, a, ses.ID); !nearCost(got, 0.75) {
		t.Fatalf("after recovery cost_total=%v, want 0.75 (origin 9 must not be included)", got)
	}
	// a second start (or a crash right after recovery) adds nothing
	a.recoverChatSessions(ctx)
	if got := sessionCostUSD(t, a, ses.ID); !nearCost(got, 0.75) {
		t.Fatalf("after second recovery cost_total=%v, want 0.75", got)
	}
	d, err = a.GetChatSession(ctx, ses.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "idle" || d.Turns != 2 {
		t.Fatalf("after recovery: status %s turns %d", d.Status, d.Turns)
	}

	started, err := a.SendChatTurn(ctx, ses.ID, "third")
	if err != nil {
		t.Fatal(err)
	}
	d = waitTurn(t, a, ses.ID, 3)
	last := d.TurnList[len(d.TurnList)-1]
	if last.Index != 3 || last.RunID != started.RunID || last.Message != "third" {
		t.Fatalf("new turn %+v", d.TurnList)
	}
	// The mock prices the new turn at 0, so release() adds nothing; the
	// interrupted turn's 0.25 must still be there once, not twice.
	if got := sessionCostUSD(t, a, ses.ID); !nearCost(got, 0.75) {
		t.Fatalf("after next turn cost_total=%v, want 0.75 (no double count)", got)
	}
}

// Recovery adds only the interrupted turn, at its recorded cost: turns
// release() already counted are not re-priced after a rate change, and an
// idle session is left alone.
func TestChatSessionCostRecoverOnlyInterruptedTurn(t *testing.T) {
	chatHome(t)
	a := newTestApp(t)
	ctx := context.Background()
	if err := a.SetFX(ctx, pricing.FX{Display: pricing.USD, USDCNY: 7}); err != nil {
		t.Fatal(err)
	}
	mk := func(status string, turns int, total float64, costs ...float64) string {
		ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
		if err != nil {
			t.Fatal(err)
		}
		for i, c := range costs {
			rid := originRun(t, a, runner.Options{Prompt: "turn"})
			if _, err := a.DB.ExecContext(ctx, `UPDATE runs SET cost_amount=?, cost_currency='CNY' WHERE id=?`, c, rid); err != nil {
				t.Fatal(err)
			}
			if _, err := a.DB.ExecContext(ctx, `INSERT INTO chat_session_turns(session_id, turn_index, run_id, created_at) VALUES(?,?,?,?)`, ses.ID, i+1, rid, nowUTC()); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := a.DB.ExecContext(ctx, `UPDATE chat_sessions SET status=?, turns=?, cost_total=? WHERE id=?`, status, turns, total, ses.ID); err != nil {
			t.Fatal(err)
		}
		return ses.ID
	}
	// turn 1 (CNY 7) was counted as 1 USD at 7/USD; turn 2 was interrupted
	running := mk("running", 1, 1, 7, 7)
	// an idle session whose total differs from its turns is not "fixed"
	idle := mk("idle", 2, 0.5, 7, 7)
	if err := a.SetFX(ctx, pricing.FX{Display: pricing.USD, USDCNY: 3.5}); err != nil {
		t.Fatal(err)
	}
	a.recoverChatSessions(ctx)
	if got := sessionCostUSD(t, a, running); !nearCost(got, 3) {
		t.Fatalf("running cost_total=%v, want 1 + 7/3.5 = 3 (turn 1 not re-priced)", got)
	}
	if got := sessionCostUSD(t, a, idle); !nearCost(got, 0.5) {
		t.Fatalf("idle cost_total=%v, want 0.5 untouched", got)
	}
	a.recoverChatSessions(ctx)
	if got := sessionCostUSD(t, a, running); !nearCost(got, 3) {
		t.Fatalf("second recovery cost_total=%v, want 3", got)
	}
}

// The pool has one connection and a cold rate cache reads the DB: recovery
// must read the rate before its query holds the connection.
func TestChatSessionCostRecoverColdFX(t *testing.T) {
	chatHome(t)
	a := newTestApp(t)
	ses, err := a.CreateChatSession(context.Background(), webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	lost := originRun(t, a, runner.Options{Prompt: "lost"})
	if _, err := a.DB.Exec(`UPDATE runs SET cost_amount=0.25, cost_currency='USD' WHERE id=?`, lost); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO chat_session_turns(session_id, turn_index, run_id, created_at) VALUES(?,?,?,?)`, ses.ID, 1, lost, nowUTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`UPDATE chat_sessions SET status='running', turns=0, cost_total=0 WHERE id=?`, ses.ID); err != nil {
		t.Fatal(err)
	}
	a.fxCache.Store(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { a.recoverChatSessions(ctx); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("recovery with a cold rate cache blocked on the single connection")
	}
	if got := sessionCostUSD(t, a, ses.ID); !nearCost(got, 0.25) {
		t.Fatalf("cold FX cost_total=%v, want 0.25", got)
	}
}

// CNY mid-run costs are stored in the session total as USD.
func TestChatSessionCostRecoverConvertsCNY(t *testing.T) {
	chatHome(t)
	a := newTestApp(t)
	ctx := context.Background()
	if err := a.SetFX(ctx, pricing.FX{Display: pricing.USD, USDCNY: 7}); err != nil {
		t.Fatal(err)
	}
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	lost := originRun(t, a, runner.Options{Prompt: "priced in yuan"})
	if _, err := a.DB.ExecContext(ctx, `UPDATE runs SET status='canceled', error_message='interrupted', cost_amount=7, cost_currency='CNY' WHERE id=?`, lost); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.ExecContext(ctx, `INSERT INTO chat_session_turns(session_id, turn_index, run_id, created_at) VALUES(?,?,?,?)`, ses.ID, 1, lost, nowUTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.ExecContext(ctx, `UPDATE chat_sessions SET status='running', turns=0, cost_total=0 WHERE id=?`, ses.ID); err != nil {
		t.Fatal(err)
	}
	a.recoverChatSessions(ctx)
	if got := sessionCostUSD(t, a, ses.ID); !nearCost(got, 1) {
		t.Fatalf("CNY 7 at 7/USD cost_total=%v, want 1 USD", got)
	}
}

// A cost already added for a turn whose row was never written is kept.
func TestChatSessionCostRecoverKeepsUnrowedCost(t *testing.T) {
	chatHome(t)
	a := newTestApp(t)
	ctx := context.Background()
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.ExecContext(ctx, `UPDATE chat_sessions SET turns=1, cost_total=1 WHERE id=?`, ses.ID); err != nil {
		t.Fatal(err)
	}
	a.recoverChatSessions(ctx)
	if got := sessionCostUSD(t, a, ses.ID); !nearCost(got, 1) {
		t.Fatalf("unrowed cost_total=%v, want 1", got)
	}
}
