package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
)

// HandlerFunc handles one RPC method.
type HandlerFunc func(ctx context.Context, params json.RawMessage) (any, error)

// Server serves JSON-RPC over a stream (one request/response per connection line-framed by newline, or whole-conn length).
// Framing: each message is a single JSON object terminated by '\n'.
type Server struct {
	Log      *slog.Logger
	handlers map[string]HandlerFunc
	mu       sync.RWMutex
	ln       net.Listener
}

func NewServer(log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{Log: log, handlers: map[string]HandlerFunc{}}
}

// Register adds a method handler.
func (s *Server) Register(method string, h HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = h
}

// Serve starts accepting connections until ctx cancelled or listener closed.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.ln = ln
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)
	for {
		var req Request
		if err := dec.Decode(&req); err != nil {
			if err != io.EOF {
				s.Log.Debug("ipc decode", "error", err)
			}
			return
		}
		resp := s.dispatch(ctx, req)
		if err := enc.Encode(resp); err != nil {
			return
		}
	}
}

func (s *Server) dispatch(ctx context.Context, req Request) Response {
	resp := Response{JSONRPC: JSONRPCVersion, ID: req.ID}
	if req.JSONRPC != "" && req.JSONRPC != JSONRPCVersion {
		resp.Error = &ErrorObj{Code: ErrInvalidRequest, Message: "jsonrpc must be 2.0"}
		return resp
	}
	s.mu.RLock()
	h, ok := s.handlers[req.Method]
	s.mu.RUnlock()
	if !ok {
		resp.Error = &ErrorObj{Code: ErrMethodNotFound, Message: fmt.Sprintf("method %q not found", req.Method)}
		return resp
	}
	result, err := h(ctx, req.Params)
	if err != nil {
		resp.Error = &ErrorObj{Code: ErrApp, Message: err.Error()}
		return resp
	}
	b, err := json.Marshal(result)
	if err != nil {
		resp.Error = &ErrorObj{Code: ErrInternal, Message: err.Error()}
		return resp
	}
	resp.Result = b
	return resp
}

// Close closes the listener if set.
func (s *Server) Close() error {
	if s.ln != nil {
		return s.ln.Close()
	}
	return nil
}
