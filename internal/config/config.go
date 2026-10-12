// Package config parses CLI options without opening SSH connections or files.
package config

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Config contains shared connection options and command/file policies.
type Config struct {
	Hosts              []string
	Port               int
	Username           string
	Password           string
	PrivateKey         string
	SSHConfigFile      string
	PTY                bool
	TransportMode      string
	Whitelist          string
	Blacklist          string
	AllowedLocalPaths  []string
	AllowedRemotePaths []string
	TimeoutMs          int
}

// ParseFlags parses CLI arguments, excluding the executable name. It accepts
// both the upstream long/short spellings and the legacy positional form.
func ParseFlags(args []string) (*Config, error) {
	fs := flag.NewFlagSet("ssh-mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	host := fs.String("host", "", "SSH host(s)")
	fs.StringVar(host, "h", "", "SSH host(s)")
	port := fs.Int("port", 22, "SSH port")
	fs.IntVar(port, "p", 22, "SSH port")
	username := fs.String("username", "", "SSH username")
	fs.StringVar(username, "u", "", "SSH username")
	password := fs.String("password", "", "SSH password")
	fs.StringVar(password, "w", "", "SSH password")
	privateKey := fs.String("privateKey", "", "SSH private key")
	fs.StringVar(privateKey, "k", "", "SSH private key")
	sshConfigFile := fs.String("ssh-config-file", "~/.ssh/config", "OpenSSH config file")
	ptyValue := fs.String("pty", "false", "enable PTY")
	transportMode := fs.String("transport-mode", "exec", "exec or shell")
	whitelist := fs.String("whitelist", "", "command whitelist")
	fs.StringVar(whitelist, "W", "", "command whitelist")
	blacklist := fs.String("blacklist", "", "command blacklist")
	fs.StringVar(blacklist, "B", "", "command blacklist")
	allowedLocal := fs.String("allowed-local-paths", "", "allowed local paths")
	allowedRemote := fs.String("allowed-remote-paths", "", "allowed remote paths")
	timeoutMs := fs.Int("timeout-ms", 30000, "operation timeout in milliseconds")

	// flag.Value for strings requires a following token. Upstream also treats
	// bare --pty as true, so normalize that spelling before flag parsing.
	normalized := make([]string, 0, len(args)+1)
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			normalized = append(normalized, args[i:]...)
			break
		}
		if args[i] == "--pty" {
			if i+1 < len(args) && (args[i+1] == "true" || args[i+1] == "false") {
				i++
				normalized = append(normalized, "--pty="+args[i])
			} else {
				normalized = append(normalized, "--pty=true")
			}
		} else {
			normalized = append(normalized, args[i])
		}
	}
	if err := fs.Parse(normalized); err != nil {
		return nil, err
	}

	positionals := fs.Args()
	if *host == "" && len(positionals) > 0 {
		*host = positionals[0]
	}
	if *port == 22 && len(positionals) > 1 {
		var err error
		*port, err = strconv.Atoi(positionals[1])
		if err != nil {
			return nil, fmt.Errorf("invalid positional port %q: %w", positionals[1], err)
		}
	}
	if *username == "" && len(positionals) > 2 {
		*username = positionals[2]
	}
	if *password == "" && len(positionals) > 3 {
		*password = positionals[3]
	}

	pty, err := parseBool(*ptyValue)
	if err != nil {
		return nil, fmt.Errorf("invalid --pty value: %w", err)
	}
	if *port <= 0 || *port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	if *timeoutMs <= 0 {
		return nil, fmt.Errorf("timeout-ms must be positive")
	}
	if *transportMode != "exec" && *transportMode != "shell" {
		return nil, fmt.Errorf("transport-mode must be either %q or %q", "exec", "shell")
	}

	return &Config{
		Hosts: splitCSV(*host), Port: *port, Username: *username, Password: *password,
		PrivateKey: *privateKey, SSHConfigFile: *sshConfigFile, PTY: pty,
		TransportMode: *transportMode, Whitelist: *whitelist, Blacklist: *blacklist,
		AllowedLocalPaths: splitCSV(*allowedLocal), AllowedRemotePaths: splitCSV(*allowedRemote),
		TimeoutMs: *timeoutMs,
	}, nil
}

func splitCSV(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func parseBool(value string) (bool, error) {
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return false, fmt.Errorf("%q (expected true or false)", value)
	}
	return parsed, nil
}
