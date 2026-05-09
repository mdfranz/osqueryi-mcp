package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Executor struct {
	binaryPath string
	timeout    time.Duration
	mu         sync.RWMutex
	tables     []string
	schemas    map[string][]TableColumn
}

func NewExecutor(binaryPath string, timeout time.Duration) *Executor {
	return &Executor{
		binaryPath: binaryPath,
		timeout:    timeout,
		schemas:    make(map[string][]TableColumn),
	}
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
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	slog.Debug("executing_sql", "sql", sql)
	cmd := exec.CommandContext(ctx, e.binaryPath, "--json", "--config_path=/dev/null", sql)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	if err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			slog.Debug("exec_failed", "sql", sql, "error", err, "duration_ms", duration.Milliseconds())
			return nil, err
		}
		slog.Debug("exec_failed", "sql", sql, "error", errMsg, "duration_ms", duration.Milliseconds())
		return nil, fmt.Errorf("%s", errMsg)
	}

	slog.Debug("exec_completed", "sql", sql, "duration_ms", duration.Milliseconds(), "bytes", stdout.Len())
	return stdout.Bytes(), nil
}

func (e *Executor) RefreshCache(ctx context.Context) error {
	e.mu.Lock()
	e.tables = nil
	e.schemas = make(map[string][]TableColumn)
	e.mu.Unlock()

	_, err := e.listTables(ctx)
	return err
}

func (e *Executor) listTables(ctx context.Context) ([]string, error) {
	e.mu.RLock()
	if len(e.tables) > 0 {
		tables := append([]string(nil), e.tables...)
		e.mu.RUnlock()
		return tables, nil
	}
	e.mu.RUnlock()

	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	slog.Debug("listing_tables")
	cmd := exec.CommandContext(ctx, e.binaryPath, "--config_path=/dev/null")
	cmd.Stdin = strings.NewReader(".tables\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	if err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			slog.Debug("exec_failed", "op", "list_tables", "error", err, "duration_ms", duration.Milliseconds())
			return nil, err
		}
		slog.Debug("exec_failed", "op", "list_tables", "error", errMsg, "duration_ms", duration.Milliseconds())
		return nil, fmt.Errorf("%s", errMsg)
	}

	slog.Debug("exec_completed", "op", "list_tables", "duration_ms", duration.Milliseconds())

	// osqueryi .tables output looks like:
	//   => table_name
	//   => other_table
	lines := strings.Split(stdout.String(), "\n")
	var tables []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "=> ") {
			tables = append(tables, strings.TrimPrefix(line, "=> "))
		}
	}

	sort.Strings(tables)

	e.mu.Lock()
	if len(e.tables) == 0 {
		e.tables = append([]string(nil), tables...)
	}
	e.mu.Unlock()

	return tables, nil
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

	return fmt.Errorf("unknown table: %s", tableName)
}

func (e *Executor) describeTableColumns(ctx context.Context, tableName string) ([]TableColumn, error) {
	if err := e.ensureKnownTable(ctx, tableName); err != nil {
		return nil, err
	}

	e.mu.RLock()
	if columns, ok := e.schemas[tableName]; ok {
		cached := append([]TableColumn(nil), columns...)
		e.mu.RUnlock()
		return cached, nil
	}
	e.mu.RUnlock()

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
	}
	e.mu.Unlock()

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

func validateSelectedColumns(selected []string, valid map[string]struct{}) ([]string, error) {
	if len(selected) == 0 {
		return nil, nil
	}

	normalized := make([]string, 0, len(selected))
	for _, column := range selected {
		column = strings.TrimSpace(column)
		if column == "" {
			continue
		}
		if _, ok := valid[column]; !ok {
			return nil, fmt.Errorf("unknown column: %s", column)
		}
		normalized = append(normalized, column)
	}

	if len(normalized) == 0 {
		return nil, nil
	}

	return normalized, nil
}

func validateOrderBy(orderBy []string, valid map[string]struct{}) ([]string, error) {
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
			return nil, fmt.Errorf("unknown order_by column: %s", column)
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

func (e *Executor) DescribeTable(ctx context.Context, tableName string) ([]byte, error) {
	columns, err := e.describeTableColumns(ctx, tableName)
	if err != nil {
		return nil, err
	}
	return json.Marshal(columns)
}

func (e *Executor) RunQuery(ctx context.Context, sql string) ([]byte, error) {
	return e.runSQL(ctx, sql)
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

	matches := make([]SearchMatch, 0)
	for _, table := range tables {
		match := SearchMatch{TableName: table}
		if strings.Contains(strings.ToLower(table), query) {
			match.MatchReasons = append(match.MatchReasons, "table_name")
		}

		if searchColumns {
			columns, err := e.describeTableColumns(ctx, table)
			if err != nil {
				return nil, err
			}
			for _, column := range columns {
				if strings.Contains(strings.ToLower(column.Name), query) {
					match.MatchingColumns = append(match.MatchingColumns, column.Name)
				}
			}
			if len(match.MatchingColumns) > 0 {
				match.MatchReasons = append(match.MatchReasons, "columns")
			}
		}

		if len(match.MatchReasons) > 0 {
			matches = append(matches, match)
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
	}{
		TableName: tableName,
		Columns:   columns,
		Rows:      previewRows,
	}

	return json.Marshal(preview)
}

func (e *Executor) QueryTable(ctx context.Context, tableName string, columns []string, where string, orderBy []string, limit int) ([]byte, error) {
	schema, err := e.describeTableColumns(ctx, tableName)
	if err != nil {
		return nil, err
	}

	validColumns := columnSet(schema)
	columns, err = validateSelectedColumns(columns, validColumns)
	if err != nil {
		return nil, err
	}

	where, err = validateWhereClause(where)
	if err != nil {
		return nil, err
	}

	orderBy, err = validateOrderBy(orderBy, validColumns)
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

	return e.runSQL(ctx, builder.String())
}
