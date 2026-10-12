# Go ssh-mcp Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a high-performance, single-binary Go rewrite of `ssh-mcp` that drop-in replaces `@fangjunjie/ssh-mcp-server` over stdio with strict single-host precision, robust connection pooling, and dual-track interactive terminal handling.

**Architecture:** A lightweight JSON-RPC 2.0 stdio MCP engine dispatches to modular services (Connection Pool, SSH Executor with expect/idle detection, SFTP File Service, Job Manager, and Persistent PTY Manager). All external commands target an explicit `connectionName` with non-blocking lazy re-connection.

**Tech Stack:** Go 1.22+, `golang.org/x/crypto/ssh`, `github.com/pkg/sftp`, `github.com/kevinburke/ssh_config`, Git.

**Spec:** `docs/superpowers/specs/2026-10-12-ssh-mcp-go-design.md`

## Global Constraints
- Single binary, zero external runtime dependencies, stdio JSON-RPC transport.
- 100% CLI flag compatibility with `@fangjunjie/ssh-mcp-server` (`--host`, `-h`, `--port`, `-p`, `--username`, `-u`, `--password`, `-w`, `--privateKey`, `-k`, `--ssh-config-file`, `--pty`, etc.).
- Drop-in schema and output compatibility for 10 standard tools (`list-servers`, `execute-command`, `read-file`, `write-file`, `upload`, `download`, `job-start`, `job-status`, `job-kill`, `job-list`).
- Strict single-host precision: all execution and file mutations target exactly one host.
- Dual-track interactive session support: expect/idle detection in `execute-command` + persistent PTY tools (`session-open`, `session-send`, `session-read`, `session-close`).

## Review Focus
- Host unreachable or times out during connection: must not hang process or fail other tools.
- Unexpected prompt on `execute-command`: idle-timeout triggers early bail-out before client timeout.
- SFTP large file pagination: byte offset and chunk boundaries respected without NUL byte corruption.
- PTY session cleanup: channel teardown and process exit handled cleanly on close.
- Remote job file layout: matches upstream `$HOME/.ssh-mcp-jobs/<jobId>/` format.

---

### Task 1: Project Scaffolding & CLI Flags Parser
**Files:**
- Create: `go.mod`, `cmd/ssh-mcp/main.go`, `internal/config/config.go`, `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Config`, `config.ParseFlags(args []string) (*Config, error)`

- [ ] **Step 1: Write test for CLI flag parsing**
- [ ] **Step 2: Run test to verify it fails**
- [ ] **Step 3: Implement `config.ParseFlags` supporting upstream long/short flags & `--pty true|false`**
- [ ] **Step 4: Run test to verify it passes**
- [ ] **Step 5: Commit scaffolding**

---

### Task 2: SSH Config Parser & Resilient Connection Pool
**Files:**
- Create: `internal/sshpool/pool.go`, `internal/sshpool/pool_test.go`, `internal/sshpool/client.go`

**Interfaces:**
- Consumes: `config.Config`
- Produces: `sshpool.Pool`, `pool.GetClient(ctx context.Context, host string) (*ssh.Client, error)`, `pool.ListServers() []ServerInfo`

- [ ] **Step 1: Write test for `~/.ssh/config` parsing and client resolution**
- [ ] **Step 2: Run test to verify it fails**
- [ ] **Step 3: Implement lazy connection pool with keepalive and health checks**
- [ ] **Step 4: Run test to verify it passes**
- [ ] **Step 5: Commit connection pool**

---

### Task 3: JSON-RPC 2.0 MCP Protocol Core
**Files:**
- Create: `internal/mcp/protocol.go`, `internal/mcp/server.go`, `internal/mcp/server_test.go`

**Interfaces:**
- Produces: `mcp.Server`, `server.RegisterTool(tool Tool)`, `server.ServeStdio()`

- [ ] **Step 1: Write test for MCP handshake (`initialize`), `tools/list`, and `tools/call`**
- [ ] **Step 2: Run test to verify it fails**
- [ ] **Step 3: Implement stdio JSON-RPC 2.0 MCP server loop**
- [ ] **Step 4: Run test to verify it passes**
- [ ] **Step 5: Commit MCP core**

---

### Task 4: Command Execution with Expect & Idle Detection
**Files:**
- Create: `internal/tools/exec.go`, `internal/tools/exec_test.go`

**Interfaces:**
- Consumes: `sshpool.Pool`, `mcp.Server`
- Produces: `execute-command` tool with single-host precision, `timeout`, `inputs`, `expect`, ANSI stripping, and idle prompt bail-out

- [ ] **Step 1: Write test for command execution, exit code formatting (`\n[exit code] N`), and prompt detection**
- [ ] **Step 2: Run test to verify it fails**
- [ ] **Step 3: Implement `execute-command` with timeout & expect logic**
- [ ] **Step 4: Run test to verify it passes**
- [ ] **Step 5: Commit exec tool**

---

### Task 5: Dual-Track Interactive PTY Sessions
**Files:**
- Create: `internal/tools/pty.go`, `internal/tools/pty_test.go`, `internal/pty/session.go`

**Interfaces:**
- Consumes: `sshpool.Pool`, `mcp.Server`
- Produces: `session-open`, `session-send`, `session-read`, `session-close` tools

- [ ] **Step 1: Write test for PTY session lifecycle (open, send input, read buffer, close)**
- [ ] **Step 2: Run test to verify it fails**
- [ ] **Step 3: Implement persistent PTY manager and MCP handlers**
- [ ] **Step 4: Run test to verify it passes**
- [ ] **Step 5: Commit PTY session tools**

---

### Task 6: SFTP File Operations (`read-file`, `write-file`, `upload`, `download`)
**Files:**
- Create: `internal/tools/sftp.go`, `internal/tools/sftp_test.go`

**Interfaces:**
- Consumes: `sshpool.Pool`, `mcp.Server`
- Produces: `read-file`, `write-file`, `upload`, `download` tools

- [ ] **Step 1: Write test for SFTP pagination, atomic overwrite/append, and directory sync**
- [ ] **Step 2: Run test to verify it fails**
- [ ] **Step 3: Implement SFTP tools using `github.com/pkg/sftp`**
- [ ] **Step 4: Run test to verify it passes**
- [ ] **Step 5: Commit SFTP tools**

---

### Task 7: Background Remote Jobs (`job-*`) & `list-servers`
**Files:**
- Create: `internal/tools/jobs.go`, `internal/tools/servers.go`, `internal/tools/jobs_test.go`

**Interfaces:**
- Consumes: `sshpool.Pool`, `mcp.Server`
- Produces: `job-start`, `job-status`, `job-kill`, `job-list`, `list-servers`

- [ ] **Step 1: Write test for job lifecycle matching upstream `$HOME/.ssh-mcp-jobs/` scripts**
- [ ] **Step 2: Run test to verify it fails**
- [ ] **Step 3: Implement job and server enumeration tools**
- [ ] **Step 4: Run test to verify it passes**
- [ ] **Step 5: Commit job tools**

---

### Task 8: Integration Testing, Binary Build & Beautiful README
**Files:**
- Create: `README.md`, `Makefile`, `scripts/build.sh`

- [ ] **Step 1: Run end-to-end integration tests against real cluster nodes (`bgp01`)**
- [ ] **Step 2: Build static binary `bin/ssh-mcp` and test MCP replacement with Hermes**
- [ ] **Step 3: Craft a professional, beautiful English `README.md` with badges, diagrams, quickstart, and benchmark highlights**
- [ ] **Step 4: Sync all commits and push to remote `inailong/ssh-mcp`**
