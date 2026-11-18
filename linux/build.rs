//! Builds the app's texts into it: every po/<code>.po (generated from lang/ by tools/i18n) becomes an
//! entry of the list src/i18n.rs includes, so a new language needs no code.

use std::path::Path;
use std::{env, fs};

fn main() {
    println!("cargo::rerun-if-changed=po");
    let dir = Path::new(&env::var("CARGO_MANIFEST_DIR").expect("CARGO_MANIFEST_DIR")).join("po");
    let mut codes = Vec::new();
    for entry in fs::read_dir(&dir).expect("po") {
        let name = entry.expect("po").file_name().into_string().expect("a UTF-8 file name");
        if let Some(code) = name.strip_suffix(".po") {
            codes.push(code.to_owned());
        }
    }
    codes.sort();
    let mut list = String::from("&[\n");
    for code in &codes {
        let path = dir.join(format!("{code}.po"));
        list += &format!("    ({code:?}, include_str!({path:?})),\n");
    }
    list += "]\n";
    fs::write(Path::new(&env::var("OUT_DIR").expect("OUT_DIR")).join("catalogs.rs"), list).expect("catalogs.rs");
}
