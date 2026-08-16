"""Generate reproducible charts for ollama-shootout-081626.md.

The values below are the suite and task means recorded in the accompanying
report, derived from the EU Logfire `mcp-eval` project. All four models
(granite4.1:8b, ornith:9b, gemma4:e4b, qwen3.5:9b) are based on two suites
each, run serially against a local Ollama server. qwen3.5:9b's batch ran
separately (2026-08-16 15:27-16:07 UTC) from the other three (11:51-12:58
UTC), both under the same OLLAMA_CONTEXT_LENGTH=32768 configuration.
"""

from pathlib import Path

import matplotlib.pyplot as plt
import numpy as np
import pandas as pd


OUTPUT_DIR = Path(__file__).parent

MODEL_SUMMARY = pd.DataFrame(
    [
        ("granite4.1:8b", "IBM Granite", 2, 25.0, 21.0, 80_541, 346.9),
        ("gemma4:e4b", "Google Gemma", 2, 22.5, 18.5, 89_729, 371.7),
        ("ornith:9b", "Ornith (Qwen3.5 fine-tune)", 2, 32.0, 53.5, 303_771, 1_279.3),
        ("qwen3.5:9b", "Alibaba Qwen3.5", 2, 43.5, 55.5, 399_680, 1_364.5),
    ],
    columns=["model", "family", "suites", "turns", "calls", "tokens", "seconds"],
)

TASK_METRICS = pd.DataFrame(
    [
        ("Structured discovery", "granite4.1:8b", 59.5, 3_906, 2.0),
        ("Structured discovery", "gemma4:e4b", 52.7, 4_574, 2.0),
        ("Structured discovery", "ornith:9b", 197.7, 11_588, 12.5),
        ("Structured discovery", "qwen3.5:9b", 284.6, 99_751, 20.0),
        ("Single-table query", "granite4.1:8b", 58.2, 6_843, 2.0),
        ("Single-table query", "gemma4:e4b", 71.1, 8_876, 2.0),
        ("Single-table query", "ornith:9b", 88.1, 8_101, 2.0),
        ("Single-table query", "qwen3.5:9b", 96.0, 8_585, 2.0),
        ("Join-heavy workload", "granite4.1:8b", 80.9, 16_790, 7.0),
        ("Join-heavy workload", "gemma4:e4b", 103.4, 22_387, 5.5),
        ("Join-heavy workload", "ornith:9b", 225.9, 34_891, 10.0),
        ("Join-heavy workload", "qwen3.5:9b", 236.2, 22_440, 6.0),
        ("Ambiguous classification", "granite4.1:8b", 148.4, 53_003, 10.0),
        ("Ambiguous classification", "gemma4:e4b", 144.6, 53_893, 9.0),
        ("Ambiguous classification", "ornith:9b", 767.6, 249_191, 29.0),
        ("Ambiguous classification", "qwen3.5:9b", 747.7, 268_905, 27.5),
    ],
    columns=["task", "model", "seconds", "tokens", "calls"],
)

MODELS = MODEL_SUMMARY["model"].tolist()
TASKS = TASK_METRICS["task"].drop_duplicates().tolist()
COLORS = {
    "granite4.1:8b": "#2ca02c",
    "gemma4:e4b": "#1f77b4",
    "ornith:9b": "#d62728",
    "qwen3.5:9b": "#9467bd",
}


def configure_style() -> None:
    plt.style.use("seaborn-v0_8-whitegrid")
    plt.rcParams.update(
        {
            "axes.titleweight": "bold",
            "axes.labelcolor": "#263238",
            "axes.edgecolor": "#b0bec5",
            "font.size": 10,
            "figure.dpi": 150,
            "savefig.dpi": 180,
        }
    )


def save(fig: plt.Figure, filename: str) -> None:
    fig.tight_layout()
    fig.savefig(OUTPUT_DIR / filename, bbox_inches="tight", facecolor="white")
    plt.close(fig)


def suite_efficiency_chart() -> None:
    fig, ax = plt.subplots(figsize=(9, 5.4))
    for row in MODEL_SUMMARY.itertuples(index=False):
        ax.scatter(
            row.seconds,
            row.tokens,
            s=45 + row.calls * 6,
            color=COLORS[row.model],
            edgecolor="white",
            linewidth=1.3,
            zorder=3,
        )
        ax.annotate(
            row.model,
            (row.seconds, row.tokens),
            xytext=(7, 5),
            textcoords="offset points",
            fontsize=9,
        )
    ax.set_yscale("log")
    ax.set_xlabel("Mean task time per suite (seconds)  ← faster")
    ax.set_ylabel("Mean total tokens per suite (log scale)  ← leaner")
    ax.set_title("Suite efficiency: latency vs. token use (local Ollama)")
    ax.text(
        0.01,
        -0.22,
        "Bubble area = mean MCP calls. Lower-left is preferable. n=2 suites per model.",
        transform=ax.transAxes,
        fontsize=8.5,
        color="#546e7a",
    )
    ax.grid(True, which="both", alpha=0.35)
    save(fig, "ollama-shootout-suite-efficiency.png")


def task_metric_chart(metric: str, ylabel: str, title: str, filename: str) -> None:
    fig, ax = plt.subplots(figsize=(10, 5.4))
    x = np.arange(len(TASKS))
    width = 0.19
    for index, model in enumerate(MODELS):
        values = (
            TASK_METRICS[TASK_METRICS["model"] == model]
            .set_index("task")
            .loc[TASKS, metric]
        )
        bars = ax.bar(
            x + (index - (len(MODELS) - 1) / 2) * width,
            values,
            width,
            label=model,
            color=COLORS[model],
        )
        if metric == "calls":
            ax.bar_label(bars, labels=[f"{value:g}" for value in values], padding=2, fontsize=7)
    ax.set_xticks(x, TASKS)
    ax.set_ylabel(ylabel)
    ax.set_title(title)
    ax.legend(ncol=4, loc="upper left", fontsize=8.5)
    ax.grid(axis="y", alpha=0.35)
    save(fig, filename)


def main() -> None:
    configure_style()
    suite_efficiency_chart()
    task_metric_chart(
        "seconds",
        "Mean task time (seconds)",
        "Task latency by model (local Ollama)",
        "ollama-shootout-task-latency.png",
    )
    task_metric_chart(
        "calls",
        "Mean MCP calls per task",
        "Tool-call discipline by task (local Ollama)",
        "ollama-shootout-task-tool-calls.png",
    )


if __name__ == "__main__":
    main()
