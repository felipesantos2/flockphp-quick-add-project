import Foundation

/// Editors that dump sources can be opened in.
public enum Editor: String, CaseIterable, Sendable, Identifiable {
    case phpstorm, vscode, cursor, zed, none

    public var id: String { rawValue }

    public var name: String {
        switch self {
        case .phpstorm: "PhpStorm"
        case .vscode: "VS Code"
        case .cursor: "Cursor"
        case .zed: "Zed"
        case .none: String(localized: "settings.editor.none")
        }
    }

    /// A URL that opens file (at line, when known) in the editor.
    public func url(file: String, line: Int) -> URL? {
        var allowed = CharacterSet.urlPathAllowed
        allowed.remove(charactersIn: "?#")
        guard let path = file.addingPercentEncoding(withAllowedCharacters: allowed) else { return nil }
        let at = line > 0 ? ":\(line)" : ""

        return switch self {
        case .phpstorm: URL(string: "phpstorm://open?file=\(path)\(line > 0 ? "&line=\(line)" : "")")
        case .vscode: URL(string: "vscode://file\(path)\(at)")
        case .cursor: URL(string: "cursor://file\(path)\(at)")
        case .zed: URL(string: "zed://file\(path)\(at)")
        case .none: nil
        }
    }
}

/// One entry of the timeline: a dump, or an event the PHP agent captured.
public enum TimelineItem: Sendable, Equatable, Identifiable {
    case dump(Dump)
    case event(Event)

    public var id: String {
        switch self {
        case let .dump(d): "dump-\(d.id)"
        case let .event(e): "event-\(e.id)"
        }
    }

    public var time: Date {
        switch self {
        case let .dump(d): d.time
        case let .event(e): e.time
        }
    }

    public var group: String {
        switch self {
        case let .dump(d): d.group
        case let .event(e): e.group
        }
    }

    var site: String {
        switch self {
        case let .dump(d): d.site
        case let .event(e): e.site
        }
    }

    var request: Dump.Request? {
        switch self {
        case let .dump(d): d.request
        case let .event(e): e.request
        }
    }

    var commandLine: String? {
        switch self {
        case let .dump(d): d.commandLine
        case let .event(e): e.commandLine
        }
    }

    /// The finished request's own event, shown in its group's header rather than as a row.
    var isRequest: Bool {
        if case let .event(e) = self { return e.kind == .request }
        return false
    }

    /// Everything a search looks at.
    var searchText: [String] {
        switch self {
        case let .dump(d):
            [d.text, d.source?.file ?? "", d.origin, d.label ?? ""]
        case let .event(e):
            [e.sql, e.message, e.level, e.view, e.path, e.job, e.queue, e.state, e.exception, e.connection,
             e.source?.file ?? "", e.origin, e.context.isEmpty ? "" : JSONValue.object(e.context).json()]
        }
    }
}

/// Renders the timeline as one terminal-like page: requests newest first, each request's dumps
/// and events in the order they happened, every entry with its source, origin and time.
public enum DumpTimeline {
    /// Dumps and events together, newest first (both lists come newest first from the API).
    public static func merge(dumps: [Dump], events: [Event]) -> [TimelineItem] {
        (dumps.map(TimelineItem.dump) + events.map(TimelineItem.event))
            .enumerated()
            .sorted { $0.element.time != $1.element.time ? $0.element.time > $1.element.time : $0.offset < $1.offset }
            .map(\.element)
    }

    /// Entries belonging to the most recent request / CLI run.
    public static func latestGroup(of items: [TimelineItem]) -> [TimelineItem] {
        guard let group = items.first?.group else { return [] }
        return items.filter { $0.group == group }
    }

    /// Dumps belonging to the most recent request / CLI run.
    public static func latestGroup(of dumps: [Dump]) -> [Dump] {
        guard let group = dumps.first?.group else { return [] }
        return dumps.filter { $0.group == group }
    }

    /// Case-insensitive match on the dumped value or the event's SQL, message, view, job, ...,
    /// its source file and origin.
    public static func filter(_ items: [TimelineItem], query: String) -> [TimelineItem] {
        let q = query.trimmingCharacters(in: .whitespaces)
        guard !q.isEmpty else { return items }
        return items.filter { $0.searchText.contains { $0.localizedCaseInsensitiveContains(q) } }
    }

    public static func filter(_ dumps: [Dump], query: String) -> [Dump] {
        filter(dumps.map(TimelineItem.dump), query: query).compactMap {
            if case let .dump(d) = $0 { return d }
            return nil
        }
    }

    public static func html(dumps: [Dump], header: String, editor: Editor) -> String {
        html(items: dumps.map(TimelineItem.dump), header: header, editor: editor)
    }

    /// `items` must be newest first (see `merge`). A request's entries stay together even when
    /// another request's land between them.
    public static func html(items: [TimelineItem], header: String, editor: Editor) -> String {
        var groups: [(key: String, items: [TimelineItem])] = []
        var index: [String: Int] = [:]
        for item in items {
            if !item.group.isEmpty, let i = index[item.group] {
                groups[i].items.append(item)
            } else {
                if !item.group.isEmpty { index[item.group] = groups.count }
                groups.append((item.group, [item]))
            }
        }

        var body = ""
        for group in groups {
            let chronological = Array(group.items.reversed())
            let rows = chronological.filter { !$0.isRequest }
            let finished: Event? = chronological.lazy.compactMap {
                if case let .event(e) = $0, e.kind == .request { return e }
                return nil
            }.last
            guard let first = chronological.first(where: { $0.request != nil || $0.commandLine != nil }) ?? chronological.first else { continue }
            let site = chronological.first { !$0.site.isEmpty }?.site ?? ""
            body += """
            <section class="group">
            <header class="origin">\(originHTML(request: first.request, commandLine: first.commandLine))\
            \(site.isEmpty ? "" : "<span class=\"site\">\(escape(site))</span>")\
            \(finished.map(requestHTML) ?? "")\
            <time>\(time(chronological[0].time))</time></header>
            """
            for item in rows {
                switch item {
                case let .dump(dump):
                    body += """
                    <article class="dump">
                    <div class="meta">\(source(dump.source, fallback: "dump", editor: editor))\
                    <time>\(time(dump.time))</time></div>
                    \(dump.html)
                    </article>
                    """
                case let .event(event):
                    body += eventHTML(event, editor: editor)
                }
            }
            body += "</section>\n"
        }

        return """
        <!doctype html><html><head><meta charset="utf-8">
        \(header)
        <style>\(style)</style>
        </head><body>
        \(body)
        </body></html>
        """
    }

    /// A compact row per event: what kind it is, the gist on one line, details below.
    static func eventHTML(_ e: Event, editor: Editor) -> String {
        var tag = "", main = "", extra = "", details = ""
        var meta: [String] = []
        switch e.kind {
        case .query:
            tag = "<span class=\"kind query\">SQL</span>"
            main = "<code class=\"sql\" title=\"\(escape(e.sql))\">\(escape(interpolate(sql: e.sql, bindings: e.bindings)))</code>"
            if !e.connection.isEmpty { meta.append(escape(e.connection)) }
        case .log:
            let level = e.level.isEmpty ? "log" : e.level.lowercased()
            tag = "<span class=\"kind level-\(escape(levelClass(level)))\">\(escape(level.uppercased()))</span>"
            main = "<span class=\"message\">\(escape(e.message))</span>"
            if !e.context.isEmpty {
                details = "<details><summary>context</summary><pre class=\"json\">\(escape(JSONValue.object(e.context).json(indent: "  ")))</pre></details>"
            }
        case .view:
            tag = "<span class=\"kind view\">VIEW</span>"
            main = "<span class=\"name\">\(escape(e.view))</span>"
            if !e.path.isEmpty {
                extra = source(Dump.Source(file: e.path, line: 0), fallback: "", editor: editor)
            }
        case .job:
            let state = e.state.isEmpty ? "job" : e.state.lowercased()
            tag = "<span class=\"kind job\">JOB</span>"
            main = "<span class=\"name\">\(escape(e.job))</span><span class=\"state \(escape(state))\">\(escape(state))</span>"
            if !e.queue.isEmpty { meta.append("queue \(escape(e.queue))") }
            if !e.connection.isEmpty { meta.append(escape(e.connection)) }
            if !e.exception.isEmpty {
                details = "<pre class=\"exception\">\(escape(e.exception))</pre>"
            }
        case .request, nil:
            return ""
        }
        if let ms = e.durationMs, e.kind != .log, e.kind != .view {
            meta.append("<span class=\"duration\">\(escape(duration(ms)))</span>")
        }
        let src = e.source.map { source($0, fallback: "", editor: editor) } ?? ""
        return """
        <article class="event \(e.kind?.rawValue ?? "")">
        <div class="line">\(tag)\(main)\(extra)<span class="info">\(meta.joined(separator: " · "))</span>\
        \(src)<time>\(time(e.time))</time></div>\(details)
        </article>
        """
    }

    /// The SQL with its bindings in place of the `?`s, for reading. When the count doesn't match
    /// (named bindings, a literal "?"), the bindings are listed after it instead.
    static func interpolate(sql: String, bindings: [JSONValue]) -> String {
        guard !bindings.isEmpty else { return sql }
        let parts = sql.split(separator: "?", omittingEmptySubsequences: false)
        guard parts.count == bindings.count + 1 else {
            return sql + "  -- " + bindings.map(literal).joined(separator: ", ")
        }
        var out = String(parts[0])
        for (binding, part) in zip(bindings, parts.dropFirst()) {
            out += literal(binding) + part
        }
        return out
    }

    private static func literal(_ value: JSONValue) -> String {
        switch value {
        case .null: "NULL"
        case let .bool(b): b ? "1" : "0"
        case let .number(n): JSONValue.format(n)
        case let .string(s): "'" + s.replacingOccurrences(of: "'", with: "''") + "'"
        case .array, .object: "'" + value.json() + "'"
        }
    }

    /// "0.42 ms", "31 ms", "1.24 s".
    static func duration(_ ms: Double) -> String {
        if ms >= 1000 { return String(format: "%.2f s", ms / 1000) }
        if ms >= 10 { return String(format: "%.0f ms", ms) }
        return String(format: "%.2f ms", ms)
    }

    /// PSR-3 levels mapped to the three colors the theme has.
    private static func levelClass(_ level: String) -> String {
        switch level {
        case "emergency", "alert", "critical", "error": "error"
        case "warning", "notice": "warning"
        default: "info"
        }
    }

    /// The status code pill and duration of a finished request.
    private static func requestHTML(_ e: Event) -> String {
        var out = ""
        if let code = e.statusCode {
            out += "<span class=\"status s\(code / 100)xx\">\(code)</span>"
        }
        if let ms = e.durationMs {
            out += "<span class=\"duration\">\(escape(duration(ms)))</span>"
        }
        return out
    }

    /// Native-looking theme: system fonts and colors, light and dark mode, cards on the window
    /// background, Xcode-like syntax colors for Symfony's dump markup (its JS keeps working).
    static let style = """
    :root {
      color-scheme: light dark;
      --text: #1d1d1f; --secondary: #6e6e73; --tertiary: #a1a1a6;
      --card: #ffffff; --card-border: rgba(0,0,0,.08); --shadow: 0 1px 2px rgba(0,0,0,.06);
      --header: rgba(246,246,248,.85); --separator: rgba(0,0,0,.08); --hover: rgba(0,0,0,.05);
      --accent: #0a64d8; --string: #c41a16; --number: #1c00cf; --key: #326d74; --type: #6c36a9;
      --keyword: #9b2393; --meta: #8a8a8e; --get: #1f9d55; --post: #c87400; --cli: #6e6e73; --error: #d70015;
    }
    @media (prefers-color-scheme: dark) {
      :root {
        --text: #f5f5f7; --secondary: #98989d; --tertiary: #6e6e73;
        --card: #2a2a2d; --card-border: rgba(255,255,255,.07); --shadow: 0 1px 2px rgba(0,0,0,.35);
        --header: rgba(30,30,32,.85); --separator: rgba(255,255,255,.08); --hover: rgba(255,255,255,.06);
        --accent: #4aa3ff; --string: #fc6a5d; --number: #d0bf69; --key: #67b7a4; --type: #a167e6;
        --keyword: #fc5fa3; --meta: #8e8e93; --get: #4cd07d; --post: #ffb340; --cli: #98989d; --error: #ff453a;
      }
    }
    html, body { margin: 0; background: transparent; color: var(--text);
                 font: 13px -apple-system, BlinkMacSystemFont, system-ui, sans-serif; -webkit-font-smoothing: antialiased; }
    body { padding: 0 0 16px; }

    .group { padding: 0 14px; }
    .origin { position: sticky; top: 0; z-index: 10; display: flex; gap: 8px; align-items: center;
              margin: 0 -14px; padding: 9px 16px 7px; background: var(--header);
              -webkit-backdrop-filter: saturate(180%) blur(20px); backdrop-filter: saturate(180%) blur(20px);
              border-bottom: 1px solid var(--separator); font-size: 12px; color: var(--secondary); }
    .origin .verb { font: 600 10px ui-monospace, SFMono-Regular, Menlo, monospace; letter-spacing: .04em;
                    padding: 2px 6px; border-radius: 5px; color: #fff; background: var(--get); }
    .origin .verb.post, .origin .verb.put, .origin .verb.patch, .origin .verb.delete { background: var(--post); }
    .origin .verb.cli { background: var(--cli); }
    .origin .what { color: var(--text); font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
    .origin .site { padding: 1px 7px; border-radius: 10px; background: var(--hover); font-size: 11px; white-space: nowrap; }

    .dump { margin: 10px 0 0; background: var(--card); border: 1px solid var(--card-border);
            border-radius: 10px; box-shadow: var(--shadow); overflow: hidden; }
    .meta { display: flex; gap: 8px; align-items: center; padding: 7px 12px;
            border-bottom: 1px solid var(--separator); font-size: 11.5px; color: var(--secondary); }
    .meta a, .meta span.src { font: 11.5px ui-monospace, SFMono-Regular, Menlo, monospace; color: var(--accent); text-decoration: none; }
    .meta span.src { color: var(--secondary); }
    .meta a:hover { text-decoration: underline; }
    time { margin-left: auto; color: var(--tertiary); font-size: 11px; font-variant-numeric: tabular-nums; white-space: nowrap; }

    pre.sf-dump, pre.sf-dump .sf-dump-default {
      background: transparent !important; color: var(--text) !important; z-index: auto !important;
      font: 12px/1.5 ui-monospace, SFMono-Regular, Menlo, monospace !important; }
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

    .origin .status { font: 600 10px ui-monospace, SFMono-Regular, Menlo, monospace; padding: 2px 6px; border-radius: 5px;
                      color: #fff; background: var(--get); white-space: nowrap; }
    .origin .status.s3xx { background: var(--accent); }
    .origin .status.s4xx { background: var(--post); }
    .origin .status.s5xx { background: var(--error); }
    .origin .duration { font-size: 11px; color: var(--secondary); font-variant-numeric: tabular-nums; white-space: nowrap; }

    .event { margin: 6px 0 0; background: var(--card); border: 1px solid var(--card-border);
             border-radius: 8px; box-shadow: var(--shadow); overflow: hidden; }
    .event + .dump { margin-top: 10px; }
    .event .line { display: flex; gap: 8px; align-items: baseline; padding: 6px 12px; font-size: 12px; min-width: 0; }
    .event .kind { flex: none; font: 600 9.5px ui-monospace, SFMono-Regular, Menlo, monospace; letter-spacing: .04em;
                   padding: 1px 5px; border-radius: 4px; color: var(--secondary); background: var(--hover); }
    .event .kind.query { color: var(--key); }
    .event .kind.view { color: var(--type); }
    .event .kind.job { color: var(--keyword); }
    .event .kind.level-error { color: #fff; background: var(--error); }
    .event .kind.level-warning { color: #fff; background: var(--post); }
    .event .kind.level-info { color: var(--accent); }
    .event .sql { flex: 1 1 auto; min-width: 0; font: 11.5px/1.45 ui-monospace, SFMono-Regular, Menlo, monospace;
                  white-space: pre-wrap; word-break: break-word; }
    .event .message, .event .name { flex: 0 1 auto; min-width: 0; overflow-wrap: anywhere; }
    .event .name { font-weight: 500; }
    .event .src { flex: none; font: 11px ui-monospace, SFMono-Regular, Menlo, monospace; color: var(--accent);
                  text-decoration: none; white-space: nowrap; }
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
    .event pre.json, .event pre.exception { margin: 4px 0 0; font: 11.5px/1.45 ui-monospace, SFMono-Regular, Menlo, monospace;
                                            color: var(--text); white-space: pre-wrap; word-break: break-word; }
    .event pre.exception { margin: 0; padding: 6px 12px 8px; border-top: 1px solid var(--separator); color: var(--error); }
    """

    /// "GET" pill + URL for requests, "CLI" pill + command for artisan and scripts.
    private static func originHTML(request: Dump.Request?, commandLine: String?) -> String {
        if let request, !request.uri.isEmpty {
            let verb = request.method.uppercased()
            return "<span class=\"verb \(escape(verb.lowercased()))\">\(escape(verb))</span><span class=\"what\">\(escape(request.uri))</span>"
        }
        if let commandLine, !commandLine.isEmpty {
            return "<span class=\"verb cli\">CLI</span><span class=\"what\">\(escape(commandLine))</span>"
        }
        return "<span class=\"what\">dump</span>"
    }

    /// The file (and line) as an editor link; `fallback` when there is no file.
    private static func source(_ source: Dump.Source?, fallback: String, editor: Editor) -> String {
        guard let source, !source.file.isEmpty else { return fallback.isEmpty ? "" : "<span>\(escape(fallback))</span>" }
        let text = escape(shortPath(source.file) + (source.line > 0 ? ":\(source.line)" : ""))
        guard let url = editor.url(file: source.file, line: source.line) else {
            return "<span class=\"src\" title=\"\(escape(source.file))\">\(text)</span>"
        }
        return "<a class=\"src\" href=\"\(escape(url.absoluteString))\" title=\"\(escape(source.file))\">\(text)</a>"
    }

    /// The path relative to the project (everything after app/, resources/, routes/, ... when present).
    static func shortPath(_ file: String) -> String {
        for marker in ["/app/", "/resources/", "/routes/", "/database/", "/config/", "/tests/", "/vendor/", "/bootstrap/"] {
            if let range = file.range(of: marker, options: .backwards) {
                return String(file[file.index(after: range.lowerBound)...])
            }
        }
        return file.split(separator: "/").suffix(3).joined(separator: "/")
    }

    private static func time(_ date: Date) -> String {
        date.formatted(.dateTime.hour(.twoDigits(amPM: .omitted)).minute(.twoDigits).second(.twoDigits))
    }

    static func escape(_ s: String) -> String {
        s.replacingOccurrences(of: "&", with: "&amp;")
            .replacingOccurrences(of: "<", with: "&lt;")
            .replacingOccurrences(of: ">", with: "&gt;")
            .replacingOccurrences(of: "\"", with: "&quot;")
    }
}

extension Dump {
    /// "GET https://app.test/x" for requests, the command line for CLI runs.
    public var origin: String { DumpTimeline.origin(request: request, commandLine: commandLine) }
}

extension Event {
    /// "GET https://app.test/x" for requests, the command line for CLI runs.
    public var origin: String { DumpTimeline.origin(request: request, commandLine: commandLine) }
}

extension DumpTimeline {
    static func origin(request: Dump.Request?, commandLine: String?) -> String {
        if let request, !request.uri.isEmpty {
            return "\(request.method) \(request.uri)"
        }
        if let commandLine, !commandLine.isEmpty {
            return commandLine
        }
        return ""
    }
}
