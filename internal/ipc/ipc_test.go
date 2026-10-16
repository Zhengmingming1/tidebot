package ipc

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestUnixJSONRPCRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "t.sock")
	srv := NewServer(nil)
	srv.Register("echo", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct {
			Msg string `json:"msg"`
		}
		_ = json.Unmarshal(params, &p)
		return map[string]string{"echo": p.Msg}, nil
	})
	ln, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ctx, ln) }()

	time.Sleep(50 * time.Millisecond)
	cli, err := DialClient(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	var out map[string]string
	if err := cli.Call("echo", map[string]string{"msg": "hi"}, &out); err != nil {
		t.Fatal(err)
	}
	if out["echo"] != "hi" {
		t.Fatalf("%v", out)
	}
	if err := cli.Call("missing", nil, nil); err == nil {
		t.Fatal("expected method error")
	}
	cancel()
}
