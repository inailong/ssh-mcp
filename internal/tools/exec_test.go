package tools

import (
	"context"
	"github.com/inailong/ssh-mcp/internal/config"
	"github.com/inailong/ssh-mcp/internal/testssh"
	"golang.org/x/crypto/ssh"
	"strings"
	"testing"
)

func TestExecuteOutputExitAndDirectory(t *testing.T) {
	p := testssh.Pool(t, func(c ssh.Channel, requests <-chan *ssh.Request) {
		for r := range requests {
			if r.Type == "exec" {
				var cmd struct{ Command string }
				ssh.Unmarshal(r.Payload, &cmd)
				r.Reply(true, nil)
				c.Write([]byte(cmd.Command))
				c.Stderr().Write([]byte("failure"))
				testssh.Exit(c, 7)
				return
			}
		}
	})
	result, err := Execute(context.Background(), p, &config.Config{}, map[string]any{"cmdString": "echo hi", "directory": "a'$(oops)"})
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].Text
	if result.IsError || !strings.Contains(text, "cd 'a'\"'\"'$(oops)' && echo hi") || !strings.HasSuffix(text, "\n[exit code] 7") || !strings.Contains(text, "failure") {
		t.Fatalf("unexpected result: %+v", result)
	}
}
