package main

import (
	"bytes"
	"context"
	"fmt"
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

	cmd := exec.CommandContext(ctx, e.binaryPath, "--json", "--config_path=/dev/null", sql)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			return nil, err
		}
		return nil, fmt.Errorf("%s", errMsg)
	}

	return stdout.Bytes(), nil
}

func (e *Executor) listTables(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, e.binaryPath, "--config_path=/dev/null")
	cmd.Stdin = strings.NewReader(".tables\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			return nil, err
		}
		return nil, fmt.Errorf("%s", errMsg)
	}

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
