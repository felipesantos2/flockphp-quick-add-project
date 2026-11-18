using System.Globalization;
using System.Text.Json.Serialization;

namespace Flock.Kit;

/// <summary>
/// GET /v1/postgres/versions — the PostgreSQL majors Flock offers (18 down to 14), what is installed
/// of each, and which one is active. One PostgreSQL runs at a time, on the PostgreSQL port; every
/// major keeps its own data directory, so switching leaves the other's databases untouched.
/// </summary>
public sealed class PostgresVersions
{
    /// <summary>The major selected to run; empty when none is installed.</summary>
    public string Active { get; init; } = "";
    public bool Running { get; init; }
    /// <summary>Not switched off by the user.</summary>
    public bool Enabled { get; init; }
    public int Port { get; init; }
    public DateTimeOffset? CheckedAt { get; init; }
    public string CheckError { get; init; } = "";
    /// <summary>Always 18, 17, 16, 15, 14 (newest first).</summary>
    public List<PostgresMajor> Majors { get; init; } = [];

    /// <summary>The active major's entry, when one is active.</summary>
    [JsonIgnore]
    public PostgresMajor? ActiveMajor => Majors.FirstOrDefault(m => m.Major == Active && Active.Length > 0);

    [JsonIgnore]
    public bool AnyInstalled => Majors.Any(m => m.IsInstalled);

    /// <summary>Some major has an operation in flight (maybe started by another window or the CLI), so the UI polls.</summary>
    [JsonIgnore]
    public bool AnyBusy => Majors.Any(m => m.IsBusy);

    [JsonIgnore]
    public bool AnyUpdate => Majors.Any(m => m.Update);
}

/// <summary>Values of PostgresMajor.Busy; anything else is an operation this app doesn't know yet.</summary>
public static class PostgresBusy
{
    public const string Installing = "installing";
    public const string Updating = "updating";
    public const string Switching = "switching";
    public const string Uninstalling = "uninstalling";
}

/// <summary>How close a version is to its upstream end of life.</summary>
public enum EndOfLife { Unknown, Supported, Soon, Past }

/// <summary>One PostgreSQL major (e.g. "18") and what is installed of it.</summary>
public sealed class PostgresMajor
{
    public string Major { get; init; } = "";
    /// <summary>The installed release, e.g. "18.6"; empty when not installed.</summary>
    public string Installed { get; init; } = "";
    /// <summary>The newest release for this platform; empty when unknown (never checked, offline).</summary>
    public string Latest { get; init; } = "";
    /// <summary>A newer release (or a newer build of the installed one) can be installed.</summary>
    public bool Update { get; init; }
    /// <summary>Latest is a pre-release (beta channel).</summary>
    public bool Prerelease { get; init; }
    /// <summary>Why the installed build was withdrawn; empty when it wasn't. Flock replaces it by itself.</summary>
    public string Yanked { get; init; } = "";
    public bool Active { get; init; }
    /// <summary>A data directory (cluster) of this major exists — also after uninstalling it.</summary>
    public bool HasData { get; init; }
    public long DataSize { get; init; }
    /// <summary>Upstream end-of-life date (yyyy-MM-dd); empty when unknown.</summary>
    public string Eol { get; init; } = "";
    /// <summary>One of PostgresBusy, or empty when idle.</summary>
    public string Busy { get; init; } = "";
    /// <summary>Names of the extensions installed for this major ("postgis", "pgvector").</summary>
    public List<string> Extensions { get; init; } = [];

    [JsonIgnore]
    public bool IsInstalled => Installed.Length > 0;

    [JsonIgnore]
    public bool IsBusy => Busy.Length > 0;

    /// <summary>"Installing…", "Switching…"; a generic text for operations this app doesn't know.</summary>
    [JsonIgnore]
    public string BusyText => Busy switch
    {
        PostgresBusy.Installing => Strings.Common_Installing,
        PostgresBusy.Updating => Strings.Common_Updating,
        PostgresBusy.Switching => Strings.Common_Switching,
        PostgresBusy.Uninstalling => Strings.Common_Uninstalling,
        "" => "",
        _ => Strings.Common_Working,
    };

    [JsonIgnore]
    public DateOnly? EolDate =>
        DateOnly.TryParseExact(Eol, "yyyy-MM-dd", CultureInfo.InvariantCulture, DateTimeStyles.None, out var date) ? date : null;

    /// <summary>Past its end of life, or within about six months of it.</summary>
    public EndOfLife EndOfLifeState(DateOnly today) => EolDate switch
    {
        null => EndOfLife.Unknown,
        { } date when date <= today => EndOfLife.Past,
        { } date when date <= today.AddMonths(6) => EndOfLife.Soon,
        _ => EndOfLife.Supported,
    };
}

/// <summary>Shared state and progress rules for daemon jobs.</summary>
public abstract class JobState
{
    /// <summary>"install" or "update".</summary>
    public string Action { get; init; } = "";
    /// <summary>"running" or "failed"; anything else counts as neither.</summary>
    public string State { get; init; } = "";
    /// <summary>"downloading" or "installing"; see PhaseText.</summary>
    public string Phase { get; init; } = "";
    /// <summary>0…1, or -1 when the job can't tell.</summary>
    public double Progress { get; init; } = -1;
    public string Error { get; init; } = "";
    public DateTimeOffset? StartedAt { get; init; }

    [JsonIgnore]
    public bool IsRunning => State == "running";

    [JsonIgnore]
    public bool IsFailed => State == "failed";

    [JsonIgnore]
    public bool HasProgress => Progress is >= 0 and <= 1;

    /// <summary>"Downloading…", "Installing…"; a generic text for phases this app doesn't know.</summary>
    [JsonIgnore]
    public string PhaseText => Phase switch
    {
        "downloading" => Strings.Common_Downloading,
        "installing" => Strings.Common_Installing,
        _ => Action == "update" ? Strings.Common_Updating : Strings.Common_Working,
    };
}

/// <summary>An install or update running in the daemon (PHP or FrankenPHP), or a failed one.</summary>
public sealed class ComponentJob : JobState
{
}

/// <summary>
/// GET /v1/catalog — the services Flock installs on request besides PostgreSQL: MySQL, MariaDB,
/// MongoDB, Valkey, Redis, Meilisearch, Typesense, RustFS. Their lines install side by side, and one
/// of them (the active one) runs on the service's port; installs run as jobs in the daemon.
/// </summary>
public sealed class ServiceCatalog
{
    public DateTimeOffset? CheckedAt { get; init; }
    public string CheckError { get; init; } = "";
    public List<CatalogService> Services { get; init; } = [];

    /// <summary>Some install runs, so the UI polls.</summary>
    [JsonIgnore]
    public bool HasRunningJob => Services.Any(s => s.Job?.IsRunning == true);

    public CatalogService? this[string id] => Services.FirstOrDefault(s => s.Id == id);
}

public sealed class CatalogService
{
    private readonly string? name;

    public string Id { get; init; } = "";
    public string Name { get => name ?? Id; init => name = value; }
    public string Category { get; init; } = "Other";
    public string Description { get; init; } = "";
    public bool Available { get; init; } = true;
    /// <summary>Why it isn't available on this platform.</summary>
    public string Unavailable { get; init; } = "";
    public string Estimate { get; init; } = "";
    /// <summary>The installed version; empty when not installed.</summary>
    public string Installed { get; init; } = "";
    /// <summary>The line of the installed version ("8.4").</summary>
    public string Line { get; init; } = "";
    /// <summary>The newest release of Line; empty when unknown.</summary>
    public string Latest { get; init; } = "";
    public bool Update { get; init; }
    /// <summary>Why the installed build was withdrawn; empty when it wasn't. Flock replaces it by itself.</summary>
    public string Yanked { get; init; } = "";
    /// <summary>The configured port, or the one Flock suggests when not installed.</summary>
    public int Port { get; init; }
    public int DefaultPort { get; init; }
    public List<CatalogLine> Lines { get; init; } = [];
    public CatalogJob? Job { get; init; }

    [JsonIgnore]
    public bool IsInstalled => Installed.Length > 0;

    /// <summary>The active line, else the newest.</summary>
    [JsonIgnore]
    public CatalogLine? PreferredLine => Lines.FirstOrDefault(l => l.Active) ?? Lines.FirstOrDefault();
}

/// <summary>A release line of a catalog service ("8.4 LTS").</summary>
public sealed class CatalogLine
{
    private readonly string? label;

    public string Id { get; init; } = "";
    public string Label { get => label ?? Id; init => label = value; }
    public string Latest { get; init; } = "";
    /// <summary>Installed (lines install side by side; one of them is active).</summary>
    public bool Installed { get; init; }
    /// <summary>The installed version; empty when not installed.</summary>
    public string Version { get; init; } = "";
    /// <summary>The installed version is the one that runs.</summary>
    public bool Active { get; init; }
    /// <summary>Latest (or a newer build of it) can replace Version.</summary>
    public bool Update { get; init; }
    /// <summary>Why the installed build was withdrawn; empty when it wasn't. Flock replaces it by itself.</summary>
    public string Yanked { get; init; } = "";
    /// <summary>The line has data (each line keeps its own).</summary>
    public bool HasData { get; init; }
}

/// <summary>An install of a catalog service, running or failed.</summary>
public sealed class CatalogJob : JobState
{
    /// <summary>What it installs; empty until the newest release is known.</summary>
    public string Version { get; init; } = "";
    public string Line { get; init; } = "";
}
/// <summary>
/// GET /v1/postgres/versions/{major}/extensions — the extensions Flock offers for one major
/// (PostGIS, pgRouting, pgvector, TimescaleDB), each a download of its own, and the folder for the
/// user's own extensions, which Flock keeps across updates.
/// </summary>
public sealed class PostgresExtensions
{
    public string Major { get; init; } = "";
    public List<PostgresExtension> Extensions { get; init; } = [];
    /// <summary>The folder for extensions of your own: lib\, extension\ and deps\.</summary>
    public string CustomDir { get; init; } = "";
    /// <summary>What is in that folder, read only.</summary>
    public List<PostgresCustomExtension> Custom { get; init; } = [];

    [JsonIgnore]
    public bool AnyBusy => Extensions.Any(e => e.IsBusy);

    /// <summary>"PostGIS, pgvector" for the names of a major's extensions.</summary>
    public static string Titles(IEnumerable<string> names) => string.Join(", ", names.Select(n => n switch
    {
        "postgis" => "PostGIS",
        "pgrouting" => "pgRouting",
        "timescaledb" => "TimescaleDB",
        _ => n,
    }));
}

/// <summary>One extension for one PostgreSQL major.</summary>
public sealed class PostgresExtension
{
    public string Name { get; init; } = "";
    public string Title { get; init; } = "";
    public string Description { get; init; } = "";
    /// <summary>What CREATE EXTENSION takes ("vector" for pgvector).</summary>
    public string Creates { get; init; } = "";
    public string License { get; init; } = "";
    /// <summary>Loaded at server start: installing or removing it restarts PostgreSQL.</summary>
    public bool Preload { get; init; }
    /// <summary>A build exists for this major and platform.</summary>
    public bool Supported { get; init; }
    public string Installed { get; init; } = "";
    public string Latest { get; init; } = "";
    public bool Update { get; init; }
    /// <summary>Newer versions exist, but no longer for this major.</summary>
    public bool Discontinued { get; init; }
    public long Size { get; init; }
    public bool Prerelease { get; init; }
    public string Yanked { get; init; } = "";
    /// <summary>"", "installing", "updating" or "uninstalling".</summary>
    public string Busy { get; init; } = "";

    [JsonIgnore]
    public bool IsInstalled => Installed.Length > 0;

    [JsonIgnore]
    public bool IsBusy => Busy.Length > 0;
}

/// <summary>An extension in a major's own extensions folder: what Flock found, and whether the server has it.</summary>
public sealed class PostgresCustomExtension
{
    /// <summary>What CREATE EXTENSION takes.</summary>
    public string Name { get; init; } = "";
    /// <summary>Its control file's default_version; empty when it has none.</summary>
    public string Version { get; init; } = "";
    public bool Linked { get; init; }
    /// <summary>Why it can't be used; empty when nothing is in the way.</summary>
    public string Problem { get; init; } = "";
}
