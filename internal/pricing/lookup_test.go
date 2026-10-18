package pricing

import "testing"

func TestCalculatorCatalogFallback(t *testing.T) {
	list := func(m string) *ModelPrice {
		if m == "glm-9" {
			return &ModelPrice{Model: "glm-9", Currency: USD, Input: F(1), Output: F(2)}
		}
		return nil
	}
	own := []ModelPrice{{Model: "glm-9", Currency: CNY, Input: F(4), Output: F(16)}}
	u := []Usage{{Model: "glm-9", Input: 1_000_000, Output: 1_000_000}}

	// the user's / official price wins over the catalog
	c := Calculator{Billing: Billing{Mode: ModeMetered}, Prices: own, Lookup: list}.Compute(u, nil, 0)
	if c.Amount != 20 || c.Currency != CNY || c.Source != SourcePriced || c.Lines[0].Catalog {
		t.Fatalf("own price: %+v", c)
	}
	// missing → catalog list price, flagged as an estimate
	c = Calculator{Billing: Billing{Mode: ModeMetered}, Lookup: list}.Compute(u, nil, 0)
	if c.Amount != 3 || c.Currency != USD || c.Source != SourceEstimate || !c.Lines[0].Catalog {
		t.Fatalf("catalog fallback: %+v", c)
	}
	// subscription plans are never priced from the catalog
	c = Calculator{Billing: Billing{Mode: ModeSubscription}, Lookup: list}.Compute(u, nil, 0)
	if c.Amount != 0 || c.Source != SourceSubscription {
		t.Fatalf("subscription: %+v", c)
	}
	// unknown model stays missing
	c = Calculator{Billing: Billing{Mode: ModeMetered}, Lookup: list}.Compute([]Usage{{Model: "x", Input: 10}}, nil, 0)
	if !c.Lines[0].Missing || c.Source != SourceEstimate {
		t.Fatalf("missing: %+v", c)
	}
}
