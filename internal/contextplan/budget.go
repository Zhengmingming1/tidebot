package contextplan

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

var ErrBudget = errors.New("context budget exceeded; required instructions were not truncated")
var ErrAccess = errors.New("required context is outside the authorized scope")

type BudgetError struct{ RequiredTokens, BudgetTokens int }

func (e *BudgetError) Error() string {
	return fmt.Sprintf("%s (need at least %d estimated tokens, available %d)", ErrBudget, e.RequiredTokens, e.BudgetTokens)
}
func (e *BudgetError) Unwrap() error { return ErrBudget }

// Overlay preserves global reservations/window when a node changes only its cap.
// Zero means inherit (and finally use the documented default), never disable.
func (b Budget) Overlay(node Budget) Budget {
	if node.WindowTokens != 0 {
		b.WindowTokens = node.WindowTokens
	}
	if node.MaxInputTokens != 0 {
		b.MaxInputTokens = node.MaxInputTokens
	}
	if node.OutputReserve != 0 {
		b.OutputReserve = node.OutputReserve
	}
	if node.ToolReserve != 0 {
		b.ToolReserve = node.ToolReserve
	}
	if node.SystemReserve != 0 {
		b.SystemReserve = node.SystemReserve
	}
	return b
}

func (b Budget) Resolve() (Budget, int, error) {
	for _, n := range []int{b.WindowTokens, b.MaxInputTokens, b.OutputReserve, b.ToolReserve, b.SystemReserve} {
		if n < 0 || n > 2_000_000 {
			return b, 0, errors.New("context budget values must be between 0 and 2000000")
		}
	}
	if b.MaxInputTokens == 0 {
		b.MaxInputTokens = 65536
	}
	if b.OutputReserve == 0 {
		b.OutputReserve = 4096
	}
	if b.ToolReserve == 0 {
		b.ToolReserve = 8192
	}
	if b.SystemReserve == 0 {
		b.SystemReserve = 4096
	}
	available := b.MaxInputTokens
	if b.WindowTokens > 0 {
		available = min(available, b.WindowTokens-b.OutputReserve-b.ToolReserve)
	}
	available -= b.SystemReserve
	if available <= 0 {
		return b, 0, ErrBudget
	}
	return b, available, nil
}

// Estimate is deliberately versioned and shared with preflight. It is not a
// provider tokenizer. Runtime-generated system/tool context needs reservations.
func Estimate(data []byte) int {
	ascii, other := 0, 0
	for len(data) > 0 {
		if data[0] < utf8.RuneSelf {
			ascii++
			data = data[1:]
			continue
		}
		_, n := utf8.DecodeRune(data)
		other++
		data = data[n:]
	}
	return (ascii+3)/4 + other
}
