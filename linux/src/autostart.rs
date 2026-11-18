//! Opening the app at login: a desktop entry in $XDG_CONFIG_HOME/autostart that starts it with
//! --background, so it waits in the tray (XDG Autostart, which GNOME and KDE follow). The daemon
//! doesn't need it: its systemd service starts with the computer.

use std::fs;
use std::io;
use std::path::PathBuf;

use crate::{APP_ID, paths};

fn file() -> PathBuf {
    std::env::var_os("XDG_CONFIG_HOME")
        .map(PathBuf::from)
        .filter(|d| d.is_absolute())
        .unwrap_or_else(|| paths::home().join(".config"))
        .join("autostart")
        .join(format!("{APP_ID}.desktop"))
}

/// The app opens at login: the entry exists and isn't turned off (desktops' own startup settings
/// may set Hidden or X-GNOME-Autostart-enabled instead of deleting it).
pub fn enabled() -> bool {
    fs::read_to_string(file())
        .is_ok_and(|entry| !entry.lines().any(|line| matches!(line.trim(), "Hidden=true" | "X-GNOME-Autostart-enabled=false")))
}

/// Points an existing entry at this app, which may have moved (a development build, then the
/// package's).
pub fn refresh() {
    if enabled() && fs::read_to_string(file()).ok() != paths::app_exe().ok().map(|exe| entry(&exe.to_string_lossy())) {
        let _ = set(true);
    }
}

pub fn set(on: bool) -> io::Result<()> {
    let file = file();
    if !on {
        return match fs::remove_file(&file) {
            Err(e) if e.kind() == io::ErrorKind::NotFound => Ok(()),
            result => result,
        };
    }
    fs::create_dir_all(file.parent().expect("autostart dir"))?;
    fs::write(&file, entry(&paths::app_exe()?.to_string_lossy()))
}

/// The desktops' startup settings list it by Comment, in their language like dev.flock.Flock.desktop.
fn entry(exe: &str) -> String {
    format!(
        "[Desktop Entry]\nType=Application\nName=Flock\nComment=Shows Flock in the tray\nComment[cs]=Zobrazí Flock v oznamovací oblasti\nComment[es]=Muestra Flock en la bandeja\nExec={} --background\nIcon={APP_ID}\nTerminal=false\nX-GNOME-Autostart-enabled=true\n",
        quote(exe)
    )
}

/// Quotes a path for Exec as the Desktop Entry spec asks: in double quotes, with ", `, $ and \
/// escaped, and the escaping backslash doubled again for the string-value level.
fn quote(path: &str) -> String {
    if path.chars().all(|c| c.is_ascii_alphanumeric() || "/._-+".contains(c)) {
        return path.to_owned();
    }
    let mut quoted = String::from("\"");
    for c in path.chars() {
        if matches!(c, '"' | '`' | '$' | '\\') {
            quoted.push_str("\\\\");
        }
        quoted.push(c);
    }
    quoted.push('"');
    quoted
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn plain_paths_stay_unquoted() {
        assert!(entry("/usr/libexec/flockphp/flock-app").contains("\nExec=/usr/libexec/flockphp/flock-app --background\n"));
    }

    #[test]
    fn quotes_paths_with_spaces_and_reserved_characters() {
        assert_eq!(quote("/home/a b/flock-app"), "\"/home/a b/flock-app\"");
        assert_eq!(quote("/home/$x/app"), "\"/home/\\\\$x/app\"");
    }
}
