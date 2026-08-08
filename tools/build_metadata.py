"""Build metadata shared by the local LLM framework harnesses."""

from __future__ import annotations

from functools import lru_cache
import os
from pathlib import Path
import subprocess


@lru_cache
def git_revision() -> str:
    """Return the current revision, or a supplied/unknown value outside a checkout."""
    if revision := os.getenv("OSQUERYI_GIT_REVISION"):
        return revision

    try:
        result = subprocess.run(
            ["git", "rev-parse", "--short=12", "HEAD"],
            cwd=Path(__file__).parent.parent,
            check=True,
            capture_output=True,
            text=True,
            timeout=1,
        )
    except (OSError, subprocess.CalledProcessError, subprocess.TimeoutExpired):
        return "unknown"

    return result.stdout.strip() or "unknown"
