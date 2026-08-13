package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Executor struct {
	binaryPath        string
	timeout           time.Duration
	cacheFile         string
	mu                sync.RWMutex
	tables            []string
	schemas           map[string][]TableColumn
	allSchemasFetched bool
	warmMu            sync.Mutex
}

type PersistedCache struct {
	Tables            []string                 `json:"tables"`
	Schemas           map[string][]TableColumn `json:"schemas"`
	AllSchemasFetched bool                     `json:"all_schemas_fetched"`
}

func NewExecutor(binaryPath string, timeout time.Duration, cacheFile string) *Executor {
	e := &Executor{
		binaryPath: binaryPath,
		timeout:    timeout,
		cacheFile:  cacheFile,
		schemas:    make(map[string][]TableColumn),
	}

	if cacheFile != "" && cacheFile != "off" {
		if err := e.loadCache(context.Background()); err != nil {
			if os.IsNotExist(err) {
				slog.Info("cache file not found, will create on exit or after warming", "file", cacheFile)
			} else {
				slog.Warn("failed to load cache", "file", cacheFile, "error", err)
			}
		} else {
			slog.Info("loaded cache from disk", "file", cacheFile, "tables", len(e.tables))
		}
	}

	return e
}

func (e *Executor) loadCache(ctx context.Context) error {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "osqueryi.cache.load")
	defer span.End()

	data, err := os.ReadFile(e.cacheFile)
	if err != nil {
		span.SetStatus(codes.Error, errorType(ctx, err))
		recordCacheOperation(ctx, "load", "error")
		return err
	}

	var pc PersistedCache
	if err := json.Unmarshal(data, &pc); err != nil {
		span.SetStatus(codes.Error, "decode_failed")
		recordCacheOperation(ctx, "load", "error")
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.tables = pc.Tables
	e.schemas = pc.Schemas
	e.allSchemasFetched = pc.AllSchemasFetched
	span.SetAttributes(attribute.Int("cache.tables", len(pc.Tables)))
	recordCacheOperation(ctx, "load", "ok")
	return nil
}

func (e *Executor) saveCache(ctx context.Context) error {
	if e.cacheFile == "" || e.cacheFile == "off" {
		return nil
	}
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "osqueryi.cache.save")
	defer span.End()

	slog.InfoContext(ctx, "saving cache to disk", "file", e.cacheFile)

	e.mu.RLock()
	pc := PersistedCache{
		Tables:            e.tables,
		Schemas:           e.schemas,
		AllSchemasFetched: e.allSchemasFetched,
	}
	e.mu.RUnlock()

	data, err := json.MarshalIndent(pc, "", "  ")
	if err != nil {
		span.SetStatus(codes.Error, "encode_failed")
		recordCacheOperation(ctx, "save", "error")
		slog.ErrorContext(ctx, "failed to marshal cache", "error", err, "error_type", "encode_failed")
		return err
	}

	err = os.WriteFile(e.cacheFile, data, 0644)
	if err != nil {
		span.SetStatus(codes.Error, errorType(ctx, err))
		recordCacheOperation(ctx, "save", "error")
		slog.ErrorContext(ctx, "failed to write cache file", "file", e.cacheFile, "error", err, "error_type", errorType(ctx, err))
	} else {
		span.SetAttributes(attribute.Int("cache.tables", len(pc.Tables)), attribute.Int("osquery.output.bytes", len(data)))
		recordCacheOperation(ctx, "save", "ok")
		slog.InfoContext(ctx, "saved cache to disk", "file", e.cacheFile, "tables", len(pc.Tables), "outcome", "ok")
	}
	return err
}

type TableColumn struct {
	CID       string `json:"cid"`
	DfltValue string `json:"dflt_value"`
	Name      string `json:"name"`
	NotNull   string `json:"notnull"`
	PK        string `json:"pk"`
	Type      string `json:"type"`
}

type SearchMatch struct {
	TableName       string   `json:"table_name"`
	MatchReasons    []string `json:"match_reasons"`
	MatchingColumns []string `json:"matching_columns,omitempty"`
}

func (e *Executor) runSQL(ctx context.Context, sql string) ([]byte, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "osqueryi.execute")
	defer span.End()
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	slog.DebugContext(ctx, "executing_sql", "sql", sql)
	cmd := exec.CommandContext(ctx, e.binaryPath, "--json", "--config_path=/dev/null", sql)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	if err != nil {
		outcome := errorType(ctx, err)
		span.SetStatus(codes.Error, outcome)
		recordOSQueryMetrics(ctx, duration, outcome)
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			slog.DebugContext(ctx, "exec_failed", "sql", sql, "error", err, "error_type", outcome, "duration_ms", duration.Milliseconds())
			return nil, err
		}
		slog.DebugContext(ctx, "exec_failed", "sql", sql, "error", errMsg, "error_type", outcome, "duration_ms", duration.Milliseconds())
		return nil, fmt.Errorf("%s", errMsg)
	}

	span.SetAttributes(attribute.Int("osquery.output.bytes", stdout.Len()), attribute.String("outcome", "ok"))
	recordOSQueryMetrics(ctx, duration, "ok")
	slog.DebugContext(ctx, "exec_completed", "sql", sql, "duration_ms", duration.Milliseconds(), "bytes", stdout.Len(), "outcome", "ok")
	return stdout.Bytes(), nil
}

func (e *Executor) listTables(ctx context.Context) ([]string, error) {
	e.mu.RLock()
	if len(e.tables) > 0 {
		tables := append([]string(nil), e.tables...)
		e.mu.RUnlock()
		trace.SpanFromContext(ctx).SetAttributes(attribute.Bool("cache.hit", true))
		return tables, nil
	}
	e.mu.RUnlock()

	trace.SpanFromContext(ctx).SetAttributes(attribute.Bool("cache.hit", false))
	slog.DebugContext(ctx, "listing_tables")
	data, err := e.runSQL(ctx, "SELECT name FROM osquery_registry WHERE registry = 'table' ORDER BY name;")
	if err != nil {
		return nil, err
	}

	var results []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, fmt.Errorf("failed to parse table list: %w", err)
	}

	var tables []string
	for _, r := range results {
		tables = append(tables, r.Name)
	}

	e.mu.Lock()
	if len(e.tables) == 0 {
		e.tables = append([]string(nil), tables...)
		e.mu.Unlock()
		_ = e.saveCache(ctx)
	} else {
		e.mu.Unlock()
	}

	return tables, nil
}

func (e *Executor) fetchAllSchemas(ctx context.Context) error {
	slog.DebugContext(ctx, "fetching_all_schemas")
	query := "SELECT m.name as table_name, p.* FROM (SELECT name FROM osquery_registry WHERE registry = 'table') m, pragma_table_info(m.name) p;"
	data, err := e.runSQL(ctx, query)
	if err != nil {
		return err
	}

	type fullSchemaEntry struct {
		TableColumn
		TableName string `json:"table_name"`
	}

	var entries []fullSchemaEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("failed to parse all schemas: %w", err)
	}

	newSchemas := make(map[string][]TableColumn)
	for _, entry := range entries {
		newSchemas[entry.TableName] = append(newSchemas[entry.TableName], entry.TableColumn)
	}

	e.mu.Lock()
	for table, cols := range newSchemas {
		e.schemas[table] = cols
	}
	// Also update tables list if empty
	if len(e.tables) == 0 {
		tables := make([]string, 0, len(newSchemas))
		for table := range newSchemas {
			tables = append(tables, table)
		}
		sort.Strings(tables)
		e.tables = tables
	}
	e.allSchemasFetched = true
	e.mu.Unlock()

	return e.saveCache(ctx)
}

func (e *Executor) ensureAllSchemas(ctx context.Context) error {
	e.mu.RLock()
	if e.allSchemasFetched {
		e.mu.RUnlock()
		return nil
	}
	e.mu.RUnlock()

	e.warmMu.Lock()
	defer e.warmMu.Unlock()

	// Double check
	e.mu.RLock()
	if e.allSchemasFetched {
		e.mu.RUnlock()
		return nil
	}
	e.mu.RUnlock()

	return e.fetchAllSchemas(ctx)
}

func (e *Executor) RefreshCache(ctx context.Context) error {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "osqueryi.cache.refresh")
	defer span.End()
	e.warmMu.Lock()
	defer e.warmMu.Unlock()

	e.mu.Lock()
	e.tables = nil
	e.schemas = make(map[string][]TableColumn)
	e.allSchemasFetched = false
	e.mu.Unlock()

	err := e.fetchAllSchemas(ctx)
	if err != nil {
		span.SetStatus(codes.Error, errorType(ctx, err))
		recordCacheOperation(ctx, "refresh", "error")
		return err
	}
	recordCacheOperation(ctx, "refresh", "ok")
	return nil
}

var tableNameRegex = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func (e *Executor) validateTableName(name string) error {
	if !tableNameRegex.MatchString(name) {
		return fmt.Errorf("invalid table name: %s", name)
	}
	return nil
}

func (e *Executor) ensureKnownTable(ctx context.Context, tableName string) error {
	if err := e.validateTableName(tableName); err != nil {
		return err
	}

	tables, err := e.listTables(ctx)
	if err != nil {
		return err
	}

	for _, table := range tables {
		if table == tableName {
			return nil
		}
	}

	if suggestions := similarTables(tableName, tables); len(suggestions) > 0 {
		return fmt.Errorf("unknown table: %s\ndid you mean: %s", tableName, strings.Join(suggestions, ", "))
	}
	return fmt.Errorf("unknown table: %s\nno similar table exists; use search_tables to find one", tableName)
}

func commonPrefixLen(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// Near-miss table names (interfaces_addresses for interface_addresses) are a common model
// error. Substring/prefix/token overlap catches those without pulling in a distance library,
// and returning nothing is fine — the caller falls back to pointing at search_tables.
func similarTables(name string, tables []string) []string {
	wanted := strings.Split(name, "_")
	type scored struct {
		name  string
		score int
	}

	var matches []scored
	for _, table := range tables {
		score := commonPrefixLen(name, table)
		if strings.Contains(table, name) || strings.Contains(name, table) {
			score += min(len(name), len(table))
		}
		for _, token := range wanted {
			if len(token) >= 4 && strings.Contains(table, token) {
				score += len(token)
			}
		}
		if score >= 6 {
			matches = append(matches, scored{table, score})
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].name < matches[j].name
	})

	suggestions := make([]string, 0, 5)
	for _, match := range matches {
		if len(suggestions) == 5 {
			break
		}
		suggestions = append(suggestions, match.name)
	}
	return suggestions
}

func (e *Executor) describeTableColumns(ctx context.Context, tableName string) ([]TableColumn, error) {
	if err := e.ensureKnownTable(ctx, tableName); err != nil {
		return nil, err
	}

	e.mu.RLock()
	if columns, ok := e.schemas[tableName]; ok {
		cached := append([]TableColumn(nil), columns...)
		e.mu.RUnlock()
		trace.SpanFromContext(ctx).SetAttributes(attribute.Bool("cache.hit", true))
		return cached, nil
	}
	e.mu.RUnlock()
	trace.SpanFromContext(ctx).SetAttributes(attribute.Bool("cache.hit", false))

	query := fmt.Sprintf("PRAGMA table_info(%s);", tableName)
	data, err := e.runSQL(ctx, query)
	if err != nil {
		return nil, err
	}

	var columns []TableColumn
	if err := json.Unmarshal(data, &columns); err != nil {
		return nil, fmt.Errorf("failed to parse schema for %s: %w", tableName, err)
	}

	e.mu.Lock()
	if _, ok := e.schemas[tableName]; !ok {
		e.schemas[tableName] = append([]TableColumn(nil), columns...)
		e.mu.Unlock()
		_ = e.saveCache(ctx)
	} else {
		e.mu.Unlock()
	}

	return append([]TableColumn(nil), columns...), nil
}

func normalizeLimit(limit, defaultValue, maxValue int) int {
	if limit <= 0 {
		return defaultValue
	}
	if limit > maxValue {
		return maxValue
	}
	return limit
}

func trimOptionalSemicolon(value string) string {
	return strings.TrimSuffix(strings.TrimSpace(value), ";")
}

func columnSet(columns []TableColumn) map[string]struct{} {
	valid := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		valid[column.Name] = struct{}{}
	}
	return valid
}

func columnNames(columns []TableColumn) []string {
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		names = append(names, column.Name)
	}
	return names
}

// maxColumnListChars keeps an error from growing past the point of usefulness. Results are
// capped by truncateJSON; errors are not, and a wide join can name five tables at once.
const maxColumnListChars = 600

func formatColumnList(schema []TableColumn) string {
	names := columnNames(schema)
	joined := strings.Join(names, ", ")
	if len(joined) <= maxColumnListChars {
		return joined
	}

	kept := 0
	length := 0
	for _, name := range names {
		next := length + len(name) + 2 // ", "
		if next > maxColumnListChars {
			break
		}
		length = next
		kept++
	}
	return fmt.Sprintf("%s, ... (%d more, use describe_table)", strings.Join(names[:kept], ", "), len(names)-kept)
}

// Models routinely guess column names and, without the valid set in the error, burn a whole
// extra round trip on describe_table before retrying. Naming the alternatives lets them fix
// the call from the error alone.
func unknownColumnError(kind, column string, schema []TableColumn) error {
	return fmt.Errorf("unknown %s: %s\nvalid columns for this table: %s",
		kind, column, formatColumnList(schema))
}

func validateSelectedColumns(selected []string, schema []TableColumn) ([]string, error) {
	if len(selected) == 0 {
		return nil, nil
	}

	valid := columnSet(schema)
	normalized := make([]string, 0, len(selected))
	for _, column := range selected {
		column = strings.TrimSpace(column)
		if column == "" {
			continue
		}
		if _, ok := valid[column]; !ok {
			return nil, unknownColumnError("column", column, schema)
		}
		normalized = append(normalized, column)
	}

	if len(normalized) == 0 {
		return nil, nil
	}

	return normalized, nil
}

func validateOrderBy(orderBy []string, schema []TableColumn) ([]string, error) {
	valid := columnSet(schema)
	normalized := make([]string, 0, len(orderBy))

	for _, raw := range orderBy {
		raw = trimOptionalSemicolon(raw)
		if raw == "" {
			continue
		}

		parts := strings.Fields(raw)
		if len(parts) == 0 || len(parts) > 2 {
			return nil, fmt.Errorf("invalid order_by clause: %s", raw)
		}

		column := parts[0]
		if _, ok := valid[column]; !ok {
			return nil, unknownColumnError("order_by column", column, schema)
		}

		if len(parts) == 1 {
			normalized = append(normalized, column)
			continue
		}

		direction := strings.ToUpper(parts[1])
		if direction != "ASC" && direction != "DESC" {
			return nil, fmt.Errorf("invalid order_by direction in clause: %s", raw)
		}

		normalized = append(normalized, column+" "+direction)
	}

	return normalized, nil
}

func validateWhereClause(where string) (string, error) {
	where = trimOptionalSemicolon(where)
	if where == "" {
		return "", nil
	}
	if strings.Contains(where, ";") {
		return "", fmt.Errorf("where clause must not contain semicolons")
	}
	return where, nil
}

const (
	MaxPayloadSize = 16384 // 16KB soft limit
	MaxRows        = 100   // Max rows to return in a single query
)

type QueryResult struct {
	Truncated bool   `json:"truncated"`
	Message   string `json:"message,omitempty"`
	Results   any    `json:"results"`
}

func (e *Executor) truncateJSON(data []byte) ([]byte, error) {
	if len(data) < MaxPayloadSize {
		var results []map[string]any
		if err := json.Unmarshal(data, &results); err != nil {
			return nil, fmt.Errorf("failed to parse results for wrapping: %w", err)
		}

		truncated := false
		msg := ""
		if len(results) > MaxRows {
			results = results[:MaxRows]
			truncated = true
			msg = fmt.Sprintf("Results truncated to %d rows to stay within limits.", MaxRows)
		}

		res := QueryResult{
			Truncated: truncated,
			Message:   msg,
			Results:   results,
		}
		return json.Marshal(res)
	}

	var results []map[string]any
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, fmt.Errorf("failed to parse results for truncation: %w", err)
	}

	originalCount := len(results)
	if originalCount > MaxRows {
		results = results[:MaxRows]
	}

	for len(results) > 1 {
		newData, err := json.Marshal(results)
		if err == nil && len(newData) < MaxPayloadSize {
			break
		}
		results = results[:len(results)/2]
	}

	res := QueryResult{
		Truncated: true,
		Message:   fmt.Sprintf("Results truncated from %d to %d rows to stay within size limits.", originalCount, len(results)),
		Results:   results,
	}
	return json.Marshal(res)
}

func (e *Executor) DescribeTable(ctx context.Context, tableName string) ([]byte, error) {
	columns, err := e.describeTableColumns(ctx, tableName)
	if err != nil {
		return nil, err
	}
	return json.Marshal(columns)
}

var (
	noSuchColumnRegex = regexp.MustCompile(`no such column: [A-Za-z_][A-Za-z0-9_.]*`)
	noSuchTableRegex  = regexp.MustCompile(`no such table: ([A-Za-z_][A-Za-z0-9_]*)`)
	sqlTableRefRegex  = regexp.MustCompile(`(?i)\b(?:from|join)\s+([a-z_][a-z0-9_]*)`)
	// A CTE is the only place an identifier is followed by "AS (" — a column alias ("as ts")
	// has no parenthesis, and a derived-table alias follows the closing one.
	sqlCTENameRegex = regexp.MustCompile(`(?i)\b([a-z_][a-z0-9_]*)\s+as\s*\(`)
)

// maxAnnotatedTables bounds how many schemas get appended to a failing query, so a wide join
// cannot turn one error into a larger payload than the result would have been.
const maxAnnotatedTables = 5

func referencedTables(sql string) []string {
	var tables []string
	// "WITH recent AS (...) SELECT * FROM recent" otherwise reports "unknown table: recent",
	// sending the model after a table that was never missing.
	ctes := make(map[string]struct{})
	for _, match := range sqlCTENameRegex.FindAllStringSubmatch(sql, -1) {
		ctes[strings.ToLower(match[1])] = struct{}{}
	}

	seen := make(map[string]struct{})
	for _, match := range sqlTableRefRegex.FindAllStringSubmatch(sql, -1) {
		table := match[1]
		if _, isCTE := ctes[strings.ToLower(table)]; isCTE {
			continue
		}
		if _, ok := seen[table]; ok {
			continue
		}
		seen[table] = struct{}{}
		tables = append(tables, table)
		if len(tables) == maxAnnotatedTables {
			break
		}
	}
	return tables
}

// osqueryi reports only "no such column: x" / "no such table: y", which costs a model an extra
// describe_table turn before it can retry. Attach the same context the query_table validation
// errors carry. Best-effort: any lookup failure leaves the original error untouched.
func (e *Executor) annotateQueryError(ctx context.Context, sql string, queryErr error) error {
	message := queryErr.Error()

	if match := noSuchTableRegex.FindStringSubmatch(message); match != nil {
		tables, err := e.listTables(ctx)
		if err != nil {
			return queryErr
		}
		if suggestions := similarTables(match[1], tables); len(suggestions) > 0 {
			return fmt.Errorf("%s\ndid you mean: %s", message, strings.Join(suggestions, ", "))
		}
		return fmt.Errorf("%s\nno similar table exists; use search_tables to find one", message)
	}

	if noSuchColumnRegex.MatchString(message) {
		var schemas strings.Builder
		for _, table := range referencedTables(sql) {
			schema, err := e.describeTableColumns(ctx, table)
			if err != nil {
				// osqueryi reports a missing table as a column error, so the real problem is
				// often the table name. ensureKnownTable's message already carries suggestions.
				if strings.HasPrefix(err.Error(), "unknown table:") {
					fmt.Fprintf(&schemas, "\n%s", err.Error())
				}
				continue
			}
			fmt.Fprintf(&schemas, "\ncolumns in %s: %s", table, strings.Join(columnNames(schema), ", "))
		}
		if schemas.Len() > 0 {
			return fmt.Errorf("%s%s", message, schemas.String())
		}
	}

	return queryErr
}

func (e *Executor) RunQuery(ctx context.Context, sql string) ([]byte, error) {
	data, err := e.runSQL(ctx, sql)
	if err != nil {
		return nil, e.annotateQueryError(ctx, sql, err)
	}
	return e.truncateJSON(data)
}

func (e *Executor) SearchTables(ctx context.Context, query string, searchColumns bool, limit int) ([]byte, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, fmt.Errorf("missing search query")
	}

	limit = normalizeLimit(limit, 20, 100)

	tables, err := e.listTables(ctx)
	if err != nil {
		return nil, err
	}

	if searchColumns {
		if err := e.ensureAllSchemas(ctx); err != nil {
			return nil, err
		}
	}

	matches := make([]SearchMatch, 0, len(tables))
	matchesMu := sync.Mutex{}

	if searchColumns {
		var wg sync.WaitGroup
		semaphore := make(chan struct{}, 8)
		for _, table := range tables {
			wg.Add(1)
			go func(tableName string) {
				defer wg.Done()
				semaphore <- struct{}{}
				defer func() { <-semaphore }()

				columns, err := e.describeTableColumns(ctx, tableName)
				if err != nil {
					return
				}

				match := SearchMatch{TableName: tableName}
				if strings.Contains(strings.ToLower(tableName), query) {
					match.MatchReasons = append(match.MatchReasons, "table_name")
				}

				for _, column := range columns {
					if strings.Contains(strings.ToLower(column.Name), query) {
						match.MatchingColumns = append(match.MatchingColumns, column.Name)
					}
				}
				if len(match.MatchingColumns) > 0 {
					match.MatchReasons = append(match.MatchReasons, "columns")
				}

				if len(match.MatchReasons) > 0 {
					matchesMu.Lock()
					matches = append(matches, match)
					matchesMu.Unlock()
				}
			}(table)
		}
		wg.Wait()
	} else {
		for _, table := range tables {
			match := SearchMatch{TableName: table}
			if strings.Contains(strings.ToLower(table), query) {
				match.MatchReasons = append(match.MatchReasons, "table_name")
			}

			if len(match.MatchReasons) > 0 {
				matches = append(matches, match)
			}
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		iTable := slicesContains(matches[i].MatchReasons, "table_name")
		jTable := slicesContains(matches[j].MatchReasons, "table_name")
		if iTable != jTable {
			return iTable
		}
		if len(matches[i].MatchingColumns) != len(matches[j].MatchingColumns) {
			return len(matches[i].MatchingColumns) > len(matches[j].MatchingColumns)
		}
		return matches[i].TableName < matches[j].TableName
	})

	if len(matches) > limit {
		matches = matches[:limit]
	}

	return json.Marshal(matches)
}

func slicesContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (e *Executor) PreviewTable(ctx context.Context, tableName string, limit int) ([]byte, error) {
	columns, err := e.describeTableColumns(ctx, tableName)
	if err != nil {
		return nil, err
	}

	limit = normalizeLimit(limit, 5, 100)
	query := fmt.Sprintf("SELECT * FROM %s LIMIT %d;", tableName, limit)
	rows, err := e.runSQL(ctx, query)
	if err != nil {
		return nil, err
	}

	var previewRows []map[string]any
	if err := json.Unmarshal(rows, &previewRows); err != nil {
		return nil, fmt.Errorf("failed to parse preview rows for %s: %w", tableName, err)
	}

	preview := struct {
		TableName string           `json:"table_name"`
		Columns   []TableColumn    `json:"columns"`
		Rows      []map[string]any `json:"rows"`
		Truncated bool             `json:"truncated"`
	}{
		TableName: tableName,
		Columns:   columns,
		Rows:      previewRows,
	}

	if len(preview.Rows) > MaxRows {
		preview.Rows = preview.Rows[:MaxRows]
		preview.Truncated = true
	}

	data, err := json.Marshal(preview)
	if err == nil && len(data) > MaxPayloadSize {
		for len(preview.Rows) > 1 {
			preview.Rows = preview.Rows[:len(preview.Rows)/2]
			preview.Truncated = true
			data, err = json.Marshal(preview)
			if err == nil && len(data) < MaxPayloadSize {
				break
			}
		}
	}

	return data, nil
}

func (e *Executor) QueryTable(ctx context.Context, tableName string, columns []string, where string, orderBy []string, limit int) ([]byte, error) {
	schema, err := e.describeTableColumns(ctx, tableName)
	if err != nil {
		return nil, err
	}

	columns, err = validateSelectedColumns(columns, schema)
	if err != nil {
		return nil, err
	}

	where, err = validateWhereClause(where)
	if err != nil {
		return nil, err
	}

	orderBy, err = validateOrderBy(orderBy, schema)
	if err != nil {
		return nil, err
	}

	limit = normalizeLimit(limit, 50, 1000)

	selectedColumns := "*"
	if len(columns) > 0 {
		selectedColumns = strings.Join(columns, ", ")
	}

	var builder strings.Builder
	builder.WriteString("SELECT ")
	builder.WriteString(selectedColumns)
	builder.WriteString(" FROM ")
	builder.WriteString(tableName)

	if where != "" {
		builder.WriteString(" WHERE ")
		builder.WriteString(where)
	}

	if len(orderBy) > 0 {
		builder.WriteString(" ORDER BY ")
		builder.WriteString(strings.Join(orderBy, ", "))
	}

	builder.WriteString(fmt.Sprintf(" LIMIT %d;", limit))

	data, err := e.runSQL(ctx, builder.String())
	if err != nil {
		return nil, err
	}
	return e.truncateJSON(data)
}
