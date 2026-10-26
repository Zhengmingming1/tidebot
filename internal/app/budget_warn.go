package app

import (
	"strings"
	"sync"
	"time"
)

var budgetWarned sync.Map // "<day>|<scope>" → struct{}

// warnOnce emits one budget_warning event per scope per day when spend crosses warn_at_pct.
func (a *App) warnOnce(detail string) {
	scope := detail
	if i := strings.Index(detail, " "); i > 0 {
		scope = detail[:i]
	}
	key := time.Now().In(a.Loc()).Format("2006-01-02") + "|" + scope
	if _, dup := budgetWarned.LoadOrStore(key, struct{}{}); dup {
		return
	}
	a.Events.Emit(Event{Kind: EventBudgetWarning, Detail: detail})
}
