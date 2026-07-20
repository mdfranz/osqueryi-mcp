# 3rd Party Dependencies

This document lists and classifies the 3rd party dependencies used in the `osqueryi-mcp` project.

## Core Infrastructure

### System Binary Integration
*   **[osqueryi](https://osquery.io)**: Interactive SQL shell for osquery. In this project, `cmd/osqueryi-mcp/executor.go` executes `osqueryi --json` as a subprocess to query system state, discover table schemas, and retrieve operating system telemetry.

### Model Context Protocol (MCP)
*   **[github.com/modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk)**: Official Go SDK for the Model Context Protocol. In this project, it powers the stdio MCP server in `cmd/osqueryi-mcp/main.go` and `cmd/osqueryi-mcp/tools.go`, registering tools (`list_tables`, `describe_table`, `preview_table`, `search_tables`, `query_table`, `run_query`, `refresh_cache`), serving JSON-RPC requests, and managing tool schemas.

### Go Support & Transitive Libraries
*   **[github.com/google/jsonschema-go](https://github.com/google/jsonschema-go)**: JSON Schema parser and validator used transitively by `modelcontextprotocol/go-sdk` for validating tool parameter schemas and payloads.
*   **[github.com/segmentio/encoding](https://github.com/segmentio/encoding)** & **[github.com/segmentio/asm](https://github.com/segmentio/asm)**: High-performance SIMD-accelerated JSON encoding/decoding library used under the hood by the MCP SDK for fast protocol serialization.
*   **[github.com/yosida95/uritemplate/v3](https://github.com/yosida95/uritemplate)**: RFC 6570 URI template library used by the MCP Go SDK for resource URI matching.
*   **[golang.org/x/sys](https://golang.org/x/sys)**: Low-level Go OS and system call primitives used for subprocess pipe execution and cross-platform process handling.

## Python Test & Integration Harnesses

### Agent Frameworks
*   **[pydantic-ai](https://github.com/pydantic/pydantic-ai)**: Pydantic AI framework used in `tools/pydantic_ai_test_mcp.py` to evaluate typed agent workflows, per-turn token logging, and model benchmarks.
*   **[strands-agents](https://github.com/strands-ai/strands)**: Strands agent framework used in `tools/strands_test_mcp.py` to test multi-turn tool calling, latency, and token consumption across models.
*   **[agno](https://github.com/agno-agi/agno)**: Agno agent framework used in `tools/agno_test_mcp.py` to validate multi-framework MCP client execution against `osqueryi-mcp`.

### LLM Provider SDKs
*   **[google-genai](https://github.com/googleapis/python-genai)**: Official Google GenAI Python SDK enabling test integration harnesses to connect directly to Gemini models (e.g., `gemini-3.1-flash`).
*   **[openai](https://github.com/openai/openai-python)**: Official OpenAI Python SDK supporting cross-model benchmarking (e.g., `gpt-5-mini`, `gpt-5-nano`) in Python integration tests.
