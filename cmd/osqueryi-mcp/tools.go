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
		Description: "Lists all table names. Cheaper than search_tables.",
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
		Description: "Gets table schema only. Use preview_table for schema + sample rows.",
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
		Description: "Executes any SQL including JOINs. Use query_table for single-table queries.",
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

	// search_tables
	searchTablesTool := &mcp.Tool{
		Name:        "search_tables",
		Description: "Finds tables by keyword. Search once broadly; search_columns=true is expensive.",
		InputSchema: struct {
			Type       string   `json:"type"`
			Properties any      `json:"properties"`
			Required   []string `json:"required"`
		}{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Substring to match against table names and optionally column names",
				},
				"search_columns": map[string]any{
					"type":        "boolean",
					"description": "Search column names too. Expensive — default false.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Max results. Use higher value (10+) to reduce re-searches.",
				},
			},
			Required: []string{"query"},
		},
	}

	type searchTablesArgs struct {
		Query         string `json:"query" jsonschema:"substring to match against table names and column names"`
		SearchColumns *bool  `json:"search_columns,omitempty" jsonschema:"whether to search within column names"`
		Limit         int    `json:"limit,omitempty" jsonschema:"maximum number of matches to return"`
	}

	mcp.AddTool(s, searchTablesTool, func(ctx context.Context, req *mcp.CallToolRequest, args searchTablesArgs) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		if strings.TrimSpace(args.Query) == "" {
			return errorResult("missing or invalid query"), nil, nil
		}

		searchColumns := false
		if args.SearchColumns != nil {
			searchColumns = *args.SearchColumns
		}

		slog.Info("tool_called", "tool", "search_tables", "query", args.Query, "search_columns", searchColumns, "limit", args.Limit)
		data, err := e.SearchTables(ctx, args.Query, searchColumns, args.Limit)
		duration := time.Since(start)

		if err != nil {
			slog.Error("tool_failed", "tool", "search_tables", "query", args.Query, "error", err.Error(), "duration_ms", duration.Milliseconds())
			return errorResult(err.Error()), nil, nil
		}

		slog.Info("tool_completed", "tool", "search_tables", "duration_ms", duration.Milliseconds(), "bytes_returned", len(data))
		return textResult(string(data)), nil, nil
	})

	// preview_table
	previewTableTool := &mcp.Tool{
		Name:        "preview_table",
		Description: "Returns schema and sample rows. Better than describe_table for exploration.",
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
				"limit": map[string]any{
					"type":        "integer",
					"description": "Sample rows to return. Keep low if previewing multiple tables.",
				},
			},
			Required: []string{"table_name"},
		},
	}

	type previewArgs struct {
		TableName string `json:"table_name" jsonschema:"osquery table name (e.g. 'processes')"`
		Limit     int    `json:"limit,omitempty" jsonschema:"number of sample rows to return"`
	}

	mcp.AddTool(s, previewTableTool, func(ctx context.Context, req *mcp.CallToolRequest, args previewArgs) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		if args.TableName == "" {
			return errorResult("missing or invalid table_name"), nil, nil
		}

		slog.Info("tool_called", "tool", "preview_table", "table", args.TableName, "limit", args.Limit)
		data, err := e.PreviewTable(ctx, args.TableName, args.Limit)
		duration := time.Since(start)

		if err != nil {
			slog.Error("tool_failed", "tool", "preview_table", "table", args.TableName, "error", err.Error(), "duration_ms", duration.Milliseconds())
			return errorResult(err.Error()), nil, nil
		}

		slog.Info("tool_completed", "tool", "preview_table", "table", args.TableName, "duration_ms", duration.Milliseconds(), "bytes_returned", len(data))
		return textResult(string(data)), nil, nil
	})

	// query_table
	queryTableTool := &mcp.Tool{
		Name:        "query_table",
		Description: "Queries one table with validation. Use for single-table work.",
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
				"columns": map[string]any{
					"type":        "array",
					"description": "Optional list of columns to select; defaults to all columns",
					"items": map[string]any{
						"type": "string",
					},
				},
				"where": map[string]any{
					"type":        "string",
					"description": "Optional SQL WHERE clause without the WHERE keyword",
				},
				"order_by": map[string]any{
					"type":        "array",
					"description": "Optional ORDER BY clauses such as 'pid DESC' or 'name'",
					"items": map[string]any{
						"type": "string",
					},
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of rows to return",
				},
			},
			Required: []string{"table_name"},
		},
	}

	type queryTableArgs struct {
		TableName string   `json:"table_name" jsonschema:"osquery table name (e.g. 'processes')"`
		Columns   []string `json:"columns,omitempty" jsonschema:"columns to select"`
		Where     string   `json:"where,omitempty" jsonschema:"SQL WHERE clause without the WHERE keyword"`
		OrderBy   []string `json:"order_by,omitempty" jsonschema:"ORDER BY clauses such as 'pid DESC'"`
		Limit     int      `json:"limit,omitempty" jsonschema:"maximum number of rows to return"`
	}

	mcp.AddTool(s, queryTableTool, func(ctx context.Context, req *mcp.CallToolRequest, args queryTableArgs) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		if args.TableName == "" {
			return errorResult("missing or invalid table_name"), nil, nil
		}

		slog.Info("tool_called", "tool", "query_table", "table", args.TableName, "columns", args.Columns, "where", args.Where, "order_by", args.OrderBy, "limit", args.Limit)
		data, err := e.QueryTable(ctx, args.TableName, args.Columns, args.Where, args.OrderBy, args.Limit)
		duration := time.Since(start)

		if err != nil {
			slog.Error("tool_failed", "tool", "query_table", "table", args.TableName, "error", err.Error(), "duration_ms", duration.Milliseconds())
			return errorResult(err.Error()), nil, nil
		}

		slog.Info("tool_completed", "tool", "query_table", "table", args.TableName, "duration_ms", duration.Milliseconds(), "bytes_returned", len(data))
		return textResult(string(data)), nil, nil
	})

	// refresh_cache
	refreshCacheTool := &mcp.Tool{
		Name:        "refresh_cache",
		Description: "Reloads all table schemas. Slow — call only if schema changed.",
		InputSchema: struct {
			Type       string `json:"type"`
			Properties any    `json:"properties"`
		}{
			Type:       "object",
			Properties: struct{}{},
		},
	}

	mcp.AddTool(s, refreshCacheTool, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		slog.Info("tool_called", "tool", "refresh_cache")
		err := e.RefreshCache(ctx)
		duration := time.Since(start)

		if err != nil {
			slog.Error("tool_failed", "tool", "refresh_cache", "error", err.Error(), "duration_ms", duration.Milliseconds())
			return errorResult(err.Error()), nil, nil
		}
		slog.Info("tool_completed", "tool", "refresh_cache", "duration_ms", duration.Milliseconds())
		return textResult("Cache refreshed successfully"), nil, nil
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
