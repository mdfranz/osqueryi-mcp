use regex::Regex;
use serde::{Deserialize, Serialize};
use std::collections::{HashMap, HashSet};
use std::time::Duration;
use tokio::fs;
use tokio::process::Command;
use tokio::sync::{Mutex, RwLock};
use tokio::time::timeout;

const MAX_PAYLOAD_SIZE: usize = 16384; // 16KB soft limit
const MAX_ROWS: usize = 100;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct TableColumn {
    pub cid: String,
    pub dflt_value: String,
    pub name: String,
    pub notnull: String,
    pub pk: String,
    pub r#type: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PersistedCache {
    pub tables: Vec<String>,
    pub schemas: HashMap<String, Vec<TableColumn>>,
    pub all_schemas_fetched: bool,
}

pub struct ExecutorState {
    pub tables: Vec<String>,
    pub schemas: HashMap<String, Vec<TableColumn>>,
    pub all_schemas_fetched: bool,
}

pub struct Executor {
    binary_path: String,
    timeout: Duration,
    cache_file: String,
    state: RwLock<ExecutorState>,
    warm_mu: Mutex<()>,
    table_name_regex: Regex,
}

#[derive(Deserialize)]
struct TableListEntry {
    name: String,
}

#[derive(Deserialize)]
struct FullSchemaEntry {
    #[serde(flatten)]
    column: TableColumn,
    table_name: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SearchMatch {
    pub table_name: String,
    pub match_reasons: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty", default)]
    pub matching_columns: Vec<String>,
}

#[derive(Serialize)]
pub struct QueryResult {
    pub truncated: bool,
    #[serde(skip_serializing_if = "String::is_empty", default)]
    pub message: String,
    pub results: serde_json::Value,
}

#[derive(Serialize)]
struct PreviewResult {
    table_name: String,
    columns: Vec<TableColumn>,
    rows: Vec<serde_json::Value>,
    truncated: bool,
}

fn normalize_limit(limit: i32, default_value: i32, max_value: i32) -> i32 {
    if limit <= 0 {
        default_value
    } else if limit > max_value {
        max_value
    } else {
        limit
    }
}

fn trim_optional_semicolon(value: &str) -> &str {
    value.trim().trim_end_matches(';')
}

fn column_set(columns: &[TableColumn]) -> HashSet<String> {
    columns.iter().map(|col| col.name.clone()).collect()
}

fn validate_selected_columns(
    selected: &[String],
    valid: &HashSet<String>,
) -> Result<Vec<String>, String> {
    if selected.is_empty() {
        return Ok(Vec::new());
    }
    let mut normalized = Vec::new();
    for col in selected {
        let trimmed = col.trim();
        if trimmed.is_empty() {
            continue;
        }
        if !valid.contains(trimmed) {
            return Err(format!("unknown column: {}", trimmed));
        }
        normalized.push(trimmed.to_string());
    }
    Ok(normalized)
}

fn validate_order_by(order_by: &[String], valid: &HashSet<String>) -> Result<Vec<String>, String> {
    let mut normalized = Vec::new();
    for raw in order_by {
        let trimmed = trim_optional_semicolon(raw);
        if trimmed.is_empty() {
            continue;
        }
        let parts: Vec<&str> = trimmed.split_whitespace().collect();
        if parts.is_empty() || parts.len() > 2 {
            return Err(format!("invalid order_by clause: {}", raw));
        }
        let column = parts[0];
        if !valid.contains(column) {
            return Err(format!("unknown order_by column: {}", column));
        }
        if parts.len() == 1 {
            normalized.push(column.to_string());
            continue;
        }
        let direction = parts[1].to_uppercase();
        if direction != "ASC" && direction != "DESC" {
            return Err(format!("invalid order_by direction in clause: {}", raw));
        }
        normalized.push(format!("{} {}", column, direction));
    }
    Ok(normalized)
}

fn validate_where_clause(where_clause: &str) -> Result<String, String> {
    let trimmed = trim_optional_semicolon(where_clause);
    if trimmed.is_empty() {
        return Ok(String::new());
    }
    if trimmed.contains(';') {
        return Err("where clause must not contain semicolons".to_string());
    }
    Ok(trimmed.to_string())
}

fn truncate_json(data: &[u8]) -> Result<Vec<u8>, String> {
    if data.len() < MAX_PAYLOAD_SIZE {
        let mut results: Vec<serde_json::Value> = serde_json::from_slice(data)
            .map_err(|e| format!("failed to parse results for wrapping: {}", e))?;

        let mut truncated = false;
        let mut msg = String::new();
        if results.len() > MAX_ROWS {
            results.truncate(MAX_ROWS);
            truncated = true;
            msg = format!(
                "Results truncated to {} rows to stay within limits.",
                MAX_ROWS
            );
        }

        let res = QueryResult {
            truncated,
            message: msg,
            results: serde_json::Value::Array(results),
        };
        return serde_json::to_vec(&res).map_err(|e| e.to_string());
    }

    let mut results: Vec<serde_json::Value> = serde_json::from_slice(data)
        .map_err(|e| format!("failed to parse results for truncation: {}", e))?;

    let original_count = results.len();
    if original_count > MAX_ROWS {
        results.truncate(MAX_ROWS);
    }

    while results.len() > 1 {
        let new_data = serde_json::to_vec(&results).map_err(|e| e.to_string())?;
        if new_data.len() < MAX_PAYLOAD_SIZE {
            break;
        }
        let half = results.len() / 2;
        results.truncate(half);
    }

    let res = QueryResult {
        truncated: true,
        message: format!(
            "Results truncated from {} to {} rows to stay within size limits.",
            original_count,
            results.len()
        ),
        results: serde_json::Value::Array(results),
    };
    serde_json::to_vec(&res).map_err(|e| e.to_string())
}

impl Executor {
    pub fn new(binary_path: &str, timeout: Duration, cache_file: &str) -> Self {
        let regex = Regex::new(r"^[a-z][a-z0-9_]*$").unwrap();
        let state = ExecutorState {
            tables: Vec::new(),
            schemas: HashMap::new(),
            all_schemas_fetched: false,
        };
        let e = Self {
            binary_path: binary_path.to_string(),
            timeout,
            cache_file: cache_file.to_string(),
            state: RwLock::new(state),
            warm_mu: Mutex::new(()),
            table_name_regex: regex,
        };

        if !cache_file.is_empty() && cache_file != "off" {
            if let Ok(data) = std::fs::read(cache_file) {
                if let Ok(pc) = serde_json::from_slice::<PersistedCache>(&data) {
                    let mut st = e.state.try_write().unwrap();
                    st.tables = pc.tables;
                    st.schemas = pc.schemas;
                    st.all_schemas_fetched = pc.all_schemas_fetched;
                    tracing::info!(file = %cache_file, tables = st.tables.len(), "loaded cache from disk");
                } else {
                    tracing::warn!(file = %cache_file, "failed to parse cache JSON on startup");
                }
            } else {
                tracing::info!(file = %cache_file, "cache file not found, will create on exit or after warming");
            }
        }
        e
    }

    pub async fn save_cache(&self) -> Result<(), String> {
        if self.cache_file.is_empty() || self.cache_file == "off" {
            return Ok(());
        }

        tracing::info!(file = %self.cache_file, "saving cache to disk");

        let pc = {
            let state = self.state.read().await;
            PersistedCache {
                tables: state.tables.clone(),
                schemas: state.schemas.clone(),
                all_schemas_fetched: state.all_schemas_fetched,
            }
        };

        let data = serde_json::to_vec_pretty(&pc)
            .map_err(|e| format!("failed to marshal cache: {}", e))?;

        fs::write(&self.cache_file, data).await.map_err(|e| {
            tracing::error!(file = %self.cache_file, error = %e, "failed to write cache file");
            e.to_string()
        })?;

        tracing::info!(file = %self.cache_file, tables = pc.tables.len(), "saved cache to disk");
        Ok(())
    }

    pub async fn run_sql(&self, sql: &str) -> Result<Vec<u8>, String> {
        tracing::debug!(sql = sql, "executing_sql");

        let mut cmd = Command::new(&self.binary_path);
        cmd.args(&["--json", "--config_path=/dev/null", sql]);
        cmd.kill_on_drop(true);

        let start = std::time::Instant::now();
        let output_fut = cmd.output();

        match timeout(self.timeout, output_fut).await {
            Ok(Ok(output)) => {
                let duration_ms = start.elapsed().as_millis();
                if output.status.success() {
                    tracing::debug!(
                        sql = sql,
                        duration_ms = duration_ms,
                        bytes = output.stdout.len(),
                        "exec_completed"
                    );
                    Ok(output.stdout)
                } else {
                    let err_msg = String::from_utf8_lossy(&output.stderr).trim().to_string();
                    tracing::debug!(sql = sql, error = %err_msg, duration_ms = duration_ms, "exec_failed");
                    Err(if err_msg.is_empty() {
                        format!("osqueryi exited with status {}", output.status)
                    } else {
                        err_msg
                    })
                }
            }
            Ok(Err(e)) => {
                let duration_ms = start.elapsed().as_millis();
                tracing::debug!(sql = sql, error = %e, duration_ms = duration_ms, "exec_failed");
                Err(e.to_string())
            }
            Err(_) => {
                let duration_ms = start.elapsed().as_millis();
                tracing::debug!(
                    sql = sql,
                    error = "timeout",
                    duration_ms = duration_ms,
                    "exec_failed"
                );
                Err("query timeout exceeded".to_string())
            }
        }
    }

    pub async fn list_tables(&self) -> Result<Vec<String>, String> {
        {
            let state = self.state.read().await;
            if !state.tables.is_empty() {
                return Ok(state.tables.clone());
            }
        }

        tracing::debug!("listing_tables");
        let data = self
            .run_sql("SELECT name FROM osquery_registry WHERE registry = 'table' ORDER BY name;")
            .await?;
        let results: Vec<TableListEntry> = serde_json::from_slice(&data)
            .map_err(|e| format!("failed to parse table list: {}", e))?;

        let tables: Vec<String> = results.into_iter().map(|entry| entry.name).collect();

        let mut state = self.state.write().await;
        if state.tables.is_empty() {
            state.tables = tables.clone();
            drop(state);
            let _ = self.save_cache().await;
        }

        Ok(tables)
    }

    pub async fn fetch_all_schemas(&self) -> Result<(), String> {
        tracing::debug!("fetching_all_schemas");
        let query = "SELECT m.name as table_name, p.* FROM (SELECT name FROM osquery_registry WHERE registry = 'table') m, pragma_table_info(m.name) p;";
        let data = self.run_sql(query).await?;

        let entries: Vec<FullSchemaEntry> = serde_json::from_slice(&data)
            .map_err(|e| format!("failed to parse all schemas: {}", e))?;

        let mut new_schemas: HashMap<String, Vec<TableColumn>> = HashMap::new();
        for entry in entries {
            new_schemas
                .entry(entry.table_name)
                .or_default()
                .push(entry.column);
        }

        let mut state = self.state.write().await;
        for (table, cols) in new_schemas {
            state.schemas.insert(table, cols);
        }

        if state.tables.is_empty() {
            let mut tables: Vec<String> = state.schemas.keys().cloned().collect();
            tables.sort();
            state.tables = tables;
        }
        state.all_schemas_fetched = true;
        drop(state);

        let _ = self.save_cache().await;
        Ok(())
    }

    pub async fn ensure_all_schemas(&self) -> Result<(), String> {
        {
            let state = self.state.read().await;
            if state.all_schemas_fetched {
                return Ok(());
            }
        }

        let _guard = self.warm_mu.lock().await;
        {
            let state = self.state.read().await;
            if state.all_schemas_fetched {
                return Ok(());
            }
        }

        self.fetch_all_schemas().await
    }

    pub async fn refresh_cache(&self) -> Result<(), String> {
        let _guard = self.warm_mu.lock().await;

        {
            let mut state = self.state.write().await;
            state.tables.clear();
            state.schemas.clear();
            state.all_schemas_fetched = false;
        }

        self.fetch_all_schemas().await
    }

    pub fn validate_table_name(&self, name: &str) -> Result<(), String> {
        if !self.table_name_regex.is_match(name) {
            return Err(format!("invalid table name: {}", name));
        }
        Ok(())
    }

    pub async fn ensure_known_table(&self, table_name: &str) -> Result<(), String> {
        self.validate_table_name(table_name)?;
        let tables = self.list_tables().await?;
        if tables.iter().any(|t| t == table_name) {
            Ok(())
        } else {
            Err(format!("unknown table: {}", table_name))
        }
    }

    pub async fn describe_table_columns(
        &self,
        table_name: &str,
    ) -> Result<Vec<TableColumn>, String> {
        self.ensure_known_table(table_name).await?;

        {
            let state = self.state.read().await;
            if let Some(columns) = state.schemas.get(table_name) {
                return Ok(columns.clone());
            }
        }

        let query = format!("PRAGMA table_info({});", table_name);
        let data = self.run_sql(&query).await?;
        let columns: Vec<TableColumn> = serde_json::from_slice(&data)
            .map_err(|e| format!("failed to parse schema for {}: {}", table_name, e))?;

        let mut state = self.state.write().await;
        if !state.schemas.contains_key(table_name) {
            state
                .schemas
                .insert(table_name.to_string(), columns.clone());
            drop(state);
            let _ = self.save_cache().await;
        }

        Ok(columns)
    }

    pub async fn describe_table(&self, table_name: &str) -> Result<Vec<u8>, String> {
        let columns = self.describe_table_columns(table_name).await?;
        serde_json::to_vec(&columns).map_err(|e| e.to_string())
    }

    pub async fn run_query(&self, sql: &str) -> Result<Vec<u8>, String> {
        let data = self.run_sql(sql).await?;
        truncate_json(&data)
    }

    pub async fn search_tables(
        &self,
        query: &str,
        search_columns: bool,
        limit: i32,
    ) -> Result<Vec<u8>, String> {
        let query_lower = query.to_lowercase().trim().to_string();
        if query_lower.is_empty() {
            return Err("missing search query".to_string());
        }

        let limit_val = normalize_limit(limit, 20, 100) as usize;
        let tables = self.list_tables().await?;

        if search_columns {
            self.ensure_all_schemas().await?;
        }

        let mut matches = Vec::new();

        if search_columns {
            let state = self.state.read().await;
            for table in &tables {
                let mut match_entry = SearchMatch {
                    table_name: table.clone(),
                    match_reasons: Vec::new(),
                    matching_columns: Vec::new(),
                };

                let table_lower = table.to_lowercase();
                if table_lower.contains(&query_lower) {
                    match_entry.match_reasons.push("table_name".to_string());
                }

                if let Some(columns) = state.schemas.get(table) {
                    for col in columns {
                        if col.name.to_lowercase().contains(&query_lower) {
                            match_entry.matching_columns.push(col.name.clone());
                        }
                    }
                }

                if !match_entry.matching_columns.is_empty() {
                    match_entry.match_reasons.push("columns".to_string());
                }

                if !match_entry.match_reasons.is_empty() {
                    matches.push(match_entry);
                }
            }
        } else {
            for table in &tables {
                let table_lower = table.to_lowercase();
                if table_lower.contains(&query_lower) {
                    let match_entry = SearchMatch {
                        table_name: table.clone(),
                        match_reasons: vec!["table_name".to_string()],
                        matching_columns: Vec::new(),
                    };
                    matches.push(match_entry);
                }
            }
        }

        matches.sort_by(|a, b| {
            let a_has_table = a.match_reasons.iter().any(|r| r == "table_name");
            let b_has_table = b.match_reasons.iter().any(|r| r == "table_name");
            if a_has_table != b_has_table {
                return b_has_table.cmp(&a_has_table);
            }
            let a_col_count = a.matching_columns.len();
            let b_col_count = b.matching_columns.len();
            if a_col_count != b_col_count {
                return b_col_count.cmp(&a_col_count);
            }
            a.table_name.cmp(&b.table_name)
        });

        if matches.len() > limit_val {
            matches.truncate(limit_val);
        }

        serde_json::to_vec(&matches).map_err(|e| e.to_string())
    }

    pub async fn preview_table(&self, table_name: &str, limit: i32) -> Result<Vec<u8>, String> {
        let columns = self.describe_table_columns(table_name).await?;
        let limit_val = normalize_limit(limit, 5, 100);

        let query = format!("SELECT * FROM {} LIMIT {};", table_name, limit_val);
        let rows_data = self.run_sql(&query).await?;

        let mut preview_rows: Vec<serde_json::Value> = serde_json::from_slice(&rows_data)
            .map_err(|e| format!("failed to parse preview rows for {}: {}", table_name, e))?;

        let mut truncated = false;
        if preview_rows.len() > MAX_ROWS {
            preview_rows.truncate(MAX_ROWS);
            truncated = true;
        }

        let mut preview = PreviewResult {
            table_name: table_name.to_string(),
            columns,
            rows: preview_rows,
            truncated,
        };

        let mut data = serde_json::to_vec(&preview).map_err(|e| e.to_string())?;

        if data.len() > MAX_PAYLOAD_SIZE {
            while preview.rows.len() > 1 {
                let half = preview.rows.len() / 2;
                preview.rows.truncate(half);
                preview.truncated = true;
                data = serde_json::to_vec(&preview).map_err(|e| e.to_string())?;
                if data.len() < MAX_PAYLOAD_SIZE {
                    break;
                }
            }
        }

        Ok(data)
    }

    pub async fn query_table(
        &self,
        table_name: &str,
        columns: &[String],
        where_clause: &str,
        order_by: &[String],
        limit: i32,
    ) -> Result<Vec<u8>, String> {
        let schema = self.describe_table_columns(table_name).await?;
        let valid_columns = column_set(&schema);

        let validated_columns = validate_selected_columns(columns, &valid_columns)?;
        let validated_where = validate_where_clause(where_clause)?;
        let validated_order_by = validate_order_by(order_by, &valid_columns)?;

        let limit_val = normalize_limit(limit, 50, 1000);

        let selected_columns = if validated_columns.is_empty() {
            "*".to_string()
        } else {
            validated_columns.join(", ")
        };

        let mut sql = format!("SELECT {} FROM {}", selected_columns, table_name);

        if !validated_where.is_empty() {
            sql.push_str(" WHERE ");
            sql.push_str(&validated_where);
        }

        if !validated_order_by.is_empty() {
            sql.push_str(" ORDER BY ");
            sql.push_str(&validated_order_by.join(", "));
        }

        sql.push_str(&format!(" LIMIT {};", limit_val));

        let data = self.run_sql(&sql).await?;
        truncate_json(&data)
    }
}
