package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

type Tool struct {
	Name        string                                                         `json:"name"`
	Description string                                                         `json:"description,omitempty"`
	InputSchema map[string]any                                                 `json:"inputSchema"`
	Handler     func(context.Context, map[string]any) (*CallToolResult, error) `json:"-"`
}
type CallToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func TextResult(s string) *CallToolResult {
	return &CallToolResult{Content: []ContentBlock{{"text", s}}}
}
func ErrorResult(c, m string, r bool) *CallToolResult {
	b, _ := json.Marshal(map[string]any{"code": c, "message": m, "retriable": r})
	return &CallToolResult{Content: []ContentBlock{{"text", string(b)}}, IsError: true}
}

type Server struct {
	name, version string
	mu            sync.RWMutex
	tools         map[string]Tool
	writeMu       sync.Mutex
}
type req struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}
type resp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   any             `json:"error,omitempty"`
}

func NewServer(n, v string) *Server   { return &Server{name: n, version: v, tools: map[string]Tool{}} }
func (s *Server) RegisterTool(t Tool) { s.mu.Lock(); s.tools[t.Name] = t; s.mu.Unlock() }
func (s *Server) ServeStdio() error {
	return s.Serve(os.Stdin, os.Stdout)
}
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	var wg sync.WaitGroup
	for sc.Scan() {
		var q req
		if e := json.Unmarshal(sc.Bytes(), &q); e != nil {
			return e
		}
		if len(q.ID) == 0 || string(q.ID) == "null" {
			continue
		}
		wg.Add(1)
		go func() { defer wg.Done(); s.handle(q, w) }()
	}
	wg.Wait()
	return sc.Err()
}
func (s *Server) handle(q req, w io.Writer) {
	var x any
	var rpcError any
	switch q.Method {
	case "initialize":
		var p map[string]any
		_ = json.Unmarshal(q.Params, &p)
		v := "2024-11-05"
		if z, ok := p["protocolVersion"].(string); ok && z != "" {
			v = z
		}
		x = map[string]any{"protocolVersion": v, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": s.name, "version": s.version}}
	case "ping":
		x = map[string]any{}
	case "tools/list":
		s.mu.RLock()
		a := []Tool{}
		for _, t := range s.tools {
			a = append(a, t)
		}
		s.mu.RUnlock()
		x = map[string]any{"tools": a}
	case "tools/call":
		x = s.call(q.Params)
	default:
		rpcError = map[string]any{"code": -32601, "message": "method not found"}
	}
	b, _ := json.Marshal(resp{"2.0", q.ID, x, rpcError})
	s.writeMu.Lock()
	_, _ = w.Write(append(b, '\n'))
	s.writeMu.Unlock()
}
func (s *Server) call(b json.RawMessage) (out *CallToolResult) {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if e := json.Unmarshal(b, &p); e != nil {
		return ErrorResult("invalid_arguments", e.Error(), false)
	}
	s.mu.RLock()
	t, ok := s.tools[p.Name]
	s.mu.RUnlock()
	if !ok {
		return ErrorResult("tool_not_found", p.Name, false)
	}
	if t.Handler == nil {
		return ErrorResult("invalid_tool", "handler is nil", false)
	}
	defer func() {
		if r := recover(); r != nil {
			out = ErrorResult("internal_error", fmt.Sprint(r), false)
		}
	}()
	x, e := t.Handler(context.Background(), p.Arguments)
	if e != nil {
		return ErrorResult("tool_error", e.Error(), false)
	}
	return x
}
