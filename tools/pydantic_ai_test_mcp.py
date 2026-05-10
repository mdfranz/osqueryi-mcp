import asyncio
import logging
import os
import shutil
import sys
import time
from dataclasses import dataclass, field
from typing import Any

from pydantic_ai import Agent, RunContext
from pydantic_ai.mcp import MCPServerStdio

# Configure logging to file and console
LOG_FILE = "pydantic_ai_test.log"
logging.basicConfig(
    level=logging.DEBUG,
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
    datefmt="%Y-%m-%d %H:%M:%S",
    handlers=[logging.FileHandler(LOG_FILE), logging.StreamHandler()],
)
# Set console to INFO to keep it clean while logging DEBUG to file
for h in logging.root.handlers:
    if type(h) is logging.StreamHandler:
        h.setLevel(logging.INFO)

# Enable DEBUG logging for key libraries to capture HTTP and MCP traffic
logging.getLogger("mcp").setLevel(logging.DEBUG)
logging.getLogger("httpx").setLevel(logging.DEBUG)
logging.getLogger("pydantic_ai").setLevel(logging.DEBUG)

logger = logging.getLogger("pydantic_ai_test")


@dataclass
class TokenUsageTotals:
    requests: int = 0
    input_tokens: int = 0
    output_tokens: int = 0
    total_tokens: int = 0

    def add(self, usage: Any) -> None:
        self.requests += getattr(usage, "requests", 0) or 0
        self.input_tokens += getattr(usage, "input_tokens", 0) or 0
        self.output_tokens += getattr(usage, "output_tokens", 0) or 0
        self.total_tokens += getattr(usage, "total_tokens", 0) or 0

    def summary(self) -> str:
        return (
            f"requests={self.requests}, input={self.input_tokens}, "
            f"output={self.output_tokens}, total={self.total_tokens}"
        )


@dataclass
class RunStats:
    task_tokens: TokenUsageTotals = field(default_factory=TokenUsageTotals)
    overall_tokens: TokenUsageTotals = field(default_factory=TokenUsageTotals)

    def reset_task(self) -> None:
        self.task_tokens = TokenUsageTotals()

    def update(self, usage: Any) -> None:
        self.task_tokens.add(usage)
        self.overall_tokens.add(usage)


async def run_pydantic_ai_mcp(requested_model: str):
    server_path = shutil.which("osqueryi-mcp")
    os.environ.setdefault("OSQUERYI_LOCKFILE", "off")
    os.environ.setdefault("OSQUERYI_LOGFILE", "off")

    if not server_path:
        logger.error("Error: osqueryi-mcp not found in PATH.")
        return

    # Intelligent model provider detection
    model_name = requested_model
    if ":" not in model_name:
        if model_name.startswith(("gpt-", "o1-")):
            model_name = f"openai:{model_name}"
        elif model_name.startswith("gemini-"):
            model_name = f"google-gla:{model_name}"
    
    # Check for required API keys
    if model_name.startswith("openai:") and not os.getenv("OPENAI_API_KEY"):
        logger.error("Error: OPENAI_API_KEY not found in environment.")
        return
    elif model_name.startswith("google-") and not (os.getenv("GOOGLE_API_KEY") or os.getenv("GEMINI_API_KEY")):
        logger.error("Error: GOOGLE_API_KEY or GEMINI_API_KEY not found in environment.")
        return
    
    logger.info(f"Using model: {model_name}")

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

    stats = RunStats()

    # 1. Setup transport
    # MCPServerStdio acts as an async context manager
    async with MCPServerStdio(server_path, args=[]) as server:
        # 2. Initialize Agent with the toolset
        agent = Agent(
            model_name,
            toolsets=[server],
            system_prompt="""
            You are an osquery expert. Use the available MCP tools to query system information.
            Prefer search_tables, preview_table, and query_table for discovery and single-table work.
            Use run_query for joins and more complex SQL.

            CRITICAL: After using tools, always provide a detailed final answer that includes:
            1. Summary of what you discovered or found
            2. Which specific tools you used and why each one
            3. Key findings from the data (include sample rows if relevant)
            4. Your analysis and conclusions

            Do not respond with blank or minimal text. Provide comprehensive explanations.
            """,
        )

        try:
            for title, prompt in tasks:
                stats.reset_task()
                start = time.perf_counter()
                logger.info(f"\n" + "=" * 20)
                logger.info(f" TASK: {title}")
                logger.info("=" * 20)
                
                # Pydantic AI run is async
                result = await agent.run(prompt)
                
                logger.info("\n[Final Answer]")
                logger.info(result.output)
                
                usage = result.usage()
                stats.update(usage)
                
                logger.info(f"[Token Totals] {stats.task_tokens.summary()}")
                elapsed_ms = (time.perf_counter() - start) * 1000
                logger.info(f"\n[elapsed: {elapsed_ms:.1f} ms]")

            logger.info(f"\n[Overall Token Totals] {stats.overall_tokens.summary()}")

        except Exception as e:
            logger.exception(f"An error occurred during agent execution: {e}")


if __name__ == "__main__":
    default_model = "gemini-3.1-flash-lite"
    model = sys.argv[1] if len(sys.argv) > 1 else default_model
    asyncio.run(run_pydantic_ai_mcp(model))
