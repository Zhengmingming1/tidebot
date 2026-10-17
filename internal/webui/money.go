package webui

import (
	"fmt"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/pricing"
)

// Money shown in the UI is in the user's display currency, converted at the
// fixed rate they set (Settings → 预算 / 币种). Field names keep "usd" for
// API compatibility; responses carry "currency".

func (s *Server) fx() pricing.FX {
	if s.deps.Cost == nil {
		return pricing.DefaultFX
	}
	return s.deps.Cost.GetFX()
}

// ledgerAmt is a SQL expression for a cost_ledger row's amount in the display currency.
func (s *Server) ledgerAmt(alias string) string {
	f := s.fx()
	return cost.AmountExpr(f, f.Display, alias)
}

// runAmt is a SQL expression for a run's cost in the display currency. Runs
// recorded before tidebot priced runs itself fall back to total_cost_usd.
func (s *Server) runAmt(alias string) string {
	f := s.fx()
	return fmt.Sprintf("(CASE WHEN %[1]scost_source='' THEN COALESCE(%[1]stotal_cost_usd,0)*%[2]g WHEN %[1]scost_currency='CNY' THEN %[1]scost_amount*%[3]g ELSE %[1]scost_amount*%[2]g END)",
		alias, f.Factor(pricing.USD, f.Display), f.Factor(pricing.CNY, f.Display))
}
