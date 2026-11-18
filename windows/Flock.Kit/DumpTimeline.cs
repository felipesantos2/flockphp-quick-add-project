using System.Globalization;
using System.Text;

namespace Flock.Kit;

/// <summary>Editors that dump sources can be opened in.</summary>
public enum Editor { PhpStorm, VsCode, Cursor, Zed, None }

public static class EditorInfo
{
    public static IReadOnlyList<Editor> All { get; } = Enum.GetValues<Editor>();

    public static string Name(this Editor editor) => editor switch
    {
        Editor.PhpStorm => "PhpStorm",
        Editor.VsCode => "VS Code",
        Editor.Cursor => "Cursor",
        Editor.Zed => "Zed",
        _ => Strings.Settings_Editor_None,
    };

    /// <summary>
    /// A URL that opens file (at line, when known) in the editor. Windows paths are written with
    /// forward slashes ("C:\code\x.php" → vscode://file/C:/code/x.php:3).
    /// </summary>
    public static string? Url(this Editor editor, string file, int line)
    {
        var path = EscapePath(file.Replace('\\', '/'));
        var rooted = path.StartsWith('/') ? path : "/" + path;
        var at = line > 0 ? $":{line}" : "";

        return editor switch
        {
            Editor.PhpStorm => $"phpstorm://open?file={EscapeQueryValue(file.Replace('\\', '/'))}" + (line > 0 ? $"&line={line}" : ""),
            Editor.VsCode => $"vscode://file{rooted}{at}",
            Editor.Cursor => $"cursor://file{rooted}{at}",
            Editor.Zed => $"zed://file{rooted}{at}",
            _ => null,
        };
    }

    /// <summary>Percent-encodes everything outside RFC 3986's path characters (like Foundation's urlPathAllowed, minus ? and #).</summary>
    private static string EscapePath(string path)
    {
        var b = new StringBuilder();
        foreach (var by in Encoding.UTF8.GetBytes(path))
        {
            var c = (char)by;
            if (by < 0x80 && (char.IsAsciiLetterOrDigit(c) || "-._~/:@!$&'()*+,;=".Contains(c)))
            {
                b.Append(c);
            }
            else
            {
                b.Append('%').Append(by.ToString("X2", CultureInfo.InvariantCulture));
            }
        }
        return b.ToString();
    }

    private static string EscapeQueryValue(string value)
    {
        var b = new StringBuilder();
        foreach (var by in Encoding.UTF8.GetBytes(value))
        {
            var c = (char)by;
            if (by < 0x80 && (char.IsAsciiLetterOrDigit(c) || "-._~/:@!$'()*+,;".Contains(c)))
            {
                b.Append(c);
            }
            else
            {
                b.Append('%').Append(by.ToString("X2", CultureInfo.InvariantCulture));
            }
        }
        return b.ToString();
    }

    public static Editor Parse(string? value) =>
        Enum.TryParse<Editor>(value, ignoreCase: true, out var editor) ? editor : Editor.PhpStorm;
}

/// <summary>
/// Renders dumps and the queries, logs, views and jobs captured with them as one terminal-like
/// page: requests newest first, each request's entries in the order they happened, every entry with
/// its source and time, and the request's status and duration in its header.
/// </summary>
public static partial class DumpTimeline
{
    /// <summary>Dumps belonging to the most recent request / CLI run.</summary>
    public static List<Dump> LatestGroup(IReadOnlyList<Dump> dumps) => LatestGroup(dumps, []).Dumps;

    /// <summary>
    /// The dumps and events of the most recent request / CLI run, whichever of the two has the newest
    /// entry. Both lists must be newest first.
    /// </summary>
    public static (List<Dump> Dumps, List<RuntimeEvent> Events) LatestGroup(IReadOnlyList<Dump> dumps, IReadOnlyList<RuntimeEvent> events)
    {
        var dump = dumps.Count > 0 ? dumps[0] : null;
        var ev = events.FirstOrDefault(e => e.IsKnown);
        string group;
        if (dump is not null && (ev is null || dump.Time >= ev.Time))
        {
            group = dump.Group;
        }
        else if (ev is not null)
        {
            group = ev.Group;
        }
        else
        {
            return ([], []);
        }
        return (dumps.Where(d => d.Group == group).ToList(), events.Where(e => e.Group == group && e.IsKnown).ToList());
    }

    /// <summary>Case-insensitive match on the dumped value, source file and origin.</summary>
    public static List<Dump> Filter(IReadOnlyList<Dump> dumps, string query)
    {
        var q = query.Trim();
        if (q.Length == 0)
        {
            return dumps.ToList();
        }
        return dumps.Where(d => new[] { d.Text, d.Source?.File ?? "", d.Origin, d.Label ?? "" }
            .Any(s => s.Contains(q, StringComparison.CurrentCultureIgnoreCase))).ToList();
    }

    /// <summary>
    /// Case-insensitive match on what an event shows (SQL and bindings, log message and level, view,
    /// job and exception), its source file and origin.
    /// </summary>
    public static List<RuntimeEvent> Filter(IReadOnlyList<RuntimeEvent> events, string query)
    {
        var q = query.Trim();
        if (q.Length == 0)
        {
            return events.ToList();
        }
        return events.Where(e => new[] { e.Sql, e.BindingsText, e.Message, e.Level, e.View, e.Path, e.Job, e.Exception, e.Source?.File ?? "", e.Origin }
            .Any(s => s.Contains(q, StringComparison.CurrentCultureIgnoreCase))).ToList();
    }

    /// <summary>dumps must be newest first (as the API returns them).</summary>
    public static string Html(IReadOnlyList<Dump> dumps, string header, Editor editor) => Html(dumps, [], header, editor);

    /// <summary>
    /// dumps and events must be newest first (as the API returns them). Entries of one request stay
    /// together even when another request's happened in between; events of unknown kinds are left out.
    /// </summary>
    public static string Html(IReadOnlyList<Dump> dumps, IReadOnlyList<RuntimeEvent> events, string header, Editor editor)
    {
        var groups = new List<(string Key, List<Entry> Entries)>();
        var byKey = new Dictionary<string, List<Entry>>();
        foreach (var entry in Merge(dumps, events.Where(e => e.IsKnown).ToList()))
        {
            if (entry.Group.Length > 0 && byKey.TryGetValue(entry.Group, out var members))
            {
                members.Add(entry);
                continue;
            }
            List<Entry> list = [entry];
            groups.Add((entry.Group, list));
            if (entry.Group.Length > 0)
            {
                byKey[entry.Group] = list;
            }
        }

        var body = new StringBuilder();
        foreach (var (_, members) in groups)
        {
            var chronological = Enumerable.Reverse(members).ToList();
            var first = chronological.FirstOrDefault(e => e.Request is { Uri.Length: > 0 } || e.CommandLine is { Length: > 0 }) ?? chronological[0];
            var site = chronological.Select(e => e.Site).FirstOrDefault(s => s.Length > 0) ?? "";
            var request = chronological.LastOrDefault(e => e.Event?.Kind == EventKind.Request)?.Event;
            body.Append("<section class=\"group\">\n<header class=\"origin\">").Append(OriginHtml(first.Request, first.CommandLine))
                .Append(request is null ? "" : RequestHtml(request))
                .Append(site.Length == 0 ? "" : $"<span class=\"site\">{Escape(site)}</span>")
                .Append($"<time>{Time(chronological[0].Time)}</time></header>\n");
            foreach (var entry in chronological)
            {
                if (entry.Dump is { } dump)
                {
                    body.Append("<article class=\"dump\">\n<div class=\"meta\">").Append(Source(dump.Source, editor, "dump"))
                        .Append($"<time>{Time(dump.Time)}</time></div>\n")
                        .Append(dump.Html).Append("\n</article>\n");
                }
                else if (entry.Event is { Kind: not EventKind.Request } e)
                {
                    body.Append(EventHtml(e, editor));
                }
            }
            body.Append("</section>\n");
        }

        return $"""
            <!doctype html><html><head><meta charset="utf-8">
            {header}
            <style>{Style}</style>
            </head><body>
            {body}
            </body></html>
            """;
    }

    /// <summary>
    /// Native-looking theme: Windows fonts and colors, light and dark mode, cards on the window
    /// background, editor-like syntax colors for Symfony's dump markup (its JS keeps working).
    /// </summary>
    public const string Style = """
        :root {
          color-scheme: light dark;
          --text: #1b1b1b; --secondary: #5d5d5d; --tertiary: #8a8a8a;
          --card: #ffffff; --card-border: rgba(0,0,0,.0578); --shadow: 0 1px 2px rgba(0,0,0,.04);
          --header: rgba(243,243,243,.92); --separator: rgba(0,0,0,.08); --hover: rgba(0,0,0,.04);
          --accent: #005fb8; --string: #a31515; --number: #098658; --key: #0451a5; --type: #6f42c1;
          --keyword: #af00db; --meta: #767676; --get: #0f7b0f; --post: #9d5d00; --cli: #5d5d5d;
          --error: #c42b1c; --on-error: #fff;
        }
        @media (prefers-color-scheme: dark) {
          :root {
            --text: #ffffff; --secondary: #c5c5c5; --tertiary: #9a9a9a;
            --card: #2b2b2b; --card-border: rgba(255,255,255,.07); --shadow: 0 1px 2px rgba(0,0,0,.3);
            --header: rgba(32,32,32,.92); --separator: rgba(255,255,255,.08); --hover: rgba(255,255,255,.06);
            --accent: #60cdff; --string: #ce9178; --number: #b5cea8; --key: #9cdcfe; --type: #c586c0;
            --keyword: #569cd6; --meta: #9d9d9d; --get: #6ccb5f; --post: #fce100; --cli: #9d9d9d;
            --error: #ff99a4; --on-error: #000;
          }
        }
        html, body { margin: 0; background: transparent; color: var(--text);
                     font: 14px "Segoe UI Variable Text", "Segoe UI", system-ui, sans-serif; }
        body { padding: 0 0 16px; }

        .group { padding: 0 14px; }
        .origin { position: sticky; top: 0; z-index: 10; display: flex; gap: 8px; align-items: center;
                  margin: 0 -14px; padding: 9px 16px 7px; background: var(--header);
                  backdrop-filter: saturate(180%) blur(20px);
                  border-bottom: 1px solid var(--separator); font-size: 12px; color: var(--secondary); }
        .origin .verb { font: 600 10px "Cascadia Mono", Consolas, monospace; letter-spacing: .04em;
                        padding: 2px 6px; border-radius: 4px; color: #fff; background: var(--get); }
        .origin .verb.post, .origin .verb.put, .origin .verb.patch, .origin .verb.delete { background: var(--post); color: #000; }
        .origin .verb.cli { background: var(--cli); }
        .origin .what { color: var(--text); font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
        .origin .site { padding: 1px 7px; border-radius: 10px; background: var(--hover); font-size: 11px; white-space: nowrap; }

        .dump { margin: 10px 0 0; background: var(--card); border: 1px solid var(--card-border);
                border-radius: 8px; box-shadow: var(--shadow); overflow: hidden; }
        .meta { display: flex; gap: 8px; align-items: center; padding: 7px 12px;
                border-bottom: 1px solid var(--separator); font-size: 12px; color: var(--secondary); }
        .meta a, .meta span.src { font: 12px "Cascadia Mono", Consolas, monospace; color: var(--accent); text-decoration: none; }
        .meta span.src { color: var(--secondary); }
        .meta a:hover { text-decoration: underline; }
        time { margin-left: auto; color: var(--tertiary); font-size: 11px; font-variant-numeric: tabular-nums; white-space: nowrap; }

        pre.sf-dump, pre.sf-dump .sf-dump-default {
          background: transparent !important; color: var(--text) !important; z-index: auto !important;
          font: 12.5px/1.5 "Cascadia Mono", Consolas, monospace !important; }
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

        .origin .status { font: 600 10px "Cascadia Mono", Consolas, monospace; padding: 2px 6px; border-radius: 4px;
                          color: #fff; background: var(--get); }
        .origin .status.s3xx { background: var(--cli); }
        .origin .status.s4xx { background: var(--post); color: #000; }
        .origin .status.s5xx { background: var(--error); color: var(--on-error); }
        .duration { font: 11px "Cascadia Mono", Consolas, monospace; color: var(--secondary); white-space: nowrap; }

        .event { margin: 6px 0 0; background: var(--card); border: 1px solid var(--card-border);
                 border-radius: 8px; box-shadow: var(--shadow); overflow: hidden; }
        .event .meta { border-bottom: none; padding: 6px 12px; min-width: 0; }
        .event .details { border-top: 1px solid var(--separator); padding: 7px 12px; }
        .event .kind { font: 600 10px "Cascadia Mono", Consolas, monospace; letter-spacing: .04em; text-transform: uppercase;
                       padding: 2px 6px; border-radius: 4px; background: var(--hover); color: var(--secondary); white-space: nowrap; }
        .event .kind.level.warning, .event .kind.level.notice { background: var(--post); color: #000; }
        .event .kind.level.error, .event .kind.level.critical, .event .kind.level.alert, .event .kind.level.emergency { background: var(--error); color: var(--on-error); }
        .event .what { color: var(--text); font: 12px "Cascadia Mono", Consolas, monospace; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
        .event .note { color: var(--tertiary); font-size: 11px; white-space: nowrap; }
        .event .state { font-size: 11px; padding: 1px 7px; border-radius: 10px; background: var(--hover); white-space: nowrap; }
        .event .state.processed { color: var(--get); }
        .event .state.failed { color: var(--error); }
        .event pre { margin: 0; font: 12.5px/1.5 "Cascadia Mono", Consolas, monospace; white-space: pre-wrap; word-break: break-word; }
        .event pre.sql { color: var(--text); }
        .event pre.exception { color: var(--error); }
        .event pre.context, .event .bindings { color: var(--secondary); font: 12px/1.5 "Cascadia Mono", Consolas, monospace; }
        .event .bindings { margin-top: 3px; word-break: break-word; }
        .event .message { white-space: pre-wrap; word-break: break-word; }
        .event .context { margin-top: 4px; }
        """;

    /// <summary>A dump or an event, with what grouping and the group header need.</summary>
    private sealed record Entry(DateTimeOffset Time, string Group, string Site, DumpRequest? Request, string? CommandLine, Dump? Dump, RuntimeEvent? Event);

    /// <summary>Both lists newest first → one list newest first; on equal times dumps come first.</summary>
    private static List<Entry> Merge(IReadOnlyList<Dump> dumps, IReadOnlyList<RuntimeEvent> events)
    {
        var merged = new List<Entry>(dumps.Count + events.Count);
        int i = 0, j = 0;
        while (i < dumps.Count || j < events.Count)
        {
            if (j >= events.Count || (i < dumps.Count && dumps[i].Time >= events[j].Time))
            {
                var d = dumps[i++];
                merged.Add(new Entry(d.Time, d.Group, d.Site, d.Request, d.CommandLine, d, null));
            }
            else
            {
                var e = events[j++];
                merged.Add(new Entry(e.Time, e.Group, e.Site, e.Request, e.CommandLine, null, e));
            }
        }
        return merged;
    }

    /// <summary>"GET" pill + URL for requests, "CLI" pill + command for artisan and scripts.</summary>
    private static string OriginHtml(DumpRequest? request, string? commandLine)
    {
        if (request is { Uri.Length: > 0 })
        {
            var verb = request.Method.ToUpperInvariant();
            return $"<span class=\"verb {Escape(verb.ToLowerInvariant())}\">{Escape(verb)}</span><span class=\"what\">{Escape(request.Uri)}</span>";
        }
        if (commandLine is { Length: > 0 } command)
        {
            return $"<span class=\"verb cli\">CLI</span><span class=\"what\">{Escape(command)}</span>";
        }
        return "<span class=\"what\">dump</span>";
    }

    /// <summary>The response status pill and how long the request took, for the group header.</summary>
    private static string RequestHtml(RuntimeEvent request)
    {
        var html = "";
        if (request.StatusCode is { } code)
        {
            var family = code / 100 is >= 1 and <= 5 ? $"s{code / 100}xx" : "s5xx";
            html += $"<span class=\"status {family}\">{code}</span>";
        }
        if (request.DurationMs is { } ms)
        {
            html += $"<span class=\"duration\">{Duration(ms)}</span>";
        }
        return html;
    }

    /// <summary>A query, log, view or job card (requests show in the group header instead).</summary>
    private static string EventHtml(RuntimeEvent e, Editor editor)
    {
        var meta = new StringBuilder();
        var details = new StringBuilder();
        switch (e.Kind)
        {
            case EventKind.Query:
                meta.Append($"<span class=\"kind\">{Escape(Strings.Dumps_Timeline_Query)}</span>");
                if (e.Connection.Length > 0)
                {
                    meta.Append($"<span class=\"note\">{Escape(e.Connection)}</span>");
                }
                details.Append($"<pre class=\"sql\">{Escape(e.Sql)}</pre>");
                if (e.BindingsText.Length > 0)
                {
                    details.Append($"<div class=\"bindings\">{Escape(e.BindingsText)}</div>");
                }
                break;
            case EventKind.Log:
                var level = e.Level.ToLowerInvariant();
                meta.Append($"<span class=\"kind level {Escape(level)}\">{Escape(level.Length == 0 ? "log" : level)}</span>");
                details.Append($"<div class=\"message\">{Escape(e.Message)}</div>");
                if (e.ContextText.Length > 0)
                {
                    details.Append($"<pre class=\"context\">{Escape(e.ContextText)}</pre>");
                }
                break;
            case EventKind.View:
                meta.Append($"<span class=\"kind\">{Escape(Strings.Dumps_Timeline_View)}</span><span class=\"what\">{Escape(e.View)}</span>");
                break;
            case EventKind.Job:
                meta.Append($"<span class=\"kind\">{Escape(Strings.Dumps_Timeline_Job)}</span><span class=\"what\">{Escape(e.Job)}</span>");
                if (e.State.Length > 0)
                {
                    meta.Append($"<span class=\"state {Escape(e.State)}\">{Escape(e.State)}</span>");
                }
                if (e.Queue.Length > 0)
                {
                    meta.Append($"<span class=\"note\">{Escape(e.Queue)}</span>");
                }
                if (e.Exception.Length > 0)
                {
                    details.Append($"<pre class=\"exception\">{Escape(e.Exception)}</pre>");
                }
                break;
        }

        // A view links to its template; everything else to where it came from.
        var source = e.Kind == EventKind.View && e.Path.Length > 0 ? new DumpSource { File = e.Path } : e.Source;
        if (source is { File.Length: > 0 })
        {
            meta.Append(Source(source, editor, ""));
        }
        if (e.DurationMs is { } ms)
        {
            meta.Append($"<span class=\"duration\">{Duration(ms)}</span>");
        }

        return $"<article class=\"event {Escape(e.Kind)}\">\n<div class=\"meta\">{meta}<time>{Time(e.Time)}</time></div>\n" +
            (details.Length > 0 ? $"<div class=\"details\">{details}</div>\n" : "") + "</article>\n";
    }

    /// <summary>"0.42 ms", "31.2 ms", "1.5 s".</summary>
    public static string Duration(double ms) => ms >= 1000
        ? (ms / 1000).ToString("0.##", CultureInfo.InvariantCulture) + " s"
        : ms.ToString(ms < 10 ? "0.##" : "0.#", CultureInfo.InvariantCulture) + " ms";

    private static string Source(DumpSource? source, Editor editor, string fallback)
    {
        if (source is not { File.Length: > 0 })
        {
            return fallback.Length == 0 ? "" : $"<span>{Escape(fallback)}</span>";
        }
        var text = Escape(ShortPath(source.File) + (source.Line > 0 ? $":{source.Line}" : ""));
        var url = editor.Url(source.File, source.Line);
        if (url is null)
        {
            return $"<span class=\"src\" title=\"{Escape(source.File)}\">{text}</span>";
        }
        return $"<a href=\"{Escape(url)}\" title=\"{Escape(source.File)}\">{text}</a>";
    }

    /// <summary>The path relative to the project (everything after app/, resources/, routes/, ... when present).</summary>
    public static string ShortPath(string file)
    {
        var normalized = file.Replace('\\', '/');
        foreach (var marker in new[] { "/app/", "/resources/", "/routes/", "/database/", "/config/", "/tests/", "/vendor/", "/bootstrap/" })
        {
            var at = normalized.LastIndexOf(marker, StringComparison.Ordinal);
            if (at >= 0)
            {
                return normalized[(at + 1)..];
            }
        }
        return string.Join("/", normalized.Split('/', StringSplitOptions.RemoveEmptyEntries).TakeLast(3));
    }

    private static string Time(DateTimeOffset date) => date.ToLocalTime().ToString("HH:mm:ss", CultureInfo.InvariantCulture);

    public static string Escape(string s) =>
        s.Replace("&", "&amp;").Replace("<", "&lt;").Replace(">", "&gt;").Replace("\"", "&quot;");
}