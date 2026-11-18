//! The Dumps window's page: requests and CLI runs newest first, each with its dumps, queries,
//! logs, views and jobs in the order they happened, every entry with its source and time. The
//! macOS app's DumpTimeline.

use serde::Deserialize;
use serde_json::Value;

use crate::i18n::tr;
use crate::ui;

/// A captured dump (GET /v1/dumps, dumps.Entry).
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct Dump {
    pub id: String,
    pub time: String,
    pub label: Option<String>,
    pub site: String,
    pub source: Option<Source>,
    pub request: Option<Request>,
    pub command_line: Option<String>,
    pub group: String,
    pub text: String,
    /// The dump's body; render it after the shared header (GET /v1/dumps/header).
    pub html: String,
}

/// What the PHP agent captured besides dumps (GET /v1/events, capture.Event).
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct Event {
    pub id: String,
    /// query, log, view, job or request.
    pub kind: String,
    pub time: String,
    pub site: String,
    pub group: String,
    pub request: Option<Request>,
    pub command_line: Option<String>,
    pub source: Option<Source>,
    pub sql: String,
    pub bindings: Option<Vec<Value>>,
    pub connection: String,
    pub level: String,
    pub message: String,
    pub context: Option<Value>,
    pub view: String,
    pub path: String,
    pub job: String,
    pub queue: String,
    pub state: String,
    pub exception: String,
    pub status_code: Option<u16>,
    pub duration_ms: Option<f64>,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct Source {
    pub file: String,
    pub line: u32,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct Request {
    pub method: String,
    pub uri: String,
}

/// One entry of the timeline.
#[derive(Debug, Clone, PartialEq)]
pub enum Item {
    Dump(Box<Dump>),
    Event(Box<Event>),
}

impl Item {
    fn time(&self) -> &str {
        match self {
            Item::Dump(d) => &d.time,
            Item::Event(e) => &e.time,
        }
    }

    fn group(&self) -> &str {
        match self {
            Item::Dump(d) => &d.group,
            Item::Event(e) => &e.group,
        }
    }

    fn site(&self) -> &str {
        match self {
            Item::Dump(d) => &d.site,
            Item::Event(e) => &e.site,
        }
    }

    fn request(&self) -> Option<&Request> {
        match self {
            Item::Dump(d) => d.request.as_ref(),
            Item::Event(e) => e.request.as_ref(),
        }
    }

    fn command_line(&self) -> Option<&str> {
        match self {
            Item::Dump(d) => d.command_line.as_deref(),
            Item::Event(e) => e.command_line.as_deref(),
        }
    }

    /// The finished request's own event, shown in its group's header rather than as a row.
    fn is_request(&self) -> bool {
        matches!(self, Item::Event(e) if e.kind == "request")
    }

    /// Everything a search looks at.
    fn search_text(&self) -> Vec<String> {
        let origin = origin(self.request(), self.command_line());
        match self {
            Item::Dump(d) => vec![
                d.text.clone(),
                d.source.as_ref().map(|s| s.file.clone()).unwrap_or_default(),
                origin,
                d.label.clone().unwrap_or_default(),
            ],
            Item::Event(e) => vec![
                e.sql.clone(),
                e.message.clone(),
                e.level.clone(),
                e.view.clone(),
                e.path.clone(),
                e.job.clone(),
                e.queue.clone(),
                e.state.clone(),
                e.exception.clone(),
                e.connection.clone(),
                e.source.as_ref().map(|s| s.file.clone()).unwrap_or_default(),
                origin,
                e.context.as_ref().map(Value::to_string).unwrap_or_default(),
            ],
        }
    }
}

/// Dumps and events together, newest first (both come newest first from the API).
pub fn merge(dumps: &[Dump], events: &[Event]) -> Vec<Item> {
    let mut items: Vec<(usize, Item)> = dumps
        .iter()
        .cloned()
        .map(|d| Item::Dump(Box::new(d)))
        .chain(events.iter().cloned().map(|e| Item::Event(Box::new(e))))
        .enumerate()
        .collect();
    items.sort_by(|(ia, a), (ib, b)| micros(b.time()).cmp(&micros(a.time())).then(ia.cmp(ib)));
    items.into_iter().map(|(_, item)| item).collect()
}

fn micros(time: &str) -> i64 {
    gtk::glib::DateTime::from_iso8601(time, None).map(|t| t.to_unix() * 1_000_000 + i64::from(t.microsecond())).unwrap_or_default()
}

/// The entries of the most recent request or CLI run.
pub fn latest_group(items: &[Item]) -> Vec<Item> {
    let Some(group) = items.first().map(|i| i.group().to_owned()) else { return Vec::new() };
    items.iter().filter(|i| i.group() == group).cloned().collect()
}

/// Case-insensitive match on the dumped value or the event's SQL, message, view, job, ..., its
/// source file and origin.
pub fn filter(items: Vec<Item>, query: &str) -> Vec<Item> {
    let query = query.trim().to_lowercase();
    if query.is_empty() {
        return items;
    }
    items.into_iter().filter(|i| i.search_text().iter().any(|t| t.to_lowercase().contains(&query))).collect()
}

/// The page for items (newest first, see merge). A request's entries stay together even when
/// another request's land between them.
pub fn html(items: &[Item], header: &str, editor: &str) -> String {
    let mut groups: Vec<(String, Vec<&Item>)> = Vec::new();
    for item in items {
        match groups.iter_mut().find(|(g, _)| !item.group().is_empty() && g == item.group()) {
            Some((_, list)) => list.push(item),
            None => groups.push((item.group().to_owned(), vec![item])),
        }
    }
    let mut body = String::new();
    for (_, list) in groups {
        let chronological: Vec<&Item> = list.into_iter().rev().collect();
        let finished = chronological.iter().rev().find_map(|i| match i {
            Item::Event(e) if e.kind == "request" => Some(e),
            _ => None,
        });
        let Some(first) = chronological.iter().find(|i| i.request().is_some() || i.command_line().is_some()).or(chronological.first())
        else {
            continue;
        };
        let site = chronological.iter().map(|i| i.site()).find(|s| !s.is_empty()).unwrap_or_default();
        body.push_str("<section class=\"group\">\n<header class=\"origin\">");
        body.push_str(&origin_html(first.request(), first.command_line()));
        if !site.is_empty() {
            body.push_str(&format!("<span class=\"site\">{}</span>", escape(site)));
        }
        if let Some(e) = finished {
            body.push_str(&request_html(e));
        }
        body.push_str(&format!("<time>{}</time></header>\n", ui::clock(chronological[0].time())));
        for item in chronological.iter().filter(|i| !i.is_request()) {
            match item {
                Item::Dump(d) => body.push_str(&format!(
                    "<article class=\"dump\">\n<div class=\"meta\">{}<time>{}</time></div>\n{}\n</article>\n",
                    source(d.source.as_ref(), "dump", editor),
                    ui::clock(&d.time),
                    d.html
                )),
                Item::Event(e) => body.push_str(&event_html(e, editor)),
            }
        }
        body.push_str("</section>\n");
    }
    format!("<!doctype html><html><head><meta charset=\"utf-8\">\n{header}\n<style>{STYLE}</style>\n</head><body>\n{body}\n</body></html>")
}

/// A compact row per event: what kind it is, the gist on one line, details below.
fn event_html(e: &Event, editor: &str) -> String {
    let (tag, main);
    let (mut extra, mut details) = (String::new(), String::new());
    let mut meta: Vec<String> = Vec::new();
    match e.kind.as_str() {
        "query" => {
            tag = "<span class=\"kind query\">SQL</span>".to_owned();
            main = format!(
                "<code class=\"sql\" title=\"{}\">{}</code>",
                escape(&e.sql),
                escape(&interpolate(&e.sql, e.bindings.as_deref().unwrap_or_default()))
            );
            if !e.connection.is_empty() {
                meta.push(escape(&e.connection));
            }
        }
        "log" => {
            let level = if e.level.is_empty() { "log".to_owned() } else { e.level.to_lowercase() };
            tag = format!("<span class=\"kind level-{}\">{}</span>", level_class(&level), escape(&level.to_uppercase()));
            main = format!("<span class=\"message\">{}</span>", escape(&e.message));
            if let Some(context) = e.context.as_ref().filter(|c| c.as_object().is_some_and(|o| !o.is_empty())) {
                details = format!(
                    "<details><summary>{}</summary><pre class=\"json\">{}</pre></details>",
                    escape(tr!("dumps.timeline.context")),
                    escape(&serde_json::to_string_pretty(context).unwrap_or_default())
                );
            }
        }
        "view" => {
            tag = "<span class=\"kind view\">VIEW</span>".to_owned();
            main = format!("<span class=\"name\">{}</span>", escape(&e.view));
            if !e.path.is_empty() {
                extra = source(Some(&Source { file: e.path.clone(), line: 0 }), "", editor);
            }
        }
        "job" => {
            let state = if e.state.is_empty() { "job".to_owned() } else { e.state.to_lowercase() };
            tag = "<span class=\"kind job\">JOB</span>".to_owned();
            main =
                format!("<span class=\"name\">{}</span><span class=\"state {}\">{}</span>", escape(&e.job), escape(&state), escape(&state));
            if !e.queue.is_empty() {
                meta.push(escape(&tr!("dumps.timeline.queue_name", name = e.queue)));
            }
            if !e.connection.is_empty() {
                meta.push(escape(&e.connection));
            }
            if !e.exception.is_empty() {
                details = format!("<pre class=\"exception\">{}</pre>", escape(&e.exception));
            }
        }
        _ => return String::new(),
    }
    if let Some(ms) = e.duration_ms.filter(|_| e.kind != "log" && e.kind != "view") {
        meta.push(format!("<span class=\"duration\">{}</span>", escape(&duration(ms))));
    }
    let src = e.source.as_ref().map(|s| source(Some(s), "", editor)).unwrap_or_default();
    format!(
        "<article class=\"event {}\">\n<div class=\"line\">{tag}{main}{extra}<span class=\"info\">{}</span>{src}<time>{}</time></div>{details}\n</article>\n",
        escape(&e.kind),
        meta.join(" · "),
        ui::clock(&e.time)
    )
}

/// The SQL with its bindings in place of the `?`s, for reading. When the count doesn't match
/// (named bindings, a literal "?"), the bindings are listed after it instead.
pub fn interpolate(sql: &str, bindings: &[Value]) -> String {
    if bindings.is_empty() {
        return sql.to_owned();
    }
    let parts: Vec<&str> = sql.split('?').collect();
    if parts.len() != bindings.len() + 1 {
        return format!("{sql}  -- {}", bindings.iter().map(literal).collect::<Vec<_>>().join(", "));
    }
    let mut out = parts[0].to_owned();
    for (binding, part) in bindings.iter().zip(&parts[1..]) {
        out.push_str(&literal(binding));
        out.push_str(part);
    }
    out
}

fn literal(value: &Value) -> String {
    match value {
        Value::Null => "NULL".to_owned(),
        Value::Bool(b) => if *b { "1" } else { "0" }.to_owned(),
        Value::Number(n) => n.to_string(),
        Value::String(s) => format!("'{}'", s.replace('\'', "''")),
        other => format!("'{other}'"),
    }
}

/// "0.42 ms", "31 ms", "1.24 s".
pub fn duration(ms: f64) -> String {
    match ms {
        ms if ms >= 1000.0 => format!("{:.2} s", ms / 1000.0),
        ms if ms >= 10.0 => format!("{ms:.0} ms"),
        ms => format!("{ms:.2} ms"),
    }
}

/// PSR-3 levels mapped to the three colors the theme has.
fn level_class(level: &str) -> &'static str {
    match level {
        "emergency" | "alert" | "critical" | "error" => "error",
        "warning" | "notice" => "warning",
        _ => "info",
    }
}

/// The status code pill and duration of a finished request.
fn request_html(e: &Event) -> String {
    let mut out = String::new();
    if let Some(code) = e.status_code {
        out.push_str(&format!("<span class=\"status s{}xx\">{code}</span>", code / 100));
    }
    if let Some(ms) = e.duration_ms {
        out.push_str(&format!("<span class=\"duration\">{}</span>", escape(&duration(ms))));
    }
    out
}

/// "GET" pill + URL for requests, "CLI" pill + command for artisan and scripts.
fn origin_html(request: Option<&Request>, command_line: Option<&str>) -> String {
    if let Some(r) = request.filter(|r| !r.uri.is_empty()) {
        let verb = r.method.to_uppercase();
        return format!(
            "<span class=\"verb {}\">{}</span><span class=\"what\">{}</span>",
            escape(&verb.to_lowercase()),
            escape(&verb),
            escape(&r.uri)
        );
    }
    if let Some(command) = command_line.filter(|c| !c.is_empty()) {
        return format!("<span class=\"verb cli\">CLI</span><span class=\"what\">{}</span>", escape(command));
    }
    "<span class=\"what\">dump</span>".to_owned()
}

/// "GET https://app.test/x" for requests, the command line for CLI runs.
fn origin(request: Option<&Request>, command_line: Option<&str>) -> String {
    if let Some(r) = request.filter(|r| !r.uri.is_empty()) {
        return format!("{} {}", r.method, r.uri);
    }
    command_line.unwrap_or_default().to_owned()
}

/// The file (and line) as an editor link; fallback when there is no file.
fn source(source: Option<&Source>, fallback: &str, editor: &str) -> String {
    let Some(source) = source.filter(|s| !s.file.is_empty()) else {
        return if fallback.is_empty() { String::new() } else { format!("<span>{}</span>", escape(fallback)) };
    };
    let line = if source.line > 0 { format!(":{}", source.line) } else { String::new() };
    let text = escape(&format!("{}{line}", short_path(&source.file)));
    match editor_url(editor, &source.file, source.line) {
        Some(url) => format!("<a class=\"src\" href=\"{}\" title=\"{}\">{text}</a>", escape(&url), escape(&source.file)),
        None => format!("<span class=\"src\" title=\"{}\">{text}</span>", escape(&source.file)),
    }
}

/// A URL that opens file (at line, when known) in the editor; None for "none".
pub fn editor_url(editor: &str, file: &str, line: u32) -> Option<String> {
    let path: String = file
        .bytes()
        .map(|b| match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'.' | b'_' | b'~' | b'/' => (b as char).to_string(),
            _ => format!("%{b:02X}"),
        })
        .collect();
    let at = if line > 0 { format!(":{line}") } else { String::new() };
    match editor {
        "phpstorm" => Some(format!("phpstorm://open?file={path}{}", if line > 0 { format!("&line={line}") } else { String::new() })),
        "vscode" => Some(format!("vscode://file{path}{at}")),
        "cursor" => Some(format!("cursor://file{path}{at}")),
        "zed" => Some(format!("zed://file{path}{at}")),
        _ => None,
    }
}

/// The path relative to the project (from app/, resources/, routes/, ... on when present).
pub fn short_path(file: &str) -> String {
    for marker in ["/app/", "/resources/", "/routes/", "/database/", "/config/", "/tests/", "/vendor/", "/bootstrap/"] {
        if let Some(i) = file.rfind(marker) {
            return file[i + 1..].to_owned();
        }
    }
    let parts: Vec<&str> = file.split('/').filter(|p| !p.is_empty()).collect();
    parts[parts.len().saturating_sub(3)..].join("/")
}

pub fn escape(s: &str) -> String {
    s.replace('&', "&amp;").replace('<', "&lt;").replace('>', "&gt;").replace('"', "&quot;")
}

/// The theme: the desktop's fonts, light and dark, cards on the window background, syntax colors
/// for Symfony's dump markup (its JS keeps working).
const STYLE: &str = r#"
:root {
  color-scheme: light dark;
  --text: #1e1e1e; --secondary: #6e6e73; --tertiary: #a1a1a6;
  --card: #ffffff; --card-border: rgba(0,0,0,.08); --shadow: 0 1px 2px rgba(0,0,0,.06);
  --header: rgba(250,250,250,.92); --separator: rgba(0,0,0,.08); --hover: rgba(0,0,0,.05);
  --accent: #3584e4; --string: #c01c28; --number: #1a5fb4; --key: #26a269; --type: #813d9c;
  --keyword: #a51d2d; --meta: #8a8a8e; --get: #2ec27e; --post: #e66100; --cli: #77767b; --error: #e01b24;
}
@media (prefers-color-scheme: dark) {
  :root {
    --text: #f6f5f4; --secondary: #9a9996; --tertiary: #77767b;
    --card: #303030; --card-border: rgba(255,255,255,.07); --shadow: 0 1px 2px rgba(0,0,0,.35);
    --header: rgba(36,36,36,.92); --separator: rgba(255,255,255,.08); --hover: rgba(255,255,255,.06);
    --accent: #78aeed; --string: #ff7b63; --number: #99c1f1; --key: #8ff0a4; --type: #dc8add;
    --keyword: #f66151; --meta: #9a9996; --get: #57e389; --post: #ffa348; --cli: #9a9996; --error: #ff6b6b;
  }
}
html, body { margin: 0; background: transparent; color: var(--text); font: 13px system-ui, sans-serif; }
body { padding: 0 0 16px; }
.group { padding: 0 14px; }
.origin { position: sticky; top: 0; z-index: 10; display: flex; gap: 8px; align-items: center;
          margin: 0 -14px; padding: 9px 16px 7px; background: var(--header);
          border-bottom: 1px solid var(--separator); font-size: 12px; color: var(--secondary); }
.origin .verb { font: 600 10px monospace; letter-spacing: .04em; padding: 2px 6px; border-radius: 5px; color: #fff; background: var(--get); }
.origin .verb.post, .origin .verb.put, .origin .verb.patch, .origin .verb.delete { background: var(--post); }
.origin .verb.cli { background: var(--cli); }
.origin .what { color: var(--text); font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
.origin .site { padding: 1px 7px; border-radius: 10px; background: var(--hover); font-size: 11px; white-space: nowrap; }
.dump { margin: 10px 0 0; background: var(--card); border: 1px solid var(--card-border); border-radius: 10px; box-shadow: var(--shadow); overflow: hidden; }
.meta { display: flex; gap: 8px; align-items: center; padding: 7px 12px; border-bottom: 1px solid var(--separator); font-size: 11.5px; color: var(--secondary); }
.meta a, .meta span.src { font: 11.5px monospace; color: var(--accent); text-decoration: none; }
.meta span.src { color: var(--secondary); }
.meta a:hover { text-decoration: underline; }
time { margin-left: auto; color: var(--tertiary); font-size: 11px; font-variant-numeric: tabular-nums; white-space: nowrap; }
pre.sf-dump, pre.sf-dump .sf-dump-default { background: transparent !important; color: var(--text) !important; z-index: auto !important; font: 12px/1.5 monospace !important; }
pre.sf-dump { margin: 0 !important; padding: 10px 12px !important; }
pre.sf-dump .sf-dump-str { color: var(--string) !important; font-weight: normal !important; }
pre.sf-dump .sf-dump-num { color: var(--number) !important; font-weight: normal !important; }
pre.sf-dump .sf-dump-const { color: var(--keyword) !important; font-weight: 600 !important; }
pre.sf-dump .sf-dump-key, pre.sf-dump .sf-dump-index { color: var(--key) !important; }
pre.sf-dump .sf-dump-note, pre.sf-dump .sf-dump-ellipsis-note { color: var(--type) !important; }
pre.sf-dump .sf-dump-public, pre.sf-dump .sf-dump-protected, pre.sf-dump .sf-dump-private { color: var(--text) !important; }
pre.sf-dump .sf-dump-meta, pre.sf-dump .sf-dump-ref, pre.sf-dump .sf-dump-ellipsis { color: var(--meta) !important; }
pre.sf-dump .sf-dump-toggle span { color: var(--tertiary); }
pre.sf-dump a.sf-dump-toggle { border-radius: 3px; }
pre.sf-dump a.sf-dump-toggle:hover { background: var(--hover); }
.sf-dump-hover:hover { background-color: var(--hover) !important; color: inherit !important; }
.origin .status { font: 600 10px monospace; padding: 2px 6px; border-radius: 5px; color: #fff; background: var(--get); white-space: nowrap; }
.origin .status.s3xx { background: var(--accent); }
.origin .status.s4xx { background: var(--post); }
.origin .status.s5xx { background: var(--error); }
.origin .duration { font-size: 11px; color: var(--secondary); font-variant-numeric: tabular-nums; white-space: nowrap; }
.event { margin: 6px 0 0; background: var(--card); border: 1px solid var(--card-border); border-radius: 8px; box-shadow: var(--shadow); overflow: hidden; }
.event + .dump { margin-top: 10px; }
.event .line { display: flex; gap: 8px; align-items: baseline; padding: 6px 12px; font-size: 12px; min-width: 0; }
.event .kind { flex: none; font: 600 9.5px monospace; letter-spacing: .04em; padding: 1px 5px; border-radius: 4px; color: var(--secondary); background: var(--hover); }
.event .kind.query { color: var(--key); }
.event .kind.view { color: var(--type); }
.event .kind.job { color: var(--keyword); }
.event .kind.level-error { color: #fff; background: var(--error); }
.event .kind.level-warning { color: #fff; background: var(--post); }
.event .kind.level-info { color: var(--accent); }
.event .sql { flex: 1 1 auto; min-width: 0; font: 11.5px/1.45 monospace; white-space: pre-wrap; word-break: break-word; }
.event .message, .event .name { flex: 0 1 auto; min-width: 0; overflow-wrap: anywhere; }
.event .name { font-weight: 500; }
.event .src { flex: none; font: 11px monospace; color: var(--accent); text-decoration: none; white-space: nowrap; }
.event span.src { color: var(--secondary); }
.event a.src:hover { text-decoration: underline; }
.event .info { flex: none; margin-left: auto; font-size: 11px; color: var(--secondary); white-space: nowrap; font-variant-numeric: tabular-nums; }
.event .info:empty { display: none; }
.event .line > time { margin-left: 0; }
.event .info:empty + .src, .event .info:empty + time { margin-left: auto; }
.event .state { flex: none; font-size: 10.5px; padding: 0 6px; border-radius: 9px; background: var(--hover); color: var(--secondary); }
.event .state.processed { color: var(--get); }
.event .state.failed { color: #fff; background: var(--error); }
.event details { border-top: 1px solid var(--separator); padding: 4px 12px 6px; font-size: 11.5px; color: var(--secondary); }
.event summary { cursor: pointer; }
.event pre.json, .event pre.exception { margin: 4px 0 0; font: 11.5px/1.45 monospace; color: var(--text); white-space: pre-wrap; word-break: break-word; }
.event pre.exception { margin: 0; padding: 6px 12px 8px; border-top: 1px solid var(--separator); color: var(--error); }
"#;

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn interpolates_bindings() {
        let bindings = vec![Value::from(5), Value::from("O'Brien"), Value::Null];
        assert_eq!(
            interpolate("select * from users where id = ? and name = ? and deleted_at is ?", &bindings),
            "select * from users where id = 5 and name = 'O''Brien' and deleted_at is NULL"
        );
        assert_eq!(interpolate("select :id", &[Value::from(1)]), "select :id  -- 1");
    }

    #[test]
    fn shortens_paths_and_links_editors() {
        assert_eq!(short_path("/home/a/acme/app/Http/Controllers/HomeController.php"), "app/Http/Controllers/HomeController.php");
        assert_eq!(short_path("/srv/x/y/z.php"), "x/y/z.php");
        assert_eq!(editor_url("vscode", "/home/a/b c.php", 4).as_deref(), Some("vscode://file/home/a/b%20c.php:4"));
        assert_eq!(editor_url("phpstorm", "/a.php", 0).as_deref(), Some("phpstorm://open?file=/a.php"));
        assert_eq!(editor_url("none", "/a.php", 1), None);
    }

    #[test]
    fn groups_newest_first() {
        let dump = |id: &str, time: &str, group: &str| {
            Item::Dump(Box::new(Dump { id: id.into(), time: time.into(), group: group.into(), ..Default::default() }))
        };
        let merged = merge(&[], &[]);
        assert!(merged.is_empty());
        let items = vec![dump("2", "2026-10-24T10:00:02Z", "b"), dump("1", "2026-10-24T10:00:01Z", "a")];
        assert_eq!(latest_group(&items).len(), 1);
        assert_eq!(filter(items.clone(), "  ").len(), 2);
    }
}
