# Python MCP Client Development Guide

This guide provides a technical reference for building Python Model Context Protocol (MCP) clients using the libraries available in this project. It focuses on general patterns for connecting LLM agents to any generic MCP server.

---

## 1. Core Concepts

### Transport Mechanisms
MCP clients must match the server's transport protocol:
*   **Stdio**: The client launches the server as a local subprocess.
*   **HTTP (Streamable/SSE)**: The client connects to a remote URL via JSON-RPC over HTTP or Server-Sent Events.

### Session Lifecycle
1.  **Initialize**: Negotiate protocol version and capabilities.
2.  **Notification**: Signal that the client is ready (`notifications/initialized`).
3.  **Discovery**: List available tools (`tools/list`), resources, and prompts.
4.  **Execution**: Call tools (`tools/call`) and process results.
5.  **Shutdown**: Cleanly terminate the session.

---

## 2. Framework Implementations

### **Agno (formerly Phidata)**
Agno provides a high-level `MCPTools` class that automates discovery and session management. It supports both HTTP and Stdio transports natively.
**Package:** `pip install agno`

```python
import asyncio
from agno.agent import Agent
from agno.models.openai import OpenAIChat
from agno.tools.mcp import MCPTools

async def run_agno_mcp():
    # Supports 'streamable-http', 'sse', or 'stdio'
    async with MCPTools(command="./osqueryi-mcp", transport="stdio") as mcp_tools:
        agent = Agent(
            model=OpenAIChat(id="gpt-4o"),
            tools=[mcp_tools],
            instructions="Use the available MCP tools to assist the user.",
            debug_mode=True
        )
        await agent.aprint_response("Perform a task using the toolset.")

asyncio.run(run_agno_mcp())
```

### **Strands SDK (AWS)**
Strands offers a model-driven framework for building AI agents. It leverages the official Python MCP SDK and supports Stdio, SSE, and Streamable HTTP.
**Package:** `pip install strands-agents`

```python
from strands import Agent
from strands.models.openai import OpenAIModel
from strands.tools.mcp import MCPClient
from mcp.client.stdio import stdio_client
from mcp import StdioServerParameters

def run_strands_mcp():
    # 1. Setup transport (Example: Stdio)
    server_params = StdioServerParameters(command="./osqueryi-mcp", args=[])
    mcp_client = MCPClient(lambda: stdio_client(server_params))
    
    # 2. Attach to Agent
    agent = Agent(
        model=OpenAIModel(model_id="gpt-4o"), 
        tools=[mcp_client],
        system_prompt="You are an assistant with access to MCP tools."
    )
    
    try:
        result = agent("List the available tools and pick one to demonstrate.")
        print(result)
    finally:
        # Strands requires explicit stopping of the MCP client to close subprocesses
        mcp_client.stop(None, None, None)

run_strands_mcp()
```

### **Microsoft Agent Framework**
Optimized for high-performance async loops and batching. Uses an internal `_mcp` adapter for Streamable HTTP.

```python
import asyncio
from agent_framework import Agent
from agent_framework.openai import OpenAIChatClient
from agent_framework._mcp import MCPStreamableHTTPTool

async def run_ms_agent_mcp():
    async with MCPStreamableHTTPTool(name="mcp_server", url="http://localhost:8765/mcp") as mcp:
        agent = Agent(
            client=OpenAIChatClient(model_id="gpt-4o"),
            tools=[mcp],
            instructions="Analyze the environment using the provided MCP tools."
        )
        response = await agent.run("What is the current status?")
        print(response.text)

asyncio.run(run_ms_agent_mcp())
```

### **Pydantic AI**
Uses a manual dependency injection pattern for maximum control and type-safety. Since no native adapter is currently included, you must implement the JSON-RPC handshake using `httpx`.

```python
import asyncio
import httpx
from pydantic_ai import Agent, RunContext

class MCPDeps:
    """Minimal dependency for MCP over HTTP."""
    def __init__(self, url: str):
        self.url = url
        self.client = httpx.AsyncClient(timeout=10.0)
        self.session_id = None

    async def initialize(self):
        # 1. initialize
        resp = await self.client.post(self.url, json={
            "jsonrpc": "2.0", "id": 1, "method": "initialize",
            "params": {"protocolVersion": "2024-11-05", "capabilities": {}, "clientInfo": {"name": "pydantic-ai", "version": "1.0"}}
        })
        self.session_id = resp.headers.get("Mcp-Session-Id")
        # 2. notifications/initialized
        await self.client.post(self.url, json={"jsonrpc": "2.0", "method": "notifications/initialized"}, 
                               headers={"Mcp-Session-Id": self.session_id} if self.session_id else {})

    async def call_tool(self, name: str, args: dict):
        payload = {"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": {"name": name, "arguments": args}}
        resp = await self.client.post(self.url, json=payload, headers={"Mcp-Session-Id": self.session_id} if self.session_id else {})
        return resp.json().get("result", {})

agent = Agent(model="openai:gpt-4o", deps_type=MCPDeps)

@agent.tool
async def use_mcp_tool(ctx: RunContext[MCPDeps], tool_name: str, arguments: dict):
    """Execute a tool on the remote MCP server."""
    if not ctx.deps.session_id:
        await ctx.deps.initialize()
    return await ctx.deps.call_tool(tool_name, arguments)

async def run_pydantic_mcp():
    deps = MCPDeps("http://localhost:8765/mcp")
    try:
        result = await agent.run("Use the 'echo' tool to say hello.", deps=deps)
        print(result.data)
    finally:
        await deps.client.aclose()

asyncio.run(run_pydantic_mcp())
```

---

## 3. Engineering Best Practices

### **1. Handling Structured Content**
If the MCP server provides `structuredContent` (JSON), prefer using it over raw text for state tracking.
```python
# Preferred pattern for state-aware agents
result = mcp_response.get("structuredContent")
if result:
    current_state = result["state"]
```

### **2. Observability & Logging**
Wrap tool calls to track latency. All clients in this project use the `llm_observability.py` utility for structured `key=value` logging.
```python
from llm_observability import Timer, log_kv
import logging

logger = logging.getLogger(__name__)

async def instrumented_call(tool_name, args):
    timer = Timer.start_new()
    result = await mcp_client.call_tool(tool_name, args)
    log_kv(logger, event="tool_call", tool=tool_name, latency_ms=timer.elapsed_ms())
    return result
```

### **3. Robustness & Retries**
LLMs may occasionally hallucinate tool arguments or call non-existent tools. 
*   **Agno/MS Agent/Strands**: Use the framework's built-in tool retry logic if available.
*   **Pydantic AI**: Set `output_retries=3` and use a `BaseModel` for the tool's return type to force schema adherence.

### **4. Error Handling**
Always check the `isError` flag in MCP responses. An LLM might perceive a "Successful" tool call even if the server returned an application-level error message in the payload.

---

## 4. Testing & Validation

*   **Smoke Test**: Use `scripts/mcp-smoke-test.sh` to verify raw JSON-RPC connectivity.
*   **Payload Inspection**: Enable HTTP debug logging in any client using `LOG_HTTP=True` to see the raw traffic.
*   **Benchmark**: Compare framework overhead using `scripts/mcp_benchmark.py`.
