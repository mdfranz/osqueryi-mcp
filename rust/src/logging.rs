use std::fs::OpenOptions;
use std::io;
use tracing::level_filters::LevelFilter;
use tracing_subscriber::fmt;

pub fn setup_logging(log_file: &str, debug: bool) {
    let level = if debug {
        LevelFilter::DEBUG
    } else {
        LevelFilter::INFO
    };

    let builder = fmt::Subscriber::builder().with_max_level(level);

    if log_file.is_empty() || log_file == "off" {
        let subscriber = builder.with_writer(io::stderr).finish();
        tracing::subscriber::set_global_default(subscriber).expect("failed to set subscriber");
    } else {
        match OpenOptions::new().create(true).append(true).open(log_file) {
            Ok(file) => {
                let subscriber = builder.with_ansi(false).with_writer(file).finish();
                tracing::subscriber::set_global_default(subscriber)
                    .expect("failed to set subscriber");
            }
            Err(_) => {
                let subscriber = builder.with_writer(io::stderr).finish();
                tracing::subscriber::set_global_default(subscriber)
                    .expect("failed to set subscriber");
            }
        }
    }
}
