import os
import asyncio
import time
import shutil
import logging
from strands import Agent
from strands.models.openai import OpenAIModel
from strands.models.gemini import GeminiModel
from strands.tools.mcp import MCPClient
from strands.hooks import BeforeToolCallEvent, AfterToolCallEvent, BeforeModelCallEvent
from mcp.client.stdio import stdio_client
from mcp import StdioServerParameters

# Configure logging to show MCP traffic and other debug info
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
    datefmt="%H:%M:%S"
)
# Enable DEBUG logging for MCP to see the raw JSON-RPC messages
logging.getLogger("mcp").setLevel(logging.DEBUG)

def log_agent_events(event):
    """Callback for strands agent hooks to provide visibility into its inner workings."""
    if isinstance(event, BeforeModelCallEvent):
        print(f"\n[Agent] Calling model...")
    elif isinstance(event, BeforeToolCallEvent):
        print(f"\n[Tool Call] {event.tool_use['name']}")
        print(f"  Args: {event.tool_use['input']}")
    elif isinstance(event, AfterToolCallEvent):
        status = event.result.get("status", "unknown")
        print(f"[Tool Result] status={status}")
        # Truncate large results for cleaner stdout
        res_str = str(event.result.get("content", ""))
        if len(res_str) > 500:
            res_str = res_str[:500] + "... (truncated)"
        print(f"  Output: {res_str}")

def run_strands_mcp():
    server_path = shutil.which("osqueryi-mcp")
    os.environ.setdefault("OSQUERYI_LOCKFILE", "off")
    os.environ.setdefault("OSQUERYI_LOGFILE", "off")
    
    # Normalize API keys to avoid warnings
    if os.getenv("GEMINI_API_KEY") and not os.getenv("GOOGLE_API_KEY"):
        os.environ["GOOGLE_API_KEY"] = os.environ["GEMINI_API_KEY"]
    elif os.getenv("GOOGLE_API_KEY") and os.getenv("GEMINI_API_KEY"):
        # If both are set, unset GEMINI_API_KEY to avoid the warning from google-genai
        del os.environ["GEMINI_API_KEY"]
    
    if not server_path:
        print("Error: osqueryi-mcp not found in PATH.")
        return

    # Choose model based on environment variables
    model = None
    if os.getenv("OPENAI_API_KEY"):
        model = OpenAIModel(model_id="gpt-5-mini")
        print("Using OpenAI model (gpt-5-mini)")
    elif os.getenv("GOOGLE_API_KEY"):
        model = GeminiModel(model_id="gemini-3.1-flash-image-preview")
        print("Using Gemini model (gemini-3.1-flash-image-preview)")
    else:
        print("Error: Neither OPENAI_API_KEY nor GOOGLE_API_KEY/GEMINI_API_KEY found in environment.")
        return

    tasks = [
        (
            "Structured discovery",
            """
            Use the osquery MCP tools to find account-related tables efficiently.
            Prefer search_tables first, then preview_table for the best candidates, and explain whether the helper tools reduce round trips compared with list_tables + describe_table.
            """,
        ),
        (
            "Single-table query",
            """
            Investigate processes using the structured helpers.
            Preview the processes table, then use query_table to return 5 on-disk processes with pid, name, path, uid, and start_time ordered by pid.
            End with a short note on why query_table is a better fit than raw SQL here.
            """,
        ),
        (
            "Join-heavy workload",
            """
            Run a more complex osquery investigation.
            Use run_query to join processes to users on uid and return 5 rows with pid, process name, path, and username for on-disk processes.
            Then inspect listening_ports joined to processes and summarize any listeners you find.
            Mention which tools you used.
            """,
        ),
    ]

    # 1. Setup transport
    server_params = StdioServerParameters(command=server_path, args=[])
    mcp_client = MCPClient(lambda: stdio_client(server_params))
    
    # 2. Attach to Agent
    agent = Agent(
        model=model, 
        tools=[mcp_client],
        system_prompt="""
        You are an osquery expert. Use the available MCP tools to query system information.
        Prefer search_tables, preview_table, and query_table for discovery and single-table work.
        Use run_query for joins and more complex SQL.
        Be explicit about which tools you used and why.
        """
    )
    
    # 3. Add hooks for visibility
    agent.add_hook(log_agent_events, [BeforeModelCallEvent, BeforeToolCallEvent, AfterToolCallEvent])
    
    try:
        for title, prompt in tasks:
            start = time.perf_counter()
            print(f"\n" + "="*20)
            print(f" TASK: {title}")
            print("="*20)
            result = agent(prompt)
            print("\n[Final Answer]")
            print(result)
            elapsed_ms = (time.perf_counter() - start) * 1000
            print(f"\n[elapsed: {elapsed_ms:.1f} ms]")
        
    finally:
        # Strands requires explicit stopping of the MCP client to close subprocesses
        mcp_client.stop(None, None, None)

if __name__ == "__main__":
    run_strands_mcp()
