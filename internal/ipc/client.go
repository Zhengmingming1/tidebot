package ipc

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
)

// Client is a JSON-RPC client over a single connection (mutexed).
type Client struct {
	conn net.Conn
	enc  *json.Encoder
	dec  *json.Decoder
	mu   sync.Mutex
	seq  atomic.Int64
}

// DialClient connects to the daemon IPC endpoint.
func DialClient(path string) (*Client, error) {
	conn, err := Dial(path)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, enc: json.NewEncoder(conn), dec: json.NewDecoder(conn)}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

// Call invokes method and decodes result into out (may be nil).
func (c *Client) Call(method string, params any, out any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.seq.Add(1)
	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		rawParams = b
	}
	req := Request{JSONRPC: JSONRPCVersion, ID: id, Method: method, Params: rawParams}
	if err := c.enc.Encode(req); err != nil {
		return err
	}
	var resp Response
	if err := c.dec.Decode(&resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("rpc %s: %s", method, resp.Error.Message)
	}
	if out == nil || len(resp.Result) == 0 || string(resp.Result) == "null" {
		return nil
	}
	return json.Unmarshal(resp.Result, out)
}
