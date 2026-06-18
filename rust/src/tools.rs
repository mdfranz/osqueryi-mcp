use crate::executor::Executor;
use rmcp::schemars;
use rmcp::schemars::JsonSchema;
use rmcp::{
    ServerHandler,
    handler::server::{router::tool::ToolRouter, wrapper::Parameters},
    model::{Implementation, InitializeResult, ServerCapabilities, ServerInfo},
    tool, tool_handler, tool_router,
};
use serde::Deserialize;
use std::sync::Arc;

#[derive(Deserialize, JsonSchema)]
pub struct DescribeTableRequest {
    /// osquery table name (e.g. 'processes')
    pub table_name: String,
}

#[derive(Deserialize, JsonSchema)]
pub struct RunQueryRequest {
    /// SQL SELECT query
    pub sql: String,
}

#[derive(Deserialize, JsonSchema)]
pub struct SearchTablesRequest {
    /// Single-word substring to match against table names and optionally column names (e.g. 'process')
    pub query: String,
    /// Search column names too. Expensive — default false.
    pub search_columns: Option<bool>,
    /// Max results. Use higher value (10+) to reduce re-searches.
    pub limit: Option<i32>,
}

#[derive(Deserialize, JsonSchema)]
pub struct PreviewTableRequest {
    /// osquery table name (e.g. 'processes')
    pub table_name: String,
    /// Sample rows to return. Keep low if previewing multiple tables.
    pub limit: Option<i32>,
}

#[derive(Deserialize, JsonSchema)]
pub struct QueryTableRequest {
    /// osquery table name (e.g. 'processes')
    pub table_name: String,
    /// Optional list of columns to select; defaults to all columns
    pub columns: Option<Vec<String>>,
    /// Optional SQL WHERE clause without the WHERE keyword
    #[serde(rename = "where")]
    pub r#where: Option<String>,
    /// Optional ORDER BY clauses such as 'pid DESC' or 'name'
    pub order_by: Option<Vec<String>>,
    /// Maximum number of rows to return
    pub limit: Option<i32>,
}

#[derive(Clone)]
pub struct OsqueryServer {
    tool_router: ToolRouter<Self>,
    executor: Arc<Executor>,
}

impl OsqueryServer {
    pub fn new(executor: Arc<Executor>) -> Self {
        Self {
            tool_router: Self::tool_router(),
            executor,
        }
    }
}

#[tool_handler(router = self.tool_router)]
impl ServerHandler for OsqueryServer {
    fn get_info(&self) -> ServerInfo {
        let caps = ServerCapabilities::builder().enable_tools().build();
        let commit_hash = option_env!("GIT_COMMIT").unwrap_or("dev");
        let version = format!("{}-{}", env!("CARGO_PKG_VERSION"), commit_hash);
        InitializeResult::new(caps).with_server_info(Implementation::new("osqueryi-mcp", version))
    }
}

#[tool_router(router = tool_router)]
impl OsqueryServer {
    #[tool(
        name = "list_tables",
        description = "Lists all table names. Cheaper than search_tables."
    )]
    pub async fn list_tables(&self) -> Result<String, String> {
        let start = std::time::Instant::now();
        tracing::info!(tool = "list_tables", "tool_called");
        let res = self.executor.list_tables().await;
        let duration_ms = start.elapsed().as_millis();
        match res {
            Ok(tables) => {
                let out = tables.join("\n");
                tracing::info!(
                    tool = "list_tables",
                    duration_ms = duration_ms,
                    bytes_returned = out.len(),
                    "tool_completed"
                );
                Ok(out)
            }
            Err(e) => {
                tracing::error!(tool = "list_tables", error = %e, duration_ms = duration_ms, "tool_failed");
                Err(e)
            }
        }
    }

    #[tool(
        name = "describe_table",
        description = "Gets table schema only. Use preview_table for schema + sample rows."
    )]
    pub async fn describe_table(
        &self,
        params: Parameters<DescribeTableRequest>,
    ) -> Result<String, String> {
        let start = std::time::Instant::now();
        let table_name = params.0.table_name;
        if table_name.is_empty() {
            return Err("missing or invalid table_name".to_string());
        }

        tracing::info!(tool = "describe_table", table = %table_name, "tool_called");
        let res = self.executor.describe_table(&table_name).await;
        let duration_ms = start.elapsed().as_millis();
        match res {
            Ok(data) => {
                let out = String::from_utf8_lossy(&data).to_string();
                tracing::info!(tool = "describe_table", table = %table_name, duration_ms = duration_ms, bytes_returned = out.len(), "tool_completed");
                Ok(out)
            }
            Err(e) => {
                tracing::error!(tool = "describe_table", table = %table_name, error = %e, duration_ms = duration_ms, "tool_failed");
                Err(e)
            }
        }
    }

    #[tool(
        name = "run_query",
        description = "Executes any SQL including JOINs. Use query_table for single-table queries."
    )]
    pub async fn run_query(&self, params: Parameters<RunQueryRequest>) -> Result<String, String> {
        let start = std::time::Instant::now();
        let sql = params.0.sql;
        if sql.is_empty() {
            return Err("missing or invalid sql".to_string());
        }

        tracing::info!(tool = "run_query", sql = %sql, "tool_called");
        let res = self.executor.run_query(&sql).await;
        let duration_ms = start.elapsed().as_millis();
        match res {
            Ok(data) => {
                let out = String::from_utf8_lossy(&data).to_string();
                tracing::info!(
                    tool = "run_query",
                    duration_ms = duration_ms,
                    bytes_returned = out.len(),
                    "tool_completed"
                );
                Ok(out)
            }
            Err(e) => {
                tracing::error!(tool = "run_query", sql = %sql, error = %e, duration_ms = duration_ms, "tool_failed");
                Err(e)
            }
        }
    }

    #[tool(
        name = "search_tables",
        description = "Finds tables by keyword. Search once broadly; search_columns=true is expensive. Use single-word keywords; literal substring matching is used, so compound multi-word queries (e.g. 'processes users') will fail."
    )]
    pub async fn search_tables(
        &self,
        params: Parameters<SearchTablesRequest>,
    ) -> Result<String, String> {
        let start = std::time::Instant::now();
        let req = params.0;
        let query = req.query;
        if query.trim().is_empty() {
            return Err("missing or invalid query".to_string());
        }

        let search_columns = req.search_columns.unwrap_or(false);
        let limit = req.limit.unwrap_or(0);

        tracing::info!(tool = "search_tables", query = %query, search_columns = search_columns, limit = limit, "tool_called");
        let res = self
            .executor
            .search_tables(&query, search_columns, limit)
            .await;
        let duration_ms = start.elapsed().as_millis();
        match res {
            Ok(data) => {
                let out = String::from_utf8_lossy(&data).to_string();
                tracing::info!(
                    tool = "search_tables",
                    duration_ms = duration_ms,
                    bytes_returned = out.len(),
                    "tool_completed"
                );
                Ok(out)
            }
            Err(e) => {
                tracing::error!(tool = "search_tables", query = %query, error = %e, duration_ms = duration_ms, "tool_failed");
                Err(e)
            }
        }
    }

    #[tool(
        name = "preview_table",
        description = "Returns schema and sample rows. Better than describe_table for exploration."
    )]
    pub async fn preview_table(
        &self,
        params: Parameters<PreviewTableRequest>,
    ) -> Result<String, String> {
        let start = std::time::Instant::now();
        let req = params.0;
        let table_name = req.table_name;
        if table_name.is_empty() {
            return Err("missing or invalid table_name".to_string());
        }

        let limit = req.limit.unwrap_or(0);

        tracing::info!(tool = "preview_table", table = %table_name, limit = limit, "tool_called");
        let res = self.executor.preview_table(&table_name, limit).await;
        let duration_ms = start.elapsed().as_millis();
        match res {
            Ok(data) => {
                let out = String::from_utf8_lossy(&data).to_string();
                tracing::info!(tool = "preview_table", table = %table_name, duration_ms = duration_ms, bytes_returned = out.len(), "tool_completed");
                Ok(out)
            }
            Err(e) => {
                tracing::error!(tool = "preview_table", table = %table_name, error = %e, duration_ms = duration_ms, "tool_failed");
                Err(e)
            }
        }
    }

    #[tool(
        name = "query_table",
        description = "Queries one table with validation. Use for single-table work."
    )]
    pub async fn query_table(
        &self,
        params: Parameters<QueryTableRequest>,
    ) -> Result<String, String> {
        let start = std::time::Instant::now();
        let req = params.0;
        let table_name = req.table_name;
        if table_name.is_empty() {
            return Err("missing or invalid table_name".to_string());
        }

        let columns = req.columns.unwrap_or_default();
        let where_clause = req.r#where.unwrap_or_default();
        let order_by = req.order_by.unwrap_or_default();
        let limit = req.limit.unwrap_or(0);

        tracing::info!(
            tool = "query_table",
            table = %table_name,
            columns = ?columns,
            r#where = %where_clause,
            order_by = ?order_by,
            limit = limit,
            "tool_called"
        );

        let res = self
            .executor
            .query_table(&table_name, &columns, &where_clause, &order_by, limit)
            .await;
        let duration_ms = start.elapsed().as_millis();
        match res {
            Ok(data) => {
                let out = String::from_utf8_lossy(&data).to_string();
                tracing::info!(tool = "query_table", table = %table_name, duration_ms = duration_ms, bytes_returned = out.len(), "tool_completed");
                Ok(out)
            }
            Err(e) => {
                tracing::error!(tool = "query_table", table = %table_name, error = %e, duration_ms = duration_ms, "tool_failed");
                Err(e)
            }
        }
    }

    #[tool(
        name = "refresh_cache",
        description = "Reloads all table schemas. Slow — call only if schema changed."
    )]
    pub async fn refresh_cache(&self) -> Result<String, String> {
        let start = std::time::Instant::now();
        tracing::info!(tool = "refresh_cache", "tool_called");
        let res = self.executor.refresh_cache().await;
        let duration_ms = start.elapsed().as_millis();
        match res {
            Ok(()) => {
                tracing::info!(
                    tool = "refresh_cache",
                    duration_ms = duration_ms,
                    "tool_completed"
                );
                Ok("Cache refreshed successfully".to_string())
            }
            Err(e) => {
                tracing::error!(tool = "refresh_cache", error = %e, duration_ms = duration_ms, "tool_failed");
                Err(e)
            }
        }
    }
}
