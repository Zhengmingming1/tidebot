package main

import (
	"reflect"
	"testing"
)

func TestInitialWindowStart(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		name := "normal launch"
		want := []string{"create", "configure", "show"}
		if hidden {
			name = "hidden autostart"
			want = []string{"create", "configure"}
		}
		t.Run(name, func(t *testing.T) {
			var calls []string
			start := initialWindowStart(hidden,
				func() { calls = append(calls, "create") },
				func() { calls = append(calls, "configure") },
				func() { calls = append(calls, "show") },
			)
			if len(calls) != 0 {
				t.Fatal("native window must wait for the application-start event")
			}
			start()
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("startup calls = %v, want %v", calls, want)
			}
			start()
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("repeated startup reset the window: %v", calls)
			}
		})
	}
}
