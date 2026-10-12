package main

import (
	"fmt"
	"github.com/inailong/ssh-mcp/internal/config"
	"github.com/inailong/ssh-mcp/internal/mcp"
	"github.com/inailong/ssh-mcp/internal/pty"
	"github.com/inailong/ssh-mcp/internal/sshpool"
	"github.com/inailong/ssh-mcp/internal/tools"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const version = "2.0.0"

func main() {
	for _, a := range os.Args[1:] {
		if a == "--version" || a == "-v" {
			fmt.Println(version)
			return
		}
		if a == "--help" || a == "-h" {
			fmt.Println("ssh-mcp [flags]")
			return
		}
	}
	cfg, e := config.ParseFlags(os.Args[1:])
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
	pool, e := sshpool.NewPool(cfg)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	manager := pty.NewManager(pool)
	defer manager.Shutdown()
	defer pool.Close()
	server := mcp.NewServer("ssh-mcp-server", version)
	tools.RegisterServers(server, pool)
	tools.RegisterExec(server, pool, cfg)
	tools.RegisterSFTP(server, pool, cfg)
	tools.RegisterJobs(server, pool)
	tools.RegisterPTY(server, manager)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; manager.Shutdown(); pool.Close(); os.Exit(0) }()
	_ = time.Second
	if e = server.ServeStdio(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
