package sshpool

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"golang.org/x/crypto/ssh"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inailong/ssh-mcp/internal/config"
)

func TestConfigResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0700); err != nil {
		t.Fatal(err)
	}
	text := "Host node alias\n HostName 127.0.0.1\n User alice\n Port 2202\n IdentityFile ~/.ssh/custom\nHost *.example\n User bob\nHost *\n Port 2222\n"
	if err := os.WriteFile(filepath.Join(home, ".ssh/config"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := NewPool(&config.Config{Hosts: []string{"node", "host.example", "raw"}, Username: "fallback", Port: 22})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	want := []ServerInfo{{Name: "node", Host: "127.0.0.1", Port: 2202, Username: "alice"}, {Name: "host.example", Host: "host.example", Port: 2222, Username: "bob"}, {Name: "raw", Host: "raw", Port: 2222, Username: "fallback"}}
	if got := p.ListServers(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	for _, name := range []string{"", "default", "node"} {
		got, err := p.ResolveTarget(name)
		if err != nil || got != "node" {
			t.Fatalf("resolve %q: %q %v", name, got, err)
		}
	}
	for _, name := range []string{"missing", "*", "127.0.0.1"} {
		if _, err := p.ResolveTarget(name); err == nil {
			t.Fatalf("accepted unconfigured target %q", name)
		}
	}
}

func TestRealConnectionReuseReconnect(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	sc := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		if c.User() != "tester" || string(p) != "secret" {
			return nil, fmt.Errorf("bad auth")
		}
		return nil, nil
	}}
	sc.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []*ssh.ServerConn
	var pings atomic.Int32
	defer func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	}()
	go func() {
		for {
			raw, e := ln.Accept()
			if e != nil {
				return
			}
			go func() {
				c, ch, requests, e := ssh.NewServerConn(raw, sc)
				if e != nil {
					raw.Close()
					return
				}
				mu.Lock()
				conns = append(conns, c)
				mu.Unlock()
				go func() {
					for x := range ch {
						x.Reject(ssh.UnknownChannelType, "no sessions")
					}
				}()
				for r := range requests {
					if r.Type == "keepalive@openssh.com" {
						pings.Add(1)
					}
					r.Reply(false, nil)
				}
			}()
		}
	}()
	host, portText, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portText)
	pool, e := NewPool(&config.Config{Hosts: []string{host}, Port: port, Username: "tester", Password: "secret", SSHConfigFile: filepath.Join(t.TempDir(), "missing")})
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, e := pool.GetClient(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	same, e := pool.GetClient(ctx, "default")
	if e != nil || same != c {
		t.Fatalf("reuse %v", e)
	}
	if pings.Load() == 0 {
		t.Fatal("no keepalive")
	}
	if !pool.ListServers()[0].Connected {
		t.Fatal("not connected")
	}
	c.Close()
	next, e := pool.GetClient(ctx, host)
	if e != nil || next == c {
		t.Fatalf("reconnect %v", e)
	}
	pool.Close()
	if pool.ListServers()[0].Connected {
		t.Fatal("still connected")
	}
}

func TestHandshakeContextTimeout(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, e := ln.Accept()
		if e == nil {
			accepted <- c
		}
	}()
	host, ps, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(ps)
	p, e := NewPool(&config.Config{Hosts: []string{host}, Port: port, Password: "secret", SSHConfigFile: filepath.Join(t.TempDir(), "missing")})
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, e = p.GetClient(ctx, "")
	if e == nil {
		t.Fatal("wanted deadline")
	}
	if time.Since(start) > time.Second {
		t.Fatal("handshake hung")
	}
	select {
	case c := <-accepted:
		c.Close()
	case <-time.After(time.Second):
		t.Fatal("not accepted")
	}
}

func TestProxyJumpConfigResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0700); err != nil {
		t.Fatal(err)
	}
	text := "Host bastion\n HostName 127.0.0.1\n Port 2221\n User jump\n\nHost target\n HostName 10.0.0.2\n Port 22\n User app\n ProxyJump bastion\n"
	if err := os.WriteFile(filepath.Join(home, ".ssh/config"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := NewPool(&config.Config{Hosts: []string{"target"}, Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	target := p.byName["target"]
	if target == nil {
		t.Fatal("target not found")
	}
	if target.proxyJump != "bastion" {
		t.Fatalf("target proxyJump = %q, want bastion", target.proxyJump)
	}
	// Bastion should be auto-discovered from SSH config
	bastion := p.byName["bastion"]
	if bastion == nil {
		t.Fatal("bastion not auto-registered in pool")
	}
	if bastion.info.Port != 2221 || bastion.info.Username != "jump" {
		t.Fatalf("bastion info = %+v, want port 2221, user jump", bastion.info)
	}
}
