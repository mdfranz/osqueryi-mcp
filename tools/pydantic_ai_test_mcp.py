import asyncio
import logging
import os
import shutil
import sys
import time
from dataclasses import dataclass, field
from typing import Any

import logfire
from pydantic_ai import Agent, RunContext
from pydantic_ai.capabilities.hooks import Hooks
from pydantic_ai.mcp import MCPToolset, StdioTransport
from pydantic_ai.models import ModelRequestContext, ModelResponse

from build_metadata import git_revision
from prompt_suite import load_prompt_suite

# Must happen before instrument_*() calls, or the hooks register with nowhere to send data.
logfire.configure(
    service_name="pydantic-ai-test-mcp",
    service_version=git_revision(),
    environment=os.getenv("LOGFIRE_ENVIRONMENT", "local"),
)
logfire.instrument_pydantic_ai()  # agent runs, model requests, tool calls, token usage
logfire.instrument_mcp()  # client-side spans for calls to the osqueryi-mcp stdio server


QUIET_LIBS = ("httpcore", "hpack")  # frame-by-frame transport decode noise; not useful at DEBUG


def setup_logging(log_file: str, debug_libs: tuple[str, ...], quiet_libs: tuple[str, ...] = ()) -> None:
    logging.basicConfig(
        level=logging.DEBUG,
        format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S",
        handlers=[
            logging.FileHandler(log_file),
            logging.StreamHandler(),
            logfire.LogfireLoggingHandler(),  # forward existing stdlib log calls to Logfire too
        ],
    )
    # Console at INFO; file stays at DEBUG. `type(h) is` (not isinstance) avoids matching FileHandler.
    for h in logging.root.handlers:
        if type(h) is logging.StreamHandler:
            h.setLevel(logging.INFO)
    for name in debug_libs:
        logging.getLogger(name).setLevel(logging.DEBUG)
    # These inherit DEBUG from the root logger regardless of debug_libs, and the file/Logfire
    # sinks aren't capped at INFO like the console is, so they flood both. httpx's own one-
    # line-per-request logs are unaffected since they're on the "httpx" logger, not these.
    for name in quiet_libs:
        logging.getLogger(name).setLevel(logging.WARNING)


setup_logging("pydantic_ai_test.log", ("mcp", "httpx", "pydantic_ai"), QUIET_LIBS)
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
    system_prompt, tasks = load_prompt_suite()

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
        # Structured, queryable counterpart to the log line above. instrument_pydantic_ai()
        # already puts usage on the model-request span; this makes per-turn usage easy to
        # find/filter/alert on directly without digging into span attributes.
        logfire.info(
            "Model turn finished: {model_name}",
            model_name=response.model_name,
            input_tokens=usage.input_tokens,
            output_tokens=usage.output_tokens,
            total_tokens=_total_tokens(usage),
            cache_read_tokens=usage.cache_read_tokens,
        )
        return response

    stats = RunStats()

    # Without an explicit env, the mcp SDK spawns the subprocess with only
    # HOME/LOGNAME/PATH/SHELL/TERM/USER inherited (a safety default) — silently dropping
    # OSQUERYI_LOCKFILE/OSQUERYI_LOGFILE above and any OTEL_EXPORTER_OTLP_* the server's own
    # telemetry needs. Pass the full parent env through explicitly, like the other two scripts.
    #
    # keep_alive defaults to True (subprocess survives __aexit__, for reuse across multiple
    # `async with` blocks). Since this script enters the toolset exactly once, that just means
    # the child never gets a graceful stdin-close: it's left running until something outside
    # Python kills it, which bypasses the Go server's deferred cleanup — including its
    # telemetry.Shutdown() that flushes buffered spans/logs/metrics. Traces/logs mostly survive
    # anyway because their batch processors flush periodically during the run, but the metrics
    # PeriodicReader's default 60s interval means short runs lose everything. keep_alive=False
    # makes __aexit__ actually call disconnect(), so the child sees a clean EOF and its own
    # graceful-shutdown path (and telemetry flush) gets to run.
    transport = StdioTransport(server_path, args=[], env=dict(os.environ), keep_alive=False)
    async with MCPToolset(transport) as server:
        agent = Agent(
            model_name,
            toolsets=[server],
            capabilities=[hooks],
            system_prompt=system_prompt,
        )

        try:
            for task in tasks:
                title, prompt = task.title, task.prompt
                with logfire.span("task: {title}", title=title):
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

                    logfire.info(
                        "Task completed: {title}",
                        title=title,
                        elapsed_ms=elapsed_ms,
                        input_tokens=stats.task_tokens.input_tokens,
                        output_tokens=stats.task_tokens.output_tokens,
                        total_tokens=stats.task_tokens.total_tokens,
                        cache_read_tokens=stats.task_tokens.cache_read_tokens,
                    )

            logger.info(f"\n[Overall Token Totals] {stats.overall_tokens.summary()}")
            logfire.info(
                "All tasks completed",
                task_count=len(tasks),
                input_tokens=stats.overall_tokens.input_tokens,
                output_tokens=stats.overall_tokens.output_tokens,
                total_tokens=stats.overall_tokens.total_tokens,
                cache_read_tokens=stats.overall_tokens.cache_read_tokens,
            )

        except Exception as e:
            logger.exception(f"An error occurred during agent execution: {e}")
            raise


if __name__ == "__main__":
    default_model = "claude-haiku-4-5"
    model = sys.argv[1] if len(sys.argv) > 1 else default_model
    asyncio.run(run_pydantic_ai_mcp(model))
