package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestServerBasics(t *testing.T) {
	s := NewServer("x", "1")
	s.RegisterTool(Tool{Name: "echo", InputSchema: map[string]any{"type": "object"}, Handler: func(_ context.Context, a map[string]any) (*CallToolResult, error) {
		return TextResult(a["x"].(string)), nil
	}})
	var out bytes.Buffer
	in := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025"}}
{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"x":"ok"}}}
`)
	if err := s.Serve(in, &out); err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte{'\n'})
	if len(lines) != 3 {
		t.Fatalf("lines=%d", len(lines))
	}
	for _, line := range lines {
		var v map[string]any
		if err := json.Unmarshal(line, &v); err != nil {
			t.Fatal(err)
		}
	}
}
