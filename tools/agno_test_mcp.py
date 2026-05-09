import asyncio
import os
import time
import shutil
from agno.agent import Agent
from agno.models.openai import OpenAIChat
from agno.models.google import Gemini
from agno.tools.mcp import MCPTools

async def run_agno_mcp():
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
        model = OpenAIChat(id="gpt-5-mini")
        print("Using OpenAI model (gpt-5-mini)")
    elif os.getenv("GOOGLE_API_KEY"):
        model = Gemini(id="gemini-3.1-flash-image-preview")
        print("Using Gemini model (gemini-3.1-flash-image-preview)")
    else:
        print("Error: Neither OPENAI_API_KEY nor GOOGLE_API_KEY/GEMINI_API_KEY found in environment.")
        return

    tasks = [
        (
            "Structured discovery",
            """
            Use the osquery MCP tools to identify account-related tables and compare the new helpers with the legacy flow.
            1. Use search_tables for "user" and also search_columns=true for "uid".
            2. Preview the most relevant tables.
            3. Finish with a short recommendation on which tool sequence is best for fast discovery.
            """,
        ),
        (
            "Single-table investigation",
            """
            Investigate currently running processes using the structured helpers.
            1. Preview the processes table.
            2. Use query_table to return 5 on-disk processes with pid, name, path, uid, and start_time ordered by pid.
            3. Summarize why query_table is safer or faster than using raw SQL for this case.
            """,
        ),
        (
            "Join-heavy query",
            """
            Perform a more complex osquery investigation.
            1. Use run_query to join processes to users on uid.
            2. Return 5 rows with pid, process name, path, and owning username for on-disk processes.
            3. Then query listening_ports joined to processes and summarize any open listeners you find.
            Keep the final answer compact but include which MCP tools were used.
            """,
        ),
    ]

    # Connect to the local osqueryi-mcp server using stdio transport
    async with MCPTools(command=server_path, transport="stdio") as mcp_tools:
        agent = Agent(
            model=model,
            tools=[mcp_tools],
            instructions="""
            You are an osquery expert. Use the available MCP tools to query system information.
            Prefer search_tables, preview_table, and query_table for discovery and single-table work.
            Use run_query when joins or more complex SQL are needed.
            Be explicit about which tools you used and why.
            """,
            debug_mode=True,
            markdown=True
        )

        for title, prompt in tasks:
            start = time.perf_counter()
            print(f"\n--- {title} ---")
            await agent.aprint_response(prompt)
            elapsed_ms = (time.perf_counter() - start) * 1000
            print(f"[elapsed: {elapsed_ms:.1f} ms]")

if __name__ == "__main__":
    asyncio.run(run_agno_mcp())
