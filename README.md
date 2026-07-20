# osqueryi-mcp

`osqueryi-mcp` is a Model Context Protocol (MCP) server written in Go that exposes the local system's [osquery](https://osquery.io/) tables as queryable tools. It wraps the `osqueryi` interactive shell via STDIN/STDOUT transport, allowing LLMs and other MCP clients to inspect and query system state using SQL.

## Features

- **Runtime Discovery**: Automatically discovers available tables and schemas based on the installed osquery version.
- **Progressive Disclosure**: Three primary tools allow for efficient exploration:
  - `list_tables`: List all available osquery tables on the current system.
  - `describe_table`: Get the schema (columns and types) for a specific table.
  - `run_query`: Execute arbitrary SQL `SELECT` queries and receive results in JSON format.
- **Workflow Helpers**: Additional tools reduce common multi-step loops:
  - `search_tables`: Find relevant tables by table or column name.
  - `preview_table`: Return schema plus sample rows in one call.
  - `query_table`: Build validated single-table queries from structured arguments.
  - `refresh_cache`: Clear and reload the cached list of tables and their schemas.
- **Safety**: Includes table name validation and uses `--config_path=/dev/null` to ensure clean execution across different environments.
- **Observability**: Structured logging (via `slog`) to `osqueryi-mcp.log` by default, and a PID lock mechanism to prevent multiple conflicting instances.

## Prerequisites

- **osquery**: You must have `osquery` installed on your system. The `osqueryi` binary should be in your `PATH`.
  - [Installation Guide for osquery](https://osquery.io/downloads)
- **Go**: Version 1.22 or later (required to build from source).

## Installation

### From Source

1. Clone the repository:
   ```bash
   git clone https://github.com/mdfranz/osqueryi-mcp.git
   cd osqueryi-mcp
   ```

2. Build the binary:
   ```bash
   make build
   ```
   This will create an `osqueryi-mcp` binary in the project root.

3. (Optional) Install to `~/.local/bin`:
   ```bash
   make install
   ```

## Configuration

The server is configured via environment variables:

| Variable | Default | Description |
|---|---|---|
| `OSQUERYI_PATH` | `osqueryi` | Path to the `osqueryi` binary. |
| `OSQUERYI_TIMEOUT` | `30s` | Maximum duration for a query to run. |
| `OSQUERYI_LOCKFILE` | `osqueryi-mcp.lock` | Path to the PID lock file. Set to `off` to disable. |
| `OSQUERYI_CACHEFILE` | `osqueryi-mcp-cache.json` | Path to the schema cache file. Set to `off` to disable. |
| `OSQUERYI_DEBUG` | (unset) | Set to any value to enable debug-level logging. |
| `OSQUERYI_LOGFILE` | `osqueryi-mcp.log` | Path to a file for logging. Set to `off` or use an empty string to log to stderr. |

## Caching

`osqueryi-mcp` uses a local JSON file to cache osquery table names and their schemas. This significantly improves performance for tools like `search_tables`, `preview_table`, and `query_table` that need to know column definitions before executing.

- **On-Disk Persistence**: The cache is saved to disk so it persists across server restarts.
- **Background Warming**: Upon startup, the server automatically starts a background process to "warm" the cache by fetching any missing schemas.
- **Auto-Update**: Whenever a new table is described via `describe_table` or `preview_table`, the cache is updated.
- **Manual Refresh**: Use the `refresh_cache` tool to force a full reload of all table schemas from `osqueryi`.
- **Disabling**: Set `OSQUERYI_CACHEFILE=off` to disable persistent caching entirely.

## Usage with MCP Clients

To use `osqueryi-mcp` with an MCP client (like Claude Desktop), add it to your configuration file:

### MCP Configuration
```json
{
  "mcpServers": {
    "osqueryi-mcp": {
      "command": "osqueryi-mcp" 
    }
  }
}
```

## Development

### Makefile Targets

- `make build`: Compiles the binary.
- `make run`: Runs the server directly using `go run`.
- `make test`: Runs the Go test suite (`go test ./...`).
- `make fmt`: Formats the source code.
- `make vet`: Runs Go static analysis.
- `make clean`: Removes the compiled binary plus the default lock, cache, and log files.

### Testing

The repository currently provides a direct MCP smoke harness and live LLM
integration/benchmark harnesses. They require a built `osqueryi-mcp` binary in
the project root, a working `osqueryi` installation, and Python dependencies
managed by `uv`.

These scripts exercise the local machine's live osquery data. The LLM harnesses
also make billable provider API calls, so they are exploratory integration
checks rather than deterministic regression tests.

### Basic Smoke Test
```bash
uv run tools/test_mcp.py
```
This starts the local server over stdio, verifies initialization and the tool
catalog, and invokes every MCP tool. It fails on protocol errors, unexpected
tool failures, or a missing expected error for an invalid raw-SQL query.

The convenience runner builds the binary, runs this smoke harness, then runs
the Agno, Strands, and Pydantic AI harnesses for every non-comment model in
`models.txt`:

```bash
./run_tests.sh
```

### Framework-Specific Examples
These examples demonstrate how to connect LLM agents to `osqueryi-mcp` using popular Python frameworks:

- **Agno (Phidata)**: `uv run tools/agno_test_mcp.py [model_id]` — default: `gemini-3.1-flash-lite-preview`
- **Strands (AWS)**: `uv run tools/strands_test_mcp.py [model_id]` — default: `claude-haiku-4-5`
- **Pydantic AI**: `uv run tools/pydantic_ai_test_mcp.py [model_id]` — default: `claude-haiku-4-5`

Set the API key for the selected provider: `OPENAI_API_KEY` for OpenAI,
`GOOGLE_API_KEY` (or `GEMINI_API_KEY` where supported) for Gemini, or
`ANTHROPIC_API_KEY` for Claude. The harnesses disable the MCP PID lock and
server logfile so they can start their own temporary stdio server.

To run the smoke harness with debug logging written to a file:

```bash
OSQUERYI_DEBUG=1 OSQUERYI_LOGFILE=osqueryi-mcp.log uv run tools/test_mcp.py
tail -f osqueryi-mcp.log
```

## Documentation & Guides

### Project & Optimization
- [Development Journey](PROJECT.md): The narrative of how `osqueryi-mcp` evolved from MVP to a tuned production-ready server.
- [Optimization Results](TUNING.md): Detailed benchmarks and key findings on token efficiency, model comparisons (Gemini vs OpenAI), and system prompting.

### Technical References
- [Python MCP Client Guide](refs/PYTHON_MCP_CLIENT.md): Technical reference for building Python clients with Agno, Strands, and Pydantic AI.
- [SQL MCP Architecture Patterns](refs/SQL-MCP.md): Deep dive into advanced patterns for schema discovery and progressive disclosure.
- [Go MCP Server Guide](refs/MCP-SQL-GUIDE.md): Best practices for building Go-based MCP servers for local command execution.
- [Original Implementation Plan](refs/claude-plan.md): The foundational design document for the server.

## License

[Specify License, e.g., MIT]
