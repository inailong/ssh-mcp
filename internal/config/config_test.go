package config

import (
	"reflect"
	"testing"
)

func TestParseFlagsLongAndShortOptions(t *testing.T) {
	for _, args := range [][]string{
		{"--host", " alpha, beta ,, ", "--port", "2222", "--username", "alice", "--password", "secret=token", "--privateKey", "~/key", "--whitelist", "^ls, ^cat", "--blacklist", "rm|shutdown"},
		{"-h", " alpha, beta ,, ", "-p", "2222", "-u", "alice", "-w", "secret=token", "-k", "~/key", "-W", "^ls, ^cat", "-B", "rm|shutdown"},
	} {
		cfg, err := ParseFlags(args)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cfg.Hosts, []string{"alpha", "beta"}) || cfg.Port != 2222 || cfg.Username != "alice" || cfg.Password != "secret=token" || cfg.PrivateKey != "~/key" || cfg.Whitelist != "^ls, ^cat" || cfg.Blacklist != "rm|shutdown" {
			t.Fatalf("incorrect flag values: %+v", cfg)
		}
	}
}

func TestParseFlagsPTYTransportAndLegacyPositionals(t *testing.T) {
	cfg, err := ParseFlags([]string{"--pty", "false", "--transport-mode", "shell", "host.example", "2200", "bob", "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PTY || cfg.TransportMode != "shell" || !reflect.DeepEqual(cfg.Hosts, []string{"host.example"}) || cfg.Port != 2200 || cfg.Username != "bob" || cfg.Password != "pw" {
		t.Fatalf("incorrect positional/boolean values: %+v", cfg)
	}
	cfg, err = ParseFlags([]string{"--pty"})
	if err != nil || !cfg.PTY {
		t.Fatalf("bare --pty: %+v, %v", cfg, err)
	}
	if _, err = ParseFlags([]string{"--transport-mode", "invalid"}); err == nil {
		t.Fatal("expected invalid transport mode error")
	}
}

func TestParseFlagsSSHConfigAndPolicies(t *testing.T) {
	cfg, err := ParseFlags([]string{"--host=alpha", "--ssh-config-file=/custom/config", "--transport-mode=shell", "--allowed-local-paths", " /srv, /opt,, ", "--allowed-remote-paths=/var/log,/home"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Hosts, []string{"alpha"}) || cfg.SSHConfigFile != "/custom/config" || cfg.TransportMode != "shell" || !reflect.DeepEqual(cfg.AllowedLocalPaths, []string{"/srv", "/opt"}) || !reflect.DeepEqual(cfg.AllowedRemotePaths, []string{"/var/log", "/home"}) {
		t.Fatalf("incorrect policies: %+v", cfg)
	}
	cfg, err = ParseFlags([]string{"--ssh-config-file", ""})
	if err != nil || cfg.SSHConfigFile != "" {
		t.Fatalf("explicit empty SSH config: %+v, %v", cfg, err)
	}
}

func TestParseFlagsDefaults(t *testing.T) {
	cfg, err := ParseFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 22 || cfg.TimeoutMs != 30000 || cfg.TransportMode != "exec" || cfg.PTY {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.SSHConfigFile != "~/.ssh/config" && cfg.SSHConfigFile != "" {
		t.Fatalf("unexpected SSH config default: %q", cfg.SSHConfigFile)
	}
	if len(cfg.Hosts) != 0 || cfg.Username != "" || cfg.Password != "" || cfg.PrivateKey != "" || cfg.Whitelist != "" || cfg.Blacklist != "" || len(cfg.AllowedLocalPaths) != 0 || len(cfg.AllowedRemotePaths) != 0 {
		t.Fatalf("unexpected optional defaults: %+v", cfg)
	}
}
