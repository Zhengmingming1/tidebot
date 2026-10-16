package webui

import "testing"

func TestParseBudgetDetail(t *testing.T) {
	c, u, l := parseBudgetDetail("task/Backup daily USD cap reached (0.1884/0.0100)")
	if c != "usd_cap" || u != "0.1884" || l != "0.0100" {
		t.Fatal(c, u, l)
	}
	if c, _, _ := parseBudgetDetail("global/ daily token cap reached (12/10)"); c != "token_cap" {
		t.Fatal(c)
	}
	if c, _, _ := parseBudgetDetail("something else"); c != "" {
		t.Fatal(c)
	}
}
