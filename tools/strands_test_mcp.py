import logging
import os
import shutil
import sys
import time
from dataclasses import dataclass, field
from typing import Any

from mcp import StdioServerParameters
from mcp.client.stdio import stdio_client
from strands import Agent
from strands.hooks import (
    AfterModelCallEvent,
    AfterToolCallEvent,
    BeforeModelCallEvent,
    BeforeToolCallEvent,
)
from strands.models.anthropic import AnthropicModel
from strands.models.gemini import GeminiModel
from strands.models.openai import OpenAIModel
from strands.tools.mcp import MCPClient


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


setup_logging(
    "strands_test.log",
    ("mcp", "httpx", "httpcore", "openai", "google.genai"),
)
logger = logging.getLogger("strands_test")


# (model_id_prefixes, api_key_env, factory, label)
PROVIDERS = [
    (("gpt-", "o1-"), "OPENAI_API_KEY", lambda m: OpenAIModel(model_id=m), "OpenAI"),
    (("gemini-",), "GOOGLE_API_KEY", lambda m: GeminiModel(model_id=m), "Gemini"),
    (
        ("claude-",),
        "ANTHROPIC_API_KEY",
        lambda m: AnthropicModel(model_id=m, max_tokens=8192),
        "Anthropic",
    ),
]


def build_model(requested: str):
    for prefixes, env_key, factory, label in PROVIDERS:
        if requested.startswith(prefixes):
            if not os.getenv(env_key):
                logger.error(f"Error: {env_key} not found in environment.")
                return None
            logger.info(f"Using {label} model ({requested})")
            return factory(requested)
    logger.error(f"Error: Unknown model provider for {requested}")
    return None


@dataclass
class TokenUsageTotals:
    model_calls: int = 0
    input_tokens: int = 0
    output_tokens: int = 0
    total_tokens: int = 0
    cache_read_input_tokens: int = 0
    cache_write_input_tokens: int = 0

    def add(self, usage: dict[str, int]) -> None:
        self.model_calls += 1
        self.input_tokens += usage["input_tokens"]
        self.output_tokens += usage["output_tokens"]
        self.total_tokens += usage["total_tokens"]
        self.cache_read_input_tokens += usage["cache_read_input_tokens"]
        self.cache_write_input_tokens += usage["cache_write_input_tokens"]

    def summary(self) -> str:
        summary = (
            f"model_calls={self.model_calls}, input={self.input_tokens}, "
            f"output={self.output_tokens}, total={self.total_tokens}"
        )
        if self.cache_read_input_tokens:
            summary += f", cache_read={self.cache_read_input_tokens}"
        if self.cache_write_input_tokens:
            summary += f", cache_write={self.cache_write_input_tokens}"
        return summary


@dataclass
class RunStats:
    task_tokens: TokenUsageTotals = field(default_factory=TokenUsageTotals)
    overall_tokens: TokenUsageTotals = field(default_factory=TokenUsageTotals)

    def reset_task(self) -> None:
        self.task_tokens = TokenUsageTotals()


def _normalize_usage(usage: Any) -> dict[str, int] | None:
    if usage is None:
        return None

    if not isinstance(usage, dict):
        if hasattr(usage, "model_dump"):
            usage = usage.model_dump()
        elif hasattr(usage, "dict"):
            usage = usage.dict()
        else:
            usage = {
                key: getattr(usage, key)
                for key in (
                    "inputTokens",
                    "outputTokens",
                    "totalTokens",
                    "cacheReadInputTokens",
                    "cacheWriteInputTokens",
                    "prompt_tokens",
                    "completion_tokens",
                    "total_tokens",
                    "cached_tokens",
                )
                if hasattr(usage, key)
            }

    if not isinstance(usage, dict):
        return None

    normalized = {
        "input_tokens": int(usage.get("inputTokens", usage.get("prompt_tokens", 0)) or 0),
        "output_tokens": int(usage.get("outputTokens", usage.get("completion_tokens", 0)) or 0),
        "total_tokens": int(usage.get("totalTokens", usage.get("total_tokens", 0)) or 0),
        "cache_read_input_tokens": int(
            usage.get("cacheReadInputTokens", usage.get("cached_tokens", 0)) or 0
        ),
        "cache_write_input_tokens": int(usage.get("cacheWriteInputTokens", 0) or 0),
    }

    if not any(normalized.values()):
        return None

    return normalized


def _extract_usage_from_event(event: AfterModelCallEvent) -> dict[str, int] | None:
    stop_response = getattr(event, "stop_response", None)
    if not stop_response:
        return None

    message = getattr(stop_response, "message", None)
    if isinstance(message, dict):
        metadata = message.get("metadata", {}) or {}
        usage = _normalize_usage(metadata.get("usage"))
        if usage:
            return usage

    return _normalize_usage(getattr(stop_response, "usage", None))


def _format_usage(usage: dict[str, int]) -> str:
    usage_info = (
        f"input={usage['input_tokens']}, output={usage['output_tokens']}, "
        f"total={usage['total_tokens']}"
    )
    if usage["cache_read_input_tokens"]:
        usage_info += f", cache_read={usage['cache_read_input_tokens']}"
    if usage["cache_write_input_tokens"]:
        usage_info += f", cache_write={usage['cache_write_input_tokens']}"
    return usage_info


def log_agent_events(event, stats: RunStats, model_id: str):
    """Hook callback that logs model calls and tool calls as they happen."""
    match event:
        case BeforeModelCallEvent():
            logger.info(f"\n[Agent] Calling model: {model_id}...")
        case AfterModelCallEvent():
            usage = _extract_usage_from_event(event)
            if usage:
                stats.task_tokens.add(usage)
                stats.overall_tokens.add(usage)
                logger.info(f"[Agent] Model call finished | Model: {model_id} | Tokens: {_format_usage(usage)}")
            else:
                logger.info(f"[Agent] Model call finished | Model: {model_id} | Tokens: unavailable")
        case BeforeToolCallEvent():
            logger.info(f"\n[Tool Call] {event.tool_use['name']}")
            logger.info(f"  Args: {event.tool_use['input']}")
        case AfterToolCallEvent():
            status = event.result.get("status", "unknown")
            logger.info(f"[Tool Result] status={status}")
            res_str = str(event.result.get("content", ""))
            if len(res_str) > 500:
                res_str = res_str[:500] + "... (truncated)"
            logger.info(f"  Output: {res_str}")


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


def run_strands_mcp(requested_model: str):
    server_path = shutil.which("osqueryi-mcp")
    os.environ.setdefault("OSQUERYI_LOCKFILE", "off")
    os.environ.setdefault("OSQUERYI_LOGFILE", "off")

    # google-genai warns when both GEMINI_API_KEY and GOOGLE_API_KEY are set; normalize to one.
    if os.getenv("GEMINI_API_KEY") and not os.getenv("GOOGLE_API_KEY"):
        os.environ["GOOGLE_API_KEY"] = os.getenv("GEMINI_API_KEY")
    elif os.getenv("GOOGLE_API_KEY") and os.getenv("GEMINI_API_KEY"):
        del os.environ["GEMINI_API_KEY"]

    if not server_path:
        logger.error("Error: osqueryi-mcp not found in PATH.")
        return

    model = build_model(requested_model)
    if model is None:
        return

    server_params = StdioServerParameters(command=server_path, args=[])
    mcp_client = MCPClient(lambda: stdio_client(server_params))
    stats = RunStats()

    agent = Agent(
        model=model,
        tools=[mcp_client],
        system_prompt=SYSTEM_PROMPT,
    )

    agent.add_hook(
        lambda event: log_agent_events(event, stats, requested_model),
        [
            BeforeModelCallEvent,
            AfterModelCallEvent,
            BeforeToolCallEvent,
            AfterToolCallEvent,
        ],
    )

    try:
        for title, prompt in TASKS:
            stats.reset_task()
            start = time.perf_counter()
            logger.info("\n" + "=" * 20)
            logger.info(f" TASK: {title}")
            logger.info("=" * 20)
            result = agent(prompt)
            logger.info("\n[Final Answer]")
            logger.info(result)
            logger.info(f"[Token Totals] {stats.task_tokens.summary()}")
            elapsed_ms = (time.perf_counter() - start) * 1000
            logger.info(f"\n[elapsed: {elapsed_ms:.1f} ms]")

        logger.info(f"\n[Overall Token Totals] {stats.overall_tokens.summary()}")

    finally:
        # Strands needs an explicit stop to tear down the MCP subprocess.
        mcp_client.stop(None, None, None)


if __name__ == "__main__":
    default_model = "claude-haiku-4-5"
    model_id = sys.argv[1] if len(sys.argv) > 1 else default_model
    run_strands_mcp(model_id)
