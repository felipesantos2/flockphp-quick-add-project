using System.Text.Json;
using System.Text.Json.Serialization;

namespace Flock.Kit;

// Wire models of the daemon's API. Fields the daemon may omit keep their defaults;
// Go's nil slices arrive as null and are read as empty lists (see FlockJson).

/// <summary>Contents of api.json, written by the daemon.</summary>
public sealed class ApiConfig
{
    public string Url { get; init; } = "";
    public string Token { get; init; } = "";
}

/// <summary>GET /v1/status</summary>
public sealed class Status
{
    public string Version { get; init; } = "";
    /// <summary>The channel config.json follows: "stable", "beta" or "experimental" (`flock channel`).</summary>
    public string Channel { get; init; } = "stable";
    public int Pid { get; init; }
    public DateTimeOffset StartedAt { get; init; }
    public DnsStatus Dns { get; init; } = new();
    public int HttpPort { get; init; }
    public int HttpsPort { get; init; }
    public string Mail { get; init; } = "";
    public string? Postgres { get; init; }
    /// <summary>The installed catalog services (MySQL, Redis, ...).</summary>
    public List<ExtraService> Extras { get; init; } = [];
    /// <summary>Xdebug detection: requests that ask for Xdebug go to a PHP pool that loads it.</summary>
    public bool Xdebug { get; init; }
    /// <summary>Sites with PhpStorm breakpoints send every request to Xdebug.</summary>
    public bool XdebugBreakpoints { get; init; }
    /// <summary>Capture is on: dumps, and with the capture extension queries, logs, views, jobs and requests.</summary>
    public bool Dumps { get; init; }
    /// <summary>Kinds (EventKind) not captured while capture is on.</summary>
    public List<string> CaptureOff { get; init; } = [];
    /// <summary>dump() output also shows in the page or terminal; dd() always does.</summary>
    public bool DumpsInPage { get; init; }
    /// <summary>Installed PHP minors without the capture extension: only dumps are captured there.</summary>
    public List<string> CaptureUnsupported { get; init; } = [];
    public int MailCount { get; init; }
    public int DumpCount { get; init; }
    public int EventCount { get; init; }
    public List<Site> Sites { get; init; } = [];
    /// <summary>Names served by a server of their own, e.g. a Node.js dev server (`flock proxy`); empty from older daemons.</summary>
    public List<Proxy> Proxies { get; init; } = [];
    public List<Process> Processes { get; init; } = [];
    public List<string> Warnings { get; init; } = [];
    /// <summary>
    /// The first run's install of the newest PHP and Node.js LTS, which then become the defaults;
    /// null once both defaults are set.
    /// </summary>
    public FirstRunDefaults? Defaults { get; init; }

    public sealed class FirstRunDefaults
    {
        public DefaultInstall Php { get; init; } = new();
        public DefaultInstall Node { get; init; } = new();
    }

    /// <summary>The install of one default (a PHP minor, a Node.js major).</summary>
    public sealed class DefaultInstall
    {
        /// <summary>"8.5", "24"; empty until the package manifest names it.</summary>
        public string Version { get; init; } = "";
        /// <summary>One of DefaultState.</summary>
        public string State { get; init; } = DefaultState.Done;
        /// <summary>Why it failed; it is tried again from Settings → PHP or Node.</summary>
        public string Error { get; init; } = "";

        /// <summary>Not installed yet, but on its way: waiting for the manifest or installing.</summary>
        [JsonIgnore]
        public bool IsPending => State is DefaultState.Waiting or DefaultState.Installing;

        [JsonIgnore]
        public bool IsInstalling => State == DefaultState.Installing;

        [JsonIgnore]
        public bool IsFailed => State == DefaultState.Failed;
    }

    /// <summary>Values of DefaultInstall.State.</summary>
    public static class DefaultState
    {
        /// <summary>No package manifest yet (e.g. offline).</summary>
        public const string Waiting = "waiting";
        public const string Installing = "installing";
        public const string Failed = "failed";
        /// <summary>The default is set.</summary>
        public const string Done = "done";
    }

    /// <summary>An installed catalog service, for the tray panel.</summary>
    public sealed class ExtraService
    {
        private readonly string? name;

        public string Id { get; init; } = "";
        public string Name { get => name ?? Id; init => name = value; }
        public string Version { get; init; } = "";
        public int Port { get; init; }
        public bool Enabled { get; init; } = true;
        public bool Up { get; init; }
    }

    /// <summary>The site's git worktrees, in the status's order.</summary>
    public List<Site> WorktreesOf(string site) => Sites.Where(s => s.Parent == site).ToList();

    /// <summary>
    /// The sites, each followed by its worktrees, for lists that nest them. A worktree whose site
    /// isn't listed comes last.
    /// </summary>
    public List<Site> SitesWithWorktrees()
    {
        var parents = Sites.Where(s => !s.IsWorktree).Select(s => s.Name).ToHashSet();
        var nested = new List<Site>(Sites.Count);
        foreach (var site in Sites.Where(s => !s.IsWorktree))
        {
            nested.Add(site);
            nested.AddRange(WorktreesOf(site.Name));
        }
        nested.AddRange(Sites.Where(s => s.IsWorktree && !parents.Contains(s.Parent!)));
        return nested;
    }

    /// <summary>Something needs attention: a warning, or a supervised process that is down.</summary>
    [JsonIgnore]
    public bool IsDegraded => Warnings.Count > 0 || Processes.Any(p => !p.Up);

    public sealed class DnsStatus
    {
        public string Tld { get; init; } = "";
        /// <summary>The DNS server's address; empty when it isn't running.</summary>
        public string Addr { get; init; } = "";
        /// <summary>
        /// Every name under the TLD resolves (any subdomain). When false (e.g. a company DNS policy
        /// overrides Flock's rule), only site domains and extra domains resolve, through the hosts file.
        /// </summary>
        public bool Wildcard { get; init; }
        public string WildcardProblem { get; init; } = "";
        /// <summary>Subdomain levels below a site (and its aliases) that are served.</summary>
        public int MaxDepth { get; init; }
    }

    /// <summary>
    /// A name served by a server of its own rather than by PHP, e.g. a Node.js dev server: every
    /// request, WebSocket upgrades included, passes to Upstream with its Host header.
    /// </summary>
    public sealed class Proxy
    {
        public string Name { get; init; } = "";
        public string Domain { get; init; } = "";
        /// <summary>Where requests go, e.g. http://127.0.0.1:3000.</summary>
        public string Upstream { get; init; } = "";
        public bool Secured { get; init; }
        public string? Url { get; init; }
        /// <summary>Why it isn't served, e.g. a site of the same name serves its domain.</summary>
        public string? Problem { get; init; }
        /// <summary>Something answers at the upstream; Server names it when it runs on this machine, e.g. "Node.js 22.23.4".</summary>
        public bool Running { get; init; }
        public string? Server { get; init; }

        /// <summary>The upstream without the http:// Flock adds: 127.0.0.1:3000.</summary>
        [JsonIgnore]
        public string UpstreamLabel => Upstream.StartsWith("http://", StringComparison.Ordinal) ? Upstream["http://".Length..] : Upstream;

        /// <summary>What answers at the upstream: the server's name, "Running" or "Not running".</summary>
        [JsonIgnore]
        public string ServerLabel => Running ? Server is { Length: > 0 } server ? server : "Running" : "Not running";
    }

    /// <summary>One of a site's extra domains: a subdomain ("tenant1") or an alias ("my-app-2.test").</summary>
    public sealed class SiteDomain
    {
        /// <summary>As stored, and as the API takes it for removal.</summary>
        public string Entry { get; init; } = "";
        /// <summary>The full name, e.g. tenant1.app.test.</summary>
        public string Host { get; init; } = "";
        public bool Alias { get; init; }
        public string? Url { get; init; }
        /// <summary>Why it isn't served, e.g. another site uses it.</summary>
        public string? Problem { get; init; }
    }

    public sealed class Site
    {
        public string Name { get; init; } = "";
        public string Domain { get; init; } = "";
        public string Path { get; init; } = "";
        /// <summary>The PHP version the site uses; PhpSource says why (see VersionSource).</summary>
        public string Php { get; init; } = "";
        public string PhpSource { get; init; } = VersionSource.Default;
        /// <summary>What the project pins (in PhpFile), also when an isolation wins over it; empty when nothing is pinned.</summary>
        public string PhpPinned { get; init; } = "";
        public string PhpFile { get; init; } = "";
        /// <summary>The Node.js version (usually a major) the site uses; NodeSource says why.</summary>
        public string? Node { get; init; }
        public string NodeSource { get; init; } = VersionSource.Default;
        public string NodePinned { get; init; } = "";
        public string NodeFile { get; init; } = "";
        /// <summary>A .nvmrc / .node-version that .flockrc's node= wins over, e.g. ".nvmrc: 20".</summary>
        public string NodeShadowed { get; init; } = "";
        public bool Secured { get; init; }
        public bool Parked { get; init; }
        public string? Url { get; init; }
        public string? Problem { get; init; }
        /// <summary>Something that needs a decision but isn't broken, e.g. that no document root was found.</summary>
        public string? Notice { get; init; }
        /// <summary>
        /// The document root relative to Path, with / ("." for the project folder); RootSource says
        /// why: set on this machine (isolated), in the project's .flockrc, or detected (default).
        /// </summary>
        public string Root { get; init; } = ".";
        public string RootSource { get; init; } = VersionSource.Default;
        /// <summary>What the project's .flockrc says (in RootFile), also when this machine's root wins over it.</summary>
        public string RootPinned { get; init; } = "";
        public string RootFile { get; init; } = "";
        /// <summary>"" when the site is served from Root, else one of DocumentRootState; RootSuggestions are then folders with an index.php.</summary>
        public string RootState { get; init; } = "";
        public List<string> RootSuggestions { get; init; } = [];
        public List<SiteDomain> Domains { get; init; } = [];
        /// <summary>PhpStorm breakpoints send every request of the site to Xdebug.</summary>
        public bool Debugging { get; init; }
        /// <summary>What runs the site's PHP: SiteServer.FastCgi (php-cgi) or SiteServer.FrankenPhp.</summary>
        public string Server { get; init; } = SiteServer.FastCgi;
        /// <summary>FrankenPHP's worker mode: "" (classic) or SiteServer.Octane.</summary>
        public string Worker { get; init; } = "";
        /// <summary>The project has Laravel Octane installed, so it can run in worker mode.</summary>
        public bool OctaneAvailable { get; init; }
        /// <summary>The site's FrankenPHP process, when Server is FrankenPHP.</summary>
        [JsonPropertyName("frankenphp")]
        public SiteFrankenPhp? FrankenPhp { get; init; }
        /// <summary>Whether the project uses Laravel Boost and has Flock's guideline: "" (no Boost) or one of BoostState.</summary>
        public string Boost { get; init; } = "";
        /// <summary>The site's tunnel (`flock share`), while there is one.</summary>
        public SiteShare? Share { get; init; }
        /// <summary>Set on a git worktree of another site: that site's name, whose settings it uses.</summary>
        public string? Parent { get; init; }
        /// <summary>A worktree's branch; empty when its HEAD is detached.</summary>
        public string Branch { get; init; } = "";
        /// <summary>A worktree's setup; null until Flock sets it up.</summary>
        public WorktreeSetup? Worktree { get; init; }

        [JsonIgnore]
        public bool IsWorktree => Parent is { Length: > 0 };

        /// <summary>What a worktree is listed as: its branch, or the name it is served under ("feature-auth" of "feature-auth.acme").</summary>
        [JsonIgnore]
        public string WorktreeLabel => Branch.Length > 0 ? Branch : Name.Split('.')[0];

        [JsonIgnore]
        public bool IsFrankenPhp => Server == SiteServer.FrankenPhp;

        [JsonIgnore]
        public bool IsShared => Share?.IsRunning == true;

        /// <summary>The project file pinning PHP sits in the site's own folder (so it can be unpinned here).</summary>
        [System.Text.Json.Serialization.JsonIgnore]
        public bool PhpPinnedHere => PhpFile.Length > 0 && SameDir(PhpFile, Path);

        [System.Text.Json.Serialization.JsonIgnore]
        public bool NodePinnedHere => NodeFile.Length > 0 && SameDir(NodeFile, Path);

        /// <summary>root= is only read from the .flockrc in the site's own folder.</summary>
        [System.Text.Json.Serialization.JsonIgnore]
        public bool RootPinnedHere => RootPinned.Length > 0;

        /// <summary>
        /// A folder as a document root of the site: relative to Path with / ("src/www", "." for the
        /// project folder itself), or null when the folder isn't in the project.
        /// </summary>
        public string? RootFor(string folder)
        {
            // Split by hand, as in SameDir, so the tests run anywhere.
            static string[] Parts(string path) => path.Split(['\\', '/'], StringSplitOptions.RemoveEmptyEntries);
            var project = Parts(Path);
            var chosen = Parts(folder);
            if (chosen.Length < project.Length || !project.SequenceEqual(chosen[..project.Length], StringComparer.OrdinalIgnoreCase))
            {
                return null;
            }
            return chosen.Length == project.Length ? "." : string.Join('/', chosen[project.Length..]);
        }

        // Splits by hand rather than with Path.GetDirectoryName, which ignores '\\' off Windows (tests run anywhere).
        private static bool SameDir(string file, string dir) =>
            string.Equals(file[..Math.Max(0, file.LastIndexOfAny(['\\', '/']))].TrimEnd('\\', '/'), dir.TrimEnd('\\', '/'), StringComparison.OrdinalIgnoreCase);
    }

    /// <summary>Values of Site.RootState: why Flock serves its page instead of the site.</summary>
    public static class DocumentRootState
    {
        /// <summary>The root set on this machine or in .flockrc isn't a folder.</summary>
        public const string Missing = "missing";
        /// <summary>A Composer project without an index.php in public/, www/, web/, webroot/ or its own folder.</summary>
        public const string NotFound = "not_found";
        /// <summary>The root has neither an index.php nor an index.html (yet).</summary>
        public const string Empty = "empty";
    }

    /// <summary>Values of Site.Server and Site.Worker.</summary>
    public static class SiteServer
    {
        public const string FastCgi = "fastcgi";
        public const string FrankenPhp = "frankenphp";
        /// <summary>FrankenPHP's worker mode for Laravel Octane.</summary>
        public const string Octane = "octane";
    }

    /// <summary>Values of Site.Boost: a project's Laravel Boost guidelines and Flock's guideline in them.</summary>
    public static class BoostState
    {
        /// <summary>Uses Boost, without Flock's guideline.</summary>
        public const string Missing = "missing";
        public const string Current = "current";
        /// <summary>Has an older version of Flock's guideline.</summary>
        public const string Outdated = "outdated";
        /// <summary>Has a flock.md Flock didn't write; Flock leaves it alone.</summary>
        public const string Custom = "custom";
    }

    /// <summary>
    /// A site's FrankenPHP process. Until it runs (State other than "running"), php-cgi serves the
    /// site and Problem says why.
    /// </summary>
    public sealed class SiteFrankenPhp
    {
        /// <summary>"running", "installing", "failed" or "unavailable".</summary>
        public string State { get; init; } = "";
        /// <summary>FrankenPHP, e.g. "1.13.0"; may be empty.</summary>
        public string Version { get; init; } = "";
        /// <summary>The embedded PHP, e.g. "8.4.14"; may be empty.</summary>
        public string PhpVersion { get; init; } = "";
        public int Port { get; init; }
        /// <summary>While installing: 0…1, or -1 when unknown.</summary>
        public double Progress { get; init; } = -1;
        public string Problem { get; init; } = "";
        /// <summary>Name for /v1/logs/services/{name}.</summary>
        public string Log { get; init; } = "";

        [JsonIgnore]
        public bool IsRunning => State == "running";

        [JsonIgnore]
        public bool IsInstalling => State == "installing";

        [JsonIgnore]
        public bool HasProgress => Progress is >= 0 and <= 1;
    }

    /// <summary>
    /// A site shared at a temporary public URL through a Cloudflare quick tunnel. It is followed
    /// through the status: building the assets, starting the tunnel, running, or failed (see Error).
    /// </summary>
    public sealed class SiteShare
    {
        public string Site { get; init; } = "";
        /// <summary>One of ShareState.</summary>
        public string State { get; init; } = "";
        public string? Url { get; init; }
        /// <summary>Emails (or *@domain) let in; empty: anyone with the URL.</summary>
        public List<string> Allow { get; init; } = [];
        /// <summary>Debug mode is on for visitors (stack traces), whatever .env says.</summary>
        public bool Debug { get; init; }
        public DateTimeOffset StartedAt { get; init; }
        /// <summary>Set once running: the share ends then.</summary>
        public DateTimeOffset? ExpiresAt { get; init; }
        /// <summary>The assets were built for the share.</summary>
        public bool Built { get; init; }
        /// <summary>The project's assets are built by Flock (Vite), so they can be rebuilt while shared.</summary>
        public bool CanRebuild { get; init; }
        public bool Rebuilding { get; init; }
        public string? RebuildError { get; init; }
        public string? Error { get; init; }
        /// <summary>What to know about how the share protects the app, e.g. that the cached config keeps debug mode on.</summary>
        public string? Warning { get; init; }
        /// <summary>Name for /v1/logs/services/{name}.</summary>
        public string Log { get; init; } = "";
        /// <summary>The URL as a QR code: a base64 PNG.</summary>
        public string? Qr { get; init; }

        [JsonIgnore]
        public bool IsRunning => State == ShareState.Running;

        [JsonIgnore]
        public bool IsFailed => State == ShareState.Failed;
    }

    /// <summary>
    /// The setup of a git worktree Flock serves: copying dependencies and .env, installs, its
    /// database and hooks. It is followed through the status, like a share.
    /// </summary>
    public sealed class WorktreeSetup
    {
        /// <summary>One of WorktreeState.</summary>
        public string State { get; init; } = "";
        /// <summary>What a running setup does now.</summary>
        public string Step { get; init; } = "";
        /// <summary>Why it failed; the worktree is still served.</summary>
        public string Error { get; init; } = "";
        /// <summary>Name for /v1/logs/services/{name}.</summary>
        public string Log { get; init; } = "";
        /// <summary>The database mode asked for: one of WorktreeDatabase, or "" for the project's (flock.yml).</summary>
        public string DbMode { get; init; } = "";
        /// <summary>The worktree's own database; empty when it uses the site's.</summary>
        public string Database { get; init; } = "";

        [JsonIgnore]
        public bool IsBusy => State is WorktreeState.Queued or WorktreeState.Running;

        [JsonIgnore]
        public bool IsFailed => State == WorktreeState.Failed;

        [JsonIgnore]
        public bool HasOwnDatabase => Database.Length > 0;
    }

    /// <summary>Values of WorktreeSetup.State.</summary>
    public static class WorktreeState
    {
        public const string Queued = "queued";
        public const string Running = "running";
        public const string Ready = "ready";
        public const string Failed = "failed";
    }

    /// <summary>A worktree's database modes.</summary>
    public static class WorktreeDatabase
    {
        /// <summary>The site's database (the default).</summary>
        public const string Shared = "shared";
        /// <summary>A copy of the site's, with the branch's new migrations run.</summary>
        public const string Copy = "copy";
        /// <summary>An empty one, with the migrations run.</summary>
        public const string Fresh = "fresh";
    }

    /// <summary>Values of SiteShare.State.</summary>
    public static class ShareState
    {
        public const string Building = "building";
        public const string Starting = "starting";
        public const string Running = "running";
        public const string Failed = "failed";
    }

    /// <summary>Where a site's PHP or Node.js version comes from, in this order.</summary>
    public static class VersionSource
    {
        /// <summary>Set for the site in Flock, on this machine only (flock isolate).</summary>
        public const string Isolated = "isolated";
        /// <summary>Pinned in the project: .flockrc, or for Node .nvmrc / .node-version (flock pin).</summary>
        public const string Project = "project";
        public const string Default = "default";
    }

    public sealed class Process
    {
        public string Name { get; init; } = "";
        public bool Up { get; init; }
        public int Restarts { get; init; }
        public string? LastError { get; init; }
    }
}

/// <summary>POST /v1/worktrees: the new worktree's folder, its branch and its setup.</summary>
public sealed class WorktreeCreated
{
    public string Path { get; init; } = "";
    /// <summary>The branch it is on: the one asked for, or the name Flock made up (e.g. "brave-otter").</summary>
    public string Branch { get; init; } = "";
    public Status.WorktreeSetup Setup { get; init; } = new();
}

/// <summary>GET /v1/sites/{name}/branches: a site's git branches, for where a new worktree's branch starts.</summary>
public sealed class SiteBranches
{
    /// <summary>The branch the site's checkout is on; empty when its HEAD is detached.</summary>
    public string Current { get; init; } = "";
    /// <summary>The repository's default branch (origin's HEAD, else main or master); empty when unknown.</summary>
    public string Default { get; init; } = "";
    /// <summary>The local branches, sorted by name.</summary>
    public List<string> Local { get; init; } = [];
    /// <summary>The remote-tracking branches ("origin/main") as git last fetched them, sorted by name.</summary>
    public List<string> Remote { get; init; } = [];

    /// <summary>The local branches as offered: the current one, the default one, then the others.</summary>
    public List<string> Ordered() =>
        new[] { Current, Default }.Where(b => b.Length > 0 && Local.Contains(b)).Concat(Local).Distinct().ToList();

    /// <summary>
    /// The name a worktree on branch is served under, as the daemon makes it (config.WorktreeSlug):
    /// lowercase letters, digits and -, with /, _ and . turned into -.
    /// </summary>
    public static string Slug(string branch)
    {
        var slug = new System.Text.StringBuilder();
        foreach (var c in branch.ToLowerInvariant())
        {
            if (c is (>= 'a' and <= 'z') or (>= '0' and <= '9') or '-')
            {
                slug.Append(c);
            }
            else if (c is '/' or '_' or '.')
            {
                slug.Append('-');
            }
        }
        var text = slug.ToString();
        while (text.Contains("--", StringComparison.Ordinal))
        {
            text = text.Replace("--", "-", StringComparison.Ordinal);
        }
        text = text.Trim('-');
        return text.Length > 63 ? text[..63].TrimEnd('-') : text;
    }
}

/// <summary>An entry of GET /v1/mail.</summary>
public sealed class MailSummary
{
    public string Id { get; init; } = "";
    public string Inbox { get; init; } = "";
    public string From { get; init; } = "";
    public List<string> To { get; init; } = [];
    public string Subject { get; init; } = "";
    public DateTimeOffset Date { get; init; }
    public int Size { get; init; }
}

/// <summary>GET /v1/mail/{id}</summary>
public sealed class MailDetail
{
    public string Id { get; init; } = "";
    public string Inbox { get; init; } = "";
    public string From { get; init; } = "";
    public List<string> To { get; init; } = [];
    public List<string> Cc { get; init; } = [];
    public string Subject { get; init; } = "";
    public DateTimeOffset Date { get; init; }
    public string Html { get; init; } = "";
    public string Text { get; init; } = "";
    public List<Header> Headers { get; init; } = [];
    public List<Attachment> Attachments { get; init; } = [];

    public sealed class Header
    {
        public string Name { get; init; } = "";
        public string Value { get; init; } = "";
    }

    public sealed class Attachment
    {
        public int Index { get; init; }
        public string Filename { get; init; } = "";
        public string ContentType { get; init; } = "";
        public long Size { get; init; }
    }
}

/// <summary>An entry of GET /v1/dumps.</summary>
public sealed class Dump
{
    public string Id { get; init; } = "";
    public DateTimeOffset Time { get; init; }
    public string Site { get; init; } = "";
    public DumpSource? Source { get; init; }
    public DumpRequest? Request { get; init; }
    public string? CommandLine { get; init; }
    /// <summary>Shared by every dump of one HTTP request or CLI run.</summary>
    public string Group { get; init; } = "";
    public string? Label { get; init; }
    public string Text { get; init; } = "";
    /// <summary>Dump body only; render it after the shared header from FlockClient.DumpHeaderAsync.</summary>
    public string Html { get; set; } = "";

    /// <summary>"Controllers/HomeController.php:42" — the last path components keep rows short.</summary>
    [JsonIgnore]
    public string? ShortSource
    {
        get
        {
            if (Source is null || Source.File.Length == 0)
            {
                return null;
            }
            var parts = string.Join("/", Source.File.Split('/', '\\').TakeLast(2));
            return Source.Line > 0 ? $"{parts}:{Source.Line}" : parts;
        }
    }

    /// <summary>"GET https://app.test/x" for requests, the command line for CLI runs.</summary>
    [JsonIgnore]
    public string Origin
    {
        get
        {
            if (Request is { Uri.Length: > 0 })
            {
                return $"{Request.Method} {Request.Uri}";
            }
            return CommandLine ?? "";
        }
    }
}

public sealed class DumpSource
{
    public string File { get; init; } = "";
    public int Line { get; init; }
    public string Name { get; init; } = "";
}

public sealed class DumpRequest
{
    public string Method { get; init; } = "";
    public string Uri { get; init; } = "";
}

/// <summary>Values of RuntimeEvent.Kind, which are also what Status.CaptureOff lists.</summary>
public static class EventKind
{
    public const string Query = "query";
    public const string Log = "log";
    public const string View = "view";
    public const string Job = "job";
    public const string Request = "request";

    /// <summary>Every kind the timeline renders, in menu order; events of other kinds are ignored.</summary>
    public static IReadOnlyList<string> All { get; } = [Query, Log, View, Job, Request];

    public static string Name(string kind) => kind switch
    {
        Query => "Queries",
        Log => "Logs",
        View => "Views",
        Job => "Jobs",
        Request => "Requests",
        _ => kind,
    };
}

/// <summary>
/// An entry of GET /v1/events: a query, log, view, job or request of the same request / CLI run
/// (Group) as its dumps. The kind-specific fields are only set for their kind.
/// </summary>
public sealed class RuntimeEvent
{
    public string Id { get; init; } = "";
    /// <summary>One of EventKind; unknown kinds are kept but not rendered.</summary>
    public string Kind { get; init; } = "";
    public DateTimeOffset Time { get; init; }
    public string Site { get; init; } = "";
    public string Group { get; init; } = "";
    public DumpRequest? Request { get; init; }
    public string? CommandLine { get; init; }
    /// <summary>The first frame outside vendor/, when known.</summary>
    public DumpSource? Source { get; init; }

    // query
    public string Sql { get; init; } = "";
    public List<JsonElement> Bindings { get; init; } = [];
    public string Connection { get; init; } = "";
    /// <summary>Queries, processed or failed jobs and requests.</summary>
    public double? DurationMs { get; init; }

    // log
    public string Level { get; init; } = "";
    public string Message { get; init; } = "";
    /// <summary>The log context, an object (may be empty).</summary>
    public JsonElement? Context { get; init; }

    // view
    public string View { get; init; } = "";
    public string Path { get; init; } = "";

    // job
    public string Job { get; init; } = "";
    public string Queue { get; init; } = "";
    /// <summary>"processing", "processed" or "failed".</summary>
    public string State { get; init; } = "";
    public string Exception { get; init; } = "";

    // request
    public int? StatusCode { get; init; }

    /// <summary>The kinds the timeline renders (see EventKind.All).</summary>
    [JsonIgnore]
    public bool IsKnown => EventKind.All.Contains(Kind);

    /// <summary>"GET https://app.test/x" for requests, the command line for CLI runs.</summary>
    [JsonIgnore]
    public string Origin
    {
        get
        {
            if (Request is { Uri.Length: > 0 })
            {
                return $"{Request.Method} {Request.Uri}";
            }
            return CommandLine ?? "";
        }
    }

    /// <summary>The bindings as JSON ("[0, \"a\"]"); empty without bindings.</summary>
    [JsonIgnore]
    public string BindingsText => Bindings.Count == 0 ? "" : "[" + string.Join(", ", Bindings.Select(b => b.GetRawText())) + "]";

    /// <summary>The log context as JSON; empty when there is none or it is empty.</summary>
    [JsonIgnore]
    public string ContextText => Context switch
    {
        { ValueKind: JsonValueKind.Object } c when c.EnumerateObject().Any() => JsonSerializer.Serialize(c, IndentedJson),
        { ValueKind: JsonValueKind.Array } c when c.GetArrayLength() > 0 => JsonSerializer.Serialize(c, IndentedJson),
        _ => "",
    };

    private static readonly JsonSerializerOptions IndentedJson = new() { WriteIndented = true };
}

/// <summary>GET /v1/logs</summary>
public sealed class LogIndex
{
    public List<string> Services { get; init; } = [];
    public List<string> Sites { get; init; } = [];
}

/// <summary>An entry of GET /v1/logs/sites/{name}: one parsed Laravel log record.</summary>
public sealed class LogEntry
{
    private readonly string level = "info";

    public DateTimeOffset Time { get; init; }
    public string Env { get; init; } = "";
    public string Level { get => level; init => level = value.ToLowerInvariant(); }
    public string Message { get; init; } = "";
    public string Details { get; init; } = "";
}

/// <summary>One template entry from GET /v1/apps/templates (apps.conf).</summary>
public sealed class AppTemplate
{
    public string Name { get; init; } = "";
    public bool RequiresDb { get; init; }
}

/// <summary>GET /v1/apps/templates</summary>
public sealed class AppTemplatesResponse
{
    public List<AppTemplate> Templates { get; init; } = [];
}