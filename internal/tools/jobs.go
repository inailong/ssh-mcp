package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/inailong/ssh-mcp/internal/mcp"
	sshpool "github.com/inailong/ssh-mcp/internal/sshpool"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func jobsDir() string { h, _ := os.UserHomeDir(); return filepath.Join(h, ".ssh-mcp-jobs") }
func jobDir(id string) string {
	if id == "" || strings.Contains(id, "/") || strings.Contains(id, "\\") || id == "." || id == ".." {
		return ""
	}
	return filepath.Join(jobsDir(), id)
}
func writeMeta(d, n, v string) error { return os.WriteFile(filepath.Join(d, n), []byte(v), 0644) }
func readMeta(d, n string) string    { b, _ := os.ReadFile(filepath.Join(d, n)); return string(b) }
func RegisterJobs(server *mcp.Server, pool *sshpool.Pool) {
	server.RegisterTool(mcp.Tool{Name: "job-start", InputSchema: schema([]string{"cmdString"}, map[string]any{"cmdString": map[string]any{"type": "string"}, "directory": map[string]any{"type": "string"}, "connectionName": map[string]any{"type": "string"}}), Handler: func(ctx context.Context, a map[string]any) (*mcp.CallToolResult, error) {
		return jobStart(ctx, pool, a)
	}})
	server.RegisterTool(mcp.Tool{Name: "job-status", InputSchema: schema([]string{"jobId"}, map[string]any{"jobId": map[string]any{"type": "string"}, "tailLines": map[string]any{"type": "number", "default": 100}, "connectionName": map[string]any{"type": "string"}}), Handler: func(ctx context.Context, a map[string]any) (*mcp.CallToolResult, error) {
		return jobStatus(ctx, pool, a)
	}})
	server.RegisterTool(mcp.Tool{Name: "job-kill", InputSchema: schema([]string{"jobId"}, map[string]any{"jobId": map[string]any{"type": "string"}, "connectionName": map[string]any{"type": "string"}}), Handler: func(ctx context.Context, a map[string]any) (*mcp.CallToolResult, error) { return jobKill(ctx, pool, a) }})
	server.RegisterTool(mcp.Tool{Name: "job-list", InputSchema: schema(nil, map[string]any{"limit": map[string]any{"type": "number", "default": 20}, "connectionName": map[string]any{"type": "string"}}), Handler: func(ctx context.Context, a map[string]any) (*mcp.CallToolResult, error) { return jobList(ctx, pool, a) }})
}
func remoteRun(ctx context.Context, pool *sshpool.Pool, a map[string]any, cmd string) (string, error) {
	name, _ := a["connectionName"].(string)
	target, e := pool.ResolveTarget(name)
	if e != nil {
		return "", e
	}
	c, e := pool.GetClient(ctx, target)
	if e != nil {
		return "", e
	}
	s, e := c.NewSession()
	if e != nil {
		return "", e
	}
	defer s.Close()
	out, e := s.CombinedOutput(cmd)
	return string(out), e
}
func jobStart(ctx context.Context, pool *sshpool.Pool, a map[string]any) (*mcp.CallToolResult, error) {
	cmd, ok := a["cmdString"].(string)
	if !ok || cmd == "" {
		return nil, fmt.Errorf("cmdString is required")
	}
	id := fmt.Sprintf("job-%d-%04x", time.Now().UnixNano(), time.Now().UnixNano()&0xffff)
	d := jobDir(id)
	if e := os.MkdirAll(d, 0700); e != nil {
		return nil, e
	}
	dir, _ := a["directory"].(string)
	if dir == "" {
		dir = "~"
	}
	if _, e := remoteRun(ctx, pool, a, fmt.Sprintf("mkdir -p %q; printf '%%s' %q > %q; printf '%%s' %q > %q; : > %q; (cd %q && setsid sh -c %q > %q 2>&1; printf '%%s' $? > %q) & echo $! > %q", jobsDir(), cmd, filepath.Join(d, "cmd"), dir, filepath.Join(d, "dir"), filepath.Join(d, "out"), dir, cmd, filepath.Join(d, "out"), filepath.Join(d, "exit"), filepath.Join(d, "pid"))); e != nil {
		return nil, e
	}
	return mcp.TextResult(fmt.Sprintf(`{"jobId":%q}`, id)), nil
}
func jobStatus(ctx context.Context, pool *sshpool.Pool, a map[string]any) (*mcp.CallToolResult, error) {
	id, _ := a["jobId"].(string)
	d := jobDir(id)
	if d == "" {
		return nil, fmt.Errorf("invalid jobId")
	}
	tail := int(numberArg(a, "tailLines", 100))
	if tail < 1 {
		tail = 100
	}
	out, e := remoteRun(ctx, pool, a, fmt.Sprintf("d=%q; state=running; [ -f $d/killed ] && state=killed; [ -f $d/exit ] && state=exited; pid=$(cat $d/pid 2>/dev/null); ex=$(cat $d/exit 2>/dev/null || true); printf '%%s\n' \"$state\"; printf '%%s\n' \"$pid\"; printf '%%s\n' \"$ex\"; tail -n %d $d/out 2>/dev/null", d, tail))
	if e != nil {
		return nil, e
	}
	parts := strings.SplitN(out, "\n", 4)
	for len(parts) < 4 {
		parts = append(parts, "")
	}
	return mcp.TextResult(fmt.Sprintf(`{"jobId":%q,"state":%q,"pid":%q,"exitCode":%q,"output":%q}`, id, parts[0], parts[1], parts[2], parts[3])), nil
}
func jobKill(ctx context.Context, pool *sshpool.Pool, a map[string]any) (*mcp.CallToolResult, error) {
	id, _ := a["jobId"].(string)
	d := jobDir(id)
	if d == "" {
		return nil, fmt.Errorf("invalid jobId")
	}
	out, e := remoteRun(ctx, pool, a, fmt.Sprintf("d=%q; touch $d/killed; p=$(cat $d/pid); kill -TERM -- -$p 2>/dev/null || kill -TERM $p 2>/dev/null || true; sleep 1; kill -KILL -- -$p 2>/dev/null || kill -KILL $p 2>/dev/null || true", d))
	_ = out
	if e != nil {
		return nil, e
	}
	return mcp.TextResult(fmt.Sprintf(`{"jobId":%q,"status":"killed"}`, id)), nil
}
func jobList(ctx context.Context, pool *sshpool.Pool, a map[string]any) (*mcp.CallToolResult, error) {
	limit := int(numberArg(a, "limit", 20))
	if limit < 1 {
		limit = 20
	}
	out, e := remoteRun(ctx, pool, a, fmt.Sprintf("for d in $(find %q -mindepth 1 -maxdepth 1 -type d | sort | head -n %d); do id=$(basename $d); state=running; [ -f $d/killed ] && state=killed; [ -f $d/exit ] && state=exited; printf '%%s\t%%s\t%%s\t%%s\n' $id $state \"$(cat $d/pid 2>/dev/null)\" \"$(cat $d/exit 2>/dev/null)\"; done", jobsDir(), limit))
	if e != nil {
		return nil, e
	}
	return mcp.TextResult(out), nil
}

var _ = json.Marshal
var _ = strconv.Itoa
var _ = syscall.SIGTERM
