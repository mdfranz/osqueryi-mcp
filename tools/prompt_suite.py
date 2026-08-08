"""Load shared LLM benchmark prompts for the framework harnesses."""

from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
from textwrap import dedent
import tomllib


PROMPT_SUITE_PATH = Path(__file__).with_name("prompts") / "osquery_benchmark.toml"


@dataclass(frozen=True)
class PromptTask:
    """A single model workload from the shared benchmark suite."""

    id: str
    title: str
    prompt: str


def _clean(value: object, field: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{PROMPT_SUITE_PATH}: {field} must be a non-empty string")
    return dedent(value).strip()


def load_prompt_suite() -> tuple[str, tuple[PromptTask, ...]]:
    """Return the common system prompt and tasks for every framework harness."""
    with PROMPT_SUITE_PATH.open("rb") as suite_file:
        suite = tomllib.load(suite_file)

    system = suite.get("system")
    if not isinstance(system, dict):
        raise ValueError(f"{PROMPT_SUITE_PATH}: missing [system] section")

    system_prompt = _clean(system.get("text"), "system prompt")

    tasks = suite.get("tasks")
    if not isinstance(tasks, list) or not tasks:
        raise ValueError(f"{PROMPT_SUITE_PATH}: define at least one [[tasks]] entry")

    loaded_tasks = []
    seen_ids = set()
    for index, task in enumerate(tasks, start=1):
        if not isinstance(task, dict):
            raise ValueError(f"{PROMPT_SUITE_PATH}: tasks entry {index} must be a table")
        task_id = _clean(task.get("id"), f"tasks entry {index}.id")
        if task_id in seen_ids:
            raise ValueError(f"{PROMPT_SUITE_PATH}: duplicate task id: {task_id}")
        seen_ids.add(task_id)

        loaded_tasks.append(
            PromptTask(
                id=task_id,
                title=_clean(task.get("title"), f"task {task_id}.title"),
                prompt=_clean(task.get("text"), f"task {task_id}.text"),
            )
        )

    return system_prompt, tuple(loaded_tasks)
