// Package sshpool resolves OpenSSH aliases and manages lazy SSH connections.
package sshpool

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/inailong/ssh-mcp/internal/config"
	sshconfig "github.com/kevinburke/ssh_config"
	"golang.org/x/crypto/ssh"
)

type ServerInfo struct {
	Name      string `json:"name"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Username  string `json:"username"`
	Connected bool   `json:"connected"`
}
type target struct {
	mu                 sync.RWMutex
	gate               chan struct{}
	info               ServerInfo
	identity, password string
	client             *ssh.Client
}
type Pool struct {
	mu      sync.RWMutex
	targets []*target
	byName  map[string]*target
}

func NewPool(cfg *config.Config) (*Pool, error) {
	if cfg == nil || len(cfg.Hosts) == 0 {
		return nil, fmt.Errorf("no hosts configured")
	}
	path := cfg.SSHConfigFile
	if path == "" {
		path = "~/.ssh/config"
	}
	var parsed *sshconfig.Config
	f, e := os.Open(expandPath(path))
	if e == nil {
		parsed, e = sshconfig.Decode(f)
		_ = f.Close()
	}
	if e != nil && !os.IsNotExist(e) {
		return nil, fmt.Errorf("read SSH config: %w", e)
	}
	port := cfg.Port
	if port == 0 {
		port = 22
	}
	user := cfg.Username
	if user == "" {
		user = "root"
	}
	identity := expandPath(cfg.PrivateKey)
	if identity == "" {
		for _, k := range []string{"~/.ssh/id_ed25519", "~/.ssh/id_rsa"} {
			if _, e = os.Stat(expandPath(k)); e == nil {
				identity = expandPath(k)
				break
			}
		}
	}
	p := &Pool{byName: map[string]*target{}}
	for _, name := range cfg.Hosts {
		if name == "" {
			return nil, fmt.Errorf("empty host name")
		}
		if _, ok := p.byName[name]; ok {
			return nil, fmt.Errorf("duplicate host %q", name)
		}
		t := &target{gate: make(chan struct{}, 1), info: ServerInfo{Name: name, Host: name, Port: port, Username: user}, identity: identity, password: cfg.Password}
		if parsed != nil {
			for _, k := range []string{"HostName", "User", "Port", "IdentityFile"} {
				v, e := parsed.Get(name, k)
				if e != nil {
					return nil, e
				}
				if v == "" {
					continue
				}
				switch k {
				case "HostName":
					t.info.Host = strings.ReplaceAll(v, "%h", name)
				case "User":
					t.info.Username = v
				case "Port":
					n, e := strconv.Atoi(v)
					if e != nil || n < 1 || n > 65535 {
						return nil, fmt.Errorf("invalid port %q", v)
					}
					t.info.Port = n
				case "IdentityFile":
					t.identity = expandPath(strings.Fields(v)[0])
				}
			}
		}
		if t.info.Port < 1 || t.info.Port > 65535 {
			return nil, fmt.Errorf("invalid port")
		}
		p.targets = append(p.targets, t)
		p.byName[name] = t
	}
	return p, nil
}
func expandPath(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, e := os.UserHomeDir(); e == nil {
			if p == "~" {
				return h
			}
			return filepath.Join(h, p[2:])
		}
	}
	return p
}
func (p *Pool) ResolveTarget(n string) (string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if n == "" || n == "default" {
		return p.targets[0].info.Name, nil
	}
	if _, ok := p.byName[n]; !ok {
		return "", fmt.Errorf("unknown connection %q", n)
	}
	return n, nil
}
func (p *Pool) GetClient(ctx context.Context, n string) (*ssh.Client, error) {
	name, e := p.ResolveTarget(n)
	if e != nil {
		return nil, e
	}
	t := p.byName[name]
	select {
	case t.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-t.gate }()
	t.mu.Lock()
	defer t.mu.Unlock()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if t.client != nil && alive(ctx, t.client) {
		return t.client, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if t.client != nil {
		_ = t.client.Close()
		t.client = nil
	}
	auth := []ssh.AuthMethod{}
	if t.password != "" {
		auth = append(auth, ssh.Password(t.password))
	}
	if t.identity != "" {
		if b, e := os.ReadFile(t.identity); e == nil {
			if s, e := ssh.ParsePrivateKey(b); e == nil {
				auth = append(auth, ssh.PublicKeys(s))
			}
		}
	}
	if len(auth) == 0 {
		return nil, fmt.Errorf("no usable SSH authentication for %s", name)
	}
	cc := &ssh.ClientConfig{User: t.info.Username, Auth: auth, HostKeyCallback: ssh.InsecureIgnoreHostKey()}
	addr := net.JoinHostPort(t.info.Host, strconv.Itoa(t.info.Port))
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	deadline, _ := dialCtx.Deadline()
	_ = raw.SetDeadline(deadline)
	stop := context.AfterFunc(dialCtx, func() { raw.Close() })
	defer stop()
	conn, ch, reqs, err := ssh.NewClientConn(raw, addr, cc)
	if err != nil {
		raw.Close()
		if dialCtx.Err() != nil {
			return nil, dialCtx.Err()
		}
		return nil, err
	}
	if !stop() || dialCtx.Err() != nil {
		conn.Close()
		return nil, dialCtx.Err()
	}
	_ = raw.SetDeadline(time.Time{})
	t.client = ssh.NewClient(conn, ch, reqs)
	return t.client, nil
}
func alive(ctx context.Context, c *ssh.Client) bool {
	x, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ch := make(chan error, 1)
	go func() { _, _, e := c.SendRequest("keepalive@openssh.com", true, nil); ch <- e }()
	select {
	case <-x.Done():
		return false
	case e := <-ch:
		return e == nil
	}
}
func (p *Pool) ListServers() []ServerInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	r := make([]ServerInfo, len(p.targets))
	for i, t := range p.targets {
		t.mu.RLock()
		r[i] = t.info
		r[i].Connected = t.client != nil
		t.mu.RUnlock()
	}
	return r
}
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var first error
	for _, t := range p.targets {
		t.mu.Lock()
		if t.client != nil {
			if e := t.client.Close(); e != nil && first == nil {
				first = e
			}
			t.client = nil
		}
		t.mu.Unlock()
	}
	return first
}
