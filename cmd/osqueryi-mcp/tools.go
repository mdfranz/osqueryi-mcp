package main

import (
	"context"
	"log/slog"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerTools(s *mcp.Server, e *Executor) {
	// list_tables
	listTablesTool := &mcp.Tool{
		Name:        "list_tables",
		Description: "List all available osquery tables",
	}

	mcp.AddTool(s, listTablesTool, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, any, error) {
		slog.Info("tool_called", "tool", "list_tables")
		tables, err := e.listTables(ctx)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		return textResult(strings.Join(tables, "\n")), nil, nil
	})

	// describe_table
	describeTableTool := &mcp.Tool{
		Name:        "describe_table",
		Description: "Get schema information for a specific osquery table",
	}

	type describeArgs struct {
		TableName string `json:"table_name" jsonschema:"osquery table name (e.g. 'processes')"`
	}

	mcp.AddTool(s, describeTableTool, func(ctx context.Context, req *mcp.CallToolRequest, args describeArgs) (*mcp.CallToolResult, any, error) {
		if args.TableName == "" {
			return errorResult("missing or invalid table_name"), nil, nil
		}

		slog.Info("tool_called", "tool", "describe_table", "table", args.TableName)
		data, err := e.DescribeTable(ctx, args.TableName)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		return textResult(string(data)), nil, nil
	})

	// run_query
	runQueryTool := &mcp.Tool{
		Name:        "run_query",
		Description: "Execute a SQL SELECT query against osquery tables",
	}

	type runArgs struct {
		SQL string `json:"sql" jsonschema:"SQL SELECT query"`
	}

	mcp.AddTool(s, runQueryTool, func(ctx context.Context, req *mcp.CallToolRequest, args runArgs) (*mcp.CallToolResult, any, error) {
		if args.SQL == "" {
			return errorResult("missing or invalid sql"), nil, nil
		}

		slog.Info("tool_called", "tool", "run_query", "sql", args.SQL)
		data, err := e.RunQuery(ctx, args.SQL)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
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
