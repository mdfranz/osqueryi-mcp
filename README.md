# osqueryi-mcp

`osqueryi-mcp` is a Model Context Protocol (MCP) server written in Go that exposes the local system's [osquery](https://osquery.io/) tables as queryable tools. The MCP server communicates with its client over stdio; when an operation needs uncached osquery data, it starts `osqueryi` as a subprocess and reads its JSON output. This lets MCP clients inspect and query local system state using SQL.

## Features

- **Runtime Discovery**: Discovers the tables and schemas exposed by the installed `osqueryi` binary. Persisted cache data is reused until it is refreshed or removed.
- **Progressive Disclosure**: Three primary tools support table exploration:
  - `list_tables`: List all available osquery tables on the current system.
  - `describe_table`: Get the schema (columns and types) for a specific table.
  - `run_query`: Execute SQL through `osqueryi`, including joins, and receive a JSON response subject to the server's output limits.
- **Workflow Helpers**: Additional tools reduce common multi-step loops:
  - `search_tables`: Find relevant tables by table or column name.
  - `preview_table`: Return schema plus sample rows in one call.
  - `query_table`: Build a single-table query with validated table, column, ordering, and limit arguments.
  - `refresh_cache`: Clear and reload the cached list of tables and their schemas.
- **Guardrails**: Structured tools validate table names and known columns. `run_query` intentionally passes its SQL through to `osqueryi`; it is not restricted to a SELECT-only subset. Every invocation uses `--config_path=/dev/null` to ignore the normal osquery configuration file on Unix-like systems.
- **Observability**: Structured logging (via `slog`) goes to `osqueryi-mcp.log` by default. A PID lock prevents another server using the same configured lock file from starting.

## Prerequisites

- **osquery**: You must have `osquery` installed on your system. Make `osqueryi` available on `PATH`, or set `OSQUERYI_PATH` to its full path.
  - [Installation Guide for osquery](https://osquery.io/downloads)
- **Go**: Version 1.26.5 or later (required to build from source).

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

3. (Optional) Install to `~/.local/bin` (create the directory first if needed, and ensure it is on `PATH`):
   ```bash
   mkdir -p ~/.local/bin
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

`osqueryi-mcp` uses a local JSON file to cache osquery table names and their schemas. This reduces repeated schema lookups for tools such as `search_tables`, `preview_table`, and `query_table`.

- **On-Disk Persistence**: The cache is saved to disk so it persists across server restarts.
- **Background Warming**: On startup, the server attempts to fetch any missing schemas in a background task.
- **Auto-Update**: Schemas fetched by `describe_table` or `preview_table` are saved to the cache.
- **Manual Refresh**: Use the `refresh_cache` tool to force a full reload of all table schemas from `osqueryi`.
- **Disabling**: Set `OSQUERYI_CACHEFILE=off` to disable persistent caching entirely.

The cache does not record the `osqueryi` version or check schema freshness. Run `refresh_cache` after upgrading or reconfiguring osquery, or remove the cache file to force rediscovery on the next start.

## Result Limits

`run_query`, `query_table`, and `preview_table` limit returned data to keep MCP responses manageable. Results are truncated when they exceed the row or payload limits; the response includes a `truncated` indicator when truncation occurs. `run_query` and `query_table` return at most 100 rows after response processing, while `preview_table` accepts at most 100 rows. A requested `query_table` limit is capped at 1,000 rows before response processing. The server attempts to keep JSON payloads below approximately 16 KiB; a single oversized row can still exceed that target.

## Usage with MCP Clients

To use an installed `osqueryi-mcp` binary with an MCP client (like Claude Desktop), add it to your configuration file. If you built from source without installing it, use the absolute path to the binary instead.

### MCP Configuration
```json
{
  "mcpServers": {
    "osqueryi-mcp": {
      "command": "/absolute/path/to/osqueryi-mcp"
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

The repository provides a direct MCP smoke harness and live LLM
integration/benchmark harnesses. They require a working `osqueryi` installation
and Python dependencies run through `uv`. The smoke harness resolves a built
`osqueryi-mcp` binary from the project root; the framework harnesses require
`osqueryi-mcp` on `PATH`.

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
`ANTHROPIC_API_KEY` for Claude. The harnesses default the MCP PID lock and
server logfile to `off` so they can start their own temporary stdio server;
explicitly supplied environment values take precedence.

For a framework example after building in the project root without installing:

```bash
PATH="$PWD:$PATH" uv run tools/agno_test_mcp.py
```

To run the smoke harness with debug logging written to a file:

```bash
OSQUERYI_DEBUG=1 OSQUERYI_LOGFILE=osqueryi-mcp.log uv run tools/test_mcp.py
tail -f osqueryi-mcp.log
```

## Documentation & Guides

### Project & Optimization
- [Development Journey](PROJECT.md): The narrative of how `osqueryi-mcp` evolved from its MVP through subsequent implementation and tuning work.
- [Optimization Results](TUNING.md): Detailed benchmarks and key findings on token efficiency, model comparisons (Gemini vs OpenAI), and system prompting.

### Technical References
- [Dependencies & Package Classification](PKG.md): Classification and enumeration of 3rd party Go, Python, and system dependencies.
- [Python MCP Client Guide](refs/PYTHON_MCP_CLIENT.md): Technical reference for building Python clients with Agno, Strands, and Pydantic AI.
- [SQL MCP Architecture Patterns](refs/SQL-MCP.md): Deep dive into advanced patterns for schema discovery and progressive disclosure.
- [Go MCP Server Guide](refs/MCP-SQL-GUIDE.md): Best practices for building Go-based MCP servers for local command execution.
- [Original Implementation Plan](refs/claude-plan.md): The foundational design document for the server.

## License

No license has been specified for this repository.
