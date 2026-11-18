//! The app's own preferences, in $XDG_CONFIG_HOME/flock/app.json (what UserDefaults holds on
//! macOS). `flock uninstall` removes the folder.

use std::path::PathBuf;

use serde::{Deserialize, Serialize};

use crate::i18n::tr_noop;
use crate::paths;

#[derive(Serialize, Deserialize, Default, Clone, PartialEq, Debug)]
#[serde(default)]
pub struct Prefs {
    /// Where the Dumps window opens source files: an id of EDITORS.
    pub editor: String,
    /// The sites whose worktrees the Sites list shows, by name; the others are collapsed.
    pub expanded_sites: Vec<String>,
    /// The app's language: "system" (also when unset) or a code of i18n::LANGUAGES. It applies
    /// from the next start.
    pub language: String,
}

/// Editors the Dumps window can open files in: id, name and URL scheme ("" for none).
pub const EDITORS: [(&str, &str); 5] =
    [("phpstorm", "PhpStorm"), ("vscode", "VS Code"), ("cursor", "Cursor"), ("zed", "Zed"), ("none", tr_noop!("settings.editor.none"))];

pub fn dir() -> PathBuf {
    std::env::var_os("XDG_CONFIG_HOME")
        .map(PathBuf::from)
        .filter(|d| d.is_absolute())
        .unwrap_or_else(|| paths::home().join(".config"))
        .join("flock")
}

fn file() -> PathBuf {
    dir().join("app.json")
}

pub fn load() -> Prefs {
    let mut prefs: Prefs = std::fs::read(file()).ok().and_then(|data| serde_json::from_slice(&data).ok()).unwrap_or_default();
    if prefs.editor.is_empty() {
        prefs.editor = "phpstorm".to_owned();
    }
    prefs
}

/// Remembers whether a site's worktrees are shown in the Sites list.
pub fn set_expanded(site: &str, expanded: bool) {
    let mut prefs = load();
    prefs.expanded_sites.retain(|s| s != site);
    if expanded {
        prefs.expanded_sites.push(site.to_owned());
    }
    let _ = save(&prefs);
}

pub fn save(prefs: &Prefs) -> std::io::Result<()> {
    std::fs::create_dir_all(dir())?;
    std::fs::write(file(), serde_json::to_vec_pretty(prefs).expect("prefs"))
}
