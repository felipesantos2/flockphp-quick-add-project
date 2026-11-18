//! Where Flock keeps its files, as the CLI resolves them (internal/paths): FLOCK_HOME, else
//! $XDG_DATA_HOME/flock, else ~/.local/share/flock.

use std::env;
use std::path::PathBuf;

pub fn root() -> PathBuf {
    if let Some(home) = env::var_os("FLOCK_HOME").filter(|h| !h.is_empty()) {
        return PathBuf::from(home);
    }
    if let Some(data) = env::var_os("XDG_DATA_HOME").map(PathBuf::from).filter(|d| d.is_absolute()) {
        return data.join("flock");
    }
    home().join(".local/share/flock")
}

pub fn home() -> PathBuf {
    env::var_os("HOME").map(PathBuf::from).unwrap_or_else(|| PathBuf::from("/"))
}

/// The daemon's address and token (api.json, readable only by the user).
pub fn api_json() -> PathBuf {
    root().join("api.json")
}

/// The app's own binary. A package update that replaced it while it ran leaves /proc/self/exe
/// naming it "<path> (deleted)"; the new binary is at the path.
pub fn app_exe() -> std::io::Result<PathBuf> {
    let exe = env::current_exe()?;
    Ok(exe.to_str().and_then(|e| e.strip_suffix(" (deleted)")).map_or(exe.clone(), PathBuf::from))
}

/// The flock binary `flock install` copied into the data dir; the app runs it for restart and setup.
pub fn flock_binary() -> PathBuf {
    root().join("bin/flock")
}
