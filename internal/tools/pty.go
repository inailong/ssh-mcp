package tools

import (
	"context"
	"fmt"
	"github.com/inailong/ssh-mcp/internal/mcp"
	"github.com/inailong/ssh-mcp/internal/pty"
	"time"
)

func RegisterPTY(server *mcp.Server, manager *pty.Manager) {
	schema := func(required []string, props map[string]any) map[string]any {
		return map[string]any{"type": "object", "required": required, "properties": props}
	}
	server.RegisterTool(mcp.Tool{Name: "session-open", InputSchema: schema(nil, map[string]any{"connectionName": map[string]any{"type": "string"}, "cmdString": map[string]any{"type": "string"}, "cols": map[string]any{"type": "integer", "default": 120}, "rows": map[string]any{"type": "integer", "default": 40}}), Handler: func(ctx context.Context, a map[string]any) (*mcp.CallToolResult, error) {
		name, _ := a["connectionName"].(string)
		cmd, _ := a["cmdString"].(string)
		cols := intNum(a["cols"], 120)
		rows := intNum(a["rows"], 40)
		id, e := manager.Open(ctx, name, cmd, cols, rows)
		if e != nil {
			return nil, e
		}
		return mcp.TextResult(fmt.Sprintf(`{"sessionId":%q,"status":"opened"}`, id)), nil
	}})
	server.RegisterTool(mcp.Tool{Name: "session-send", InputSchema: schema([]string{"sessionId", "input"}, map[string]any{"sessionId": map[string]any{"type": "string"}, "input": map[string]any{"type": "string"}, "enter": map[string]any{"type": "boolean", "default": true}}), Handler: func(ctx context.Context, a map[string]any) (*mcp.CallToolResult, error) {
		s, e := manager.Get(fmt.Sprint(a["sessionId"]))
		if e != nil {
			return nil, e
		}
		enter, ok := a["enter"].(bool)
		if !ok {
			enter = true
		}
		n, e := s.Send(fmt.Sprint(a["input"]), enter)
		if e != nil {
			return nil, e
		}
		return mcp.TextResult(fmt.Sprintf(`{"sessionId":%q,"bytesWritten":%d}`, s.ID, n)), nil
	}})
	server.RegisterTool(mcp.Tool{Name: "session-read", InputSchema: schema([]string{"sessionId"}, map[string]any{"sessionId": map[string]any{"type": "string"}, "maxBytes": map[string]any{"type": "integer", "default": 65536}, "timeoutMs": map[string]any{"type": "integer", "default": 500}}), Handler: func(ctx context.Context, a map[string]any) (*mcp.CallToolResult, error) {
		s, e := manager.Get(fmt.Sprint(a["sessionId"]))
		if e != nil {
			return nil, e
		}
		return mcp.TextResult(s.Read(intNum(a["maxBytes"], 65536), time.Duration(intNum(a["timeoutMs"], 500))*time.Millisecond)), nil
	}})
	server.RegisterTool(mcp.Tool{Name: "session-close", InputSchema: schema([]string{"sessionId"}, map[string]any{"sessionId": map[string]any{"type": "string"}}), Handler: func(ctx context.Context, a map[string]any) (*mcp.CallToolResult, error) {
		id := fmt.Sprint(a["sessionId"])
		if e := manager.Close(id); e != nil {
			return nil, e
		}
		return mcp.TextResult(fmt.Sprintf(`{"sessionId":%q,"status":"closed"}`, id)), nil
	}})

}
func intNum(v any, d int) int {
	switch n := v.(type) {
	case float64:
		if n > 0 {
			return int(n)
		}
	case int:
		if n > 0 {
			return n
		}
	}
	return d
}
