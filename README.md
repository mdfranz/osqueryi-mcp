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
- `make test`: Runs Go unit tests.
- `make fmt`: Formats the source code.
- `make vet`: Runs Go static analysis.
- `make clean`: Removes the compiled binary and lock files.

### Testing

An end-to-end test script is provided in Python using `uv`.

### Basic Smoke Test
```bash
uv run tools/test_mcp.py
```
This smoke test exercises both the original tools and the structured helpers (`search_tables`, `preview_table`, and `query_table`).

### Framework-Specific Examples
These examples demonstrate how to connect LLM agents to `osqueryi-mcp` using popular Python frameworks:

- **Agno (Phidata)**: `uv run tools/agno_test_mcp.py`
- **Strands (AWS)**: `uv run tools/strands_test_mcp.py [model_id]`
- **Pydantic AI**: `uv run tools/pydantic_ai_test_mcp.py [model_id]`

*Note: These require `OPENAI_API_KEY` or `GOOGLE_API_KEY` to be set. For Strands and Pydantic AI, the default model is `gemini-3.1-flash-lite`.*

To run with debug logging enabled and view the output:

```bash
OSQUERYI_DEBUG=1 uv run tools/test_mcp.py
tail -f osqueryi-mcp.log
```

## Documentation & Guides

- [Python MCP Client Development Guide](PYTHON_MCP_CLIENT.md): Technical reference for building Python clients with Agno, Strands, Microsoft Agent Framework, and Pydantic AI.

## License

[Specify License, e.g., MIT]
