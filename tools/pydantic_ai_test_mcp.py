import asyncio
import logging
import os
import shutil
import sys
import time
from dataclasses import dataclass, field
from typing import Any

from pydantic_ai import Agent, RunContext
from pydantic_ai.capabilities.hooks import Hooks
from pydantic_ai.mcp import MCPToolset, StdioTransport
from pydantic_ai.models import ModelRequestContext, ModelResponse


def setup_logging(log_file: str, debug_libs: tuple[str, ...]) -> None:
    logging.basicConfig(
        level=logging.DEBUG,
        format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S",
        handlers=[logging.FileHandler(log_file), logging.StreamHandler()],
    )
    # Console at INFO; file stays at DEBUG. `type(h) is` (not isinstance) avoids matching FileHandler.
    for h in logging.root.handlers:
        if type(h) is logging.StreamHandler:
            h.setLevel(logging.INFO)
    for name in debug_libs:
        logging.getLogger(name).setLevel(logging.DEBUG)


setup_logging("pydantic_ai_test.log", ("mcp", "httpx", "pydantic_ai"))
logger = logging.getLogger("pydantic_ai_test")


# (bare_model_prefixes, provider_prefix_to_apply, api_key_envs)
PROVIDERS = [
    (("gpt-", "o1-"), "openai:", ("OPENAI_API_KEY",)),
    (("gemini-",), "google:", ("GOOGLE_API_KEY", "GEMINI_API_KEY")),
    (("claude-",), "anthropic:", ("ANTHROPIC_API_KEY",)),
    (
        ("deepseek/", "deepseek-", "openrouter/", "qwen/", "qwen-"),
        "openrouter:",
        ("OPENROUTER_API_KEY",),
    ),
]


def resolve_model(requested: str) -> str | None:
    """Add a provider prefix if missing and verify the API key. Returns None if a key is missing."""
    name = requested
    if ":" not in name:
        matched = False
        for prefixes, provider, _ in PROVIDERS:
            if name.startswith(prefixes):
                name = provider + name
                matched = True
                break
        if not matched and "/" in name:
            name = "openrouter:" + name

    for _, provider, env_keys in PROVIDERS:
        if name.startswith(provider):
            if not any(os.getenv(k) for k in env_keys):
                logger.error(f"Error: {' or '.join(env_keys)} not found in environment.")
                return None
            break

    return name


def _total_tokens(usage: Any) -> int:
    t = getattr(usage, "total_tokens", 0)
    return t() if callable(t) else (t or 0)


@dataclass
class TokenUsageTotals:
    requests: int = 0
    input_tokens: int = 0
    output_tokens: int = 0
    total_tokens: int = 0
    cache_read_tokens: int = 0

    def add(self, usage: Any) -> None:
        self.requests += getattr(usage, "requests", 0) or 0
        self.input_tokens += getattr(usage, "input_tokens", 0) or 0
        self.output_tokens += getattr(usage, "output_tokens", 0) or 0
        self.total_tokens += _total_tokens(usage)
        self.cache_read_tokens += getattr(usage, "cache_read_tokens", 0) or 0

    def summary(self) -> str:
        return (
            f"requests={self.requests}, input={self.input_tokens}, "
            f"output={self.output_tokens}, total={self.total_tokens}, "
            f"cache_read={self.cache_read_tokens}"
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


TASKS = [
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


SYSTEM_PROMPT = """
You are an osquery expert. Use the available MCP tools to query system information.
Prefer search_tables, preview_table, and query_table for discovery and single-table work.
Use run_query for joins and more complex SQL.

CRITICAL: After using tools, always provide a detailed final answer that includes:
1. Summary of what you discovered or found
2. Which specific tools you used and why each one
3. Key findings from the data (include sample rows if relevant)
4. Your analysis and conclusions

Do not respond with blank or minimal text. Provide comprehensive explanations.
"""


async def run_pydantic_ai_mcp(requested_model: str):
    server_path = shutil.which("osqueryi-mcp")
    os.environ.setdefault("OSQUERYI_LOCKFILE", "off")
    os.environ.setdefault("OSQUERYI_LOGFILE", "off")

    if not server_path:
        raise RuntimeError("osqueryi-mcp not found in PATH")

    model_name = resolve_model(requested_model)
    if model_name is None:
        raise RuntimeError(f"could not configure model: {requested_model}")

    logger.info(f"Using model: {model_name}")

    hooks = Hooks()

    @hooks.on.after_model_request
    async def log_model_usage(
        ctx: RunContext[None],
        *,
        request_context: ModelRequestContext,
        response: ModelResponse,
    ) -> ModelResponse:
        usage = response.usage
        logger.info(
            f"[Agent] Model turn finished | Model: {response.model_name} | Tokens: "
            f"input={usage.input_tokens}, output={usage.output_tokens}, "
            f"total={_total_tokens(usage)}, cache_read={usage.cache_read_tokens}"
        )
        return response

    stats = RunStats()

    async with MCPToolset(StdioTransport(server_path, args=[])) as server:
        agent = Agent(
            model_name,
            toolsets=[server],
            capabilities=[hooks],
            system_prompt=SYSTEM_PROMPT,
        )

        try:
            for title, prompt in TASKS:
                stats.reset_task()
                start = time.perf_counter()
                logger.info("\n" + "=" * 20)
                logger.info(f" TASK: {title}")
                logger.info("=" * 20)

                result = await agent.run(prompt)

                logger.info("\n[Final Answer]")
                logger.info(result.output)

                stats.update(result.usage)

                logger.info(f"[Token Totals] {stats.task_tokens.summary()}")
                elapsed_ms = (time.perf_counter() - start) * 1000
                logger.info(f"\n[elapsed: {elapsed_ms:.1f} ms]")

            logger.info(f"\n[Overall Token Totals] {stats.overall_tokens.summary()}")

        except Exception as e:
            logger.exception(f"An error occurred during agent execution: {e}")
            raise


if __name__ == "__main__":
    default_model = "claude-haiku-4-5"
    model = sys.argv[1] if len(sys.argv) > 1 else default_model
    asyncio.run(run_pydantic_ai_mcp(model))
