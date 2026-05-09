# osqueryi-mcp

`osqueryi-mcp` is a Model Context Protocol (MCP) server written in Go that exposes the local system's [osquery](https://osquery.io/) tables as queryable tools. It wraps the `osqueryi` interactive shell via STDIN/STDOUT transport, allowing LLMs and other MCP clients to inspect and query system state using SQL.

## Features

- **Runtime Discovery**: Automatically discovers available tables and schemas based on the installed osquery version.
- **Progressive Disclosure**: Three primary tools allow for efficient exploration:
  - `list_tables`: List all available osquery tables on the current system.
  - `describe_table`: Get the schema (columns and types) for a specific table.
  - `run_query`: Execute arbitrary SQL `SELECT` queries and receive results in JSON format.
- **Safety**: Includes table name validation and uses `--config_path=/dev/null` to ensure clean execution across different environments.
- **Observability**: Structured logging (via `slog`) and a PID lock mechanism to prevent multiple conflicting instances.

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
| `OSQUERYI_DEBUG` | (unset) | Set to any value to enable debug-level logging. |
| `OSQUERYI_LOGFILE` | (unset) | Path to a file for logging. If unset, logs to stderr. |

## Usage with MCP Clients

To use `osqueryi-mcp` with an MCP client (like Claude Desktop), add it to your configuration file:

### Claude Desktop (Linux/macOS)
```json
{
  "mcpServers": {
    "osqueryi-mcp": {
      "command": "/path/to/osqueryi-mcp",
      "env": {
        "OSQUERYI_PATH": "/usr/local/bin/osqueryi"
      }
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

An end-to-end test script is provided in Python using `uv`:

```bash
uv run tools/test_mcp.py
```

## License

[Specify License, e.g., MIT]
