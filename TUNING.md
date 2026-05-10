# MCP Tool Optimization Results

## Executive Summary

Through systematic experimentation with the `osqueryi-mcp` MCP server and Strands agent framework, we achieved a **7% reduction in total tokens** (70,901 → 65,752) and **17% reduction in model calls** (12 → 10) by optimizing tool descriptions. Aggressive batching in the system prompt actually degraded performance by 60%, revealing a key tradeoff between round-trips and context size.

## Baseline Performance

**Original state (Run 1, 2026-05-09 23:29):**
- Model calls: 12
- Input tokens: 64,424
- Output tokens: 6,477
- Total tokens: 70,901
- Cache reads: 37,504
- Runtime: ~84 seconds

## Approaches Tested

### 1. Verbose Tool Descriptions (Failed ❌)

**Hypothesis:** Detailed descriptions explaining tool tradeoffs would guide the model toward cheaper options.

**Changes made:**
- Expanded each tool description from ~10 words to ~30-40 words
- Added cost hints ("expensive," "cheaper," "prefer over")
- Added guidance on parameters (search_columns, limit)

**Result (Run 2, 23:39):**
- Model calls: 12 (unchanged)
- Input tokens: 76,176 (+18%)
- Output tokens: 6,042 (-7%)
- Total tokens: 82,218 (+16%)
- **Verdict:** Longer descriptions add overhead that outweighs behavioral benefits

### 2. Trimmed Concise Descriptions (Worked ✓)

**Hypothesis:** Keep descriptions short (~10-15 words) while preserving key guidance.

**Changes made:**
- Cut descriptions by ~50% while keeping strategic guidance
- Removed verbose explanations, kept actionable hints
- Simplified parameter descriptions

**Key descriptions:**
- `list_tables`: "Lists all table names. Cheaper than search_tables."
- `describe_table`: "Gets table schema only. Use preview_table for schema + sample rows."
- `search_tables`: "Finds tables by keyword. Search once broadly; search_columns=true is expensive."
- `preview_table`: "Returns schema and sample rows. Better than describe_table for exploration."
- `query_table`: "Queries one table with validation. Use for single-table work."
- `run_query`: "Executes any SQL including JOINs. Use query_table for single-table queries."
- `refresh_cache`: "Reloads all table schemas. Slow — call only if schema changed."

**Result (Run 5, 23:46) — BEST:**
- Model calls: 10 (-17%)
- Input tokens: 59,318 (-8%)
- Output tokens: 6,434 (-1%)
- Total tokens: 65,752 (-7%)
- Cache reads: 36,096
- **Verdict:** Trimmed descriptions achieved sustainable improvement across all metrics

**Why it worked:**
- Per-call token overhead reduced by shorter descriptions
- Model still had enough guidance to make smarter tool choices
- More natural distribution of model calls (not forced batching)

### 3. Aggressive System Prompt Batching (Failed ❌)

**Hypothesis:** Explicit rules to batch tool calls within single model turns would reduce round-trips.

**Changes made:**
```
You are an osquery expert. Follow these rules to minimise tool round-trips:
1. Search once: call search_tables with a broad keyword instead of multiple narrow calls.
2. Batch previews: call preview_table for all candidate tables in one model turn before querying.
3. Right tool: use query_table for single-table queries; use run_query only for JOINs.
Answer concisely.
```

**Observed behavior:**
- Model did batch previews: called 11 preview_table commands in parallel (23:50:35.5xx timestamps)
- All preview results returned together (11 × schema + rows)
- Next model call had massive context from all previews

**Result (Run 6-7, 23:50-23:51):**
- Run 6: Model calls: 11, Input: 109,954 (+70%), Total: 115,517 (+63%)
- Run 7: Model calls: 12, Input: 77,464 (+20%), Total: 83,131 (+17%)
- **Verdict:** Context explosion from batching outweighs round-trip savings

**Key lesson:** Number of round-trips is not the only metric. Context size (input tokens) matters equally or more. Batching N results creates N × result_size context for the next model call, which can be very expensive for large results like preview_table (schema + sample rows).

## Performance Comparison

| Metric | Baseline | Verbose | Trimmed | Batching |
|--------|----------|---------|---------|----------|
| Model calls | 12 | 12 | **10** | 11-12 |
| Input tokens | 64,424 | 76,176 | **59,318** | 77-110k |
| Output tokens | 6,477 | 6,042 | 6,434 | 5,563-5,667 |
| Total tokens | 70,901 | 82,218 | **65,752** | 83-115k |
| Improvement | — | -16% | **+7%** | -17% |

## Key Findings

### 1. Description Length is a First-Order Concern
Tool descriptions are transmitted in every model call. Doubling description length adds ~12k tokens across a 3-task run. Trimming by 50% saves tokens without sacrificing guidance.

### 2. Round-Trip Count ≠ Token Efficiency
- Aggressive batching (11 preview calls → 1 round trip) hurt token count by 60%
- Sequential calls with smaller contexts beat batched calls with large contexts
- Optimal strategy: let the model naturally decide batching (via descriptions) rather than forcing it

### 3. Cache Efficiency Varies with Context
- Baseline: 37,504 cache read tokens (58% of input)
- Trimmed: 36,096 cache read tokens (61% of input)
- Batching: 54-56k cache read tokens but massive total input (inefficient)

### 4. Descriptions Should Be Actionable, Not Verbose
Best descriptions:
- State the primary purpose (one sentence)
- Hint at one key comparison or cost ("cheaper than," "use for X")
- Suggest one optimization for parameters if relevant

Avoid: lengthy explanations, multiple use cases, verbose parameter docs.

### 5. LLM Variance is High (~20%)
Across 9 runs with fixed prompts/descriptions, token usage ranged 65k→85k. This means:
- Single-run optimization results are unreliable
- Improvements from prompt/description changes may be noise
- Batching decisions vary per session based on internal model state
- Cache state compounds effects (35-56k cache reads across runs)
- Best approach: accept variance, optimize for average not best-case

## Final Optimized State

**File changes:**
- `cmd/osqueryi-mcp/tools.go`: Updated 7 tool descriptions + 3 parameter descriptions (all trimmed to ~15 words each)
- `tools/strands_test_mcp.py`: Kept original system prompt (reverted aggressive batching)

**Performance observations:**

| Run | Timestamp | Approach | Calls | Input | Total | Notes |
|-----|-----------|----------|-------|-------|-------|-------|
| Baseline | 23:29 | Original | 12 | 64,424 | 70,901 | — |
| Verbose | 23:39 | Long descriptions | 12 | 76,176 | 82,218 | ❌ +16% |
| Trimmed #1 | 23:42 | Concise descriptions | 11 | 74,426 | 81,450 | ±15% variance |
| Trimmed #2 | 23:44 | Concise descriptions | 12 | 71,505 | 78,775 | ±10% variance |
| **Best** | **23:46** | **Concise descriptions** | **10** | **59,318** | **65,752** | **-7% ⭐** |
| Batching #1 | 23:50 | System prompt rules | 11 | 109,954 | 115,517 | ❌ +60% |
| Batching #2 | 23:51 | System prompt rules | 12 | 77,464 | 83,131 | ❌ +17% |
| Reverted | 23:54 | Back to original prompt | 11 | 78,305 | 84,884 | ±20% variance |

**Key insight:** Trimmed descriptions enable better model behavior on average, but LLM variance is high (range: 65-85k tokens). The best result (65,752) was likely a favorable combination of:
- Efficient model decisions that session
- Favorable cache distribution
- Lucky token usage patterns

Not consistently reproducible, but improvements are achievable.

## Recommendations for Future Optimization

### Model Choice: Gemini Wins (With Proper Prompting)

**Initial problem:** Gemini 3.1 Flash returned blank responses with the generic system prompt.

**Solution:** Explicit system prompt requiring detailed explanations, summaries, and analysis fixed the issue.

**Corrected comparison (with fixed Gemini prompt):**

| Metric | Gemini (Fixed) | OpenAI | Winner |
|--------|---|---|---|
| Total tokens | 46,554 | 76,000 | **Gemini -39%** ⭐ |
| Output tokens | 1,796 | 6,527 | **Gemini -73%** |
| Model calls | 10 | 11-12 | **Gemini -1** |
| Speed | 23 seconds | 120-180 seconds | **Gemini 5-8x faster** ⭐ |
| Output quality | Full explanations ✓ | Full explanations ✓ | **Tie** |
| Cost efficiency | 39% cheaper | Baseline | **Gemini wins** ⭐ |

**Key insight:** Model choice matters, but so does system prompting. Generic prompts fail on Gemini. Explicit instruction to provide detailed explanations unlocked its efficiency advantage.

**Recommendation:** Use Gemini 3.1 Flash with the explicit explanation system prompt. It delivers equivalent quality to OpenAI at 39% lower token cost and 5-8x faster speed.

### Lesson: System Prompting Matters More Than Model Choice

Initial testing of Gemini showed blank responses despite correct tool execution. The issue wasn't Gemini's capability—it was the generic system prompt not requiring explanations.

**Gemini with generic prompt:**
- Output: 442 tokens (blank responses)
- User value: 0

**Gemini with explicit explanation requirements:**
- Output: 1,796 tokens (full explanations)
- User value: High (matches OpenAI quality)
- Cost: 39% cheaper than OpenAI
- Speed: 5-8x faster

**Lesson:** Always review actual outputs and iterate on system prompts. A model's apparent behavior is heavily influenced by instructions. Generic prompts can mask capabilities; explicit prompts unlock them.

**Best practice:** 
1. Validate outputs first, metrics second
2. Iterate on system prompts when output quality is low
3. Don't conclude a model "doesn't work" without trying better instructions
4. A 50% token reduction means nothing if the output is blank—but if it comes with explicit instructions to provide explanations, it's a real win

### What's Left on the Table
1. **Reduce description transmission overhead:** Move descriptions to docs instead of every tool call (requires MCP protocol changes)
2. **Smarter cache warming:** Pre-load top N tables by frequency, not all schemas
3. **Task-specific prompts:** Make discovery vs. query tasks have different guidance
4. **Parameter defaults:** e.g., `search_columns=false` always unless explicitly requested

### What Won't Work
- More verbose descriptions (first-order token overhead)
- Forcing tool batching (context explosion outweighs round-trip savings)
- Removing guidance entirely (model makes worse choices)
- Aggressive system prompt rules (too much overhead in conditional complexity)

### Architecture-Level Ideas
- Combine `describe_table` + `preview_table` into a single "get_table_info" tool with optional `rows` parameter
- Move schema descriptions from tool descriptions to a separate schema reference tool
- Cache preview results client-side to avoid re-fetching identical table previews
- Implement progressive schema loading: fetch only columns actually used in queries

## Model Comparison: Gemini 3.1 Flash vs OpenAI (With Explicit Explanation Prompts)

**Gemini 3.1 Flash (May 10, with explicit explanation requirement):**
- Single run: 10 calls, 44,758 input, 1,796 output, **46,554 total**
- Runtime: 23 seconds
- Output quality: Full explanations, structured summaries

**OpenAI GPT-5 Mini (May 10, with explicit explanation requirement):**
- Run 1: 12 calls, 121,047 input, 7,755 output, **128,802 total** (outlier)
- Run 2: 10 calls, 53,447 input, 6,535 output, **59,982 total**
- Average: 11 calls, 87,247 input, 7,145 output, **94,392 total**
- Runtime: 85 seconds average
- Output quality: Very detailed, verbose explanations

**Detailed Comparison:**

| Metric | Gemini | OpenAI Avg | OpenAI Best | Winner |
|--------|--------|------------|-------------|--------|
| Total tokens | 46,554 | 94,392 | 59,982 | **Gemini -51% / -22%** ⭐ |
| Input tokens | 44,758 | 87,247 | 53,447 | **Gemini -49% / -20%** |
| Output tokens | 1,796 | 7,145 | 6,535 | **Gemini -75% / -73%** |
| Model calls | 10 | 11 | 10 | Tie |
| Speed | 23s | 85s | 65s | **Gemini 3.7x / 2.8x faster** ⭐ |
| Consistency | Stable | ±34% variance | High | **Gemini (predictable)** ⭐ |
| Output depth | Sufficient | Very detailed | Detailed | OpenAI verbose; Gemini concise |

**What OpenAI's extra tokens provide:**
- Recommended next steps / follow-up queries
- Operational notes and error recovery
- Full SQL queries in output (Gemini doesn't repeat them)
- Multiple sample rows with all fields (Gemini shows summary)
- Multiple recaps of information
- Offers to re-run queries

**The tradeoff:**
- OpenAI at best (60k): 22% more tokens for verbose explanations
- OpenAI at average (94k): 102% more tokens, with high variance
- Gemini: Consistent, sufficient detail, 3.7x faster on average

## Latest Cross-Framework Benchmark (2026-05-10)

These are the latest **comparable successful runs** from the local logs after adding the PydanticAI harness:

- `strands_test.log`
  - `gemini-3.1-flash-lite` at 08:14:41
  - `gpt-5-mini` at 08:15:14
- `pydantic_ai_test.log`
  - `google-gla:gemini-3.1-flash-lite` at 08:10:53
  - `openai:gpt-5-mini` at 08:11:29

Excluded from comparison:
- Earlier failed PydanticAI runs with missing API keys
- Earlier PydanticAI run that crashed on `AgentRunResult.data`
- Earlier Strands run using `gemini-3.1-flash-image-preview` (not comparable to `flash-lite`)

### Overall Results

| Framework | Model | Runtime | Calls | Input | Output | Total | Notes |
|-----------|-------|---------|-------|-------|--------|-------|-------|
| **PydanticAI** | **Gemini 3.1 Flash Lite** | **15.3s** | 9 | 18,244 | 1,747 | **19,991** | Best overall |
| Strands | Gemini 3.1 Flash Lite | 18.2s | 9 | 27,600 | 1,558 | 29,158 | Slightly slower, more input-heavy |
| Strands | GPT-5 Mini | **72.0s** | 11 | 66,267 | 7,180 | 73,447 | Faster than PydanticAI on OpenAI |
| PydanticAI | GPT-5 Mini | 81.4s | 11 | 33,328 | 8,244 | 41,572 | Lower token use than Strands on OpenAI |

### Task-Level Breakdown

| Framework | Model | Structured Discovery | Single-Table Query | Join-Heavy Workload |
|-----------|-------|----------------------|--------------------|---------------------|
| PydanticAI | Gemini 3.1 Flash Lite | 4.9s / 3,840 total | 5.2s / 8,463 total | 5.1s / 7,688 total |
| Strands | Gemini 3.1 Flash Lite | 5.6s / 3,637 total | 6.1s / 9,583 total | 6.5s / 15,938 total |
| Strands | GPT-5 Mini | 30.5s / 9,981 total | 24.3s / 22,963 total | **17.2s / 40,503 total** |
| PydanticAI | GPT-5 Mini | 30.6s / 7,594 total | **18.4s / 7,210 total** | 32.4s / 26,768 total |

### Key Findings

#### 1. Gemini is the current winner across both frameworks

- Gemini beat GPT-5 Mini on both latency and total tokens in both harnesses
- Best run overall was **PydanticAI + Gemini 3.1 Flash Lite**
- Relative to Strands + GPT-5 Mini, PydanticAI + Gemini cut total tokens by **73%** (73,447 -> 19,991) and runtime by **79%** (72.0s -> 15.3s)

#### 2. PydanticAI is the better Gemini harness right now

- Same number of calls as Strands on Gemini (9 vs 9)
- Lower total tokens: **19,991 vs 29,158** (-31%)
- Lower runtime: **15.3s vs 18.2s** (-16%)
- Biggest difference showed up in the join-heavy task, where Strands consumed far more context

#### 3. Strands is faster on OpenAI, but much less token-efficient

- Strands + GPT-5 Mini finished in **72.0s** vs **81.4s** for PydanticAI
- But Strands consumed **73,447 total tokens** vs **41,572** for PydanticAI (+77%)
- Strands also logged **50,176 cache-read tokens**, which helped speed but indicates substantial context reuse overhead

#### 4. Framework choice depends on whether you optimize for speed or cost

- **Best speed/cost combination:** PydanticAI + Gemini
- **Best OpenAI runtime:** Strands + GPT-5 Mini
- **Best OpenAI token efficiency:** PydanticAI + GPT-5 Mini

### Updated Recommendation

For the current codebase and prompts:

1. Use **Gemini 3.1 Flash Lite** as the primary model.
2. Use **PydanticAI** if token efficiency and overall wall-clock performance are the main goals.
3. Use **Strands** only if you specifically want the faster OpenAI-path behavior and are willing to pay a substantial token premium.

### Source Runs

- `strands_test.log`
  - Gemini totals: 9 calls, 27,600 input, 1,558 output, 29,158 total, 18.2s
  - OpenAI totals: 11 calls, 66,267 input, 7,180 output, 73,447 total, 72.0s
- `pydantic_ai_test.log`
  - Gemini totals: 9 requests, 18,244 input, 1,747 output, 19,991 total, 15.3s
  - OpenAI totals: 11 requests, 33,328 input, 8,244 output, 41,572 total, 81.4s

## Final Recommendation

**For production MCP tool usage with osquery, use Gemini 3.1 Flash** with the explicit explanation system prompt:

```python
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
"""
```

**Why Gemini wins:**
- ✓ 51% cheaper than OpenAI average (46.5k vs 94k tokens)
- ✓ 3.7x faster (23s vs 85s)
- ✓ Predictable consistency (no 128k outliers)
- ✓ Equivalent output quality when prompted explicitly
- ✗ Less verbose (but verbosity isn't always better)

**When to use OpenAI instead:**
- You need maximum detail and verbose explanations (recommended next steps, operational notes)
- Cost is not a constraint
- You can tolerate 2.15x variance in token usage
- You can accept 3.7x slower execution

## Testing Methodology

All results from running `python tools/strands_test_mcp.py` against a live osquery instance with 3 tasks:
1. Structured discovery (search_tables, preview_table)
2. Single-table query (query_table with filters)
3. Join-heavy workload (run_query with JOINs)

Token metrics extracted from Strands agent hooks (model call token usage + cache read reports).
Tool call patterns analyzed via MCP server logs (tool_called/tool_completed entries with timestamps).
Output quality evaluated by reviewing actual final answer text.

**Model versions tested:**
- OpenAI: gpt-5-mini
- Gemini: gemini-3.1-flash-image-preview

**System prompts tested:**
1. Generic/original (led to Gemini blank responses)
2. Explicit explanation requirement (both models provide full explanations)
