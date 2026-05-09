import asyncio
import os
from agno.agent import Agent
from agno.models.openai import OpenAIChat
from agno.models.google import Gemini
from agno.tools.mcp import MCPTools

async def run_agno_mcp():
    # Path to the osqueryi-mcp binary built by Makefile
    server_path = os.path.abspath("osqueryi-mcp")
    
    if not os.path.exists(server_path):
        print(f"Error: Server binary not found at {server_path}")
        print("Please run 'make build' first.")
        return

    # Choose model based on environment variables
    model = None
    if os.getenv("OPENAI_API_KEY"):
        model = OpenAIChat(id="gpt-5-mini")
        print("Using OpenAI model (gpt-5-mini)")
    elif os.getenv("GOOGLE_API_KEY") or os.getenv("GEMINI_API_KEY"):
        model = Gemini(id="gemini-3.1-flash-image-preview")
        print("Using Gemini model (gemini-3.1-flash-image-preview)")
    else:
        print("Error: Neither OPENAI_API_KEY nor GOOGLE_API_KEY/GEMINI_API_KEY found in environment.")
        return

    # Connect to the local osqueryi-mcp server using stdio transport
    async with MCPTools(command=server_path, transport="stdio") as mcp_tools:
        agent = Agent(
            model=model,
            tools=[mcp_tools],
            instructions="""
            You are an osquery expert. Use the available MCP tools to query system information.
            1. List available tables if you are unsure.
            2. Describe a table to see its schema.
            3. Run SQL queries to get specific information.
            """,
            debug_mode=True,
            markdown=True
        )
        
        # Example task: Query system information
        await agent.aprint_response("What tables are available in this osquery instance? Then show me the first 2 users from the 'users' table.")

if __name__ == "__main__":
    asyncio.run(run_agno_mcp())
