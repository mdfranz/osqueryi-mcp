package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

type Executor struct {
	binaryPath string
	timeout    time.Duration
}

func NewExecutor(binaryPath string, timeout time.Duration) *Executor {
	return &Executor{
		binaryPath: binaryPath,
		timeout:    timeout,
	}
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

func (e *Executor) listTables(ctx context.Context) ([]string, error) {
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
	return tables, nil
}

var tableNameRegex = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func (e *Executor) validateTableName(name string) error {
	if !tableNameRegex.MatchString(name) {
		return fmt.Errorf("invalid table name: %s", name)
	}
	return nil
}

func (e *Executor) DescribeTable(ctx context.Context, tableName string) ([]byte, error) {
	if err := e.validateTableName(tableName); err != nil {
		return nil, err
	}
	query := fmt.Sprintf("PRAGMA table_info(%s);", tableName)
	return e.runSQL(ctx, query)
}

func (e *Executor) RunQuery(ctx context.Context, sql string) ([]byte, error) {
	return e.runSQL(ctx, sql)
}
