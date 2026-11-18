using System.Text.Json.Serialization;

namespace Flock.Kit;

/// <summary>GET /v1/config — the persisted configuration plus what is installed.</summary>
public sealed class FlockConfig
{
    public string Tld { get; init; } = "test";
    public string DefaultPhp { get; init; } = "";
    public string DefaultNode { get; init; } = "";
    public bool Xdebug { get; init; }
    public bool Dumps { get; init; }
    /// <summary>Kinds (EventKind) not captured while capture is on.</summary>
    public List<string> CaptureOff { get; init; } = [];
    /// <summary>dump() output also shows in the page or terminal; dd() always does.</summary>
    public bool DumpsInPage { get; init; }
    /// <summary>The channel of prebuilt packages: "stable", "beta" or "experimental" (`flock channel`).</summary>
    public string Channel { get; init; } = "stable";
    public string Root { get; init; } = "";
    public PortSet Ports { get; init; } = new();
    public List<string> Parked { get; init; } = [];
    public List<PhpVersion> Php { get; init; } = [];
    public List<string> Node { get; init; } = [];

    /// <summary>Node majors that are installed, newest first ("22.23.3" → "22").</summary>
    [JsonIgnore]
    public List<string> NodeMajors => Node.Select(v => v.Split('.')[0]).Distinct().ToList();

    public sealed class PortSet
    {
        public int Http { get; init; }
        public int Https { get; init; }
        public int Dns { get; init; }
        public int Smtp { get; init; }
        public int Postgres { get; init; }
        public int Dumps { get; init; }
        public int Api { get; init; }
    }

    public sealed class PhpVersion
    {
        private readonly string? fullVersion;

        public string Version { get; init; } = "";
        public string FullVersion { get => fullVersion ?? Version; init => fullVersion = value; }
        public string Ini { get; init; } = "";
        public bool XdebugAvailable { get; init; }
    }
}

/// <summary>GET /v1/services — one background service (web server, database, mail catcher, ...).</summary>
public sealed class Service
{
    private readonly string? name;

    public string Id { get; init; } = "";
    public string Name { get => name ?? Id; init => name = value; }
    public string Category { get; init; } = "Other";
    /// <summary>What it is, for services installed on request; may be empty.</summary>
    public string Description { get; init; } = "";
    /// <summary>False for a service Flock can't run on this platform; Unavailable says why.</summary>
    public bool Available { get; init; } = true;
    public string Unavailable { get; init; } = "";
    public string Version { get; init; } = "";
    public int Port { get; init; }
    public bool Installed { get; init; }
    public bool Enabled { get; init; }
    public bool Running { get; init; }
    public bool Stoppable { get; init; }
    /// <summary>.env snippet to connect a Laravel app.</summary>
    public string Env { get; init; } = "";
    /// <summary>Name for /v1/logs/services/{name}.</summary>
    public string Log { get; init; } = "";
    /// <summary>Connection URL (e.g. for TablePlus).</summary>
    public string Url { get; init; } = "";
    /// <summary>Why it isn't working, when that isn't simply "stopped".</summary>
    public string Problem { get; init; } = "";

    /// <summary>Is this a database that a GUI client like TablePlus can open?</summary>
    [JsonIgnore]
    public bool IsDatabase => Category == "Database" && Url.Length > 0;

    /// <summary>The web server and hosts ports are tied to certificates and the hosts helper; `flock install` changes them.</summary>
    [JsonIgnore]
    public bool HasConfigurablePort => Installed && Port > 0 && Id != "web" && Id != "dns";

    /// <summary>A catalog service (MySQL, Valkey, ...), installed on request from its detail.</summary>
    [JsonIgnore]
    public bool IsOnRequest => Id is not ("web" or "dns" or "hosts" or "mail" or "dumps" or "postgres");
}

/// <summary>
/// Body of POST /v1/settings; null fields are left out and stay unchanged. CaptureOff replaces the
/// list of kinds (EventKind) that aren't captured.
/// </summary>
public sealed record SettingsChange(
    bool? Xdebug = null,
    bool? XdebugBreakpoints = null,
    bool? Dumps = null,
    string? DefaultPhp = null,
    string? DefaultNode = null,
    string? Channel = null,
    List<string>? CaptureOff = null,
    bool? DumpsInPage = null);

/// <summary>
/// Body of PATCH /v1/sites/{name}; null fields are left out. An empty Php means "use the default";
/// Server is "fastcgi" or "frankenphp", Worker "" (classic) or "octane" (see Status.SiteServer);
/// Reverb turns the site's Reverb server on or off.
/// </summary>
public sealed record SiteChange(bool? Secured = null, string? Php = null, string? Node = null, string? Server = null, string? Worker = null, string? Root = null, string? Type = null, bool? Reverb = null);

/// <summary>
/// A change to a proxy (PATCH /v1/proxies/{name}); null fields stay as they are. An empty Command
/// removes the command with its folder and stop command.
/// </summary>
public sealed record ProxyChange(string? Upstream = null, bool? Secured = null, string? Command = null, string? Dir = null, string? StopCommand = null);