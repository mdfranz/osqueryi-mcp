package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeFakeOSQuery(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "osqueryi")
	script := `#!/bin/sh
case "$3" in
  *"SELECT name FROM osquery_registry"*)
    printf '%s\n' '[{"name":"processes"},{"name":"users"}]'
    ;;
  *"PRAGMA table_info(users)"*)
    printf '%s\n' '[{"cid":"0","name":"username","type":"TEXT"},{"cid":"1","name":"uid","type":"INTEGER"}]'
    ;;
  *"SELECT username, uid FROM users"*)
    printf '%s\n' '[{"username":"alice","uid":"501"}]'
    ;;
  *"bad query"*)
    printf '%s\n' 'invalid SQL' >&2
    exit 1
    ;;
  *"SELECT bogus FROM users"*)
    printf '%s\n' 'Error: no such column: bogus' >&2
    exit 1
    ;;
  *"FROM userss"*)
    printf '%s\n' 'Error: no such table: userss' >&2
    exit 1
    ;;
  *"FROM missingtbl"*)
    printf '%s\n' 'Error: no such column: name' >&2
    exit 1
    ;;
  *)
    printf '%s\n' '[]'
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatalf("write fake osqueryi: %v", err)
	}
	return path
}

func TestExecutorWithFakeOSQuery(t *testing.T) {
	e := NewExecutor(writeFakeOSQuery(t), time.Second, "off")
	ctx := context.Background()

	tables, err := e.listTables(ctx)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	if want := []string{"processes", "users"}; !reflect.DeepEqual(tables, want) {
		t.Fatalf("tables = %v, want %v", tables, want)
	}

	columns, err := e.describeTableColumns(ctx, "users")
	if err != nil {
		t.Fatalf("describe users: %v", err)
	}
	if len(columns) != 2 || columns[0].Name != "username" || columns[1].Name != "uid" {
		t.Fatalf("unexpected users schema: %#v", columns)
	}

	data, err := e.QueryTable(ctx, "users", []string{"username", "uid"}, "uid > 0", []string{"uid DESC"}, 5)
	if err != nil {
		t.Fatalf("query users: %v", err)
	}
	var result struct {
		Truncated bool                     `json:"truncated"`
		Results   []map[string]interface{} `json:"results"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode query result: %v", err)
	}
	if result.Truncated || len(result.Results) != 1 || result.Results[0]["username"] != "alice" {
		t.Fatalf("unexpected query result: %s", data)
	}

	search, err := e.SearchTables(ctx, "user", false, 10)
	if err != nil {
		t.Fatalf("search tables: %v", err)
	}
	if !strings.Contains(string(search), `"table_name":"users"`) {
		t.Fatalf("search result does not contain users: %s", search)
	}

	if _, err := e.RunQuery(ctx, "bad query"); err == nil || !strings.Contains(err.Error(), "invalid SQL") {
		t.Fatalf("RunQuery error = %v, want invalid SQL error", err)
	}
}

func TestCacheRoundTrip(t *testing.T) {
	cacheFile := filepath.Join(t.TempDir(), "cache.json")
	e := NewExecutor("unused", time.Second, cacheFile)
	e.tables = []string{"users"}
	e.schemas = map[string][]TableColumn{
		"users": {{Name: "username", Type: "TEXT"}},
	}
	e.allSchemasFetched = true
	if err := e.saveCache(context.Background()); err != nil {
		t.Fatalf("save cache: %v", err)
	}

	loaded := NewExecutor("unused", time.Second, cacheFile)
	if !reflect.DeepEqual(loaded.tables, e.tables) || !reflect.DeepEqual(loaded.schemas, e.schemas) || !loaded.allSchemasFetched {
		t.Fatalf("loaded cache = %#v, want %#v", loaded, e)
	}
}

func TestSimilarTables(t *testing.T) {
	tables := []string{"interface_addresses", "interface_details", "processes", "users"}

	got := similarTables("interfaces_addresses", tables)
	if len(got) == 0 || got[0] != "interface_addresses" {
		t.Errorf("similarTables(interfaces_addresses) = %v, want interface_addresses first", got)
	}
	if got := similarTables("signed_system_files", tables); len(got) != 0 {
		t.Errorf("similarTables with no near miss = %v, want none", got)
	}
}

func TestRunQueryErrorAnnotation(t *testing.T) {
	e := NewExecutor(writeFakeOSQuery(t), time.Second, "off")
	ctx := context.Background()

	_, err := e.RunQuery(ctx, "SELECT bogus FROM users LIMIT 1")
	if err == nil {
		t.Fatal("bad column query succeeded")
	}
	if !strings.Contains(err.Error(), "columns in users: username, uid") {
		t.Errorf("no such column error omits the table schema: %v", err)
	}

	_, err = e.RunQuery(ctx, "SELECT uid FROM userss LIMIT 1")
	if err == nil {
		t.Fatal("bad table query succeeded")
	}
	if !strings.Contains(err.Error(), "did you mean: users") {
		t.Errorf("no such table error omits suggestions: %v", err)
	}

	// osqueryi reports a missing table as a column error, so this path has to surface the
	// table problem rather than silently skipping the unknown table.
	_, err = e.RunQuery(ctx, "SELECT name FROM missingtbl LIMIT 1")
	if err == nil {
		t.Fatal("query against missing table succeeded")
	}
	if !strings.Contains(err.Error(), "unknown table: missingtbl") {
		t.Errorf("column error on a missing table omits the table problem: %v", err)
	}

	// Errors that name neither a column nor a table pass through unchanged.
	_, err = e.RunQuery(ctx, "bad query")
	if err == nil || err.Error() != "invalid SQL" {
		t.Errorf("unrelated error was rewritten: %v", err)
	}
}

func TestReferencedTables(t *testing.T) {
	sql := "SELECT p.pid, u.username FROM processes p JOIN users u ON p.uid = u.uid JOIN processes q ON 1=1"
	if got, want := referencedTables(sql), []string{"processes", "users"}; !reflect.DeepEqual(got, want) {
		t.Errorf("referencedTables = %v, want %v", got, want)
	}
}

func TestValidationAndTruncation(t *testing.T) {
	if err := (&Executor{}).validateTableName("users_2"); err != nil {
		t.Fatalf("valid table name rejected: %v", err)
	}
	for _, name := range []string{"", "Users", "users;DROP"} {
		if err := (&Executor{}).validateTableName(name); err == nil {
			t.Errorf("invalid table name accepted: %q", name)
		}
	}

	schema := []TableColumn{{Name: "uid"}, {Name: "username"}}
	// The valid-column list in the message is what lets a model retry without a describe_table
	// round trip, so assert on it rather than just on err != nil.
	_, err := validateSelectedColumns([]string{"missing"}, schema)
	if err == nil {
		t.Error("unknown selected column was accepted")
	} else if !strings.Contains(err.Error(), "uid, username") {
		t.Errorf("unknown column error omits valid columns: %v", err)
	}
	if got, err := validateOrderBy([]string{"uid desc"}, schema); err != nil || !reflect.DeepEqual(got, []string{"uid DESC"}) {
		t.Fatalf("order by = %v, %v", got, err)
	}
	if _, err := validateOrderBy([]string{"missing desc"}, schema); err == nil {
		t.Error("unknown order_by column was accepted")
	} else if !strings.Contains(err.Error(), "uid, username") {
		t.Errorf("unknown order_by column error omits valid columns: %v", err)
	}
	if _, err := validateOrderBy([]string{"uid sideways"}, schema); err == nil {
		t.Error("invalid order direction was accepted")
	}
	if _, err := validateWhereClause("uid = 1; DROP TABLE users"); err == nil {
		t.Error("multi-statement where clause was accepted")
	}

	rows := make([]map[string]interface{}, MaxRows+1)
	for i := range rows {
		rows[i] = map[string]interface{}{"id": i}
	}
	input, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal rows: %v", err)
	}
	data, err := (&Executor{}).truncateJSON(input)
	if err != nil {
		t.Fatalf("truncate JSON: %v", err)
	}
	var result struct {
		Truncated bool                     `json:"truncated"`
		Results   []map[string]interface{} `json:"results"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode truncated result: %v", err)
	}
	if !result.Truncated || len(result.Results) != MaxRows {
		t.Fatalf("truncation result = %#v", result)
	}
}
