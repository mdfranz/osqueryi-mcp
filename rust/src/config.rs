use std::env;
use std::time::Duration;

pub struct Config {
    pub binary_path: String,
    pub timeout: Duration,
    pub lock_file: String,
    pub cache_file: String,
    pub debug: bool,
    pub log_file: String,
}

pub fn parse_duration(s: &str) -> Option<Duration> {
    let s = s.trim();
    if s.ends_with("ms") {
        let val = s[..s.len() - 2].parse::<u64>().ok()?;
        Some(Duration::from_millis(val))
    } else if s.ends_with('s') {
        let val = s[..s.len() - 1].parse::<u64>().ok()?;
        Some(Duration::from_secs(val))
    } else if s.ends_with('m') {
        let val = s[..s.len() - 1].parse::<u64>().ok()?;
        Some(Duration::from_secs(val * 60))
    } else if s.ends_with('h') {
        let val = s[..s.len() - 1].parse::<u64>().ok()?;
        Some(Duration::from_secs(val * 3600))
    } else {
        s.parse::<u64>().ok().map(Duration::from_secs)
    }
}

pub fn load_config() -> Config {
    let mut cfg = Config {
        binary_path: "osqueryi".to_string(),
        timeout: Duration::from_secs(30),
        lock_file: "osqueryi-mcp.lock".to_string(),
        cache_file: "osqueryi-mcp-cache.json".to_string(),
        debug: false,
        log_file: "osqueryi-mcp.log".to_string(),
    };

    if let Ok(val) = env::var("OSQUERYI_PATH") {
        if !val.is_empty() {
            cfg.binary_path = val;
        }
    }
    if let Ok(val) = env::var("OSQUERYI_TIMEOUT") {
        if !val.is_empty() {
            if let Some(d) = parse_duration(&val) {
                cfg.timeout = d;
            }
        }
    }
    if let Ok(val) = env::var("OSQUERYI_LOCKFILE") {
        if !val.is_empty() {
            cfg.lock_file = val;
        }
    }
    if let Ok(val) = env::var("OSQUERYI_CACHEFILE") {
        if !val.is_empty() {
            cfg.cache_file = val;
        }
    }
    if let Ok(val) = env::var("OSQUERYI_DEBUG") {
        if !val.is_empty() {
            cfg.debug = true;
        }
    }
    if let Ok(val) = env::var("OSQUERYI_LOGFILE") {
        cfg.log_file = val;
    }

    cfg
}
