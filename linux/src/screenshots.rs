//! The docs' screenshots, like macos/Sources/FlockScreenshots and windows/Flock.Screenshots:
//! `cargo run --features screenshots -- --screenshots --out <dir> [--only id,id] [--theme light|dark]`
//! opens the app's windows on the demo data in tools/screenshots/fixtures and saves them as
//! <dir>/{id}-linux-{theme}.png at 2x. Each theme runs in a process of its own
//! (ADW_DEBUG_COLOR_SCHEME). The ids are the docs' screenshot:… placeholders.
//!
//! `--screenshots --serve` only runs the fake daemon, for trying the fixtures with the real app:
//! `FLOCK_HOME=<the folder it prints> cargo run`.

use std::cell::RefCell;
use std::collections::HashSet;
use std::io::{BufRead, BufReader, Write};
use std::net::{TcpListener, TcpStream};
use std::path::{Path, PathBuf};
use std::rc::Rc;
use std::time::Duration;

use adw::prelude::*;
use gtk::{gio, glib, graphene};
use serde_json::Value;
use webkit6::prelude::WebViewExt;

use crate::{Windows, model, window};

/// The shots, in the order of macOS's Screenshots.all.
const SHOTS: [&str; 17] = [
    "settings-general-project-folders",
    "settings-sites",
    "settings-sites-https",
    "settings-sites-add-domain",
    "settings-sites-pin",
    "settings-sites-worktrees",
    "settings-sites-worktree",
    "settings-php",
    "settings-node",
    "settings-services-postgres",
    "settings-services-install",
    "settings-debugger",
    "logs-window",
    "mail-window",
    "dumps-window",
    "panel-running",
    "panel-worktrees",
];

const SCALE: f32 = 2.0;

pub fn main(args: &[String]) -> glib::ExitCode {
    let flag = |name: &str| args.iter().position(|a| a == name).and_then(|i| args.get(i + 1)).cloned();
    let home = scratch_home();
    // FLOCK_HOME and with it api.json point into a scratch folder, so the app talks to the fake daemon only.
    // SAFETY: before any thread is started.
    unsafe { std::env::set_var("FLOCK_HOME", &home) };
    // The app's preferences too: expanding a site's worktrees remembers it.
    // SAFETY: as above.
    unsafe { std::env::set_var("XDG_CONFIG_HOME", &home) };
    // English whatever the developer's system speaks, GTK's own texts included: the app's language
    // stays English here, and the Language row would offer a restart into the system's.
    // SAFETY: as above.
    unsafe {
        std::env::remove_var("LANGUAGE");
        std::env::set_var("LC_ALL", "C.UTF-8");
    }
    if args.iter().any(|a| a == "--serve") {
        serve(&home);
        println!("FLOCK_HOME={}", home.display());
        loop {
            std::thread::park();
        }
    }
    let Some(out) = flag("--out").map(PathBuf::from) else {
        eprintln!("usage: flock-app --screenshots --out <dir> [--only id,id] [--theme light|dark]");
        return glib::ExitCode::from(2);
    };
    let only: Option<HashSet<String>> = flag("--only").map(|o| o.split(',').map(str::to_owned).collect());
    if let Some(unknown) = only.as_ref().map(|o| o.iter().filter(|id| !SHOTS.contains(&id.as_str())).cloned().collect::<Vec<_>>())
        && !unknown.is_empty()
    {
        eprintln!("unknown shots: {}", unknown.join(", "));
        return glib::ExitCode::FAILURE;
    }
    let Some(theme) = flag("--theme") else {
        // A process per theme: libadwaita reads ADW_DEBUG_COLOR_SCHEME when it starts.
        let failed = ["light", "dark"].iter().any(|theme| {
            let mut command = std::process::Command::new(std::env::current_exe().expect("own path"));
            command.args(args.iter().skip(1)).args(["--theme", theme]);
            command.env("ADW_DEBUG_COLOR_SCHEME", if *theme == "dark" { "prefer-dark" } else { "prefer-light" });
            !command.status().is_ok_and(|s| s.success())
        });
        return if failed { glib::ExitCode::FAILURE } else { glib::ExitCode::SUCCESS };
    };
    serve(&home);
    std::fs::create_dir_all(&out).expect("output folder");

    let app = adw::Application::builder().application_id("dev.flock.Flock.Screenshots").flags(gio::ApplicationFlags::NON_UNIQUE).build();
    app.connect_startup(|_| crate::load_style());
    let failures = Rc::new(RefCell::new(0));
    let f = failures.clone();
    app.connect_activate(move |app| {
        let hold = app.hold();
        let shots: Vec<&'static str> = SHOTS.into_iter().filter(|id| only.as_ref().is_none_or(|o| o.contains(*id))).collect();
        let (app, out, theme, failures) = (app.clone(), out.clone(), theme.clone(), f.clone());
        glib::spawn_future_local(async move {
            let _hold = hold;
            for id in shots {
                let file = out.join(format!("{id}-linux-{theme}.png"));
                // A fresh app state per shot: no selection or open window carries over.
                let model = model::AppModel::new();
                model.start_polling();
                let windows = Rc::new(Windows::default());
                windows.main.replace(Some(window::MainWindow::new(&app, model.clone())));
                match shoot(id, &app, &model, &windows, &file).await {
                    Ok(()) => println!("{}", file.display()),
                    Err(e) => {
                        *failures.borrow_mut() += 1;
                        eprintln!("{id} ({theme}) failed: {e}");
                    }
                }
                for window in app.windows() {
                    window.destroy();
                }
            }
            app.quit();
        });
    });
    app.run_with_args(&["flock-app"]);
    if *failures.borrow() > 0 { glib::ExitCode::FAILURE } else { glib::ExitCode::SUCCESS }
}

/// Opens what a shot shows, brings it into the shot's state and saves it.
async fn shoot(id: &str, app: &adw::Application, model: &Rc<model::AppModel>, windows: &Rc<Windows>, file: &Path) -> Result<(), String> {
    let name = match id {
        "settings-general-project-folders" => "settings/general",
        "settings-sites" | "settings-sites-pin" => "settings/sites/shop",
        "settings-sites-https" | "settings-sites-add-domain" | "settings-sites-worktrees" => "settings/sites/acme",
        "settings-sites-worktree" => "settings/sites/feature-checkout.acme",
        "settings-php" => "settings/php",
        "settings-node" => "settings/node",
        "settings-services-postgres" => "settings/services/postgres",
        "settings-services-install" => "settings/services/mariadb",
        "settings-debugger" => "settings/debugger",
        "logs-window" => "logs/sites/acme",
        "mail-window" => "mail/newest",
        "dumps-window" => "dumps",
        // Linux has no panel: the Flock window's General shows the status.
        "panel-running" => "settings/general",
        // ... and the Sites list the panel's sites, with acme's worktrees.
        "panel-worktrees" => "settings/sites",
        _ => return Err(format!("no shot {id}")),
    };
    // The status first: the panes and windows show it.
    for _ in 0..50 {
        if model.status().is_some() {
            break;
        }
        settle(100).await;
    }
    let window = windows.open(app, model, name).ok_or_else(|| format!("couldn't open {name}"))?;
    // Panes load their data, lists lay out.
    settle(800).await;
    match id {
        "settings-general-project-folders" => {
            let title = find(&window, &|w| w.downcast_ref::<gtk::Label>().is_some_and(|l| l.label() == "Project folders"))
                .ok_or("no Project folders")?;
            scroll_to_end(&title)?;
        }
        "settings-sites-worktrees" => {
            let title =
                find(&window, &|w| w.downcast_ref::<gtk::Label>().is_some_and(|l| l.label() == "Worktrees")).ok_or("no Worktrees group")?;
            scroll_to_end(&title)?;
        }
        "panel-worktrees" => {
            let row = find(&window, &|w| w.downcast_ref::<adw::ExpanderRow>().is_some_and(|r| r.title() == "acme"))
                .ok_or("no acme with worktrees")?;
            row.downcast::<adw::ExpanderRow>().expect("expander row").set_expanded(true);
        }
        "settings-sites-add-domain" => {
            let row = find(&window, &|w| w.downcast_ref::<adw::ActionRow>().is_some_and(|r| r.title() == "Add Domain…"))
                .ok_or("no Add Domain… row")?;
            ActionRowExt::activate(&row.downcast::<adw::ActionRow>().expect("row"));
            settle(500).await;
            let dialog = find(&window, &|w| w.is::<adw::AlertDialog>()).ok_or("no Add Domain dialog")?;
            let entry = find(&dialog, &|w| w.is::<gtk::Entry>()).ok_or("no domain entry")?;
            let entry = entry.downcast::<gtk::Entry>().expect("entry");
            entry.set_text("admin");
            entry.set_position(-1);
        }
        "settings-sites-pin" => {
            let button = find(&window, &|w| {
                w.downcast_ref::<gtk::MenuButton>().is_some_and(|b| b.icon_name().as_deref() == Some("view-pin-symbolic"))
            })
            .ok_or("no pin menu")?;
            button.downcast::<gtk::MenuButton>().expect("menu button").popup();
        }
        _ => {}
    }
    // Web views render, dialogs and menus finish opening.
    settle(1000).await;
    // Web views draw themselves elsewhere: their own snapshots go on top, where they are.
    let mut pages = Vec::new();
    if let Some(content) = window.child() {
        for web in all(&content, &|w| w.is::<webkit6::WebView>() && w.is_drawable()) {
            let web = web.downcast::<webkit6::WebView>().expect("web view");
            let texture = web
                .snapshot_future(webkit6::SnapshotRegion::Visible, webkit6::SnapshotOptions::NONE)
                .await
                .map_err(|e| format!("web view snapshot: {e}"))?;
            let bounds = web.compute_bounds(&content).ok_or("web view outside the window")?;
            pages.push((texture, bounds));
        }
    }
    save(&window, &pages, file)
}

/// Scrolls the pane a widget is in to its end.
fn scroll_to_end(widget: &gtk::Widget) -> Result<(), String> {
    let scrolled = widget.ancestor(gtk::ScrolledWindow::static_type()).ok_or("no scrolled pane")?;
    let adjustment = scrolled.downcast_ref::<gtk::ScrolledWindow>().expect("scrolled").vadjustment();
    adjustment.set_value(adjustment.upper() - adjustment.page_size());
    // Laid out now: behind a locked screen, GNOME sends the window no frames to do it in.
    let parent = scrolled.parent().ok_or("no pane")?;
    let bounds = scrolled.compute_bounds(&parent).ok_or("pane not laid out")?;
    let allocation = gtk::Allocation::new(bounds.x() as i32, bounds.y() as i32, scrolled.width(), scrolled.height());
    scrolled.size_allocate(&allocation, -1);
    Ok(())
}

/// Waits for the main loop to handle what is pending, and then some.
async fn settle(ms: u64) {
    glib::timeout_future(Duration::from_millis(ms)).await;
}

/// Every widget under root, depth first, that matches.
fn all(root: &impl IsA<gtk::Widget>, matches: &dyn Fn(&gtk::Widget) -> bool) -> Vec<gtk::Widget> {
    let mut found = Vec::new();
    let mut child = root.as_ref().first_child();
    while let Some(widget) = child {
        if matches(&widget) {
            found.push(widget.clone());
        }
        found.extend(all(&widget, matches));
        child = widget.next_sibling();
    }
    found
}

/// The first widget under root, depth first, that matches.
fn find(root: &impl IsA<gtk::Widget>, matches: &dyn Fn(&gtk::Widget) -> bool) -> Option<gtk::Widget> {
    let mut child = root.as_ref().first_child();
    while let Some(widget) = child {
        if matches(&widget) {
            return Some(widget);
        }
        if let Some(found) = find(&widget, matches) {
            return Some(found);
        }
        child = widget.next_sibling();
    }
    None
}

/// Renders the window's content at 2x into a PNG, with its web views' pages and an open popover
/// (a surface of its own) drawn on top where they are. The corners are left square: the website
/// rounds the frame.
fn save(window: &gtk::Window, pages: &[(gtk::gdk::Texture, graphene::Rect)], file: &Path) -> Result<(), String> {
    let content = window.child().ok_or("the window has no content")?;
    let (width, height) = (content.width() as f32, content.height() as f32);
    let snapshot = gtk::Snapshot::new();
    snapshot.scale(SCALE, SCALE);
    // The window draws its background itself, not its content.
    snapshot.append_color(&theme_color(window, "window_bg_color"), &graphene::Rect::new(0.0, 0.0, width, height));
    // Drawn now, not as of the last frame: a compositor that doesn't show the window sends no frames.
    window.snapshot_child(&content, &snapshot);
    for (texture, bounds) in pages {
        snapshot.append_texture(texture, bounds);
    }
    let popover = find(window, &|w| w.is::<gtk::Popover>() && w.is_visible());
    if let Some(popover) = popover.and_then(|p| p.downcast::<gtk::Popover>().ok())
        && let Some(parent) = popover.parent()
    {
        draw_popover(&snapshot, &content, &popover, &parent, width);
    }
    let node = snapshot.to_node().ok_or("the window drew nothing")?;
    let renderer = window.native().and_then(|n| n.renderer()).ok_or("the window has no renderer")?;
    let bounds = graphene::Rect::new(0.0, 0.0, width * SCALE, height * SCALE);
    let png = renderer.render_texture(&node, Some(&bounds)).save_to_png_bytes();
    std::fs::write(file, with_scale(&png)).map_err(|e| e.to_string())
}

/// The PNG with its resolution (a pHYs chunk after IHDR) at 96 dpi per 1x, as the other platforms'
/// screenshots record it: the website shows each at its size in CSS pixels.
fn with_scale(png: &[u8]) -> Vec<u8> {
    // The signature (8 bytes), then IHDR: length, type, 13 bytes of data, CRC.
    let after_header = 8 + 4 + 4 + 13 + 4;
    let per_meter = (96.0 * SCALE as f64 / 0.0254).round() as u32;
    let mut chunk = b"pHYs".to_vec();
    chunk.extend(per_meter.to_be_bytes());
    chunk.extend(per_meter.to_be_bytes());
    chunk.push(1); // the meter
    let mut out = png[..after_header].to_vec();
    out.extend(9u32.to_be_bytes());
    out.extend(&chunk);
    out.extend(crc32(&chunk).to_be_bytes());
    out.extend(&png[after_header..]);
    out
}

/// PNG's CRC-32 (ISO 3309).
fn crc32(data: &[u8]) -> u32 {
    let mut crc = !0u32;
    for byte in data {
        crc ^= u32::from(*byte);
        for _ in 0..8 {
            crc = if crc & 1 == 1 { (crc >> 1) ^ 0xEDB8_8320 } else { crc >> 1 };
        }
    }
    !crc
}

/// A popover's content under the widget it points at, on a card like the one GTK draws around it.
fn draw_popover(snapshot: &gtk::Snapshot, content: &gtk::Widget, popover: &gtk::Popover, parent: &gtk::Widget, width: f32) {
    let Some(child) = popover.child() else { return };
    let Some(anchor) = parent.compute_bounds(content) else { return };
    let (w, h) = (child.width() as f32, child.height() as f32);
    let padding = 6.0;
    let card = graphene::Rect::new(0.0, 0.0, w + 2.0 * padding, h + 2.0 * padding);
    let x = (anchor.x() + anchor.width() / 2.0 - card.width() / 2.0).clamp(8.0, width - card.width() - 8.0);
    let y = anchor.y() + anchor.height() + 4.0;
    snapshot.save();
    snapshot.translate(&graphene::Point::new(x, y));
    let rounded = gtk::gsk::RoundedRect::from_rect(card, 12.0);
    snapshot.append_outset_shadow(&rounded, &gtk::gdk::RGBA::new(0.0, 0.0, 0.0, 0.25), 0.0, 2.0, 1.0, 12.0);
    snapshot.push_rounded_clip(&rounded);
    snapshot.append_color(&theme_color(popover, "popover_bg_color"), &card);
    snapshot.pop();
    snapshot.translate(&graphene::Point::new(padding, padding));
    // Under the popover's own contents node, which draws the frame this card stands in for.
    if let Some(parent) = child.parent() {
        parent.snapshot_child(&child, snapshot);
    }
    snapshot.restore();
}

/// One of the theme's named colors, e.g. window_bg_color, as the theme on this desktop has it.
#[allow(deprecated)]
fn theme_color(widget: &impl IsA<gtk::Widget>, name: &str) -> gtk::gdk::RGBA {
    widget.as_ref().style_context().lookup_color(name).unwrap_or(gtk::gdk::RGBA::WHITE)
}

fn scratch_home() -> PathBuf {
    let home = std::env::temp_dir().join(format!("flock-screenshots-{}", std::process::id()));
    std::fs::create_dir_all(&home).expect("scratch folder");
    home
}

/// tools/screenshots/fixtures in this checkout.
fn fixtures() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).join("../tools/screenshots/fixtures")
}

/// Answers the app's API calls from fixture files, on a loopback port it announces in
/// FLOCK_HOME/api.json. GET /v1/php/versions reads php_versions.json (.json, .txt or .html).
/// Changes (POST, PATCH, DELETE) are refused: a shot shows state.
fn serve(home: &Path) {
    let listener = TcpListener::bind("127.0.0.1:0").expect("fake daemon");
    let port = listener.local_addr().expect("fake daemon's port").port();
    std::fs::write(home.join("api.json"), format!(r#"{{"url":"http://127.0.0.1:{port}","token":"screenshots"}}"#)).expect("api.json");
    std::thread::spawn(move || {
        for stream in listener.incoming().flatten() {
            std::thread::spawn(move || answer(stream));
        }
    });
}

fn answer(mut stream: TcpStream) {
    let mut reader = BufReader::new(stream.try_clone().expect("stream"));
    let mut line = String::new();
    if reader.read_line(&mut line).is_err() {
        return;
    }
    // The rest of the head; the app's GETs have no body worth reading.
    let mut header = String::new();
    while reader.read_line(&mut header).is_ok_and(|n| n > 0) && header != "\r\n" {
        header.clear();
    }
    let mut parts = line.split_whitespace();
    let (method, target) = (parts.next().unwrap_or(""), parts.next().unwrap_or(""));
    let path = target.split('?').next().unwrap_or("");
    let (status, kind, body) = if method != "GET" {
        (501, "application/json", format!(r#"{{"error":"screenshots don't change anything ({method} {path})"}}"#))
    } else if let Some((kind, body)) = fixture(path) {
        (200, kind, body)
    } else {
        (404, "application/json", format!(r#"{{"error":"no fixture for {path}"}}"#))
    };
    if status != 200 {
        eprintln!("  fake daemon: {status} {method} {path}");
    }
    let head = format!(
        "HTTP/1.1 {status} {}\r\nContent-Type: {kind}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
        if status == 200 { "OK" } else { "Error" },
        body.len()
    );
    let _ = stream.write_all(head.as_bytes()).and_then(|_| stream.write_all(body.as_bytes()));
}

fn fixture(path: &str) -> Option<(&'static str, String)> {
    let name = decode(path).trim_start_matches('/').replacen("v1/", "", 1).replace('/', "_");
    for (ext, kind) in [("json", "application/json"), ("txt", "text/plain"), ("html", "text/html")] {
        let Ok(text) = std::fs::read_to_string(fixtures().join(format!("{name}.{ext}"))) else { continue };
        let text = times(&text);
        return Some((kind, if ext == "json" { adapt(&text, &name) } else { paths(&text) }));
    }
    None
}

/// %XX escapes in a path.
fn decode(path: &str) -> String {
    let bytes = path.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'%'
            && let Some(byte) = path.get(i + 1..i + 3).and_then(|h| u8::from_str_radix(h, 16).ok())
        {
            out.push(byte);
            i += 3;
        } else {
            out.push(bytes[i]);
            i += 1;
        }
    }
    String::from_utf8_lossy(&out).into_owned()
}

/// "{{now}}", "{{now-40s}}", "{{now-3h}}" → ISO 8601 times relative to now.
fn times(text: &str) -> String {
    let mut out = String::with_capacity(text.len());
    let mut rest = text;
    while let Some(start) = rest.find("{{now") {
        out.push_str(&rest[..start]);
        let Some(end) = rest[start..].find("}}") else { break };
        let offset = &rest[start + 5..start + end];
        let seconds = offset.strip_prefix('-').and_then(|o| {
            let (amount, unit) = o.split_at(o.len().checked_sub(1)?);
            let unit = match unit {
                "s" => 1,
                "m" => 60,
                "h" => 3600,
                "d" => 86400,
                _ => return None,
            };
            Some(amount.parse::<i64>().ok()? * unit)
        });
        let time = glib::DateTime::now_utc().and_then(|now| now.add_seconds(-(seconds.unwrap_or(0) as f64)));
        out.push_str(&time.and_then(|t| t.format_iso8601()).map(|t| t.to_string()).unwrap_or_default());
        rest = &rest[start + end + 2..];
    }
    out.push_str(rest);
    out
}

/// The fixtures come from a Windows machine: what a Linux daemon answers instead (paths, PHP-FPM
/// pools, the DNS server's port, no hosts file, Typesense, PHP from 8.1 on).
fn adapt(text: &str, name: &str) -> String {
    let Ok(mut value) = serde_json::from_str::<Value>(text) else { return paths(text) };
    strings(&mut value, &|s| names(&paths(s)));
    match name {
        // The DNS server listens on 53535 (config.json's port) for systemd-resolved, not on 53.
        "status" => {
            if let Some(dns) = value.get_mut("dns").and_then(Value::as_object_mut) {
                dns.insert("addr".into(), "127.0.0.1:53535".into());
            }
        }
        // Windows resolves *.test through the hosts file as well; Linux has its resolver only.
        "services" => {
            if let Some(services) = value.as_array_mut() {
                services.retain(|s| s["id"] != "hosts");
                for service in services {
                    offered(service);
                    if service["id"] == "dns" {
                        service["port"] = 53535.into();
                    }
                }
            }
        }
        // PHP 7.4 and 8.0 are Windows-only.
        "php_versions" => {
            if let Some(versions) = value.get_mut("versions").and_then(Value::as_array_mut) {
                versions.retain(|v| !matches!(v["minor"].as_str(), Some("7.4" | "8.0")));
            }
        }
        "catalog" => {
            if let Some(services) = value.get_mut("services").and_then(Value::as_array_mut) {
                services.iter_mut().for_each(offered);
            }
        }
        _ => {}
    }
    serde_json::to_string(&value).unwrap_or_else(|_| text.to_owned())
}

/// Typesense has no Windows build; on Linux it is offered like the others (Redis isn't).
fn offered(service: &mut Value) {
    if service["unavailable"].as_str().is_some_and(|r| r.contains("Windows")) {
        service["available"] = true.into();
        service["unavailable"] = "".into();
    }
}

fn strings(value: &mut Value, transform: &dyn Fn(&str) -> String) {
    match value {
        Value::String(s) => *s = transform(s),
        Value::Array(items) => items.iter_mut().for_each(|v| strings(v, transform)),
        Value::Object(map) => map.values_mut().for_each(|v| strings(v, transform)),
        _ => {}
    }
}

/// C:\Users\demo\Code\acme → /home/demo/Code/acme, and the data folder in ~/.local/share. Also
/// inside escaped JSON (a log entry's details), where each \ is doubled.
fn paths(text: &str) -> String {
    let mut out = String::with_capacity(text.len());
    let mut rest = text;
    while let Some(start) = rest.find("C:\\") {
        out.push_str(&rest[..start]);
        let after = &rest[start + 2..];
        let slashes = after.len() - after.trim_start_matches('\\').len();
        let separator = &after[..slashes];
        let prefix = format!("{separator}Users{separator}demo");
        if !after.starts_with(&prefix) {
            out.push_str("C:");
            rest = after;
            continue;
        }
        // The path runs to the first character a path in these fixtures doesn't have.
        let path_end = after.find(|c: char| c.is_whitespace() || "\"'(),:;<>".contains(c)).unwrap_or(after.len());
        let path = after[prefix.len()..path_end].replace(separator, "/");
        let path = format!("/home/demo{path}").replace("/home/demo/AppData/Roaming/Flock", "/home/demo/.local/share/flock");
        out.push_str(&path);
        rest = &after[path_end..];
    }
    out.push_str(rest);
    out
}

/// php-cgi workers (php8.5-cgi-1) are PHP-FPM pools on Linux (php8.5-fpm).
fn names(text: &str) -> String {
    let mut out = String::with_capacity(text.len());
    let mut rest = text;
    while let Some(start) = rest.find("-cgi") {
        let before = &rest[..start];
        // php<minor> right before it.
        let minor =
            before.rfind("php").map(|p| &before[p + 3..]).filter(|m| !m.is_empty() && m.chars().all(|c| c.is_ascii_digit() || c == '.'));
        if minor.is_none() {
            out.push_str(&rest[..start + 4]);
            rest = &rest[start + 4..];
            continue;
        }
        out.push_str(before);
        out.push_str("-fpm");
        rest = &rest[start + 4..];
        // A worker's number: -1, -2…
        if let Some(number) = rest.strip_prefix('-')
            && number.starts_with(|c: char| c.is_ascii_digit())
        {
            rest = number.trim_start_matches(|c: char| c.is_ascii_digit());
        }
    }
    out.push_str(rest);
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn windows_paths_become_linux_ones() {
        assert_eq!(paths(r"C:\Users\demo\Code\acme"), "/home/demo/Code/acme");
        assert_eq!(paths(r"C:\Users\demo\AppData\Roaming\Flock\php\8.5\php.ini"), "/home/demo/.local/share/flock/php/8.5/php.ini");
        assert_eq!(paths(r#"at C:\\Users\\demo\\Code\\acme\\app.php(12): x"#), "at /home/demo/Code/acme/app.php(12): x");
        assert_eq!(paths(r"C:\Windows"), r"C:\Windows");
    }

    #[test]
    fn php_cgi_workers_become_fpm_pools() {
        assert_eq!(names("php8.5-cgi-1"), "php8.5-fpm");
        assert_eq!(names("php8.4-cgi"), "php8.4-fpm");
        assert_eq!(names("fast-cgi"), "fast-cgi");
    }

    #[test]
    fn the_scale_is_recorded_as_the_resolution() {
        // A 1×1 PNG: the signature and IHDR, then IEND.
        let mut png = b"\x89PNG\r\n\x1a\n\0\0\0\x0dIHDR".to_vec();
        png.extend([0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0]);
        png.extend([0x1f, 0x15, 0xc4, 0x89]);
        png.extend(b"\0\0\0\0IEND\xae\x42\x60\x82");
        let scaled = with_scale(&png);
        let at = 33;
        assert_eq!(&scaled[at..at + 8], b"\0\0\0\x09pHYs");
        // 192 dpi: 7559 pixels per meter, on both axes, in meters.
        assert_eq!(&scaled[at + 8..at + 17], [0, 0, 0x1d, 0x87, 0, 0, 0x1d, 0x87, 1]);
        assert_eq!(crc32(b"IEND"), 0xae42_6082);
        assert!(scaled.ends_with(b"IEND\xae\x42\x60\x82"));
    }

    #[test]
    fn times_are_relative_to_now() {
        let text = times(r#"{"a":"{{now-3h}}","b":"{{now}}"}"#);
        assert!(!text.contains("{{"), "{text}");
        assert!(text.contains('T'), "{text}");
    }
}
