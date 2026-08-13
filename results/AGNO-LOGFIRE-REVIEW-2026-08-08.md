# Final Agent Run Comparison — 2026-08-08

## Scope

This report compares the **last successful completed run** from each MCP test harness:

- Agno
- Pydantic AI
- Strands

The comparison uses the same model and the same final task in all three logs:

> Perform a more complex osquery investigation to characterize the current system state. Determine baseline, look for outliers and changes in the last 5 minutes, and classify as benign or malicious with justification.

All three runs used `deepseek/deepseek-v4-flash-0731` through OpenRouter. “Successful” here means the harness reached a final answer and emitted its token/elapsed totals. This is a harness comparison, not a correctness benchmark: there is no independent evaluator or fixed osquery snapshot in this run set.

## Executive summary

The same model produced three broadly consistent conclusions—**the observed system state was benign, with a caveat that osquery event publishers were inactive**—but the harnesses elicited very different investigation strategies.

- **Pydantic AI was the most efficient completed run:** 130.1 seconds, 9 model requests, 29 MCP calls, and 143,278 total tokens.
- **Strands was similar in elapsed time but more SQL-heavy:** 145.1 seconds, 8 model calls, 27 MCP calls, and 194,640 total tokens.
- **Agno was the most expansive:** 314.3 seconds, 18 model calls, 64 MCP calls, and 576,664 total tokens. It produced the deepest investigation, but at roughly **4.0× Pydantic’s total tokens** and **2.4× its elapsed time**.

The qualitative difference is not simply “more calls equals better.” Agno found the broadest set of environmental details and performed the most iterative schema correction. Pydantic AI gave the most compact end-to-end explanation and explicitly traced recent files to the Pydantic test process. Strands concentrated on structured anomaly checks and multi-table SQL, with the lowest MCP-call count but a larger input context than Pydantic.

## Run selection and quantitative results

### Final completed runs

Times are shown in local log time (EDT), with UTC in parentheses.

| Harness | Completed run | Elapsed | Model calls / requests | MCP calls | Input tokens | Output tokens | Total tokens | Cache read | Reasoning |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|
| **Agno** | 11:37:42–11:42:56 (15:37:42–15:42:56 UTC) | **314.327s** | **18** | **64** | **558,109** | **18,555** | **576,664** | 356,864 | 10,528 |
| **Pydantic AI** | 11:19:58–11:22:08 (15:19:58–15:22:08 UTC) | **130.080s** | **9** | **29** | **136,290** | **6,988** | **143,278** | 109,056 | — |
| **Strands** | 11:27:32–11:29:57 (15:27:32–15:29:57 UTC) | **145.140s** | **8** | **27** | **188,712** | **5,928** | **194,640** | 106,752 | — |

The Agno log also reports an estimated run cost of **$0.03218**. Cost is not included in the cross-harness table because the Pydantic AI and Strands harness summaries did not emit comparable cost totals in their local run logs.

### MCP tool mix

| Harness | Tool distribution | Interpretation |
|---|---|---|
| **Agno** | `query_table` 41, `run_query` 12, `describe_table` 8, `search_tables` 2, `list_tables` 1 | Broad iterative investigation; many single-table checks plus repeated schema inspection and correction |
| **Pydantic AI** | `run_query` 13, `query_table` 12, `preview_table` 3, `list_tables` 1 | Balanced baseline and anomaly workflow; substantial use of joins and aggregations |
| **Strands** | `run_query` 17, `preview_table` 6, `search_tables` 4 | Most SQL-centric; used raw SQL for anomaly joins and filtering, with no `query_table` calls in the final run |

The MCP counts for Agno and Strands come from their emitted tool summaries. Pydantic AI does not emit a compact tool summary, so its 29 calls were reconstructed from the new tool-call sections in each model request rather than counting repeated conversation history.

### Relative efficiency

Relative to Pydantic AI’s final run:

| Harness | Elapsed | Model calls | MCP calls | Input tokens | Total tokens |
|---|---:|---:|---:|---:|---:|
| Pydantic AI | 1.00× | 1.00× | 1.00× | 1.00× | 1.00× |
| Strands | 1.12× | 0.89× | 0.93× | 1.39× | 1.36× |
| Agno | 2.42× | 2.00× | 2.21× | 4.10× | 4.02× |

Strands made fewer model and MCP calls than Pydantic AI but still consumed 36% more total tokens, indicating that context size—not just turn count—was a major cost driver. Agno’s run combined more turns with a very large accumulated context.

## Response comparison

### Shared conclusions

All three final answers agreed on the major environmental picture:

- Debian 12 running in a ChromeOS/Crostini-style container;
- a single primary interactive user and a normal AI-assisted development workload;
- no useful recent event history because osquery event publishers were inactive;
- recent `/tmp/*deepseek*.log` activity was attributable to the test harnesses;
- network activity was consistent with the expected development and model-provider traffic;
- no strong evidence of compromise in the observed state;
- a residual caveat remained because current-state queries cannot substitute for event history.

This agreement is meaningful, but it is not an independent correctness score: the agents saw overlapping live host state and shared system/tool instructions.

### Agno: deepest exploration, highest cost

Agno spent 18 model turns and 64 MCP calls. Its investigation repeatedly expanded scope and corrected schemas before converging. The final answer covered:

- process, user, service, scheduled-task, startup, SUID, SSH, Docker, mount, and network baselines;
- attribution of `/tmp/agno_deepseek*.log` to the running Agno test process;
- the `ki` user-local binary and its surrounding process/network context;
- the unowned `100.104.119.91:42183` listener on the Tailscale interface;
- the distinction between unavailable process start times and usable filesystem mtimes;
- a nuanced final report rather than an unconditional “benign.”

The tool summary shows all 64 recorded calls completed successfully. The tradeoff was substantial context growth: **558k input tokens** and more than five minutes of elapsed time. Agno’s extra depth produced useful investigative detail, but much of it was beyond the minimum evidence needed to answer the classification prompt.

### Pydantic AI: most compact complete investigation

Pydantic AI completed in 9 requests and 29 MCP calls. Its answer was organized around the three requested steps:

1. establish a system baseline;
2. identify recent file/process/network activity;
3. classify the state as benign with an explicit caveat.

It used `list_tables` and `preview_table` for initial orientation, then combined `query_table` for individual state tables with `run_query` for joins and aggregation. Its strongest distinctive behavior was tracing recently modified Pydantic log files back to the active `timeout → uv → python` test process and explaining why the activity was expected.

At **143,278 total tokens**, Pydantic AI delivered a complete, evidence-backed report with the lowest context and shortest elapsed time of the three final runs. Its answer was still detailed—6,988 output tokens—rather than merely terse.

### Strands: SQL-heavy anomaly workflow

Strands completed in 8 model calls and 27 MCP calls. It used 17 `run_query` calls and no `query_table` calls in this final run. The answer emphasized explicit anomaly tests:

- processes with unknown users;
- processes running from temporary or cache paths;
- orphaned shells and scripting interpreters;
- CPU outliers;
- recent file changes;
- active outbound connections;
- listener exposure and ownership;
- inactive osquery event publishers.

Its final classification was benign, with a clear explanation that the lack of event publishers prevented a true temporal diff. Relative to Pydantic AI, Strands made fewer calls but accumulated more input context—**188,712 input tokens**—and produced a slightly shorter final answer at **5,928 output tokens**.

## What differs when the model is held constant?

The evidence points primarily to harness orchestration and context management:

1. **Turn mechanics differ.** Pydantic AI’s final run explicitly used non-streaming OpenAI-compatible requests, while Strands streamed model interactions; Agno used its own multi-turn client path. The same model therefore received and returned context through different client protocols.
2. **The harnesses choose different tool granularity.** Pydantic mixed single-table helpers and SQL; Strands preferred SQL for nearly every complex check; Agno repeatedly used `query_table` and schema inspection.
3. **Context accumulation dominates the outlier.** Agno’s 18-turn run reached 558k input tokens. Its higher elapsed time follows both the additional model calls and the much larger prompts.
4. **Tool count does not directly measure answer quality.** Strands used the fewest MCP calls but produced a larger context than Pydantic. Agno used the most calls and found more details, but not all were necessary for the requested verdict.
5. **The final verdict is stable, while emphasis changes.** Pydantic emphasizes process attribution, Strands emphasizes explicit anomaly checks, and Agno emphasizes investigative breadth and residual uncertainty.

## SQL strategy comparison

The raw SQL makes the framework differences easier to see. The examples below are representative templates from the final-run logs; they are normalized for readability and do not reproduce every repeated copy embedded in later model-request histories.

### Agno: iterative investigation with helper-heavy coverage

Agno issued only 12 `run_query` calls but 41 `query_table` calls. Its SQL was used for aggregation, joins, and special-case follow-up checks after the helper tools had established the relevant tables.

Representative queries included:

```sql
SELECT COUNT(*) AS total,
       COUNT(DISTINCT path) AS unique_paths,
       COUNT(DISTINCT name) AS unique_names,
       COUNT(*) FILTER (WHERE pid = 1) AS pid1
FROM processes;
```

```sql
SELECT p.pid, p.name, p.cmdline AS cmd,
       s.local_address, s.local_port,
       s.remote_address, s.remote_port,
       s.family, s.protocol, s.state
FROM process_open_sockets s
LEFT JOIN processes p ON s.pid = p.pid
WHERE s.remote_address != ''
  AND s.remote_address NOT IN ('0.0.0.0', '::')
ORDER BY s.pid;
```

```sql
SELECT ug.uid, ug.gid, g.groupname
FROM user_groups ug
LEFT JOIN groups g ON ug.gid = g.gid
WHERE ug.uid = 1000;
```

The final investigation then narrowed onto the unresolved Tailscale listener with follow-up predicates such as `address LIKE '100.%'`, `port = 42183`, and `pid = 141` checks against `process_open_sockets`. This is a classic iterative agent pattern: broad inventory, identify an unexplained item, then spend several turns trying to attribute it. Agno also used `describe_table` eight times to correct or confirm schemas before subsequent queries.

### Pydantic AI: targeted evidence collection and process attribution

Pydantic AI used 13 `run_query` and 12 `query_table` calls. Its deduplicated SQL templates show a smaller set of recurring checks:

```sql
SELECT p.name, p.pid, p.uid,
       s.remote_address, s.remote_port,
       s.local_address, s.local_port,
       s.protocol, s.state
FROM process_open_sockets s
JOIN processes p ON s.pid = p.pid
WHERE s.remote_address != ''
  AND s.remote_address NOT IN ('127.0.0.1', '::1', '0.0.0.0', '::')
ORDER BY s.remote_address;
```

```sql
SELECT name, pid, path, cmdline, uid
FROM processes
WHERE on_disk = 1
  AND (path LIKE '/tmp/%'
    OR path LIKE '/dev/shm/%'
    OR path LIKE '/var/tmp/%'
    OR path LIKE '/home/%/.cache/%'
    OR path LIKE '/home/%/tmp%');
```

```sql
SELECT p.pid, p.name, p.cmdline, p.cwd
FROM processes p
WHERE p.cmdline LIKE '%deepseek%'
   OR p.cmdline LIKE '%pydlf%'
   OR p.cwd LIKE '%pydlf%';
```

It also queried recent files with a five-minute mtime threshold and checked TCP listeners using `listening_ports` joined to `processes`. The important difference is the final attribution query: Pydantic connected the recently modified Pydantic log files to the active test process instead of treating `/tmp` activity as suspicious by path alone.

### Strands: SQL-first anomaly checklist

Strands used 17 `run_query` calls, six `preview_table` calls, and no `query_table` calls in the final run. Its queries were more explicitly shaped as independent anomaly tests.

The event-window checks were written as aggregations rather than just total counts:

```sql
SELECT 'process_events' AS src,
       count(*) AS total,
       sum(CASE WHEN time >= 1786202573 THEN 1 ELSE 0 END) AS last5
FROM process_events;
```

It repeated the same pattern for `socket_events`, `file_events`, and `user_events`. It then tested several concrete hypotheses:

```sql
SELECT pid, name, path, uid, state
FROM processes
WHERE path LIKE '/tmp/%'
   OR path LIKE '/dev/shm/%'
   OR path LIKE '/var/tmp/%'
   OR path LIKE '/home/%/.cache/%'
   OR on_disk = 0
ORDER BY pid;
```

```sql
SELECT p.pid, p.name, p.path, p.uid, p.parent, p.state
FROM processes p
LEFT JOIN users u ON p.uid = u.uid
WHERE u.uid IS NULL
ORDER BY p.pid;
```

```sql
SELECT lp.port, lp.protocol, lp.family, lp.address, lp.pid,
       p.name AS process_name, p.path, u.username
FROM listening_ports lp
LEFT JOIN processes p ON lp.pid = p.pid
LEFT JOIN users u ON p.uid = u.uid
WHERE lp.family IN (2, 10)
  AND lp.port > 0
ORDER BY lp.protocol, lp.port;
```

This produces a clean checklist structure: event availability, process baseline, external sockets, recent files, suspicious paths, CPU outliers, orphaned users/processes, and network listeners. The tradeoff is that SQL schema assumptions matter more. The log includes an initial `osquery_events` query using `count` and `last_unix_time`, followed by schema inspection; this is evidence of a schema-correction turn that a validated helper could have avoided.

### What the SQL says about the frameworks

| Dimension | Agno | Pydantic AI | Strands |
|---|---|---|---|
| Primary query style | Helper-first, then targeted SQL | Mixed helper and SQL evidence gathering | SQL-first checklist |
| Joins | Socket/process and user/group attribution; later Tailscale follow-ups | Socket/process and listener/process attribution | Socket/process, process/user, and listener/process/user joins |
| Temporal checks | Filesystem mtime and repeated live-state follow-ups | Filesystem mtime plus process-command attribution | Explicit `CASE`-based event-table `last5` counts plus file mtime |
| Anomaly narrowing | Interactive and open-ended | Focused on explaining observed changes | Predetermined predicates for suspicious paths, orphan users, shells, and listeners |
| Main SQL risk | Query volume and repeated schema exploration | Repeated baseline templates increase context | More direct schema assumptions when using `run_query` |

The strongest framework-level contrast is therefore methodological: **Agno investigates until the unexplained detail is resolved, Pydantic AI collects enough targeted evidence to explain the observed change, and Strands runs a broad SQL checklist of security hypotheses.**

## Server-level Logfire context

For the broader approximately 14:00–15:40 UTC window, `osqueryi-mcp-server` reported:

- 2,063 tool-call observations in total;
- 1,966 successful calls and 97 errors, approximately 4.7% of observations;
- 1,435 successful SQL observations totaling about 549 seconds;
- 61 failed SQL observations totaling about 29.6 seconds;
- approximately 3.33 MB of response payloads.

These metrics include all clients and testing activity in the window, not only the three selected final runs. They provide server context but should not be used as harness-specific call totals.

The completed harness spans show that model time dominates the normal workload. The MCP server’s individual metadata calls are usually milliseconds and SQL operations are generally sub-second to low-second; the major differences above come from model turns and accumulated context.

## Limitations

- The runs queried a live host. PIDs, processes, file mtimes, listener ports, and network connections can change between harnesses.
- The model and prompt were aligned, but framework request serialization, streaming, retry behavior, and context handling were not identical.
- No independent ground-truth evaluator scored the factual claims or benign/malicious classification.
- The “last five minutes” request could not be fully validated because event publishers were inactive; agents inferred recent activity from current state and file timestamps.
- Logfire server metrics cover a wider window and multiple clients.

## Recommended next benchmark

For a stronger harness comparison, run the same prompt against a captured osquery fixture or stable snapshot and record, per run:

1. final-answer correctness against expected facts;
2. model calls, MCP calls, failed calls, and unnecessary calls;
3. input, output, cache-read, and total tokens;
4. model and tool wall time separately;
5. convergence/timeout status;
6. explicit coverage of required checks.

The current result supports a practical distinction: **Pydantic AI is the best efficiency baseline, Strands is a compact SQL-oriented alternative, and Agno is the broadest investigator but needs context or turn-budget controls.**
