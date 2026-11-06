package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/pricing"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func TestListChatSessionsReleasesRowsBeforeBindingReads(t *testing.T) {
	a, capability, sid, _ := grantFixture(t)
	ctx := context.Background()
	if a.DB.Stats().MaxOpenConnections != 1 {
		t.Fatal("regression requires the production single-connection pool")
	}
	skill := importSkill(t, a, "session-list", "Read the notes.")
	if _, err := a.SetChatBindings(ctx, sid, webui.ChatBindingsInput{Skills: []string{skill}, Capabilities: []string{capability.ID}}); err != nil {
		t.Fatal(err)
	}
	other, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat", Title: "Unbound"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.Exec(`UPDATE chat_sessions SET updated_at='2000-01-01T00:00:00Z' WHERE id=?`, other.ID); err != nil {
		t.Fatal(err)
	}
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	list, err := a.ListChatSessions(readCtx, 200)
	if err != nil {
		t.Fatalf("list with bound skill and capability: %v", err)
	}
	if len(list) != 2 || list[0].ID != sid || list[1].ID != other.ID {
		t.Fatalf("session order or count: %+v", list)
	}
	if len(list[0].Bindings) != 2 || list[0].Bindings[0].Name != "session-list" || list[0].Bindings[0].Missing || list[0].Bindings[1].Name != capability.Title || list[0].Bindings[1].Missing {
		t.Fatalf("binding names lost: %+v", list[0].Bindings)
	}
	if len(list[1].Bindings) != 0 {
		t.Fatalf("bindings leaked to another session: %+v", list[1].Bindings)
	}
	if _, err = a.DB.Exec(`UPDATE chat_sessions SET bindings_json='{"skills":["missing-skill"],"capabilities":["missing-capability"]}' WHERE id=?`, other.ID); err != nil {
		t.Fatal(err)
	}
	list, err = a.ListChatSessions(readCtx, 200)
	if err != nil || len(list) != 2 || len(list[1].Bindings) != 2 || !list[1].Bindings[0].Missing || !list[1].Bindings[1].Missing {
		t.Fatalf("deleted bindings must stay visible as missing: %+v %v", list, err)
	}
	list, err = a.ListChatSessions(readCtx, 1)
	if err != nil || len(list) != 1 || list[0].ID != sid {
		t.Fatalf("session limit: %+v %v", list, err)
	}
	if err := a.DB.PingContext(readCtx); err != nil {
		t.Fatalf("session list retained the database connection: %v", err)
	}
}

func TestChatSessionReadsWithColdFXCache(t *testing.T) {
	a := newTestApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	list, err := a.ListChatSessions(ctx, 200)
	if err != nil || len(list) != 0 {
		t.Fatalf("empty history: %+v %v", list, err)
	}
	s, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.SetFX(ctx, pricing.FX{Display: pricing.CNY, USDCNY: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.Exec(`UPDATE chat_sessions SET cost_total=2 WHERE id=?`, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.Exec(`INSERT INTO runs(id,status,prompt,result_text,cost_amount,cost_currency,created_at) VALUES('run_cold_fx','succeeded','question','answer',0.5,'USD',?)`, nowUTC()); err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.Exec(`INSERT INTO chat_session_turns(session_id,turn_index,run_id,created_at) VALUES(?,1,'run_cold_fx',?)`, s.ID, nowUTC()); err != nil {
		t.Fatal(err)
	}
	a.fxCache.Store(nil)
	list, err = a.ListChatSessions(ctx, 200)
	if err != nil || len(list) != 1 || list[0].CostUSD != 14 {
		t.Fatalf("cold list FX: %+v %v", list, err)
	}
	a.fxCache.Store(nil)
	detail, err := a.GetChatSession(ctx, s.ID)
	if err != nil || detail.CostUSD != 14 || len(detail.TurnList) != 1 || detail.TurnList[0].CostUSD != 3.5 || detail.TurnList[0].Answer != "answer" {
		t.Fatalf("cold detail FX: %+v %v", detail, err)
	}
}

func TestListChatSessionsFailureReleasesConnection(t *testing.T) {
	a := newTestApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.Exec(`UPDATE chat_sessions SET turns='invalid' WHERE id=?`, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.ListChatSessions(ctx, 200); err == nil {
		t.Fatal("scan failure was ignored")
	}
	if err = a.DB.PingContext(ctx); err != nil {
		t.Fatalf("scan failure retained connection: %v", err)
	}
	conn, err := a.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	a.fxCache.Store(nil)
	reads := []struct {
		name string
		read func(context.Context) error
	}{
		{"list", func(ctx context.Context) error { _, err := a.ListChatSessions(ctx, 200); return err }},
		{"detail", func(ctx context.Context) error { _, err := a.GetChatSession(ctx, s.ID); return err }},
	}
	for _, read := range reads {
		waitCtx, stop := context.WithCancel(ctx)
		result := make(chan error, 1)
		go func() { result <- read.read(waitCtx) }()
		stop()
		select {
		case err = <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled %s: %v", read.name, err)
			}
		case <-ctx.Done():
			conn.Close()
			t.Fatalf("canceled session %s did not return", read.name)
		}
	}
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err = a.DB.PingContext(ctx); err != nil {
		t.Fatalf("canceled list retained connection: %v", err)
	}
}
