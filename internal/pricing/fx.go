package pricing

import "errors"

// FX is the user's display currency and a fixed USD→CNY rate they set.
// tidebot never fetches exchange rates.
type FX struct {
	Display string  `json:"display"` // USD | CNY
	USDCNY  float64 `json:"usd_cny"` // 1 USD = USDCNY CNY
}

// DefaultFX is used until the user sets one.
var DefaultFX = FX{Display: USD, USDCNY: 7.1}

// Validate checks an FX setting.
func (f FX) Validate() error {
	if f.Display != USD && f.Display != CNY {
		return errors.New("display currency must be USD or CNY")
	}
	if f.USDCNY < 0.1 || f.USDCNY > 100 {
		return errors.New("exchange rate must be between 0.1 and 100")
	}
	return nil
}

// Factor converts 1 unit of from into to.
func (f FX) Factor(from, to string) float64 {
	if from == "" {
		from = USD
	}
	if to == "" {
		to = USD
	}
	r := f.USDCNY
	if r <= 0 {
		r = DefaultFX.USDCNY
	}
	switch {
	case from == to:
		return 1
	case from == USD && to == CNY:
		return r
	case from == CNY && to == USD:
		return 1 / r
	}
	return 1
}

// Convert converts an amount.
func (f FX) Convert(amount float64, from, to string) float64 { return amount * f.Factor(from, to) }

// Total returns a cost's amount in currency to, summing lines in other
// currencies at the fixed rate (Compute only sums lines in its own currency).
func (c Cost) Total(f FX, to string) float64 {
	if c.Source == SourceClaude || c.Source == SourceLegacy || len(c.Lines) == 0 {
		return f.Convert(c.Amount, c.Currency, to)
	}
	t := 0.0
	for _, l := range c.Lines {
		if l.Currency != "" {
			t += f.Convert(l.Amount, l.Currency, to)
		}
	}
	return t
}
