# Third-Party Dependencies

This document describes the third-party software used by `osqueryi-mcp`.
The Go server has two direct runtime dependencies: the `osqueryi` executable
and the MCP Go SDK. The Python packages are used only by the local
integration and benchmark harnesses; they are not needed to run the compiled
server.

## Go Server Runtime

### System Binary

* **[osqueryi](https://osquery.io)**: The interactive osquery SQL shell and a
  required system dependency. `cmd/osqueryi-mcp/executor.go` starts it once per
  query as `osqueryi --json --config_path=/dev/null <SQL>`. The server uses its
  JSON output to discover tables and schemas and to execute queries against
  the local host.

### Direct Go Module

* **[github.com/modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk)**:
  The direct Go module dependency that implements the stdio MCP server. It
  registers the project's tools, handles MCP/JSON-RPC messages, and derives
  JSON Schemas from the Go tool argument types.

### Go Module Transitives

The following modules are brought into the build through the MCP SDK. They
are not separately imported by this repository.

* **[github.com/google/jsonschema-go](https://github.com/google/jsonschema-go)**:
  Used by the MCP SDK to generate and resolve JSON Schemas for tool inputs;
  the library also supplies JSON Schema validation support.
* **[github.com/segmentio/encoding](https://github.com/segmentio/encoding)**
  and **[github.com/segmentio/asm](https://github.com/segmentio/asm)**: Used
  by the SDK's internal JSON codec. `segmentio/asm` provides architecture-
  specific acceleration where available.
* **[github.com/yosida95/uritemplate/v3](https://github.com/yosida95/uritemplate)**:
  Supports URI-template matching in the MCP SDK's resource features. This
  server currently exposes tools, not resource templates, but the module is
  included by the SDK.
* **[golang.org/x/oauth2](https://pkg.go.dev/golang.org/x/oauth2)**: Used by
  the MCP SDK's authentication package. The stdio server does not configure an
  OAuth flow, but the SDK's shared MCP package imports its authentication API.
* **[golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys)**: Reached through
  `segmentio/asm` for operating-system and CPU feature support. It is not used
  by this project's subprocess execution, which uses Go's standard `os/exec`
  package.

## Python Integration and Benchmark Harnesses

These packages support the scripts in `tools/`. They make live provider API
calls when a corresponding model and API key are selected, and are not part of
the compiled server's runtime.

### Agent Frameworks

* **[pydantic-ai](https://github.com/pydantic/pydantic-ai)**: Used by
  `tools/pydantic_ai_test_mcp.py` to run MCP tools through Pydantic AI, log
  model/tool activity, and collect benchmark token usage.
* **[strands-agents](https://github.com/strands-agents/sdk-python)**: Used by
  `tools/strands_test_mcp.py` to run an agent with an MCP client, provider
  adapters, hooks, and token-usage logging.
* **[agno](https://github.com/agno-agi/agno)**: Used by
  `tools/agno_test_mcp.py` to run the same MCP server through Agno's MCP tool
  integration and collect its model metrics.
* **[mcp (Python SDK)](https://github.com/modelcontextprotocol/python-sdk)**:
  Imported directly by the Strands harness to create stdio server parameters
  and a stdio MCP client. It should be treated as a direct harness dependency,
  even if it is presently installed transitively.

### Model Provider SDKs

* **[google-genai](https://github.com/googleapis/python-genai)**: Declared for
  Gemini support. The harness source selects Gemini through framework adapters
  rather than importing this SDK itself.
* **[openai](https://github.com/openai/openai-python)**: Declared for OpenAI
  model support. The harness source selects OpenAI through framework adapters
  rather than importing this SDK itself.
* **Anthropic provider support**: All three harnesses can select Claude models
  through their framework adapters. The project does not currently declare an
  `anthropic` package directly, so its availability depends on the selected
  framework's installed provider dependencies.
