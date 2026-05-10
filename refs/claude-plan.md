# Plan: osqueryi-mcp — Go MCP Server with STDIN Transport

## Context

Build a Go MCP server that wraps `osqueryi` (osquery interactive shell) via STDIN/STDOUT transport, exposing the local system's osquery tables as queryable MCP tools. The server follows the patterns established in `MCP-SQL-GUIDE.md` (project layout, StdioTransport, PID lock, slog). The schema reference is osquery 5.23.0 (https://osquery.io/schema/5.23.0/); the actual binary may differ slightly but the tool approach is version-agnostic.

## File Structure

```
cmd/osqueryi-mcp/
  main.go       # config, slog setup, PID lock, server bootstrap
  executor.go   # osqueryi subprocess invocation and output parsing
  tools.go      # MCP tool schemas and handler registration
tools/
  test_mcp.py   # end-to-end Python test client
go.mod          # module: github.com/mdfranz/osqueryi-mcp
go.sum
Makefile
.gitignore
```

## Dependencies

- `github.com/modelcontextprotocol/go-sdk v1.4.1` — `mcp.StdioTransport`, `mcp.Server`, `mcp.Tool`, `mcp.CallToolResult`, `mcp.TextContent`
- No other external dependencies

## Environment Variables

| Variable | Default | Purpose |
|---|---|---|
| `OSQUERYI_PATH` | `osqueryi` (PATH lookup) | Path to osqueryi binary |
| `OSQUERYI_TIMEOUT` | `30s` | Query timeout |
| `OSQUERYI_LOCKFILE` | `osqueryi-mcp.lock` | PID lock path; `off` disables |
| `OSQUERYI_DEBUG` | unset | Enable debug logging |
| `OSQUERYI_LOGFILE` | `osqueryi-mcp.log` | Log to file instead of stderr |

## MCP Tools (3 total)

### `list_tables`
- **Input schema**: `{"type":"object","properties":{},"additionalProperties":false}`
- **Invocation**: Pipes `.tables\n` to stdin of `osqueryi --config_path=/dev/null`. Parses `  => tablename` output lines.
- **Output**: Newline-separated list of table names

### `describe_table`
- **Input schema**: `{"type":"object","properties":{"table_name":{"type":"string","description":"osquery table name (e.g. 'processes')"}},"required":["table_name"],"additionalProperties":false}`
- **Invocation**: `osqueryi --json --config_path=/dev/null "PRAGMA table_info(tablename);"`
- **Output**: Raw JSON from PRAGMA (fields: cid, name, type, notnull, dflt_value, pk)
- **Validation**: `validateTableName` enforces `^[a-z][a-z0-9_]*$` to prevent injection

### `run_query`
- **Input schema**: `{"type":"object","properties":{"sql":{"type":"string","description":"SQL SELECT query against osquery virtual tables"}},"required":["sql"],"additionalProperties":false}`
- **Invocation**: `osqueryi --json --config_path=/dev/null "<sql>"`
- **Output**: Raw JSON array of result rows

## Key Implementation Details

### `executor.go` — Core Functions

```go
type Executor struct {
    binaryPath string
    timeout    time.Duration
}
```

**`runSQL(ctx, sql string) ([]byte, error)`** — shared helper:
```go
cmd := exec.CommandContext(ctx, e.binaryPath, "--json", "--config_path=/dev/null", sql)
// Capture stdout (JSON) and stderr (errors) separately
// On non-zero exit: return error using trimmed stderr text
```

**`listTables(ctx) ([]string, error)`** — pipes `.tables\n` to stdin, parses `=> tablename` lines from stdout.

**`validateTableName(name string) error`** — regex `^[a-z][a-z0-9_]*$`.

**`DescribeTable(ctx, tableName)` → `runSQL` with `PRAGMA table_info(tableName);`**

**`RunQuery(ctx, sql)` → `runSQL` with user SQL**

### `tools.go` — Helpers

```go
func textResult(body string) *mcp.CallToolResult
func errorResult(msg string) *mcp.CallToolResult  // IsError: true
func parseArgs(req *mcp.CallToolRequest) (map[string]any, error)
```

All handlers log: `slog.Info("tool_called", "tool", toolName, ...)` on every invocation.

Query errors return `errorResult(...)` with `IsError: true` (not Go errors) so the LLM can self-correct.

### `main.go` — Bootstrap

1. Set up `slog.NewTextHandler` on stderr (or OSQUERYI_LOGFILE)
2. `loadConfig()` — validates osqueryi binary via `exec.LookPath`; exits 1 if not found
3. `acquireLock(cfg.LockFile)` — PID lock from MCP-SQL-GUIDE.md pattern (syscall.Signal(0) liveness check)
4. Create `mcp.NewServer(&mcp.Implementation{Name: "osqueryi-mcp", Version: "0.1.0"}, nil)`
5. `registerTools(s, executor)`
6. `s.Run(ctx, &mcp.StdioTransport{})`

## Design Rationale

- **Runtime discovery over static schema embed**: `list_tables` uses `.tables` dot-command, `describe_table` uses `PRAGMA table_info`. This works for any installed osquery version without maintaining a bundled schema file.
- **Tools only, no MCP Resources**: Table availability is platform/runtime-dependent, so tools with progressive disclosure (list → describe → query) are the right primitive.
- **`--config_path=/dev/null`**: Required on installations with system osquery config files, which emit warnings to stderr that would pollute error detection.
- **stdin pipe for `.tables`**: The `.tables` dot-command is an interactive shell command, not valid SQL. Piped via stdin; the SQL query tools pass SQL as argv instead.

## Makefile Targets

```makefile
APP_NAME = osqueryi-mcp
all: fmt vet build
build: go build -o $(APP_NAME) ./cmd/$(APP_NAME)
run: go run ./cmd/$(APP_NAME)
test: go test -v ./...
fmt: go fmt ./...
vet: go vet ./...
install: build + cp to ~/.local/bin
clean: rm -f $(APP_NAME)
```

## Verification

1. **Build**: `make build` — must compile with zero warnings
2. **Unit smoke test**: `echo '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' | ./osqueryi-mcp` → returns JSON list of 3 tools
3. **list_tables**: `echo '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_tables","arguments":{}}}' | ./osqueryi-mcp`
4. **describe_table**: call with `{"table_name":"users"}` → returns PRAGMA JSON with cid/name/type columns
5. **run_query**: call with `{"sql":"SELECT username, uid, shell FROM users LIMIT 5"}` → returns JSON rows
6. **Error case**: call `run_query` with invalid SQL → `IsError: true` in response, not a protocol error
7. **End-to-end**: `uv run tools/test_mcp.py` with a Claude agent exercising all three tools
