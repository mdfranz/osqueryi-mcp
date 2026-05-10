# Go MCP Server Guide for Local Command Execution

## 1) SDK and Core Choice

Use the official Go SDK:

```text
github.com/modelcontextprotocol/go-sdk v1.4.1
```

For tool registration, prefer the raw API:
- `server.AddTool` with `*mcp.Tool` and `json.RawMessage` schemas.
- Use `mcp.AddTool` only when simple typed schemas are enough.

Why: external APIs usually need flexible schemas and nuanced argument parsing.

## 2) Recommended Project Layout

```text
cmd/<app-name>/
  main.go      # config, logging, lock, server creation, run loop
  executor.go  # command execution and subprocess management
  tools.go     # tool schemas and handlers
  errors.go    # shared error formatting helpers (optional)
  schemas.go   # reusable JSON schema blocks (optional)
tools/
  test_mcp.py  # end-to-end MCP test client
go.mod
go.sum           # Go dependency lockfile (always commit)
pyproject.toml   # Python deps for test client
uv.lock          # Python lockfile for test client (always commit)
.gitignore
Makefile
```

Keep server code in one package under `cmd/<app-name>/` until growth justifies splitting.

## 3) `main.go`: Server Bootstrap Pattern

Key rules:
- Use `mcp.StdioTransport`.
- Treat `stdout` as protocol-only (never debug-print to stdout).
- Fail fast on missing required environment variables.

```go
package main

import (
    "context"
    "fmt"
    "io"
    "log/slog"
    "os"
    "strings"
    "syscall"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

type Config struct {
    WorkDir  string
    LockFile string
}

func loadConfig() Config {
    workDir := os.Getenv("APP_WORKDIR")
    if workDir == "" {
        workDir = "."
    }

    lockFile := os.Getenv("APP_LOCKFILE")
    if lockFile == "" {
        lockFile = "my-mcp-server.lock"
    }

    return Config{WorkDir: workDir, LockFile: lockFile}
}

func main() {
    level := slog.LevelInfo
    if os.Getenv("APP_DEBUG") != "" {
        level = slog.LevelDebug
    }

    var logWriter io.Writer = os.Stderr
    if logFile := os.Getenv("APP_LOGFILE"); logFile != "" {
        f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
        if err == nil {
            defer f.Close()
            logWriter = f
        }
    }
    slog.SetDefault(slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{Level: level})))

    cfg := loadConfig()

    cleanupLock, err := acquireLock(cfg.LockFile)
    if err != nil {
        fmt.Fprintf(os.Stderr, "lock error: %v\n", err)
        os.Exit(1)
    }
    defer cleanupLock()

    executor := NewExecutor(cfg.WorkDir)

    s := mcp.NewServer(&mcp.Implementation{Name: "my-mcp-server", Version: "0.1.0"}, nil)
    registerTools(s, executor)

    if err := s.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
        fmt.Fprintf(os.Stderr, "server error: %v\n", err)
        os.Exit(1)
    }
}
```

## 4) Single-Instance PID Lock

MCP clients may spawn your server multiple times (reconnects, IDE restarts). A PID lockfile prevents duplicate instances from competing for stdin/stdout.

Key rules:
- Write PID to a `.lock` file on startup; remove it on exit.
- On launch, check if the lockfile's PID is still alive (signal 0 on Unix).
- Remove stale lockfiles automatically.
- Allow disabling via env var (`APP_LOCKFILE=off`) for testing.

```go
const defaultLockFile = "my-mcp-server.lock"

func acquireLock(lockFile string) (func(), error) {
    if strings.EqualFold(lockFile, "off") {
        return func() {}, nil
    }

    // Check if lock file exists and is stale
    if _, err := os.Stat(lockFile); err == nil {
        content, err := os.ReadFile(lockFile)
        if err == nil {
            pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
            if err == nil {
                process, err := os.FindProcess(pid)
                if err == nil {
                    // On Unix, FindProcess always succeeds. Signal 0 checks existence.
                    err = process.Signal(syscall.Signal(0))
                    if err == nil {
                        return nil, fmt.Errorf("another instance is already running (PID: %d)", pid)
                    }
                }
            }
        }
        // Stale or unreadable — safe to remove
        _ = os.Remove(lockFile)
    }

    f, err := os.OpenFile(lockFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
    if err != nil {
        return nil, fmt.Errorf("could not create lock file: %w", err)
    }
    defer f.Close()

    _, err = f.WriteString(fmt.Sprintf("%d", os.Getpid()))
    if err != nil {
        _ = os.Remove(lockFile)
        return nil, fmt.Errorf("could not write PID to lock file: %w", err)
    }

    return func() {
        slog.Debug("Removing lock file", "path", lockFile)
        _ = os.Remove(lockFile)
    }, nil
}
```

Usage in `main()`:

```go
cleanup, err := acquireLock(cfg.LockFile)
if err != nil {
    fmt.Fprintf(os.Stderr, "lock error: %v\n", err)
    os.Exit(1)
}
defer cleanup()
```

Add `*.lock` to `.gitignore` since lockfiles are runtime artifacts.

## 5) Environment and Config Conventions

Suggested env variables:

| Purpose | Example |
|---|---|
| Working directory | `APP_WORKDIR=/path/to/work` |
| Debug logging | `APP_DEBUG=1` |
| Log file path | `APP_LOGFILE=/tmp/my-mcp.log` |
| Command timeout | `APP_CMD_TIMEOUT=30s` |
| Lock file path/disable | `APP_LOCKFILE=off` or `APP_LOCKFILE=/tmp/my-mcp.lock` |

Helper parsers are useful for consistency:

```go
func envBool(key string, def bool) bool { /* ... */ }
func envInt(key string, def int) int { /* ... */ }
func envDuration(key string, def time.Duration) time.Duration { /* ... */ }
```

## 6) `executor.go`: Local Command Execution Pattern

Design goals:
- Execute system commands with context-aware timeouts.
- Capture stdout and stderr.
- Log execution metadata for observability.
- Return raw command output as string.

```go
type Executor struct {
    workDir string
    env     []string
}

func (e *Executor) Run(ctx context.Context, cmd string, args []string) (string, error) {
    start := time.Now()
    
    c := exec.CommandContext(ctx, cmd, args...)
    c.Dir = e.workDir
    c.Env = e.env

    output, err := c.CombinedOutput()
    duration := time.Since(start)

    slog.Info("command_executed",
        "cmd", cmd,
        "args_count", len(args),
        "duration_ms", float64(duration.Microseconds())/1000,
        "output_bytes", len(output),
        "success", err == nil,
    )

    if err != nil {
        return "", fmt.Errorf("command failed: %w", err)
    }

    return string(output), nil
}
```


## 7) `tools.go`: Schema and Handler Pattern

Use reusable `json.RawMessage` schemas and explicit argument parsing.

```go
var querySchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "filter": {"type": "string", "description": "Search expression"},
    "maxCount": {"type": "integer", "minimum": 1, "maximum": 5000}
  }
}`)

func parseArgs(req *mcp.CallToolRequest) (map[string]any, error) {
    args := map[string]any{}
    if req.Params.Arguments == nil {
        return args, nil
    }
    if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
        return nil, err
    }
    return args, nil
}

func textResult(body string) (*mcp.CallToolResult, error) {
    return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: body}}}, nil
}

func errorResult(err error) (*mcp.CallToolResult, error) {
    return &mcp.CallToolResult{
        Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
        IsError: true,
    }, nil
}
```

Tool registration example:

```go
func registerTools(s *mcp.Server, executor *Executor) {
    s.AddTool(&mcp.Tool{
        Name:        "run_command",
        Description: "Execute a system command and return the output.",
        InputSchema: querySchema,
    }, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        args, err := parseArgs(req)
        if err != nil {
            return errorResult(fmt.Errorf("invalid args: %w", err))
        }

        cmd := ""
        var cmdArgs []string
        
        if v, ok := args["command"].(string); ok {
            cmd = v
        }
        if v, ok := args["args"].([]any); ok {
            for _, arg := range v {
                if s, ok := arg.(string); ok {
                    cmdArgs = append(cmdArgs, s)
                }
            }
        }

        output, callErr := executor.Run(ctx, cmd, cmdArgs)
        if callErr != nil {
            return errorResult(callErr)
        }
        return textResult(output)
    })
}
```

Important MCP behavior:
- `req.Params.Arguments` is `json.RawMessage`, not `map[string]any`.
- Numeric args from JSON are `float64` after unmarshal.
- Prefer `errorResult(..., IsError: true)` instead of bubbling raw Go errors from handlers.

## 8) Build/Test Workflow

Minimal `Makefile`:

```makefile
.PHONY: all build run clean test fmt vet install
APP_NAME = my-mcp-server

all: fmt vet build

build:
	go build -o $(APP_NAME) ./cmd/$(APP_NAME)

run:
	go run ./cmd/$(APP_NAME)

test:
	go test -v ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

install: build
	mkdir -p $(HOME)/.local/bin
	cp $(APP_NAME) $(HOME)/.local/bin/

clean:
	rm -f $(APP_NAME)
```

End-to-end MCP test (`tools/test_mcp.py`) using Pydantic AI + stdio:

```python
import asyncio
import os
from pydantic_ai import Agent
from pydantic_ai.mcp import MCPServerStdio

async def main():
    env = os.environ.copy()
    env.setdefault("APP_WORKDIR", ".")

    server = MCPServerStdio(
        "go",
        ["run", "./cmd/my-mcp-server"],
        env=env,
        cwd=os.path.abspath(os.path.join(os.path.dirname(__file__), "..")),
    )

    agent = Agent("claude-opus-4-7", toolsets=[server])

    async with server:
        result = await agent.run("List available tools and execute a sample command.")
        print(result.output)

if __name__ == "__main__":
    asyncio.run(main())
```

Run with:

```bash
uv run tools/test_mcp.py
```

## 9) Practical Design Guidance for LLM-Facing Tools

1. Put concise instructions in tool descriptions.
2. Add helper tools when the LLM needs command syntax or environment context.
3. Keep responses as raw command output; avoid post-processing unless necessary.
4. Log every tool invocation so debugging is possible when agent behavior changes.

## 10) Quick Start Sequence

1. Scaffold `main.go`, `executor.go`, `tools.go`.
2. Implement command execution in `executor.go` with proper error handling.
3. Add one tool with a strict schema for your target command.
4. Validate via `go test` + `uv run tools/test_mcp.py`.
5. Add additional tools and telemetry logging before production use.
