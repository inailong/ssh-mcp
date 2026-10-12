package pty

import (
	"context"
	"fmt"
	"github.com/inailong/ssh-mcp/internal/sshpool"
	"golang.org/x/crypto/ssh"
	"io"
	"sync"
	"time"
)

type Session struct {
	ID     string
	ssh    *ssh.Session
	input  io.WriteCloser
	mu     sync.Mutex
	buf    []byte
	notify chan struct{}
	closed bool
	last   time.Time
}

func (s *Session) append(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	const capBytes = 1 << 20
	s.buf = append(s.buf, b...)
	if len(s.buf) > capBytes {
		s.buf = s.buf[len(s.buf)-capBytes:]
	}
	s.last = time.Now()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}
func (s *Session) Read(max int, timeout time.Duration) string {
	if max <= 0 {
		max = 65536
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		s.mu.Lock()
		if len(s.buf) > 0 {
			n := max
			if n > len(s.buf) {
				n = len(s.buf)
			}
			v := string(s.buf[:n])
			s.buf = append([]byte(nil), s.buf[n:]...)
			s.mu.Unlock()
			return v
		}
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return ""
		}
		select {
		case <-s.notify:
		case <-deadline.C:
			return ""
		}
	}
}
func (s *Session) Send(input string, enter bool) (int, error) {
	if enter {
		input += "\n"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, fmt.Errorf("session is closed")
	}
	return s.input.Write([]byte(input))
}
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	return s.ssh.Close()
}

type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	pool     *sshpool.Pool
	stop     chan struct{}
}

func NewManager(pool *sshpool.Pool) *Manager {
	m := &Manager{sessions: map[string]*Session{}, pool: pool, stop: make(chan struct{})}
	go m.reaper()
	return m
}
func (m *Manager) reaper() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			m.mu.Lock()
			for id, s := range m.sessions {
				s.mu.Lock()
				idle := time.Since(s.last) > 30*time.Minute
				s.mu.Unlock()
				if idle {
					delete(m.sessions, id)
					go s.Close()
				}
			}
			m.mu.Unlock()
		case <-m.stop:
			return
		}
	}
}
func (m *Manager) Open(ctx context.Context, name, cmd string, cols, rows int) (string, error) {
	target, e := m.pool.ResolveTarget(name)
	if e != nil {
		return "", e
	}
	client, e := m.pool.GetClient(ctx, target)
	if e != nil {
		return "", e
	}
	ss, e := client.NewSession()
	if e != nil {
		return "", e
	}
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 40
	}
	if e = ss.RequestPty("xterm", rows, cols, ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}); e != nil {
		ss.Close()
		return "", e
	}
	in, e := ss.StdinPipe()
	if e != nil {
		ss.Close()
		return "", e
	}
	out, e := ss.StdoutPipe()
	if e != nil {
		ss.Close()
		return "", e
	}
	errout, e := ss.StderrPipe()
	if e != nil {
		ss.Close()
		return "", e
	}
	if cmd == "" {
		cmd = "/bin/bash"
	}
	if e = ss.Start(cmd); e != nil {
		ss.Close()
		return "", e
	}
	id := fmt.Sprintf("pty-%d", time.Now().UnixNano())
	s := &Session{ID: id, ssh: ss, input: in, notify: make(chan struct{}, 1), last: time.Now()}
	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()
	go func() { io.Copy(sWriter{s}, out) }()
	go func() { io.Copy(sWriter{s}, errout) }()
	return id, nil
}

type sWriter struct{ s *Session }

func (w sWriter) Write(b []byte) (int, error) { w.s.append(b); return len(b), nil }
func (m *Manager) Get(id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return nil, fmt.Errorf("unknown session %q", id)
	}
	return s, nil
}
func (m *Manager) Close(id string) error {
	m.mu.Lock()
	s := m.sessions[id]
	if s != nil {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if s == nil {
		return fmt.Errorf("unknown session %q", id)
	}
	return s.Close()
}
func (m *Manager) Shutdown() {
	close(m.stop)
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.sessions {
		delete(m.sessions, id)
		s.Close()
	}
}
