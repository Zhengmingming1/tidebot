package official

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"
)

type msg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Handler answers one incoming request.
type Handler func(method string, params json.RawMessage) (any, error)

// Conn is a bidirectional line-delimited JSON-RPC 2.0 connection (the plugin side).
type Conn struct {
	w       io.Writer
	wmu     sync.Mutex
	mu      sync.Mutex
	next    int64
	pending map[int64]chan msg
	closed  bool
	// ErrMethodNotFound-style errors use code -32601.
}

// NewConn creates a connection writing to w.
func NewConn(w io.Writer) *Conn { return &Conn{w: w, pending: map[int64]chan msg{}} }

func (c *Conn) send(m msg) error {
	m.JSONRPC = "2.0"
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.w.Write(append(b, '\n'))
	return err
}

// Call sends a request to the host and waits for its answer.
func (c *Conn) Call(method string, params any, out any) error {
	pb, err := json.Marshal(params)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return io.ErrClosedPipe
	}
	c.next++
	id := c.next
	ch := make(chan msg, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	if err := c.send(msg{ID: &id, Method: method, Params: pb}); err != nil {
		return err
	}
	select {
	case r, ok := <-ch:
		if !ok {
			return errors.New("connection closed")
		}
		if r.Error != nil {
			return errors.New(r.Error.Message)
		}
		if out != nil {
			return json.Unmarshal(r.Result, out)
		}
		return nil
	case <-time.After(30 * time.Second):
		return errors.New("host call timed out")
	}
}

// Notify sends a notification (no answer expected).
func (c *Conn) Notify(method string, params any) error {
	pb, _ := json.Marshal(params)
	return c.send(msg{Method: method, Params: pb})
}

// Serve reads r until EOF, dispatching requests to h (each in its own goroutine) and
// routing responses to pending Calls.
func (c *Conn) Serve(r io.Reader, h Handler) error {
	var handlers sync.WaitGroup
	// Never block the reader on a handler: handlers may be waiting for a host
	// response on this same connection. Reject overload instead of spawning an
	// unbounded number of goroutines.
	slots := make(chan struct{}, 64)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		var m msg
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if m.Method != "" {
			select {
			case slots <- struct{}{}:
			default:
				if m.ID != nil {
					_ = c.send(msg{ID: m.ID, Error: &rpcError{Code: -32000, Message: "too many concurrent requests"}})
				}
				continue
			}
			handlers.Add(1)
			go func(m msg) {
				defer handlers.Done()
				defer func() { <-slots }()
				res, err := callHandler(h, m.Method, m.Params)
				if m.ID == nil {
					return
				}
				out := msg{ID: m.ID}
				if err != nil {
					code := -32000
					if errors.Is(err, errNoMethod) {
						code = -32601
					}
					out.Error = &rpcError{Code: code, Message: err.Error()}
				} else {
					out.Result, _ = json.Marshal(res)
					if res == nil {
						out.Result = json.RawMessage("{}")
					}
				}
				_ = c.send(out)
			}(m)
			continue
		}
		if m.ID != nil {
			c.mu.Lock()
			ch := c.pending[*m.ID]
			delete(c.pending, *m.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		}
	}
	c.mu.Lock()
	c.closed = true
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	// A successful return means all response writes finished; callers can safely
	// consume the output without sleeps or concurrent buffer reads.
	handlers.Wait()
	return sc.Err()
}

func callHandler(h Handler, method string, params json.RawMessage) (result any, err error) {
	defer func() {
		if recover() != nil {
			// A panic can contain credentials from a plugin. Keep it out of RPC.
			result, err = nil, errors.New("plugin handler panicked")
		}
	}()
	return h(method, params)
}

var errNoMethod = errors.New("method not found")
