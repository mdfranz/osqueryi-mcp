mod config;
mod executor;
mod lock;
mod logging;
mod tools;

use crate::executor::Executor;
use crate::tools::OsqueryServer;
use clap::{Arg, Command};
use rmcp::ServiceExt;
use std::path::PathBuf;
use std::sync::Arc;
use tokio_util::sync::CancellationToken;

fn look_path(binary: &str) -> Option<PathBuf> {
    let path_buf = PathBuf::from(binary);
    if path_buf.is_absolute() || binary.contains('/') {
        if path_buf.is_file() {
            return Some(path_buf);
        }
        return None;
    }

    if let Ok(paths) = std::env::var("PATH") {
        for p in std::env::split_paths(&paths) {
            let candidate = p.join(binary);
            if candidate.is_file() {
                return Some(candidate);
            }
        }
    }
    None
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let matches = Command::new("osqueryi-mcp")
        .version(env!("CARGO_PKG_VERSION"))
        .about("osqueryi MCP server in Rust")
        .arg(
            Arg::new("version")
                .short('v')
                .long("version")
                .action(clap::ArgAction::SetTrue)
                .help("show version"),
        )
        .get_matches();

    let commit_hash = option_env!("GIT_COMMIT").unwrap_or("dev");

    if matches.get_flag("version") {
        println!(
            "osqueryi-mcp {} ({})",
            env!("CARGO_PKG_VERSION"),
            commit_hash
        );
        return Ok(());
    }

    let mut cfg = config::load_config();
    logging::setup_logging(&cfg.log_file, cfg.debug);

    // Validate binary path
    match look_path(&cfg.binary_path) {
        Some(path) => {
            cfg.binary_path = path.to_string_lossy().to_string();
        }
        None => {
            tracing::error!(path = %cfg.binary_path, "osqueryi not found");
            std::process::exit(1);
        }
    }

    // Acquire lock
    let lock_release = match lock::acquire_lock(&cfg.lock_file) {
        Ok(release) => release,
        Err(e) => {
            tracing::error!(error = %e, "failed to acquire lock");
            std::process::exit(1);
        }
    };

    let executor = Arc::new(Executor::new(
        &cfg.binary_path,
        cfg.timeout,
        &cfg.cache_file,
    ));

    let current_dir = std::env::current_dir().unwrap_or_default();
    tracing::info!(
        cwd = %current_dir.to_string_lossy(),
        cache_file = %cfg.cache_file,
        commit = %commit_hash,
        "server_environment"
    );

    // Warm cache in background
    let executor_clone = Arc::clone(&executor);
    let timeout_duration = cfg.timeout * 2;
    tokio::spawn(async move {
        match tokio::time::timeout(timeout_duration, executor_clone.ensure_all_schemas()).await {
            Ok(Ok(())) => {
                tracing::info!("cache warmed");
            }
            Ok(Err(e)) => {
                tracing::warn!(error = %e, "failed to warm cache");
            }
            Err(_) => {
                tracing::warn!("cache warming timed out");
            }
        }
    });

    let ct = CancellationToken::new();
    let ct_clone = ct.clone();

    // Spawn signal handler task for graceful shutdown
    tokio::spawn(async move {
        let mut sigterm =
            tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate()).unwrap();

        tokio::select! {
            _ = tokio::signal::ctrl_c() => {
                tracing::info!("Received SIGINT, shutting down");
            }
            _ = sigterm.recv() => {
                tracing::info!("Received SIGTERM, shutting down");
            }
        }
        ct_clone.cancel();
    });

    tracing::info!(bin = %cfg.binary_path, commit = %commit_hash, "starting osqueryi-mcp server");

    let server = OsqueryServer::new(executor);
    let running = server
        .serve_with_ct(rmcp::transport::io::stdio(), ct)
        .await?;
    let _ = running.waiting().await;

    tracing::info!("Server shutdown complete, releasing lock");
    lock_release();

    Ok(())
}
