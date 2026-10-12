package tools

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/inailong/ssh-mcp/internal/config"
	"github.com/inailong/ssh-mcp/internal/mcp"
	"github.com/inailong/ssh-mcp/internal/sshpool"
	"golang.org/x/crypto/ssh"
)

func RegisterExec(server *mcp.Server, pool *sshpool.Pool, cfg *config.Config) {
	server.RegisterTool(mcp.Tool{Name: "execute-command", Description: "Execute a command on one SSH host", InputSchema: map[string]any{"type": "object", "required": []string{"cmdString"}, "properties": map[string]any{"cmdString": map[string]any{"type": "string"}, "connectionName": map[string]any{"type": "string"}, "directory": map[string]any{"type": "string"}, "timeout": map[string]any{"type": "number", "default": 30000}, "inputs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "expect": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}}}, Handler: func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return Execute(ctx, pool, cfg, args)
	}})
}

func stringArg(args map[string]any, key string, required bool) (string, error) {
	value, ok := args[key]
	if !ok {
		if required {
			return "", fmt.Errorf("%s is required", key)
		}
		return "", nil
	}
	s, ok := value.(string)
	if !ok || required && strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("%s must be a nonempty string", key)
	}
	return s, nil
}
func commandAllowed(cfg *config.Config, cmd string) error {
	if cfg == nil {
		return nil
	}
	for _, rule := range []struct {
		pattern string
		allow   bool
	}{{cfg.Whitelist, true}, {cfg.Blacklist, false}} {
		if rule.pattern == "" {
			continue
		}
		re, e := regexp.Compile(rule.pattern)
		if e != nil {
			return fmt.Errorf("invalid command policy: %w", e)
		}
		if re.MatchString(cmd) != rule.allow {
			return fmt.Errorf("command denied by policy")
		}
	}
	return nil
}
func quoteShell(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func milliseconds(args map[string]any, key string, def int) (time.Duration, error) {
	v, ok := args[key]
	if !ok {
		return time.Duration(def) * time.Millisecond, nil
	}
	var n float64
	switch x := v.(type) {
	case float64:
		n = x
	case int:
		n = float64(x)
	default:
		return 0, fmt.Errorf("%s must be a number", key)
	}
	if n <= 0 || n > float64(1<<53)/float64(time.Millisecond) {
		return 0, fmt.Errorf("%s must be positive and finite", key)
	}
	return time.Duration(n * float64(time.Millisecond)), nil
}

func stripANSI(s string) string {
	return regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`).ReplaceAllString(s, "")
}

func Execute(ctx context.Context, pool *sshpool.Pool, cfg *config.Config, args map[string]any) (*mcp.CallToolResult, error) {
	cmd, err := stringArg(args, "cmdString", true)
	if err != nil {
		return nil, err
	}
	if err = commandAllowed(cfg, cmd); err != nil {
		return nil, err
	}
	name, err := stringArg(args, "connectionName", false)
	if err != nil {
		return nil, err
	}
	dir, err := stringArg(args, "directory", false)
	if err != nil {
		return nil, err
	}
	duration, err := milliseconds(args, "timeout", 30000)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	target, err := pool.ResolveTarget(name)
	if err != nil {
		return nil, err
	}
	client, err := pool.GetClient(ctx, target)
	if err != nil {
		return nil, err
	}
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	stdout, err := session.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return nil, err
	}
	if dir != "" {
		cmd = "cd " + quoteShell(dir) + " && " + cmd
	}
	var stdin io.WriteCloser
	interactive := args["inputs"] != nil || args["expect"] != nil
	if interactive {
		stdin, err = session.StdinPipe()
		if err != nil {
			return nil, err
		}
	}
	if err = session.Start(cmd); err != nil {
		return nil, err
	}
	var expect map[string]string
	if raw, ok := args["expect"].(map[string]any); ok {
		expect = make(map[string]string, len(raw))
		for pattern, value := range raw {
			if text, ok := value.(string); ok {
				expect[pattern] = text
			}
		}
	}
	if values, ok := args["inputs"].([]any); ok && stdin != nil {
		go func() {
			for _, value := range values {
				if text, ok := value.(string); ok {
					_, _ = io.WriteString(stdin, text)
				}
			}
		}()
	}
	type chunk struct {
		data   string
		stderr bool
	}
	events := make(chan chunk, 32)
	doneReading := make(chan struct{})
	go func() {
		defer close(doneReading)
		finished := make(chan struct{}, 2)
		for _, stream := range []struct {
			r   io.Reader
			err bool
		}{{stdout, false}, {stderr, true}} {
			go func(r io.Reader, isErr bool) {
				defer func() { finished <- struct{}{} }()
				b := make([]byte, 4096)
				for {
					n, e := r.Read(b)
					if n > 0 {
						select {
						case events <- chunk{string(b[:n]), isErr}:
						case <-ctx.Done():
							return
						}
					}
					if e != nil {
						return
					}
				}
			}(stream.r, stream.err)
		}
		<-finished
		<-finished
		close(events)
	}()
	exited := make(chan error, 1)
	go func() { e := session.Wait(); <-doneReading; exited <- e }()
	var out, errOut strings.Builder
	var matched string
	lastOutput := time.Now()
	idle := time.NewTicker(100 * time.Millisecond)
	defer idle.Stop()
	drain := func() {
		for ev := range events {
			if ev.stderr {
				errOut.WriteString(ev.data)
			} else {
				out.WriteString(ev.data)
			}
		}
	}
	result := func(suffix string) *mcp.CallToolResult {
		return mcp.TextResult(out.String() + errOut.String() + suffix)
	}
	for {
		select {
		case ev, ok := <-events:
			if ok {
				lastOutput = time.Now()
				if ev.stderr {
					errOut.WriteString(ev.data)
				} else {
					out.WriteString(ev.data)
				}
				clean := stripANSI(out.String() + errOut.String())
				for pattern, answer := range expect {
					if re, e := regexp.Compile(pattern); e == nil && re.MatchString(clean) {
						matched = answer
						break
					}
				}
				if matched != "" && stdin != nil {
					_, _ = io.WriteString(stdin, matched)
					matched = ""
				}
			} else {
				events = nil
			}
		case <-idle.C:
			if time.Since(lastOutput) >= 2*time.Second {
				clean := stripANSI(out.String() + errOut.String())
				standard := regexp.MustCompile(`(?i)(\[y/n\]|password:)`).FindString(clean)
				if standard != "" {
					session.Close()
					return mcp.TextResult(out.String() + errOut.String() + "\n[execution paused: interactive prompt detected: " + standard + "]"), nil
				}
			}
		case e := <-exited:
			if events != nil {
				drain()
			}
			if e != nil {
				if exit, ok := e.(*ssh.ExitError); ok {
					return result(fmt.Sprintf("\n[exit code] %d", exit.ExitStatus())), nil
				}
				return result("\n[execution failed: " + e.Error() + "]"), nil
			}
			return result(""), nil
		case <-ctx.Done():
			session.Close()
			return result("\n[execution stopped: " + ctx.Err().Error() + "]"), nil
		}
	}
}
