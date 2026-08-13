import asyncio
import logging
import os
import shutil
import sys
import time
from dataclasses import dataclass, field
from inspect import isawaitable
from typing import Any, Callable

import logfire
from agno.agent import Agent
from agno.models.anthropic import Claude
from agno.models.google import Gemini
from agno.models.openai import OpenAIChat
from agno.models.openrouter import OpenRouter
from agno.tools.mcp import MCPTools
from openinference.instrumentation.agno import AgnoInstrumentor

from build_metadata import git_revision
from prompt_suite import load_prompt_suite

# Must happen before instrument()/instrument_*() calls, or the hooks register with nowhere to
# send data. Agno itself has no native Logfire instrumentor; per
# https://pydantic.dev/docs/logfire/integrations/llms/agno/ it's instrumented via the
# OpenInference OTel instrumentor, which picks up the tracer provider logfire.configure() sets.
logfire.configure(
    service_name="agno-test-mcp",
    service_version=git_revision(),
    environment=os.getenv("LOGFIRE_ENVIRONMENT", "local"),
)
AgnoInstrumentor().instrument()  # agent runs, model requests, tool calls, token usage
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
    for handler in logging.root.handlers:
        if type(handler) is logging.StreamHandler:
            handler.setLevel(logging.INFO)
    for name in debug_libs:
        logging.getLogger(name).setLevel(logging.DEBUG)
    # These inherit DEBUG from the root logger regardless of debug_libs, and the file/Logfire
    # sinks aren't capped at INFO like the console is, so they flood both. httpx's own one-
    # line-per-request logs are unaffected since they're on the "httpx" logger, not these.
    for name in quiet_libs:
        logging.getLogger(name).setLevel(logging.WARNING)


setup_logging("agno_test.log", ("agno", "mcp", "httpx"), QUIET_LIBS)
logger = logging.getLogger("agno_test")


PROVIDERS = [
    (("gpt-", "o1-"), ("OPENAI_API_KEY",), lambda model_id: OpenAIChat(id=model_id), "OpenAI"),
    (
        ("gemini-",),
        ("GOOGLE_API_KEY", "GEMINI_API_KEY"),
        lambda model_id: Gemini(id=model_id),
        "Gemini",
    ),
    (("claude-",), ("ANTHROPIC_API_KEY",), lambda model_id: Claude(id=model_id), "Anthropic"),
    (
        ("deepseek/", "deepseek-", "openrouter/", "qwen/", "qwen-"),
        ("OPENROUTER_API_KEY",),
        # OpenRouter's own default max_tokens is 1024 — too low for the longer analysis
        # tasks, where it silently truncates the final answer mid-sentence instead of
        # erroring. 8192 matches the headroom given to the other providers.
        lambda model_id: OpenRouter(id=model_id, max_tokens=8192),
        "OpenRouter",
    ),
]


@dataclass
class TokenUsageTotals:
    runs: int = 0
    model_calls: int = 0
    input_tokens: int = 0
    output_tokens: int = 0
    total_tokens: int = 0
    cache_read_tokens: int = 0
    cache_write_tokens: int = 0
    reasoning_tokens: int = 0

    def add(self, metrics: Any, *, model_calls: int) -> None:
        self.runs += 1
        self.model_calls += model_calls
        if metrics is None:
            return

        self.input_tokens += int(getattr(metrics, "input_tokens", 0) or 0)
        self.output_tokens += int(getattr(metrics, "output_tokens", 0) or 0)
        self.total_tokens += int(getattr(metrics, "total_tokens", 0) or 0)
        self.cache_read_tokens += int(getattr(metrics, "cache_read_tokens", 0) or 0)
        self.cache_write_tokens += int(getattr(metrics, "cache_write_tokens", 0) or 0)
        self.reasoning_tokens += int(getattr(metrics, "reasoning_tokens", 0) or 0)

    def summary(self) -> str:
        parts = [
            f"runs={self.runs}",
            f"model_calls={self.model_calls}",
            f"input={self.input_tokens}",
            f"output={self.output_tokens}",
            f"total={self.total_tokens}",
        ]
        if self.cache_read_tokens:
            parts.append(f"cache_read={self.cache_read_tokens}")
        if self.cache_write_tokens:
            parts.append(f"cache_write={self.cache_write_tokens}")
        if self.reasoning_tokens:
            parts.append(f"reasoning={self.reasoning_tokens}")
        return ", ".join(parts)


@dataclass
class RunStats:
    task_tokens: TokenUsageTotals = field(default_factory=TokenUsageTotals)
    overall_tokens: TokenUsageTotals = field(default_factory=TokenUsageTotals)

    def reset_task(self) -> None:
        self.task_tokens = TokenUsageTotals()

    def update(self, response: Any) -> None:
        model_calls = _count_model_calls(getattr(response, "messages", None))
        metrics = getattr(response, "metrics", None)
        self.task_tokens.add(metrics, model_calls=model_calls)
        self.overall_tokens.add(metrics, model_calls=model_calls)


def _truncate(value: Any, limit: int = 500) -> str:
    text = str(value)
    if len(text) > limit:
        return text[:limit] + "... (truncated)"
    return text


def _count_model_calls(messages: list[Any] | None) -> int:
    if not messages:
        return 0
    return sum(
        1
        for message in messages
        if getattr(message, "metrics", None) is not None and getattr(message, "role", None) == "assistant"
    )


def _format_metrics(metrics: Any) -> str:
    if metrics is None:
        return "unavailable"

    parts = [
        f"input={int(getattr(metrics, 'input_tokens', 0) or 0)}",
        f"output={int(getattr(metrics, 'output_tokens', 0) or 0)}",
        f"total={int(getattr(metrics, 'total_tokens', 0) or 0)}",
    ]

    cache_read_tokens = int(getattr(metrics, "cache_read_tokens", 0) or 0)
    cache_write_tokens = int(getattr(metrics, "cache_write_tokens", 0) or 0)
    reasoning_tokens = int(getattr(metrics, "reasoning_tokens", 0) or 0)
    cost = getattr(metrics, "cost", None)

    if cache_read_tokens:
        parts.append(f"cache_read={cache_read_tokens}")
    if cache_write_tokens:
        parts.append(f"cache_write={cache_write_tokens}")
    if reasoning_tokens:
        parts.append(f"reasoning={reasoning_tokens}")
    if cost is not None:
        parts.append(f"cost={cost}")

    return ", ".join(parts)


def _format_tool_duration(tool_metrics: Any) -> str:
    duration_s = getattr(tool_metrics, "duration", None)
    if duration_s is None:
        return "unknown"
    return f"{duration_s * 1000:.1f} ms"


def _count_event(events: list[Any] | None, event_name: str) -> int:
    if not events:
        return 0
    return sum(1 for event in events if getattr(event, "event", None) == event_name)


def build_model(requested: str):
    for prefixes, env_keys, factory, label in PROVIDERS:
        if requested.startswith(prefixes):
            if not any(os.getenv(env_key) for env_key in env_keys):
                logger.error(f"Error: {' or '.join(env_keys)} not found in environment.")
                return None
            logger.info(f"Using {label} model ({requested})")
            return factory(requested)

    for _, env_keys, factory, label in PROVIDERS:
        if any(os.getenv(env_key) for env_key in env_keys):
            logger.info(f"Using {label} model ({requested})")
            return factory(requested)

    logger.error(
        "Error: No API key found in environment "
        "(OPENAI_API_KEY, GOOGLE_API_KEY/GEMINI_API_KEY, or ANTHROPIC_API_KEY)."
    )
    return None


def log_run_start(run_input: Any, agent: Agent | None = None) -> None:
    model_id = getattr(getattr(agent, "model", None), "id", None) or "unknown"
    logger.info(f"\n[Agent] Starting run | Model: {model_id}")
    logger.info(f"  Prompt: {_truncate(getattr(run_input, 'input_content', run_input), 300)}")


def log_run_completion(run_output: Any) -> None:
    metrics = getattr(run_output, "metrics", None)
    logger.info(f"[Agent] Run finished | Tokens: {_format_metrics(metrics)}")
    # Structured, queryable counterpart to the log line above. AgnoInstrumentor already puts
    # usage on the run span; this makes it easy to find/filter/alert on directly.
    if metrics is not None:
        logfire.info(
            "Agent run finished",
            input_tokens=int(getattr(metrics, "input_tokens", 0) or 0),
            output_tokens=int(getattr(metrics, "output_tokens", 0) or 0),
            total_tokens=int(getattr(metrics, "total_tokens", 0) or 0),
            cache_read_tokens=int(getattr(metrics, "cache_read_tokens", 0) or 0),
            cache_write_tokens=int(getattr(metrics, "cache_write_tokens", 0) or 0),
        )


async def telemetry_tool_hook(
    function_name: str,
    function_call: Callable[..., Any],
    arguments: dict[str, Any],
) -> Any:
    logger.info(f"\n[Tool Call] {function_name}")
    logger.info(f"  Args: {arguments}")
    start = time.perf_counter()

    # AgnoInstrumentor already spans tool calls; this manual span groups this hook's own
    # log lines under it in the trace tree and captures exceptions with a real span status.
    with logfire.span("tool call: {function_name}", function_name=function_name, arguments=arguments):
        try:
            result = function_call(**arguments)
            if isawaitable(result):
                result = await result
        except Exception as exc:
            elapsed_ms = (time.perf_counter() - start) * 1000
            logger.exception(f"[Tool Result] error after {elapsed_ms:.1f} ms: {exc}")
            raise

    elapsed_ms = (time.perf_counter() - start) * 1000
    logger.info(f"[Tool Result] duration={elapsed_ms:.1f} ms")
    logger.info(f"  Output: {_truncate(result)}")
    return result


def log_model_messages(messages: list[Any] | None) -> None:
    if not messages:
        return

    model_call = 0
    for message in messages:
        metrics = getattr(message, "metrics", None)
        if metrics is None or getattr(message, "role", None) != "assistant":
            continue
        model_call += 1
        logger.info(f"[Model Call {model_call}] Tokens: {_format_metrics(metrics)}")


def log_tool_summary(tools: list[Any] | None) -> None:
    if not tools:
        return

    logger.info("[Tool Summary]")
    for tool in tools:
        status = "error" if getattr(tool, "tool_call_error", False) else "ok"
        logger.info(
            f"  {getattr(tool, 'tool_name', 'unknown')} | "
            f"status={status} | duration={_format_tool_duration(getattr(tool, 'metrics', None))}"
        )


def log_event_summary(events: list[Any] | None) -> None:
    if not events:
        return

    logger.info(
        "[Event Summary] "
        f"model_requests={_count_event(events, 'ModelRequestCompleted')}, "
        f"tool_calls={_count_event(events, 'ToolCallCompleted')}"
    )


async def run_agno_mcp(model_id: str):
    server_path = shutil.which("osqueryi-mcp")
    os.environ.setdefault("OSQUERYI_LOCKFILE", "off")
    os.environ.setdefault("OSQUERYI_LOGFILE", "off")

    if os.getenv("GEMINI_API_KEY") and not os.getenv("GOOGLE_API_KEY"):
        os.environ["GOOGLE_API_KEY"] = os.getenv("GEMINI_API_KEY")
    elif os.getenv("GOOGLE_API_KEY") and os.getenv("GEMINI_API_KEY"):
        del os.environ["GEMINI_API_KEY"]

    if not server_path:
        raise RuntimeError("osqueryi-mcp not found in PATH")

    model = build_model(model_id)
    if model is None:
        raise RuntimeError(f"could not configure model: {model_id}")

    system_prompt, tasks = load_prompt_suite()

    stats = RunStats()

    # Without an explicit env, MCPTools spawns the subprocess with only
    # HOME/LOGNAME/PATH/SHELL/TERM/USER inherited (mcp SDK's get_default_environment()) —
    # silently dropping OSQUERYI_LOCKFILE/OSQUERYI_LOGFILE above and any OTEL_EXPORTER_OTLP_*
    # the server's own telemetry needs. Pass the full parent env through explicitly.
    # (Unlike pydantic_ai's StdioTransport, MCPTools.__aexit__ has no keep_alive escape hatch —
    # it always closes the connection, so the child already gets a graceful shutdown here.)
    async with MCPTools(command=server_path, env=dict(os.environ), transport="stdio") as mcp_tools:
        agent = Agent(
            model=model,
            tools=[mcp_tools],
            instructions=system_prompt,
            tool_hooks=[telemetry_tool_hook],
            pre_hooks=[log_run_start],
            post_hooks=[log_run_completion],
            store_events=True,
            debug_mode=True,
            markdown=False,
        )

        for task in tasks:
            title, prompt = task.title, task.prompt
            with logfire.span("task: {title}", title=title):
                stats.reset_task()
                start = time.perf_counter()
                logger.info("\n" + "=" * 20)
                logger.info(f" TASK: {title}")
                logger.info("=" * 20)

                response = await agent.arun(prompt)
                stats.update(response)

                logger.info("\n[Final Answer]")
                logger.info(response.content)
                log_model_messages(getattr(response, "messages", None))
                log_tool_summary(getattr(response, "tools", None))
                log_event_summary(getattr(response, "events", None))
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

if __name__ == "__main__":
    default_model = "gemini-3.1-flash-lite-preview"
    model = sys.argv[1] if len(sys.argv) > 1 else default_model
    asyncio.run(run_agno_mcp(model))
