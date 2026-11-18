namespace Flock.Kit;

/// <summary>
/// Another program on one of Flock's ports (`flock conflicts --json`). The first setup waits
/// until it is quit. The macOS app's Installation.Conflict.
/// </summary>
public sealed class Conflict
{
    public int? Port { get; init; }
    /// <summary>The resolver file another program set up for the TLD (macOS only).</summary>
    public string? File { get; init; }
    /// <summary>What Flock needs the port for: "HTTP", "mail".</summary>
    public string Service { get; init; } = "";
    /// <summary>"Herd (nginx.exe, pid 812)".</summary>
    public string Holder { get; init; } = "";
    /// <summary>What to quit: "Herd", or the program when no app is known.</summary>
    public string App { get; init; } = "";
    /// <summary>"quit Herd".</summary>
    public string? Hint { get; init; }
    public string Message { get; init; } = "";
}

/// <summary>The response of `flock conflicts --json`.</summary>
public sealed class ConflictList
{
    public List<Conflict> Conflicts { get; init; } = [];
}

/// <summary>One app to quit and what it is in the way of.</summary>
public sealed class ConflictingApp
{
    public string App { get; init; } = "";
    public string? Hint { get; init; }
    public List<Conflict> Conflicts { get; } = [];

    /// <summary>"Quit Herd".</summary>
    public string Title => string.IsNullOrEmpty(Hint) ? Localization.Format(Strings.App_Conflict_Quit, App) : char.ToUpperInvariant(Hint[0]) + Hint[1..];

    /// <summary>"Herd (nginx.exe, pid 812) uses port 80 (HTTP) and port 443 (HTTPS)."</summary>
    public string Detail
    {
        get
        {
            var uses = Conflicts.Where(c => c.Port is not null).Select(c => Localization.Format(Strings.App_Conflict_Port, c.Port, c.Service)).ToList();
            var parts = new List<string>();
            if (uses.Count > 0)
            {
                parts.Add(Localization.Format(Strings.App_Conflict_Uses, Joined(uses)));
            }
            if (Conflicts.FirstOrDefault(c => c.File is not null) is { } dns)
            {
                parts.Add(Localization.Format(Strings.App_Conflict_AnswersFor, dns.Service.Replace(" DNS", "", StringComparison.Ordinal), dns.File));
            }
            var what = parts.Count == 2 ? Localization.Format(Strings.App_Conflict_Both, parts[0], parts[1]) : string.Concat(parts);
            var text = Localization.Format(Strings.App_Conflict_Detail, Conflicts.FirstOrDefault()?.Holder ?? App, what);
            return char.ToUpperInvariant(text[0]) + text[1..];
        }
    }

    /// <summary>"a, b and c" in the current language.</summary>
    private static string Joined(List<string> items) =>
        items.Count < 2 ? string.Concat(items) : Localization.Format(Strings.Common_List_And, string.Join(Strings.Common_List_Separator, items.Take(items.Count - 1)), items[^1]);

    /// <summary>The conflicts grouped by the app to quit, in the order they were found.</summary>
    public static List<ConflictingApp> Group(IEnumerable<Conflict> conflicts)
    {
        var apps = new List<ConflictingApp>();
        foreach (var c in conflicts)
        {
            var app = apps.FirstOrDefault(a => a.App == c.App);
            if (app is null)
            {
                app = new ConflictingApp { App = c.App, Hint = c.Hint };
                apps.Add(app);
            }
            app.Conflicts.Add(c);
        }
        return apps;
    }
}
