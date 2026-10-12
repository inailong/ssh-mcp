// Package testssh provides a local SSH peer for integration tests.
package testssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"sync"
	"testing"

	"github.com/inailong/ssh-mcp/internal/config"
	"github.com/inailong/ssh-mcp/internal/sshpool"
	"golang.org/x/crypto/ssh"
)

func Pool(t *testing.T, handler func(ssh.Channel, <-chan *ssh.Request)) *sshpool.Pool {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { return nil, nil }}
	cfg.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var connections []net.Conn
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			raw, e := listener.Accept()
			if e != nil {
				return
			}
			mu.Lock()
			connections = append(connections, raw)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				conn, channels, requests, e := ssh.NewServerConn(raw, cfg)
				if e != nil {
					return
				}
				defer conn.Close()
				go func() {
					for req := range requests {
						req.Reply(true, nil)
					}
				}()
				for ch := range channels {
					if ch.ChannelType() != "session" {
						ch.Reject(ssh.UnknownChannelType, "unsupported")
						continue
					}
					c, r, e := ch.Accept()
					if e != nil {
						continue
					}
					wg.Add(1)
					go func() { defer wg.Done(); defer c.Close(); handler(c, r) }()
				}
			}()
		}
	}()
	p, err := sshpool.NewPool(&config.Config{Hosts: []string{"127.0.0.1"}, Port: listener.Addr().(*net.TCPAddr).Port, Username: "test", Password: "test", SSHConfigFile: t.TempDir() + "/missing"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.Close()
		listener.Close()
		mu.Lock()
		for _, c := range connections {
			c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return p
}

func Exit(c ssh.Channel, code uint32) {
	c.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{code}))
}
