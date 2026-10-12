package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/inailong/ssh-mcp/internal/mcp"
	"github.com/inailong/ssh-mcp/internal/sshpool"
)

func RegisterServers(server *mcp.Server, pool *sshpool.Pool) {
	server.RegisterTool(mcp.Tool{Name: "list-servers", Description: "List configured SSH servers", InputSchema: map[string]any{"type": "object"}, Handler: func(context.Context, map[string]any) (*mcp.CallToolResult, error) {
		items := pool.ListServers()
		b, _ := json.Marshal(items)
		lines := ""
		for _, x := range items {
			state := "disconnected"
			if x.Connected {
				state = "connected"
			}
			lines += fmt.Sprintf("[%s] %s | %s@%s:%d\n", state, x.Name, x.Username, x.Host, x.Port)
		}
		return mcp.TextResult(lines + "\nRaw JSON:\n" + string(b)), nil
	}})
}
