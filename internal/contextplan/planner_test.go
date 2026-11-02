package contextplan

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func material(id, body string) Material {
	return Material{ID: id, Kind: "skill", Scope: "run-a", Allowed: true, Required: true, Body: body, Version: "v1", Ref: "skill:one@v1"}
}
func request(ms ...Material) Request {
	return Request{Scope: "run-a", Budget: Budget{MaxInputTokens: 100, SystemReserve: 1}, Materials: ms}
}

func TestDeterministicVersionHashDedupAndAudit(t *testing.T) {
	a, b := material("a", "private instruction sentinel"), material("b", "private instruction sentinel")
	p, e := (Deterministic{}).Plan(context.Background(), request(a, b))
	if e != nil || len(p.Materials) != 1 || len(p.Omitted) != 1 {
		t.Fatal(p, e)
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "private instruction sentinel") {
		t.Fatal("raw material leaked")
	}
	q, e := (Deterministic{}).Plan(context.Background(), request(b, a))
	if e != nil || q.ID != p.ID {
		t.Fatal("non-deterministic plan", q, e)
	}
	for _, variant := range []Material{func() Material { c := b; c.Version = "v2"; return c }(), func() Material { c := b; c.Ref = "skill:other@v1"; return c }(), material("b", "different instructions")} {
		q, e := (Deterministic{}).Plan(context.Background(), request(a, variant))
		if e != nil || len(q.Materials) != 2 {
			t.Fatal("over-broad dedup", q, e)
		}
	}
}

func TestAccessBeforeLoadAndHashVerification(t *testing.T) {
	loads := 0
	a := material("foreign", "")
	a.Scope = "run-b"
	a.Load = func(context.Context) (string, error) { loads++; return "secret", nil }
	if _, e := (Deterministic{}).Plan(context.Background(), request(a)); !errors.Is(e, ErrAccess) || loads != 0 {
		t.Fatal(e, loads)
	}
	a.Required = false
	p, e := (Deterministic{}).Plan(context.Background(), request(a))
	if e != nil || loads != 0 || p.Omitted[0].Reason != "access_denied" {
		t.Fatal(p, e)
	}
	a = material("changed", "changed")
	a.Hash = Hash("original")
	if _, e := (Deterministic{}).Plan(context.Background(), request(a)); e == nil {
		t.Fatal("hash mismatch accepted")
	}
}

func TestLazyDuplicateOnlyLoadsOnce(t *testing.T) {
	loads := 0
	a := material("a", "")
	a.Hash = Hash("frozen")
	a.Load = func(context.Context) (string, error) { loads++; return "frozen", nil }
	b := a
	b.ID = "b"
	if _, e := (Deterministic{}).Plan(context.Background(), request(a, b)); e != nil || loads != 1 {
		t.Fatal(e, loads)
	}
}

func TestBudgetReservationsRequiredAndPriority(t *testing.T) {
	b, available, e := (Budget{WindowTokens: 32768, MaxInputTokens: 64000, OutputReserve: 4096, ToolReserve: 8192, SystemReserve: 4096}).Resolve()
	if e != nil || available != 16384 || b.WindowTokens != 32768 {
		t.Fatal(b, available, e)
	}
	a := material("required", strings.Repeat("x", 400))
	if _, e := (Deterministic{}).Plan(context.Background(), request(a)); !errors.Is(e, ErrBudget) {
		t.Fatal(e)
	}
	a = material("task", "always")
	b1, b2 := material("first", strings.Repeat("a", 240)), material("second", strings.Repeat("b", 240))
	b1.Required = false
	b2.Required = false
	b1.Priority = 10
	p, e := (Deterministic{}).Plan(context.Background(), request(b2, b1, a))
	if e != nil || len(p.Materials) != 2 || p.Materials[1].ID != "first" || p.Omitted[0].Reason != "budget" {
		t.Fatal(p, e)
	}
	for _, bad := range []Budget{{MaxInputTokens: -1}, {WindowTokens: 1}, {OutputReserve: 2000001}} {
		if _, _, e := bad.Resolve(); e == nil {
			t.Fatal("bad budget accepted")
		}
	}
}

func TestCompactPreservesFailureEvidenceAndReference(t *testing.T) {
	text := strings.Repeat("normal output\n", 1000) + "FAIL TestCritical\nerror at main.go:42\n" + strings.Repeat("中文输出\n", 1000)
	got := Compact(text, "workflow:one/node:a", 256)
	if !utf8.ValidString(got) || Estimate([]byte(got)) > 256 || !strings.Contains(got, "FAIL TestCritical") || !strings.Contains(got, Hash(text)) || !strings.Contains(got, "Lossy") {
		t.Fatal(got)
	}
	if Compact("small", "ref", 256) != "small" {
		t.Fatal("unnecessary compaction")
	}
}

func TestCanceledAndConcurrentPlans(t *testing.T) {
	r := request(material("a", "instructions"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := (Deterministic{}).Plan(ctx, r); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	want, _ := (Deterministic{}).Plan(context.Background(), r)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, e := (Deterministic{}).Plan(context.Background(), r)
			if e != nil || p.ID != want.ID {
				t.Error("unstable concurrent plan", e)
			}
		}()
	}
	wg.Wait()
}

func BenchmarkPlan16Skills(b *testing.B) {
	r := request()
	r.Budget = Budget{}
	for i := 0; i < 16; i++ {
		m := material(string(rune('a'+i)), strings.Repeat("instruction\n", 300))
		m.Ref = m.ID
		r.Materials = append(r.Materials, m)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, e := (Deterministic{}).Plan(context.Background(), r); e != nil {
			b.Fatal(e)
		}
	}
}

func TestNodeBudgetInheritsModelWindow(t *testing.T) {
	b := Budget{WindowTokens: 32768, OutputReserve: 4096, ToolReserve: 8192, SystemReserve: 4096}.Overlay(Budget{MaxInputTokens: 65536})
	if _, available, e := b.Resolve(); e != nil || available != 16384 {
		t.Fatal(b, available, e)
	}
}

func TestBudgetExhaustionDoesNotLoadOptionalBody(t *testing.T) {
	a := material("required", strings.Repeat("x", 396))
	b := material("optional", "")
	b.Required = false
	b.Load = func(context.Context) (string, error) {
		t.Fatal("optional body loaded despite no space")
		return "", nil
	}
	p, e := (Deterministic{}).Plan(context.Background(), request(a, b))
	if e != nil || p.Omitted[0].Reason != "budget" {
		t.Fatal(p, e)
	}
}
