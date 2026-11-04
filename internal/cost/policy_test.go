package cost

import (
	"context"
	"database/sql"
	"github.com/Zhengmingming1/tidebot/internal/pricing"
	"sync"
	"testing"
	"time"
)

func TestPolicyModesAccountingAndNoHiddenCap(t *testing.T) {
	s := holdService(t)
	ctx := context.Background()
	setDayCap(t, s, "task", "t", "", 0.01, pricing.USD)
	if err := s.RecordEntry(ctx, Entry{RunID: "used", TaskID: "t", Amount: .02, Currency: pricing.USD, Source: pricing.SourceClaude}); err != nil {
		t.Fatal(err)
	}
	chk, err := s.CheckBeforeRunPolicy(ctx, "t", BucketProduction, 0, ModeTask)
	if err != nil || chk.OK || chk.BreachScope != "task" {
		t.Fatalf("task: %+v %v", chk, err)
	}
	chk, err = s.CheckBeforeRunPolicy(ctx, "t", BucketProduction, 0, ModeGlobal)
	if err != nil || !chk.OK || !near(chk.RunMaxUSD, 4.98) {
		t.Fatalf("global should not use hidden five cents: %+v %v", chk, err)
	}
	h, cap, chk, err := s.ReserveRunPolicy(ctx, "t", BucketProduction, 0, pricing.USD, ModeBypass)
	if err != nil || !chk.OK || cap != 0 || chk.RunMaxTokens != 0 {
		t.Fatalf("bypass: %+v %v", chk, err)
	}
	defer h.Release()
	if held, n := s.Held("global", "", "", pricing.USD); held != 0 || n != 0 {
		t.Fatalf("bypass hold %v/%d", held, n)
	}
	if err := s.RecordEntry(ctx, Entry{RunID: "bypass", TaskID: "t", Amount: 6, Currency: pricing.USD, Source: pricing.SourceClaude, AccountingAt: h.AccountingAt()}); err != nil {
		t.Fatal(err)
	}
	chk, err = s.CheckBeforeRunPolicy(ctx, "other", BucketProduction, 0, ModeGlobal)
	if err != nil || chk.OK || chk.BreachScope != "global" {
		t.Fatalf("bypass spend must count: %+v %v", chk, err)
	}
	chk, err = s.CheckBeforeRunPolicy(ctx, "t", BucketProduction, .3, ModeBypass)
	if err != nil || !chk.OK || chk.RunMaxUSD != .3 {
		t.Fatalf("explicit caller cap retained: %+v %v", chk, err)
	}
}

func TestPolicyTokenReservationAndScopeFiltering(t *testing.T) {
	s := holdService(t)
	ctx := context.Background()
	if err := s.UpsertBudget(ctx, Budget{Scope: "task", ScopeID: "t", Period: "day", MaxTokens: sql.NullInt64{Int64: 100, Valid: true}, Currency: pricing.USD, Action: "skip"}); err != nil {
		t.Fatal(err)
	}
	h, _, chk, err := s.ReserveRunPolicy(ctx, "t", BucketProduction, 0, pricing.USD, ModeTask)
	if err != nil || !chk.OK || chk.RunMaxTokens != 100 {
		t.Fatalf("first: %+v %v", chk, err)
	}
	_, _, chk, err = s.ReserveRunPolicy(ctx, "t", BucketProduction, 0, pricing.USD, ModeTask)
	if err != nil || chk.OK || !chk.Held {
		t.Fatalf("tokens double reserved: %+v %v", chk, err)
	}
	b, _, chk, err := s.ReserveRunPolicy(ctx, "t", BucketProduction, 0, pricing.USD, ModeBypass)
	if err != nil || !chk.OK || chk.RunMaxTokens != 0 {
		t.Fatalf("bypass tokens: %+v %v", chk, err)
	}
	b.Release()
	h.Release()
	h, _, chk, err = s.ReserveRunPolicy(ctx, "t", BucketProduction, 0, pricing.USD, ModeTask)
	if err != nil || !chk.OK {
		t.Fatalf("release: %+v %v", chk, err)
	}
	h.Release()
}

func TestPolicyCurrencyAndCategory(t *testing.T) {
	s := holdService(t)
	ctx := context.Background()
	s.FX = func() pricing.FX { return pricing.FX{Display: pricing.CNY, USDCNY: 7.1} }
	setDayCap(t, s, "global", "", "", .2, pricing.USD)
	chk, err := s.CheckBeforeRunPolicy(ctx, "t", BucketAssist, 0, ModeGlobal)
	if err != nil || !chk.OK || chk.Currency != pricing.CNY || !near(chk.RunMaxUSD, 1.42) {
		t.Fatalf("CNY: %+v %v", chk, err)
	}
	chk, err = s.CheckBeforeRunPolicy(ctx, "t", BucketAssist, 0, ModeBypass)
	if err != nil || !chk.OK || chk.RunMaxUSD != 0 {
		t.Fatalf("category bypass: %+v %v", chk, err)
	}
}

func TestPolicyAccountingAtSurvivesMidnight(t *testing.T) {
	s := holdService(t)
	ctx := context.Background()
	s.Loc = time.UTC
	at := time.Date(2026, 1, 1, 23, 59, 0, 0, time.UTC)
	s.Now = func() time.Time { return at }
	h, _, chk, err := s.ReserveRunPolicy(ctx, "t", BucketProduction, .2, pricing.USD, ModeTask)
	if err != nil || !chk.OK {
		t.Fatalf("reserve: %+v %v", chk, err)
	}
	defer h.Release()
	at = at.Add(2 * time.Minute)
	if err := s.RecordEntry(ctx, Entry{RunID: "r", TaskID: "t", Amount: .1, Currency: pricing.USD, Source: pricing.SourceClaude, AccountingAt: h.AccountingAt()}); err != nil {
		t.Fatal(err)
	}
	var day string
	if err := s.DB.QueryRow(`SELECT day FROM cost_ledger WHERE run_id='r'`).Scan(&day); err != nil || day != "2026-01-01" {
		t.Fatalf("day %q %v", day, err)
	}
}

func TestPolicyConcurrentTokensAndDurableUsage(t *testing.T) {
	s := holdService(t)
	ctx := context.Background()
	if err := s.UpsertBudget(ctx, Budget{Scope: "task", ScopeID: "t", Period: "day", MaxTokens: sql.NullInt64{Int64: 100, Valid: true}, Currency: pricing.USD}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var holds []*Hold
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, _, chk, err := s.ReserveRunPolicy(ctx, "t", BucketProduction, 0, pricing.USD, ModeTask)
			if err != nil {
				t.Error(err)
			}
			if chk.OK {
				mu.Lock()
				holds = append(holds, h)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(holds) != 1 {
		t.Fatalf("admitted %d runs sharing all tokens", len(holds))
	}
	h := holds[0]
	defer h.Release()
	e := Entry{RunID: "r", TaskID: "t", Input: 20, Amount: .1, Currency: pricing.USD, Source: pricing.SourceClaude, AccountingAt: h.AccountingAt()}
	if err := s.RecordEntryHeld(ctx, e, h); err != nil {
		t.Fatal(err)
	}
	if held := s.heldTokens("task", "t", ""); held != 80 {
		t.Fatalf("held tokens %d", held)
	}
	_, _, chk, err := s.ReserveRunPolicy(ctx, "t", BucketProduction, 0, pricing.USD, ModeTask)
	if err != nil || chk.OK {
		t.Fatalf("ledger + hold should exhaust capacity: %+v %v", chk, err)
	}
	h.Release()
	h2, _, chk, err := s.ReserveRunPolicy(ctx, "t", BucketProduction, 0, pricing.USD, ModeTask)
	if err != nil || !chk.OK || chk.RunMaxTokens != 80 {
		t.Fatalf("remaining %v %+v", err, chk)
	}
	h2.Release()
}

func TestPolicyFrozenDaySurvivesTimezoneChange(t *testing.T) {
	s := holdService(t)
	ctx := context.Background()
	at := time.Date(2026, 1, 2, 0, 30, 0, 0, time.FixedZone("east", 8*3600))
	s.Loc = time.UTC
	s.Now = func() time.Time { return at.Add(time.Hour) }
	e := Entry{RunID: "tz", Amount: 1, Currency: pricing.USD, Source: pricing.SourceClaude, AccountingAt: at, AccountingDay: "2026-01-02"}
	if err := s.RecordEntry(ctx, e); err != nil {
		t.Fatal(err)
	}
	sum, err := s.Summary(ctx, "2026-01-02")
	if err != nil || sum.TotalUSD != 1 {
		t.Fatalf("frozen day %+v %v", sum, err)
	}
	sum, err = s.Summary(ctx, "2026-01-01")
	if err != nil || sum.TotalUSD != 0 {
		t.Fatalf("must not move to UTC previous day %+v %v", sum, err)
	}
	e.RunID = "bad"
	e.AccountingDay = "not-a-day"
	if err := s.RecordEntry(ctx, e); err == nil {
		t.Fatal("accepted invalid day")
	}
}

func TestPolicyRejectsInvalidModeAndDatabaseFailure(t *testing.T) {
	s := holdService(t)
	ctx := context.Background()
	if _, err := s.CheckBeforeRunPolicy(ctx, "t", BucketProduction, 0, Mode("unknown")); err == nil {
		t.Fatal("accepted invalid mode")
	}
	if err := s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	h, _, _, err := s.ReserveRunPolicy(ctx, "t", BucketProduction, 0, pricing.USD, ModeTask)
	if err == nil || h != nil {
		t.Fatalf("must not reserve after DB failure: %v %v", h, err)
	}
}

func TestPolicyNonMeteredReservesTokensNotMoney(t *testing.T) {
	s := holdService(t)
	ctx := context.Background()
	if err := s.UpsertBudget(ctx, Budget{Scope: "task", ScopeID: "sub", Period: "day", MaxTokens: sql.NullInt64{Int64: 100, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	h, cap, chk, err := s.ReserveRunPolicy(ctx, "sub", BucketProduction, 0, pricing.USD, ModeTask, false)
	if err != nil || !chk.OK || cap != 0 || h == nil || chk.RunMaxTokens != 100 {
		t.Fatalf("nonmetered: %v %+v %v", cap, chk, err)
	}
	defer h.Release()
	if amount, n := s.Held("global", "", "", pricing.USD); amount != 0 || n != 0 {
		t.Fatalf("money held %v by %d", amount, n)
	}
	if tok := s.heldTokens("task", "sub", ""); tok != 100 {
		t.Fatalf("tokens held %d", tok)
	}
}

func TestPolicyHoldConservativeAcrossFXChanges(t *testing.T) {
	for _, tc := range []struct {
		name, spendCurrency string
		rate, spent, want   float64
	}{
		{"usd_rate_up", pricing.USD, 14, .25, 10.5},
		{"cny_rate_down", pricing.CNY, 3.5, 1.75, 5.25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := holdService(t)
			ctx := context.Background()
			fx := pricing.FX{Display: pricing.CNY, USDCNY: 7}
			s.FX = func() pricing.FX { return fx }
			h, cap, chk, err := s.ReserveRunPolicy(ctx, "t", BucketProduction, 7, pricing.CNY, ModeTask)
			if err != nil || !chk.OK || cap != 7 {
				t.Fatalf("reserve %v %+v %v", cap, chk, err)
			}
			defer h.Release()
			fx.USDCNY = tc.rate
			h.SetUsage(tc.spent, tc.spendCurrency, 0)
			got, _ := s.Held("global", "", "", pricing.CNY)
			if !near(got, tc.want) {
				t.Fatalf("held %.4f want %.4f", got, tc.want)
			}
		})
	}
}
