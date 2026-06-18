use nix::sys::signal::kill;
use nix::unistd::Pid;
use std::fs::OpenOptions;
use std::io::{Read, Seek, SeekFrom, Write};

pub fn acquire_lock(lock_file: &str) -> Result<Box<dyn FnOnce() + Send + Sync>, String> {
    if lock_file == "off" {
        return Ok(Box::new(|| {}));
    }

    let mut file = OpenOptions::new()
        .read(true)
        .write(true)
        .create(true)
        .open(lock_file)
        .map_err(|e| format!("failed to open lock file: {}", e))?;

    let mut content = String::new();
    if file.read_to_string(&mut content).is_ok() && !content.is_empty() {
        if let Ok(pid_val) = content.trim().parse::<i32>() {
            if pid_val > 0 {
                let pid = Pid::from_raw(pid_val);
                // Signal None is equivalent to signal 0, checking process existence
                if kill(pid, None).is_ok() {
                    return Err(format!("server already running (PID {})", pid_val));
                }
            }
        }
    }

    // Write current PID
    file.seek(SeekFrom::Start(0)).map_err(|e| e.to_string())?;
    file.set_len(0).map_err(|e| e.to_string())?;
    let current_pid = std::process::id();
    write!(file, "{}", current_pid).map_err(|e| e.to_string())?;

    let lock_path = lock_file.to_string();
    Ok(Box::new(move || {
        drop(file);
        let _ = std::fs::remove_file(lock_path);
    }))
}
