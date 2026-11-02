package ipc

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

func TestIdleConnectionClosesOnCancellation(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	s := NewServer(nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.handleConn(ctx, server); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("idle IPC handler survived cancellation")
	}
}
func TestHandlerPanicDoesNotTerminateServer(t *testing.T) {
	s := NewServer(nil)
	s.Register("panic", func(context.Context, json.RawMessage) (any, error) { panic("fixture-private-value") })
	r := s.dispatch(context.Background(), Request{Method: "panic"})
	if r.Error == nil || r.Error.Code != ErrInternal || strings.Contains(r.Error.Message, "fixture-private-value") {
		t.Fatalf("%+v", r)
	}
	s.Register("ping", func(context.Context, json.RawMessage) (any, error) { return true, nil })
	if r := s.dispatch(context.Background(), Request{Method: "ping"}); r.Error != nil {
		t.Fatal(r.Error)
	}
}
