# Plan: better error messages + persistent osqueryd session

## Context

Logfire telemetry from ~600 real LLM tool calls (PydanticAI/Agno/Strands, several models including a weaker one) surfaced two concrete, evidence-backed problems in `cmd/osqueryi-mcp`:

1. **Unhelpful errors cause repeated failed attempts.** ~11% of `run_query` and ~10% of `query_table` calls fail, and 35+/37 sampled failures are "unknown/no such column" errors — the model guessing plausible column names that don't exist on the table. The error text returned to the LLM (`"unknown column: X"` or raw SQLite `"no such column: X"`) gives no hint at the real columns, even though that data is already in memory at the point the error is built. This forces a blind guess-and-check loop instead of a one-shot self-correction.

2. **Every query pays full subprocess-spawn cost.** `runSQL` shells out to a fresh `osqueryi` process per call — measured p50 352ms, p95 889ms, p99 1.65s, max 5.27s across 466 real invocations, vs. microseconds for the pure-schema-cache tools (`describe_table`/`search_tables`/`list_tables`). No concurrency control either: an observed outlier showed 3 near-simultaneous `run_query` calls producing one 5.27s success and two ~1.4s failures (resource contention).

I empirically validated a fix for #2 using `github.com/osquery/osquery-go`'s Thrift client against a real, persistent `osqueryd --extensions_socket=...`: 20 repeated queries over one connection averaged **9.07ms/query** (~35-40x faster than today's p50), returning correct data from real system tables, with an error shape (`resp.Status.Message`) that is byte-identical to today's raw SQLite stderr text — so fix #1 and fix #2 are independent and compose cleanly regardless of ship order, with one coordination point noted below.

Decisions made with the user (all recommended options): hard-require the daemon at startup with an explicit `OSQUERYI_TRANSPORT=subprocess` opt-out escape hatch (no silent fallback); a single shared `osquery-go` client relying on its own internal serialization (no connection pool); simple bare-identifier regex matching for error enrichment (no qualified `table.column` parsing); auto-generated PID-scoped socket path.

**Ship order: Fix 1 first** (small, contained, no new dependency), **Fix 2 second** (bigger, needs the new `osquery-go` dependency). They can ship independently, but only if Fix 2 preserves the raw error-text shape Fix 1's regexes match against (see coordination note in Fix 2).

---

## Fix 1: Error message enrichment

All changes confined to `cmd/osqueryi-mcp/executor.go` (+ tests in `executor_test.go`). No signature changes to any exported method — `tools.go`/`telemetry.go` untouched, since both only ever consume `err.Error()`.

**Structured-validation errors** (schema data already in scope, just unused today):
- `ensureKnownTable` (~L315, `"unknown table: %s"`) — has `tables []string` in scope from the preceding `listTables` call.
- `validateSelectedColumns` (~L394, `"unknown column: %s"`) and `validateOrderBy` (~L418, `"unknown order_by column: %s"`) — both have the `valid map[string]struct{}` parameter in scope.

Add small local helpers (no new dependency, reuse existing `MaxPayloadSize`/`MaxRows`-style constant idiom at ~L464):
- `levenshtein(a, b string) int` — tiny ~15-line DP function for distance-bounded "did you mean" suggestions.
- `suggestColumns(name string, valid map[string]struct{}) string` / `suggestTables(name string, tables []string) string` — try a distance-≤2 fuzzy narrowing first (targets the actual observed failure mode: `process_name` guessed instead of real `name`); fall back to the first `MaxSuggestions` (new const, e.g. 15) alphabetically if the narrowed set is empty, always appending `"... and N more"` when truncated (matches `truncateJSON`'s existing truncation-with-message convention at ~L475).
- Wire these into the three error sites above, replacing the bare `fmt.Errorf(...)` calls.

**`run_query`'s raw-SQL path** (the highest-value target — this is where the majority of sampled failures live): `runSQL`'s error, when it reaches `RunQuery` (~L532), is SQLite's raw stderr text verbatim with no schema context. Add a post-processing wrapper applied at `RunQuery` (and, cheaply/defensively, `PreviewTable`/`describeTableColumns` too, since the regex just won't match there and degrades to a no-op):

```go
var (
    noSuchColumnRe = regexp.MustCompile(`(?i)no such column:\s*([A-Za-z_][A-Za-z0-9_.]*)`)
    noSuchTableRe  = regexp.MustCompile(`(?i)no such table:\s*([A-Za-z_][A-Za-z0-9_]*)`)
)

func (e *Executor) enrichSQLError(ctx context.Context, err error) error {
    if err == nil { return nil }
    msg := err.Error()
    if m := noSuchTableRe.FindStringSubmatch(msg); m != nil {
        if tables, tErr := e.listTables(ctx); tErr == nil {
            return fmt.Errorf("%s (%s)", msg, suggestTables(m[1], tables))
        }
        return err
    }
    if m := noSuchColumnRe.FindStringSubmatch(msg); m != nil {
        if hint := e.crossTableColumnHint(m[1]); hint != "" {
            return fmt.Errorf("%s (%s)", msg, hint)
        }
        return err
    }
    return err // no match -> byte-identical to today's message, never worse
}
```

`crossTableColumnHint` searches already-cached `e.schemas` (under `e.mu.RLock()`) for tables containing a column named `m[1]`, per the "simple: bare identifier only" decision — no `table.column` parsing. **Hard rule**: `enrichSQLError` only ever touches already-cached in-memory state (or a `listTables` cache-hit) — never calls `runSQL` itself, and any failure inside enrichment returns the original unmodified error, never a worse message or a panic.

**Tests to add in `executor_test.go`**: extend `writeFakeOSQuery`'s fake-binary cases with new stderr-producing patterns for "no such column"/"no such table"; assert enriched messages contain the real column/table names; assert the pre-warm (empty schema cache) case degrades gracefully to today's exact message; assert an unrecognized stderr pattern is byte-identical to today's passthrough (regression guard); a pure-function test for `suggestColumns`/`suggestTables` truncation behavior with a 30+ column fixture, independent of any subprocess.

---

## Fix 2: Persistent osqueryd session via osquery-go

### Ownership & lifecycle
The Go server spawns and owns `osqueryd` itself (same posture as today's required-`osqueryi`-on-PATH — `osqueryd` ships in the same osquery install, confirmed present at `/usr/bin/osqueryd` v5.22.1). Launched in `main()` right after `acquireLock` succeeds, before `registerTools`/cache-warm starts.

Flags: `osqueryd --extensions_socket=<socketPath> --config_path=/dev/null --disable_watchdog --ephemeral --pidfile=<tmp> --force`.

**Readiness is already handled by the library** — confirmed by reading `osquery-go`'s `transport.Open`: `NewClient(socketPath, timeout)` internally polls every 200ms until the socket is dialable, bounded by the passed timeout. No custom retry loop needed.

**Crash recovery**: on a transport-level error (not a normal `Status.Code != 0` SQL failure) in `runSQLViaDaemon`, attempt exactly one reconnect (`Close()` + `NewClient(...)` under `daemonMu.Lock()`, swap `e.daemonClient`, retry once); if that also fails, return the error as-is. Do not spawn a fresh daemon from inside a query call.

**Shutdown**: `defer executor.StopDaemon()` in `main()`, ordered (via Go's LIFO defer semantics) to run before `defer release()` — `StopDaemon()` closes the client, sends `SIGTERM` to the daemon process, waits briefly, falls back to `Kill()`.

**Fallback**: per decision, hard-require — `StartDaemon` failure logs an error and `os.Exit(1)`, *unless* `OSQUERYI_TRANSPORT=subprocess` is explicitly set (skips `StartDaemon` entirely, keeps today's exec-per-call path verbatim).

### Config / Executor changes
New `Config` fields (main.go, same `if val := os.Getenv("OSQUERYI_X"); val != "" {...}` idiom as every existing field):

| Var | Default | Purpose |
|---|---|---|
| `OSQUERYI_TRANSPORT` | `daemon` | `daemon` or `subprocess` |
| `OSQUERYI_DAEMON_PATH` | `osqueryd` | resolved via `exec.LookPath` |
| `OSQUERYI_SOCKET` | `""` → auto-generate `os.TempDir()/osqueryi-mcp-<pid>.sock` | override |
| `OSQUERYI_DAEMON_TIMEOUT` | `10s` | bound for socket-ready wait |

`Executor` struct gains `daemonPath`, `socketPath string`, `daemonCmd *exec.Cmd`, `daemonClient daemonQuerier` (new narrow interface, see Tests below), `daemonMu sync.RWMutex`. **`NewExecutor`'s existing 3-arg signature stays unchanged** (zero test-file churn for existing coverage) — add a new opt-in method `StartDaemon(ctx, daemonPath, socketPath string, timeout time.Duration) error` that `main()` calls right after construction, mutating the executor to switch its query transport.

### `runSQL` restructuring
Keep `runSQL(ctx, sql) ([]byte, error)`'s exact signature — none of its 6 callers (`listTables`, `fetchAllSchemas`, `describeTableColumns`, `RunQuery`, `PreviewTable`, `QueryTable`) change.

Confirmed JSON-shape compatible: `osqueryi --json` output and `osquery-go`'s `ExtensionPluginResponse` (`[]map[string]string`) are structurally identical (verified live). Use `QueryContext` + manual status check, **not** `QueryRowsContext`, specifically to preserve the exact `resp.Status.Message` text (byte-identical to today's stderr) that Fix 1's regexes match:

```go
resp, err := e.daemonClient.QueryContext(ctx, sql)
if err != nil { return nil, err }
if resp.Status != nil && resp.Status.Code != 0 {
    return nil, fmt.Errorf("%s", resp.Status.Message)
}
return json.Marshal(resp.Response)
```

Rename today's body to `runSQLViaSubprocess` (unchanged logic, kept for the fallback path); new `runSQLViaDaemon` as above. `runSQL` becomes a 3-line dispatcher on whether `e.daemonClient != nil`. Both variants keep the existing `otel.Tracer(...).Start(ctx, "osqueryi.execute")` span and `recordOSQueryMetrics` call for telemetry continuity.

**Timeout coordination (important)**: confirmed the client library's internal locker defaults to a 200ms wait if the call context has no deadline. Keep wrapping every call with `context.WithTimeout(ctx, e.timeout)` exactly as `runSQL` does today, and additionally pass `osquery.DefaultWaitTime(e.timeout)` as a `NewClient` option — otherwise callers under load could hit a confusing internal 200ms timeout unrelated to `OSQUERYI_TIMEOUT`.

### Concurrency
Per decision: single shared `*osquery.ExtensionManagerClient` on `Executor.daemonClient`, no pool. Confirmed by reading `client.go`/`locker.go`: the client already internally serializes concurrent calls via its own `chan struct{}`-based locker (explicit comment: the underlying Thrift socket only allows one actor at a time). At ~9ms/query, even 8 queued concurrent calls (matching `SearchTables`' existing semaphore width) total ~72ms worst case — still ~5x faster than one subprocess call today.

### Dependency
Add `github.com/osquery/osquery-go v0.0.0-20260630173615-eb39ad3443df` to `go.mod` (pseudo-version, no tagged releases — already resolved in the local module cache, `go mod tidy` should be clean). Its transitive `go.opentelemetry.io/otel v1.16.0` requirement resolves fine under MVS against this project's direct `v1.44.0` — run `go mod tidy` and `go build`/`go test` to confirm no API friction. Update `PKG.md`'s dependency table per its existing convention (new direct dep `osquery-go` + transitive `github.com/pkg/errors`), and note near the `go.mod` require line that this pseudo-version has no semver contract and should be periodically re-reviewed (`go get github.com/osquery/osquery-go@latest`), not auto-upgraded.

### Tests
- Existing `executor_test.go` tests **stay unchanged and passing** — `NewExecutor` alone never touches `daemonClient`, so the subprocess path (now `runSQLViaSubprocess`) is exercised exactly as today via the existing fake-binary fixture.
- Introduce a narrow interface for the daemon client only (subprocess path keeps its existing real-fake-binary test approach, no redesign needed):
  ```go
  type daemonQuerier interface {
      QueryContext(ctx context.Context, sql string) (*osquery.ExtensionResponse, error)
      Close()
  }
  ```
  `*osquery.ExtensionManagerClient` satisfies it structurally. Unit tests inject a hand-written fake returning canned responses/status codes to test `runSQLViaDaemon`'s error passthrough and the single-reconnect-then-give-up logic, without a real daemon or Thrift wire server.
- Separate `executor_daemon_test.go`, gated behind an env check (`if os.Getenv("OSQUERYI_TEST_DAEMON") == "" { t.Skip(...) }`) so default `go test ./...` stays fast/CI-independent: spins up a real `osqueryd`, calls `StartDaemon`, runs a real query, asserts JSON round-trips and `StopDaemon` leaves no orphaned process.
- Extend `main_test.go`'s `TestLoadConfigEnvironmentOverrides` with the 4 new env vars.

---

## Verification

1. `go build ./...` and `go vet ./...` after each fix — both should be clean, no new lint issues.
2. `go test ./cmd/osqueryi-mcp/...` — all existing tests plus new ones from both fixes must pass; confirm existing subprocess-path tests are untouched by Fix 2's changes (this is the key regression check for the "zero test-file churn" design goal).
3. Manual end-to-end for Fix 1: run `tools/pydantic_ai_test_mcp.py` (or any of the three harnesses) against a model prone to column mistakes, and confirm via the local log / Logfire that a `run_query`/`query_table` failure now includes a valid-column hint, and that the model self-corrects in fewer follow-up calls than before.
4. Manual end-to-end for Fix 2: `make install`, run one of the harnesses with `OTEL_EXPORTER_OTLP_ENDPOINT`/`OTEL_EXPORTER_OTLP_HEADERS` set (as done throughout this session), then query Logfire's `osqueryi_mcp_osquery_duration` metric/`osqueryi.execute` span durations and confirm p50 drops from the current ~350ms baseline to roughly the ~9-15ms range observed in the standalone benchmark. Also confirm no orphaned `osqueryd` process remains after the harness exits (`ps aux | grep osqueryd`).
5. Confirm `OSQUERYI_TRANSPORT=subprocess` still works end-to-end as an explicit fallback (today's exact behavior, unchanged).

## Critical files
- `cmd/osqueryi-mcp/executor.go` — both fixes' core logic
- `cmd/osqueryi-mcp/executor_test.go` — new test coverage for both fixes
- `cmd/osqueryi-mcp/main.go` — `Config` fields, `StartDaemon`/`StopDaemon` wiring, defer ordering
- `cmd/osqueryi-mcp/main_test.go` — new env var coverage
- `go.mod` / `go.sum` — new `osquery-go` dependency
- `PKG.md` — dependency documentation update

---

*Background: this plan was informed by empirical validation — a throwaway benchmark client (in scratch space, not committed) confirmed `osquery-go`'s `ExtensionManagerClient.QueryContext` against a real `osqueryd --extensions_socket=...` daemon averages ~9ms/query vs. today's ~352ms p50 subprocess-spawn approach, and that its error shape (`resp.Status.Message`) is byte-identical to today's raw SQLite stderr text.*
