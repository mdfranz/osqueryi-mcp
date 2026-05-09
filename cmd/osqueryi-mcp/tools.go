package main

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerTools(s *mcp.Server, e *Executor) {
	// list_tables
	listTablesTool := &mcp.Tool{
		Name:        "list_tables",
		Description: "List all available osquery tables",
		InputSchema: struct {
			Type       string `json:"type"`
			Properties any    `json:"properties"`
		}{
			Type:       "object",
			Properties: struct{}{},
		},
	}

	mcp.AddTool(s, listTablesTool, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		slog.Info("tool_called", "tool", "list_tables")
		tables, err := e.listTables(ctx)
		duration := time.Since(start)

		if err != nil {
			slog.Error("tool_failed", "tool", "list_tables", "error", err.Error(), "duration_ms", duration.Milliseconds())
			return errorResult(err.Error()), nil, nil
		}
		res := strings.Join(tables, "\n")
		slog.Info("tool_completed", "tool", "list_tables", "duration_ms", duration.Milliseconds(), "bytes_returned", len(res))
		return textResult(res), nil, nil
	})

	// describe_table
	describeTableTool := &mcp.Tool{
		Name:        "describe_table",
		Description: "Get schema information for a specific osquery table",
		InputSchema: struct {
			Type       string   `json:"type"`
			Properties any      `json:"properties"`
			Required   []string `json:"required"`
		}{
			Type: "object",
			Properties: map[string]any{
				"table_name": map[string]any{
					"type":        "string",
					"description": "osquery table name (e.g. 'processes')",
				},
			},
			Required: []string{"table_name"},
		},
	}

	type describeArgs struct {
		TableName string `json:"table_name" jsonschema:"osquery table name (e.g. 'processes')"`
	}

	mcp.AddTool(s, describeTableTool, func(ctx context.Context, req *mcp.CallToolRequest, args describeArgs) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		if args.TableName == "" {
			return errorResult("missing or invalid table_name"), nil, nil
		}

		slog.Info("tool_called", "tool", "describe_table", "table", args.TableName)
		data, err := e.DescribeTable(ctx, args.TableName)
		duration := time.Since(start)

		if err != nil {
			slog.Error("tool_failed", "tool", "describe_table", "table", args.TableName, "error", err.Error(), "duration_ms", duration.Milliseconds())
			return errorResult(err.Error()), nil, nil
		}
		slog.Info("tool_completed", "tool", "describe_table", "table", args.TableName, "duration_ms", duration.Milliseconds(), "bytes_returned", len(data))
		return textResult(string(data)), nil, nil
	})

	// run_query
	runQueryTool := &mcp.Tool{
		Name:        "run_query",
		Description: "Execute a SQL SELECT query against osquery tables",
		InputSchema: struct {
			Type       string   `json:"type"`
			Properties any      `json:"properties"`
			Required   []string `json:"required"`
		}{
			Type: "object",
			Properties: map[string]any{
				"sql": map[string]any{
					"type":        "string",
					"description": "SQL SELECT query",
				},
			},
			Required: []string{"sql"},
		},
	}

	type runArgs struct {
		SQL string `json:"sql" jsonschema:"SQL SELECT query"`
	}

	mcp.AddTool(s, runQueryTool, func(ctx context.Context, req *mcp.CallToolRequest, args runArgs) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		if args.SQL == "" {
			return errorResult("missing or invalid sql"), nil, nil
		}

		slog.Info("tool_called", "tool", "run_query", "sql", args.SQL)
		data, err := e.RunQuery(ctx, args.SQL)
		duration := time.Since(start)

		if err != nil {
			slog.Error("tool_failed", "tool", "run_query", "sql", args.SQL, "error", err.Error(), "duration_ms", duration.Milliseconds())
			return errorResult(err.Error()), nil, nil
		}
		slog.Info("tool_completed", "tool", "run_query", "duration_ms", duration.Milliseconds(), "bytes_returned", len(data))
		return textResult(string(data)), nil, nil
	})
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: text,
			},
		},
	}
}

func errorResult(msg string) *mcp.CallToolResult {
	res := textResult(msg)
	res.IsError = true
	return res
}
