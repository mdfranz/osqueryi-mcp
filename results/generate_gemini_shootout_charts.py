"""Generate reproducible charts for gemini-shootout-081426.md.

The values below are the suite and task means recorded in the accompanying
report from the EU Logfire `mcp-eval` project. Gemini and DeepSeek are based on
two suites per model; GPT-5.6 Terra is based on three.
"""

from pathlib import Path

import matplotlib.pyplot as plt
import numpy as np
import pandas as pd


OUTPUT_DIR = Path(__file__).parent

MODEL_SUMMARY = pd.DataFrame(
    [
        ("Gemini 3.5 Flash", "Google Gemini", 2, 51.5, 50.0, 496_643, 163.3),
        ("Gemini 3.6 Flash", "Google Gemini", 2, 32.5, 31.5, 209_789, 84.2),
        ("Gemini 3.7 Flash", "Google Gemini", 2, 21.5, 30.0, 129_373, 56.3),
        ("GPT-5.6 Terra", "OpenAI", 3, 15.3, 35.0, 70_638, 89.2),
        ("DeepSeek V4 Flash", "DeepSeek / OpenRouter", 2, 29.5, 63.0, 348_166, 418.3),
    ],
    columns=["model", "family", "suites", "turns", "calls", "tokens", "seconds"],
)

TASK_METRICS = pd.DataFrame(
    [
        ("Structured discovery", "Gemini 3.5 Flash", 26.4, 33_071, 11.0),
        ("Structured discovery", "Gemini 3.6 Flash", 20.9, 21_942, 8.5),
        ("Structured discovery", "Gemini 3.7 Flash", 13.5, 13_477, 11.0),
        ("Structured discovery", "GPT-5.6 Terra", 20.0, 7_318, 9.3),
        ("Structured discovery", "DeepSeek V4 Flash", 93.6, 22_905, 18.0),
        ("Single-table query", "Gemini 3.5 Flash", 9.8, 10_568, 2.0),
        ("Single-table query", "Gemini 3.6 Flash", 8.4, 9_898, 2.0),
        ("Single-table query", "Gemini 3.7 Flash", 7.9, 8_791, 2.0),
        ("Single-table query", "GPT-5.6 Terra", 8.3, 5_871, 2.0),
        ("Single-table query", "DeepSeek V4 Flash", 52.9, 40_014, 4.0),
        ("Join-heavy workload", "Gemini 3.5 Flash", 39.8, 107_223, 10.0),
        ("Join-heavy workload", "Gemini 3.6 Flash", 21.6, 63_368, 8.0),
        ("Join-heavy workload", "Gemini 3.7 Flash", 11.0, 26_058, 4.0),
        ("Join-heavy workload", "GPT-5.6 Terra", 16.8, 12_137, 4.3),
        ("Join-heavy workload", "DeepSeek V4 Flash", 86.9, 35_336, 8.0),
        ("Ambiguous classification", "Gemini 3.5 Flash", 87.3, 345_781, 27.0),
        ("Ambiguous classification", "Gemini 3.6 Flash", 33.4, 114_581, 13.0),
        ("Ambiguous classification", "Gemini 3.7 Flash", 23.9, 81_048, 13.0),
        ("Ambiguous classification", "GPT-5.6 Terra", 44.0, 45_312, 19.3),
        ("Ambiguous classification", "DeepSeek V4 Flash", 184.9, 249_912, 33.0),
    ],
    columns=["task", "model", "seconds", "tokens", "calls"],
)

MODELS = MODEL_SUMMARY["model"].tolist()
TASKS = TASK_METRICS["task"].drop_duplicates().tolist()
COLORS = {
    "Gemini 3.5 Flash": "#8e5ea2",
    "Gemini 3.6 Flash": "#4c78a8",
    "Gemini 3.7 Flash": "#1f9d8a",
    "GPT-5.6 Terra": "#f28e2b",
    "DeepSeek V4 Flash": "#e15759",
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
            row.model.replace(" Flash", ""),
            (row.seconds, row.tokens),
            xytext=(7, 5),
            textcoords="offset points",
            fontsize=9,
        )
    ax.set_yscale("log")
    ax.set_xlabel("Mean task time per suite (seconds)  ← faster")
    ax.set_ylabel("Mean total tokens per suite (log scale)  ← leaner")
    ax.set_title("Suite efficiency: latency vs. token use")
    ax.text(
        0.01,
        -0.22,
        "Bubble area = mean MCP calls. Lower-left is preferable. Gemini and DeepSeek: n=2 suites; Terra: n=3.",
        transform=ax.transAxes,
        fontsize=8.5,
        color="#546e7a",
    )
    ax.grid(True, which="both", alpha=0.35)
    save(fig, "gemini-shootout-suite-efficiency.png")


def task_metric_chart(metric: str, ylabel: str, title: str, filename: str) -> None:
    fig, ax = plt.subplots(figsize=(10, 5.4))
    x = np.arange(len(TASKS))
    width = 0.15
    for index, model in enumerate(MODELS):
        values = (
            TASK_METRICS[TASK_METRICS["model"] == model]
            .set_index("task")
            .loc[TASKS, metric]
        )
        bars = ax.bar(
            x + (index - 2) * width,
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
    ax.legend(ncol=3, loc="upper left", fontsize=8)
    ax.grid(axis="y", alpha=0.35)
    save(fig, filename)


def main() -> None:
    configure_style()
    suite_efficiency_chart()
    task_metric_chart(
        "seconds",
        "Mean task time (seconds)",
        "Task latency by model",
        "gemini-shootout-task-latency.png",
    )
    task_metric_chart(
        "calls",
        "Mean MCP calls per task",
        "Tool-call discipline by task",
        "gemini-shootout-task-tool-calls.png",
    )


if __name__ == "__main__":
    main()
