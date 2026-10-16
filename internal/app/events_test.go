package app

import "testing"

func TestBusNonBlocking(t *testing.T) {
	var nilBus *Bus
	nilBus.Emit(Event{Kind: EventRunFinished})
	b := &Bus{}
	ch := b.Subscribe(1)
	b.Emit(Event{Kind: EventRunFailed})
	b.Emit(Event{Kind: EventRunFinished}) // dropped, must not block
	if e := <-ch; e.Kind != EventRunFailed {
		t.Fatal(e.Kind)
	}
	select {
	case e := <-ch:
		t.Fatalf("unexpected %v", e)
	default:
	}
}
