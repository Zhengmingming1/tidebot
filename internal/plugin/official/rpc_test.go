package official

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestConnCallFailureCleansPending(t *testing.T) {
	c := NewConn(brokenWriter{})
	for i := 0; i < 10; i++ {
		if err := c.Call("ping", nil, nil); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatal(err)
		}
	}
	if len(c.pending) != 0 {
		t.Fatal("pending call leak")
	}
	if err := c.Serve(strings.NewReader(""), nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call("ping", nil, nil); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
}

func TestServeDrainsHandlersAndContainsPanic(t *testing.T) {
	var out bytes.Buffer
	c := NewConn(&out)
	input := `{"jsonrpc":"2.0","id":1,"method":"panic"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"call"}` + "\n"
	err := c.Serve(strings.NewReader(input), func(method string, _ json.RawMessage) (any, error) {
		if method == "panic" {
			panic("private-fixture-value")
		}
		return nil, c.Call("host.request", nil, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "plugin handler panicked") || strings.Contains(out.String(), "private-fixture-value") {
		t.Fatal(out.String())
	}
	for _, id := range []string{`"id":1`, `"id":2`} {
		if !strings.Contains(out.String(), id) {
			t.Fatal("missing response", out.String())
		}
	}
}
