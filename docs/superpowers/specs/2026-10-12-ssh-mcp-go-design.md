# Design Specification: Go ssh-mcp Server

## 1. Executive Summary & Intent
`ssh-mcp` is a high-performance, lightweight, single-binary Go rewrite and drop-in replacement for the Node.js-based `@fangjunjie/ssh-mcp-server`. It implements the Model Context Protocol (MCP) over stdio to provide LLM agents with robust, concurrent, and resilient remote host execution, SFTP file management, and long-running job tracking across multi-node Linux infrastructure clusters.

## 2. Goals, Non-Goals & Anti-Goals
- **Goals**:
  - 100% CLI parameter compatibility with `@fangjunjie/ssh-mcp-server` (`--host`, `-h`, `--port`, `-p`, `--username`, `-u`, `--password`, `-w`, `--privateKey`, `-k`, `--ssh-config-file`, `--pty`, `--transport-mode`, `--whitelist`, `--blacklist`, and all standard options).
  - Drop-in schema and behavior compatibility for all 10 standard MCP tools.
  - Multi-node concurrent batch execution on `execute-command` (`connectionNames` array or `["*"]` wildcard).
  - Dual-track interactive session support:
    - Track 1: automated expect / idle-timeout bail-out on `execute-command` before client timeouts.
    - Track 2: persistent PTY session tools (`session-open`, `session-send`, `session-read`, `session-close`).
  - High concurrency, low memory footprint (<15MB RAM per daemon), sub-second initialization, zero runtime dependencies.
  - Robust connection pooling with async health checking and non-blocking lazy reconnection.
  - Stdio MCP protocol transport for native agent execution.
- **Non-Goals**:
  - Replacing native terminal for rich desktop GUI terminal emulators.
  - Supporting Windows PowerShell native remoting (SSH on Windows is supported, WinRM is not).
  - Multi-host broadcast on file modification tools (file operations remain single-host directed for safety and idempotency).
- **Anti-Goals**:
  - Never allow a hung/unresponsive remote host to freeze the MCP stdio loop or block commands directed to other healthy hosts.
  - Never lose file permissions or corrupt file transfers due to shell escaping hazards.

## 3. Architecture & Core Components
```
                    ┌────────────────────────┐
                    │    LLM Agent / Client  │
                    └───────────┬────────────┘
                                │ JSON-RPC 2.0 (stdio)
                    ┌───────────▼────────────┐
                    │       MCP Engine       │
                    │ (Go MCP Protocol Core) │
                    └───────────┬────────────┘
                                │ Dispatcher
       ┌────────────────────────┼────────────────────────┐
       ▼                        ▼                        ▼
┌──────────────┐         ┌──────────────┐         ┌──────────────┐
│  Host Pool   │         │ PTY Manager  │         │ File/Job Svc │
│ (SSH Conns)  │         │(Interactive) │         │(SFTP/Scripts)│
└──────┬───────┘         └──────┬───────┘         └──────┬───────┘
       │                        │                        │
       └────────────────────────┼────────────────────────┘
                                │
                  ┌─────────────▼─────────────┐
                  │ Remote Cluster (bgp01...) │
                  └───────────────────────────┘
```

### 3.1 CLI Compatibility Layer
Parses flag options matching upstream:
- `--host, -h <hosts>`: Comma-separated list of Host aliases or IPs.
- `--port, -p`: SSH Port (default 22).
- `--username, -u`: SSH username.
- `--password, -w`: Password.
- `--privateKey, -k`: Private key path.
- `--ssh-config-file`: Path to OpenSSH config (defaults to `~/.ssh/config`).
- `--pty`: Boolean flag (supports space-separated `--pty false`).
- `--transport-mode`: SSH exec vs shell mode.
- `--whitelist`, `--blacklist`: Regex filters.
- Supports all legacy flags, config-file flags, and positional args.

### 3.2 OpenSSH Config Parser & Connection Pool
- Implements standard OpenSSH parser supporting `Host`, `HostName`, `User`, `Port`, `IdentityFile`, `ProxyJump`, and wildcards.
- Each connection is lazily dialed on first request with KeepAlive ping goroutine.
- Dead connections are automatically evicted and re-dialed without client intervention.
- One unhealthy node never delays startup or blocks queries to healthy nodes.

### 3.3 Tool Registry & Interfaces
1. **Host & Discovery**:
   - `list-servers`: Enumerate configured hosts, status (connected/idle/disconnected), IP/ports.
2. **Command Execution**:
   - `execute-command`:
     - Params:
       - `cmdString` (string, required)
       - `connectionName` (string, optional)
       - `connectionNames` (array of string, optional, e.g. `["sh01", "sz01"]` or `["*"]`)
       - `directory` (string, optional)
       - `timeout` (number in milliseconds, default 30000)
       - `inputs` (array of string, optional auto-feed inputs)
       - `expect` (map of prompt regex -> answer string, optional)
     - Behavior:
       - Returns command stdout/stderr with trailing `\n[exit code] N` on non-zero exit.
       - Batched execution runs concurrently via goroutines with per-host timeout, aggregated deterministically.
       - Idle-timeout prompt detection: strips ANSI codes, checks for stalled prompts (`[Y/n]`, `password:`), feeds matching expect response or bails out with current buffer before client timeout.
3. **Interactive PTY Session Tools (Dual-Track Interactive)**:
   - `session-open`: Creates a persistent pseudo-terminal session on remote host. Returns `sessionId`.
   - `session-send`: Sends keystrokes/data (`sessionId`, `input`, `enter: bool`).
   - `session-read`: Reads current terminal buffer (`sessionId`, `maxBytes`, `timeoutMs`).
   - `session-close`: Closes `sessionId` and cleans up pty channel.
4. **SFTP File Management**:
   - `read-file`: Byte-offset pagination with chunk size control (default 64KB), NUL-byte binary check.
   - `write-file`: Atomic overwrite or append over SFTP, permissions preserving.
   - `upload`: Recursive local-to-remote directory/file transfer with checksum verification (skips symlinks, 2k file limit).
   - `download`: Recursive remote-to-local directory/file transfer.
5. **Background Job Runner**:
   - `$HOME/.ssh-mcp-jobs/<jobId>/` compatible layout with `{cmd, dir, out, pid, started, exit, killed}`.
   - `job-start`: Detached process execution.
   - `job-status`: Reads exit status and recent output lines.
   - `job-kill`: Sends SIGTERM/SIGKILL to remote process.
   - `job-list`: Enumerate remote active/finished jobs.

## 4. Dependencies & Implementation Details
- **Go Version**: 1.22+
- **SSH**: `golang.org/x/crypto/ssh` + `golang.org/x/net/proxy`
- **SFTP**: `github.com/pkg/sftp`
- **Config**: `github.com/kevinburke/ssh_config`
- **MCP Protocol**: High-performance, zero-overhead stdio JSON-RPC 2.0 implementation (<500 LOC).
