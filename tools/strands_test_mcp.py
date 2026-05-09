import os
import asyncio
from strands import Agent
from strands.models.openai import OpenAIModel
from strands.models.gemini import GeminiModel
from strands.tools.mcp import MCPClient
from mcp.client.stdio import stdio_client
from mcp import StdioServerParameters

def run_strands_mcp():
    # Path to the osqueryi-mcp binary built by Makefile
    server_path = os.path.abspath("osqueryi-mcp")
    
    if not os.path.exists(server_path):
        print(f"Error: Server binary not found at {server_path}")
        print("Please run 'make build' first.")
        return

    # Choose model based on environment variables
    model = None
    if os.getenv("OPENAI_API_KEY"):
        model = OpenAIModel(model_id="gpt-5-mini")
        print("Using OpenAI model (gpt-5-mini)")
    elif os.getenv("GOOGLE_API_KEY") or os.getenv("GEMINI_API_KEY"):
        model = GeminiModel(model_id="gemini-3.1-flash-image-preview")
        print("Using Gemini model (gemini-3.1-flash-image-preview)")
    else:
        print("Error: Neither OPENAI_API_KEY nor GOOGLE_API_KEY/GEMINI_API_KEY found in environment.")
        return

    # 1. Setup transport
    server_params = StdioServerParameters(command=server_path, args=[])
    mcp_client = MCPClient(lambda: stdio_client(server_params))
    
    # 2. Attach to Agent
    agent = Agent(
        model=model, 
        tools=[mcp_client],
        system_prompt="""
        You are an osquery expert. Use the available MCP tools to query system information.
        1. List available tables if you are unsure.
        2. Describe a table to see its schema.
        3. Run SQL queries to get specific information.
        """
    )
    
    try:
        # Example task: Query system information
        print("\n--- Task: What tables are available? ---")
        result = agent("What tables are available in this osquery instance?")
        print(result)
        
        print("\n--- Task: Show first 2 users ---")
        result = agent("Show me the first 2 users from the 'users' table.")
        print(result)
        
    finally:
        # Strands requires explicit stopping of the MCP client to close subprocesses
        mcp_client.stop(None, None, None)

if __name__ == "__main__":
    run_strands_mcp()
