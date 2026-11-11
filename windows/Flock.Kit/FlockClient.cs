using System.Net.Http.Headers;
using System.Text;
using System.Text.Json;

namespace Flock.Kit;

public class FlockException(string message, Exception? innerException = null) : Exception(message, innerException);

/// <summary>Nothing answers on the daemon's API.</summary>
public sealed class FlockNotRunningException(Exception? innerException = null) : FlockException("Flock is not running", innerException);

/// <summary>The daemon answered with an error.</summary>
public sealed class FlockServerException(int status, string message, Exception? innerException = null)
    : FlockException(message.Length == 0 ? $"Flock answered with HTTP {status}" : message, innerException)
{
    public int StatusCode { get; } = status;
}


/// <summary>The daemon may have applied a write before the connection failed.</summary>
public sealed class FlockRequestOutcomeUnknownException(Exception innerException)
    : FlockException("The connection failed. The change may have completed; refresh its state before retrying.", innerException);

/// <summary>Talks to the daemon's local HTTP API (the loopback listener described by api.json).</summary>
public sealed class FlockClient : IDisposable
{
    private readonly string configPath;
    // Installing or uninstalling PHP waits for the daemon's work (the Go CLI allows 2 min too).
    private static readonly TimeSpan PhpChangeTimeout = TimeSpan.FromMinutes(2);
    // Installing or updating PostgreSQL downloads a server build and waits for it (and initdb).
    private static readonly TimeSpan PostgresDownloadTimeout = TimeSpan.FromMinutes(10);
    // Switching a site to FrankenPHP or restarting it waits for the process to come up (~20 s at most).
    private static readonly TimeSpan ServerSwitchTimeout = TimeSpan.FromMinutes(1);
    // Creating a worktree runs git worktree add (the Go CLI allows 2 min); removing one runs its before_delete hook too.
    private static readonly TimeSpan WorktreeCreateTimeout = TimeSpan.FromMinutes(2);
    private static readonly TimeSpan WorktreeRemoveTimeout = TimeSpan.FromMinutes(10);
    // Registering with Claude Code runs its CLI.
    private static readonly TimeSpan McpChangeTimeout = TimeSpan.FromSeconds(30);
    // Adding Flock's Boost guideline runs `php artisan boost:update`, which boots the app.
    private static readonly TimeSpan BoostTimeout = TimeSpan.FromMinutes(2);
    // Creating an app may download and extract an archive like WordPress (CLI allows 2 min).
    private static readonly TimeSpan AppCreateTimeout = TimeSpan.FromMinutes(3);

    private readonly HttpClient http;
    private readonly TimeProvider timeProvider;
    private readonly SemaphoreSlim configLock = new(1, 1);
    private ApiConfig? config;

    /// <summary>Creates a client over an application-owned transport. The transport is not disposed by this client.</summary>
    public FlockClient(HttpClient http, string? configPath = null, TimeProvider? timeProvider = null)
    {
        ArgumentNullException.ThrowIfNull(http);
        this.configPath = configPath ?? FlockPaths.ApiJson;
        this.http = http;
        this.timeProvider = timeProvider ?? TimeProvider.System;
    }

    /// <summary>Releases client-local configuration locking. The application-owned transport remains live.</summary>
    public void Dispose() => configLock.Dispose();

    // Endpoints

    public Task<Status> StatusAsync(CancellationToken cancellationToken = default) => Get<Status>("/v1/status", cancellationToken: cancellationToken);

    /// <summary>Changes only the non-null settings and returns the new status.</summary>
    public Task<Status> UpdateSettingsAsync(SettingsChange change, CancellationToken cancellationToken = default) => Send<Status>(HttpMethod.Post, "/v1/settings", change, cancellationToken: cancellationToken);

    public Task<FlockConfig> ConfigAsync(CancellationToken cancellationToken = default) => Get<FlockConfig>("/v1/config", cancellationToken: cancellationToken);

    public Task<List<Service>> ServicesAsync(CancellationToken cancellationToken = default) => Get<List<Service>>("/v1/services", cancellationToken: cancellationToken);

    public Task<List<Service>> StartServiceAsync(string id, CancellationToken cancellationToken = default) => Send<List<Service>>(HttpMethod.Post, $"/v1/services/{Escape(id)}/start", cancellationToken: cancellationToken);

    public Task<List<Service>> StopServiceAsync(string id, CancellationToken cancellationToken = default) => Send<List<Service>>(HttpMethod.Post, $"/v1/services/{Escape(id)}/stop", cancellationToken: cancellationToken);

    public Task<List<Service>> SetServicePortAsync(string id, int port, CancellationToken cancellationToken = default) =>
        Send<List<Service>>(HttpMethod.Patch, $"/v1/services/{Escape(id)}", new { port }, cancellationToken: cancellationToken);

    /// <summary>Links a directory as a site (the name defaults to the directory name on the daemon side).</summary>
    public Task<Status> LinkSiteAsync(string path, string? name = null, string? php = null, bool secured = false, CancellationToken cancellationToken = default) =>
        Send<Status>(HttpMethod.Post, "/v1/sites", new { path, name, php, secured }, cancellationToken: cancellationToken);

    /// <summary>Lists available templates from apps.conf.</summary>
    public async Task<List<AppTemplate>> AppTemplatesAsync(CancellationToken cancellationToken = default) =>
        (await Get<AppTemplatesResponse>("/v1/apps/templates", cancellationToken: cancellationToken)).Templates;

    /// <summary>Creates a new project directory from an apps.conf template and links it as a site.</summary>
    public Task<Status> CreateAppAsync(string name, string parentDir, string type = "php", bool db = false, string? dbService = null, string? php = null, bool secured = false, CancellationToken cancellationToken = default) =>
        Send<Status>(HttpMethod.Post, "/v1/apps", new { name, parent_dir = parentDir, type, db, db_service = dbService, php, secured }, timeout: AppCreateTimeout, cancellationToken: cancellationToken);

    /// <summary>Switching the server or worker mode reloads the web server and may start FrankenPHP, so it gets longer.</summary>
    public Task<Status> UpdateSiteAsync(string name, SiteChange change, CancellationToken cancellationToken = default) =>
        Send<Status>(HttpMethod.Patch, $"/v1/sites/{Escape(name)}", change,
            timeout: change.Server is not null || change.Worker is not null ? ServerSwitchTimeout : null, cancellationToken: cancellationToken);

    /// <summary>Restarts a FrankenPHP site's process, e.g. so Octane's workers load changed code.</summary>
    public Task<Status> RestartSiteAsync(string name, CancellationToken cancellationToken = default) => Send<Status>(HttpMethod.Post, $"/v1/sites/{Escape(name)}/restart", timeout: ServerSwitchTimeout, cancellationToken: cancellationToken);

    public Task<Status> UnlinkSiteAsync(string name, CancellationToken cancellationToken = default) => Send<Status>(HttpMethod.Delete, $"/v1/sites/{Escape(name)}", cancellationToken: cancellationToken);

    /// <summary>
    /// Serves &lt;name&gt;.test from upstream (a port, host:port or URL), adding the proxy or changing its
    /// upstream. A null secured keeps the proxy's setting (HTTP for a new one).
    /// </summary>
    public Task<Status> ProxyAsync(string name, string upstream, bool? secured = null, CancellationToken cancellationToken = default) =>
        Send<Status>(HttpMethod.Post, "/v1/proxies", new ProxyRequest(name, upstream, secured), cancellationToken: cancellationToken);

    /// <summary>Serves a proxy over HTTPS (on) or plain HTTP.</summary>
    public Task<Status> SecureProxyAsync(string name, bool secured, CancellationToken cancellationToken = default) =>
        Send<Status>(HttpMethod.Patch, $"/v1/proxies/{Escape(name)}", new { secured }, cancellationToken: cancellationToken);

    /// <summary>Stops serving a proxy; the server at its upstream keeps running.</summary>
    public Task<Status> RemoveProxyAsync(string name, CancellationToken cancellationToken = default) =>
        Send<Status>(HttpMethod.Delete, $"/v1/proxies/{Escape(name)}", cancellationToken: cancellationToken);

    // Secured is left out when null (FlockJson ignores nulls), so the daemon keeps an existing proxy's setting.
    private sealed record ProxyRequest(string Name, string Upstream, bool? Secured);

    /// <summary>Adds an extra domain: a subdomain ("tenant1", "a.b") or an alias ("my-app-2.test").</summary>
    public Task<Status> AddDomainAsync(string site, string domain, CancellationToken cancellationToken = default) =>
        Send<Status>(HttpMethod.Post, $"/v1/sites/{Escape(site)}/domains", new { domain }, cancellationToken: cancellationToken);

    /// <summary>Removes an extra domain, given as SiteDomain.Entry (or its full name).</summary>
    public Task<Status> RemoveDomainAsync(string site, string domain, CancellationToken cancellationToken = default) =>
        Send<Status>(HttpMethod.Delete, $"/v1/sites/{Escape(site)}/domains/{Escape(domain)}", cancellationToken: cancellationToken);

    /// <summary>
    /// Writes a version ("php" or "node"; empty version: the one the site uses now) into the
    /// site's project files and lifts the site's isolation for that tool.
    /// </summary>
    public Task<Status> PinAsync(string site, string tool, string version = "", CancellationToken cancellationToken = default) =>
        Send<Status>(HttpMethod.Post, $"/v1/sites/{Escape(site)}/pin", new { tool, version }, cancellationToken: cancellationToken);

    /// <summary>Removes a tool's version from the site's project files.</summary>
    public Task<Status> UnpinAsync(string site, string tool, CancellationToken cancellationToken = default) => Send<Status>(HttpMethod.Delete, $"/v1/sites/{Escape(site)}/pin/{Escape(tool)}", cancellationToken: cancellationToken);

    /// <summary>
    /// Writes Flock's guideline into the site's Laravel Boost guidelines (.ai\guidelines\flock.md) and
    /// runs `php artisan boost:update`, so the agents' guideline files include it.
    /// </summary>
    public Task<Status> AddBoostGuidelineAsync(string site, CancellationToken cancellationToken = default) => Send<Status>(HttpMethod.Post, $"/v1/sites/{Escape(site)}/boost", timeout: BoostTimeout, cancellationToken: cancellationToken);

    /// <summary>
    /// Shares a site at a temporary public URL; allow lists the emails (or *@domain) let in, none for
    /// anyone with the URL, and debug turns the app's debug mode on for visitors. It returns at once:
    /// the status follows the share until it runs.
    /// </summary>
    public Task<Status.SiteShare> StartShareAsync(string site, IReadOnlyList<string> allow, bool debug = false, CancellationToken cancellationToken = default) =>
        Send<Status.SiteShare>(HttpMethod.Post, $"/v1/shares/{Escape(site)}", new { allow, debug }, cancellationToken: cancellationToken);

    /// <summary>Builds a running share's assets again, so visitors see the latest changes when they reload.</summary>
    public Task<Status.SiteShare> RebuildShareAsync(string site, CancellationToken cancellationToken = default) =>
        Send<Status.SiteShare>(HttpMethod.Post, $"/v1/shares/{Escape(site)}/rebuild", cancellationToken: cancellationToken);

    /// <summary>Ends a site's share (and forgets a failed one).</summary>
    public Task<List<Status.SiteShare>> StopShareAsync(string site, CancellationToken cancellationToken = default) =>
        Send<List<Status.SiteShare>>(HttpMethod.Delete, $"/v1/shares/{Escape(site)}", cancellationToken: cancellationToken);

    /// <summary>A site's git branches, for where a new worktree's branch starts.</summary>
    public Task<SiteBranches> SiteBranchesAsync(string site, CancellationToken cancellationToken = default) =>
        Get<SiteBranches>($"/v1/sites/{Escape(site)}/branches", cancellationToken: cancellationToken);

    /// <summary>
    /// Creates a git worktree of site on branch (checked out when it exists, else a new branch from
    /// from, the current commit by default; an empty branch gets a name Flock makes up), at path (null: where flock.yml says), with the database
    /// mode db (null: the project's). It returns once git has created it: the status follows its setup.
    /// </summary>
    public Task<WorktreeCreated> CreateWorktreeAsync(string site, string branch, string? from = null, string? path = null, string? db = null, CancellationToken cancellationToken = default) =>
        Send<WorktreeCreated>(HttpMethod.Post, "/v1/worktrees", new { site, branch, from, path, db }, timeout: WorktreeCreateTimeout, cancellationToken: cancellationToken);

    /// <summary>Starts the setup of the worktree at path, or joins the one that runs; again sets up one that is set up already.</summary>
    public Task<Status.WorktreeSetup> SetupWorktreeAsync(string path, bool again = false, string? db = null, CancellationToken cancellationToken = default) =>
        Send<Status.WorktreeSetup>(HttpMethod.Post, "/v1/worktrees/setup", new { path, again, db }, cancellationToken: cancellationToken);

    /// <summary>The setup (or database switch) of the worktree at path: a 404 while Flock hasn't set it up.</summary>
    public Task<Status.WorktreeSetup> WorktreeSetupAsync(string path, CancellationToken cancellationToken = default) =>
        Get<Status.WorktreeSetup>("/v1/worktrees/setup?path=" + Uri.EscapeDataString(path), cancellationToken: cancellationToken);

    /// <summary>
    /// Gives the worktree a database by mode (WorktreeDatabase), as its job: the status follows it.
    /// The database Flock made for it before is dropped.
    /// </summary>
    public Task<Status.WorktreeSetup> SwitchWorktreeDatabaseAsync(string path, string db, CancellationToken cancellationToken = default) =>
        Send<Status.WorktreeSetup>(HttpMethod.Post, "/v1/worktrees/db", new { path, db }, cancellationToken: cancellationToken);

    /// <summary>
    /// Runs the worktree's before_delete hook, removes it with git and drops the database Flock made
    /// for it; the branch stays. force removes it even with changes or a failing hook. Returns what
    /// the hook and git printed.
    /// </summary>
    public async Task<string> RemoveWorktreeAsync(string path, bool force = false, CancellationToken cancellationToken = default) =>
        (await Send<WorktreeRemoval>(HttpMethod.Post, "/v1/worktrees/remove", new { path, force }, timeout: WorktreeRemoveTimeout, cancellationToken: cancellationToken)).Output;

    private sealed class WorktreeRemoval
    {
        public string Output { get; init; } = "";
    }

    public Task<ExtensionReport> SiteExtensionsAsync(string site, CancellationToken cancellationToken = default) => Get<ExtensionReport>($"/v1/sites/{Escape(site)}/extensions", cancellationToken: cancellationToken);

    /// <summary>Adds the extension to the site's composer.json (ext-NAME) with Composer (up to 5 minutes).</summary>
    public Task<ExtensionReport> RequireExtensionAsync(string site, string name, CancellationToken cancellationToken = default) => Send<ExtensionReport>(HttpMethod.Post, $"/v1/sites/{Escape(site)}/extensions/{Escape(name)}/require", timeout: TimeSpan.FromMinutes(5), cancellationToken: cancellationToken);

    /// <summary>The AI clients Flock's MCP server can be registered with.</summary>
    public Task<McpState> McpAsync(CancellationToken cancellationToken = default) => Get<McpState>("/v1/mcp", cancellationToken: cancellationToken);

    /// <summary>Registers Flock's MCP server with a client ("claude-code", "cursor", "vscode", "windsurf", "codex").</summary>
    public Task<McpState> RegisterMcpAsync(string client, CancellationToken cancellationToken = default) => Send<McpState>(HttpMethod.Post, $"/v1/mcp/clients/{Escape(client)}", timeout: McpChangeTimeout, cancellationToken: cancellationToken);

    /// <summary>Removes Flock's MCP server from a client; nothing else in its configuration changes.</summary>
    public Task<McpState> UnregisterMcpAsync(string client, CancellationToken cancellationToken = default) => Send<McpState>(HttpMethod.Delete, $"/v1/mcp/clients/{Escape(client)}", timeout: McpChangeTimeout, cancellationToken: cancellationToken);

    public Task<Status> ParkAsync(string path, CancellationToken cancellationToken = default) => Send<Status>(HttpMethod.Post, "/v1/parked", new { path }, cancellationToken: cancellationToken);

    public Task<Status> UnparkAsync(string path, CancellationToken cancellationToken = default) => Send<Status>(HttpMethod.Delete, "/v1/parked", new { path }, cancellationToken: cancellationToken);

    // Node.js versions

    /// <summary>The Node.js majors; the daemon may ask npm's registry first, which can take a while.</summary>
    public Task<NodeCatalog> NodeVersionsAsync(CancellationToken cancellationToken = default) => Send<NodeCatalog>(HttpMethod.Get, "/v1/node/versions", timeout: TimeSpan.FromMinutes(1), cancellationToken: cancellationToken);

    /// <summary>Installs the newest release of a major line, or updates it; a download of about 30 MB.</summary>
    public Task<NodeCatalog> InstallNodeMajorAsync(string major, CancellationToken cancellationToken = default) =>
        Send<NodeCatalog>(HttpMethod.Post, $"/v1/node/versions/{Escape(major)}/install", timeout: TimeSpan.FromMinutes(10), cancellationToken: cancellationToken);

    /// <summary>Updates a line's npm to the newest version that supports it; its global packages stay.</summary>
    public Task<NodeCatalog> UpdateNpmAsync(string major, CancellationToken cancellationToken = default) =>
        Send<NodeCatalog>(HttpMethod.Post, $"/v1/node/versions/{Escape(major)}/npm", timeout: TimeSpan.FromMinutes(10), cancellationToken: cancellationToken);

    public Task<NodeCatalog> UninstallNodeAsync(string major, CancellationToken cancellationToken = default) => Send<NodeCatalog>(HttpMethod.Delete, $"/v1/node/versions/{Escape(major)}", cancellationToken: cancellationToken);

    /// <summary>Asks the daemon to fetch the package manifest now.</summary>
    public Task<NodeCatalog> CheckNodeReleasesAsync(CancellationToken cancellationToken = default) => Send<NodeCatalog>(HttpMethod.Post, "/v1/node/check", timeout: TimeSpan.FromMinutes(1), cancellationToken: cancellationToken);

    // PHP versions

    public Task<PhpCatalog> PhpVersionsAsync(CancellationToken cancellationToken = default) => Get<PhpCatalog>("/v1/php/versions", cancellationToken: cancellationToken);

    /// <summary>Starts installing (or updating to) the channel's newest release of a version; follow it with PhpVersionsAsync.</summary>
    public Task<PhpCatalog> InstallPhpAsync(string minor, CancellationToken cancellationToken = default) => Send<PhpCatalog>(HttpMethod.Post, $"/v1/php/versions/{Escape(minor)}/install", timeout: PhpChangeTimeout, cancellationToken: cancellationToken);

    public Task<PhpCatalog> CancelPhpInstallAsync(string minor, CancellationToken cancellationToken = default) => Send<PhpCatalog>(HttpMethod.Post, $"/v1/php/versions/{Escape(minor)}/cancel", timeout: PhpChangeTimeout, cancellationToken: cancellationToken);

    /// <summary>Forgets a failed install.</summary>
    public Task<PhpCatalog> DismissPhpJobAsync(string minor, CancellationToken cancellationToken = default) => Send<PhpCatalog>(HttpMethod.Delete, $"/v1/php/versions/{Escape(minor)}/job", cancellationToken: cancellationToken);

    public Task<PhpCatalog> UninstallPhpAsync(string minor, CancellationToken cancellationToken = default) => Send<PhpCatalog>(HttpMethod.Delete, $"/v1/php/versions/{Escape(minor)}", timeout: PhpChangeTimeout, cancellationToken: cancellationToken);

    /// <summary>Asks the daemon to fetch the binaries manifest now.</summary>
    public Task<PhpCatalog> CheckPhpReleasesAsync(CancellationToken cancellationToken = default) => Send<PhpCatalog>(HttpMethod.Post, "/v1/php/check", timeout: TimeSpan.FromMinutes(1), cancellationToken: cancellationToken);

    public async Task<string> PhpInstallLogAsync(string minor, CancellationToken cancellationToken = default) => await Text(HttpMethod.Get, $"/v1/php/versions/{Escape(minor)}/log", cancellationToken);

    // FrankenPHP builds

    /// <summary>The builds per PHP minor; 501 (FlockServerException) when FrankenPHP isn't supported here.</summary>
    public Task<FrankenPhpState> FrankenPhpAsync(CancellationToken cancellationToken = default) => Get<FrankenPhpState>("/v1/frankenphp", cancellationToken: cancellationToken);

    /// <summary>Asks the daemon to fetch the list of builds now.</summary>
    public Task<FrankenPhpState> CheckFrankenPhpAsync(CancellationToken cancellationToken = default) => Send<FrankenPhpState>(HttpMethod.Post, "/v1/frankenphp/check", timeout: TimeSpan.FromMinutes(1), cancellationToken: cancellationToken);

    /// <summary>Starts installing, updating or retrying a minor's build in the background; poll FrankenPhpAsync.</summary>
    public Task<FrankenPhpState> InstallFrankenPhpAsync(string minor, CancellationToken cancellationToken = default) => Send<FrankenPhpState>(HttpMethod.Post, $"/v1/frankenphp/{Escape(minor)}/install", cancellationToken: cancellationToken);

    public Task<FrankenPhpState> CancelFrankenPhpJobAsync(string minor, CancellationToken cancellationToken = default) => Send<FrankenPhpState>(HttpMethod.Post, $"/v1/frankenphp/{Escape(minor)}/cancel", cancellationToken: cancellationToken);

    /// <summary>Forgets a failed install.</summary>
    public Task<FrankenPhpState> DismissFrankenPhpJobAsync(string minor, CancellationToken cancellationToken = default) => Send<FrankenPhpState>(HttpMethod.Delete, $"/v1/frankenphp/{Escape(minor)}/job", cancellationToken: cancellationToken);

    /// <summary>Removes a minor's build; 409 (FlockServerException) while a site runs on it.</summary>
    public Task<FrankenPhpState> UninstallFrankenPhpAsync(string minor, CancellationToken cancellationToken = default) =>
        Send<FrankenPhpState>(HttpMethod.Delete, $"/v1/frankenphp/{Escape(minor)}", timeout: TimeSpan.FromMinutes(1), cancellationToken: cancellationToken);

    /// <summary>The end of the current or last install's output; 404 (FlockServerException) when there is none.</summary>
    public Task<string> FrankenPhpLogAsync(string minor, CancellationToken cancellationToken = default) => Text(HttpMethod.Get, $"/v1/frankenphp/{Escape(minor)}/log", cancellationToken);

    // PostgreSQL versions

    /// <summary>From the daemon's cached manifest, so it answers at once.</summary>
    public Task<PostgresVersions> PostgresVersionsAsync(CancellationToken cancellationToken = default) => Get<PostgresVersions>("/v1/postgres/versions", cancellationToken: cancellationToken);

    /// <summary>Asks the daemon to look for new PostgreSQL releases now.</summary>
    public Task<PostgresVersions> CheckPostgresReleasesAsync(CancellationToken cancellationToken = default) =>
        Send<PostgresVersions>(HttpMethod.Post, "/v1/postgres/check", timeout: TimeSpan.FromMinutes(1), cancellationToken: cancellationToken);

    /// <summary>Downloads and installs a major's newest release; the first one installed becomes active and starts.</summary>
    public Task<PostgresVersions> InstallPostgresAsync(string major, CancellationToken cancellationToken = default) =>
        Send<PostgresVersions>(HttpMethod.Post, $"/v1/postgres/versions/{Escape(major)}/install", timeout: PostgresDownloadTimeout, cancellationToken: cancellationToken);

    /// <summary>Installs a major with the extensions it has a build of.</summary>
    public Task<PostgresVersions> InstallPostgresAsync(string major, IReadOnlyList<string> extensions, CancellationToken cancellationToken = default) =>
        Send<PostgresVersions>(HttpMethod.Post, $"/v1/postgres/versions/{Escape(major)}/install", new { extensions }, PostgresDownloadTimeout, cancellationToken);

    // PostgreSQL extensions

    /// <summary>The extensions of a major; creates its folder for your own extensions.</summary>
    public Task<PostgresExtensions> PostgresExtensionsAsync(string major, CancellationToken cancellationToken = default) =>
        Get<PostgresExtensions>($"/v1/postgres/versions/{Escape(major)}/extensions", cancellationToken);

    /// <summary>Downloads an extension for a major; a preloaded one (TimescaleDB) restarts that major when it runs.</summary>
    public Task<PostgresExtensions> InstallPostgresExtensionAsync(string major, string name, CancellationToken cancellationToken = default) =>
        Send<PostgresExtensions>(HttpMethod.Post, $"/v1/postgres/versions/{Escape(major)}/extensions/{Escape(name)}/install", timeout: PostgresDownloadTimeout, cancellationToken: cancellationToken);

    public Task<PostgresExtensions> UpdatePostgresExtensionAsync(string major, string name, CancellationToken cancellationToken = default) =>
        Send<PostgresExtensions>(HttpMethod.Post, $"/v1/postgres/versions/{Escape(major)}/extensions/{Escape(name)}/update", timeout: PostgresDownloadTimeout, cancellationToken: cancellationToken);

    public Task<PostgresExtensions> UninstallPostgresExtensionAsync(string major, string name, CancellationToken cancellationToken = default) =>
        Send<PostgresExtensions>(HttpMethod.Delete, $"/v1/postgres/versions/{Escape(major)}/extensions/{Escape(name)}", timeout: TimeSpan.FromMinutes(2), cancellationToken: cancellationToken);

    /// <summary>Installs a major's newest release over the installed one; its databases stay, a running server restarts.</summary>
    public Task<PostgresVersions> UpdatePostgresAsync(string major, CancellationToken cancellationToken = default) =>
        Send<PostgresVersions>(HttpMethod.Post, $"/v1/postgres/versions/{Escape(major)}/update", timeout: PostgresDownloadTimeout, cancellationToken: cancellationToken);

    /// <summary>Stops the active major and starts this one with its own databases.</summary>
    public Task<PostgresVersions> ActivatePostgresAsync(string major, CancellationToken cancellationToken = default) =>
        Send<PostgresVersions>(HttpMethod.Post, $"/v1/postgres/versions/{Escape(major)}/activate", timeout: TimeSpan.FromMinutes(2), cancellationToken: cancellationToken);

    /// <summary>Removes a major's binaries; its data directory goes too only when asked.</summary>
    public Task<PostgresVersions> UninstallPostgresAsync(string major, bool deleteData = false, CancellationToken cancellationToken = default) =>
        Send<PostgresVersions>(HttpMethod.Delete, $"/v1/postgres/versions/{Escape(major)}" + (deleteData ? "?delete_data=1" : ""), timeout: TimeSpan.FromMinutes(2), cancellationToken: cancellationToken);

    // Catalog (MySQL, MariaDB, Redis, MongoDB, Meilisearch, Typesense, RustFS)

    public Task<ServiceCatalog> CatalogAsync(CancellationToken cancellationToken = default) => Get<ServiceCatalog>("/v1/catalog", cancellationToken: cancellationToken);

    public Task<ServiceCatalog> CheckCatalogAsync(CancellationToken cancellationToken = default) =>
        Send<ServiceCatalog>(HttpMethod.Post, "/v1/catalog/check", timeout: TimeSpan.FromMinutes(1), cancellationToken: cancellationToken);

    /// <summary>
    /// Starts installing the newest release of a line on a port in the background (null: the
    /// installed line or the newest, and the configured or suggested port); poll CatalogAsync.
    /// </summary>
    public Task<ServiceCatalog> InstallCatalogServiceAsync(string id, string? line = null, int? port = null, CancellationToken cancellationToken = default) =>
        Send<ServiceCatalog>(HttpMethod.Post, $"/v1/catalog/{Escape(id)}/install", new { line = line ?? "", port = port ?? 0 }, cancellationToken: cancellationToken);

    public Task<ServiceCatalog> CancelCatalogJobAsync(string id, CancellationToken cancellationToken = default) => Send<ServiceCatalog>(HttpMethod.Post, $"/v1/catalog/{Escape(id)}/cancel", cancellationToken: cancellationToken);

    /// <summary>Forgets a failed install.</summary>
    public Task<ServiceCatalog> DismissCatalogJobAsync(string id, CancellationToken cancellationToken = default) => Send<ServiceCatalog>(HttpMethod.Delete, $"/v1/catalog/{Escape(id)}/job", cancellationToken: cancellationToken);

    /// <summary>The end of the current or last install's output; 404 (FlockServerException) when there is none.</summary>
    public Task<string> CatalogLogAsync(string id, CancellationToken cancellationToken = default) => Text(HttpMethod.Get, $"/v1/catalog/{Escape(id)}/log", cancellationToken);

    /// <summary>Runs an installed line of a catalog service instead of the active one, on the service's port.</summary>
    public Task<ServiceCatalog> ActivateCatalogServiceAsync(string id, string line, CancellationToken cancellationToken = default) =>
        Send<ServiceCatalog>(HttpMethod.Post, $"/v1/catalog/{Escape(id)}/activate", new { line }, timeout: TimeSpan.FromMinutes(1), cancellationToken: cancellationToken);

    /// <summary>
    /// Removes a line of a catalog service (null: every line); its data goes too only when asked.
    /// When the active line goes, the newest other installed line takes over.
    /// </summary>
    public Task<ServiceCatalog> UninstallCatalogServiceAsync(string id, string? line = null, bool deleteData = false, CancellationToken cancellationToken = default)
    {
        var query = new List<string>();
        if (line is not null)
        {
            query.Add("line=" + Escape(line));
        }
        if (deleteData)
        {
            query.Add("delete_data=1");
        }
        var path = $"/v1/catalog/{Escape(id)}" + (query.Count > 0 ? "?" + string.Join("&", query) : "");
        return Send<ServiceCatalog>(HttpMethod.Delete, path, timeout: TimeSpan.FromMinutes(1), cancellationToken: cancellationToken);
    }

    // Mail

    public Task<List<MailSummary>> MailAsync(CancellationToken cancellationToken = default) => Get<List<MailSummary>>("/v1/mail", cancellationToken: cancellationToken);

    public Task<MailDetail> MailAsync(string id, CancellationToken cancellationToken = default) => Get<MailDetail>($"/v1/mail/{Escape(id)}", cancellationToken: cancellationToken);

    public Task<string> RawMailAsync(string id, CancellationToken cancellationToken = default) => Text(HttpMethod.Get, $"/v1/mail/{Escape(id)}/raw", cancellationToken);

    public Task<byte[]> AttachmentAsync(string mailId, int index, CancellationToken cancellationToken = default) => Raw(HttpMethod.Get, $"/v1/mail/{Escape(mailId)}/attachments/{index}", cancellationToken: cancellationToken);

    public async Task ClearMailAsync(string? inbox = null, CancellationToken cancellationToken = default) =>
        await Raw(HttpMethod.Delete, "/v1/mail" + (inbox is null ? "" : "?inbox=" + Uri.EscapeDataString(inbox)), cancellationToken: cancellationToken);

    // Dumps

    public Task<List<Dump>> DumpsAsync(CancellationToken cancellationToken = default) => Get<List<Dump>>("/v1/dumps", cancellationToken: cancellationToken);

    /// <summary>Symfony's dump &lt;script&gt;/&lt;style&gt; header, shared by all dump bodies.</summary>
    public Task<string> DumpHeaderAsync(CancellationToken cancellationToken = default) => Text(HttpMethod.Get, "/v1/dumps/header", cancellationToken);

    public async Task ClearDumpsAsync(CancellationToken cancellationToken = default) => await Raw(HttpMethod.Delete, "/v1/dumps", cancellationToken: cancellationToken);

    // Events: queries, logs, views, jobs and requests captured next to the dumps

    /// <summary>Newest first; kinds (EventKind values) and limit narrow the list.</summary>
    public Task<List<RuntimeEvent>> EventsAsync(IEnumerable<string>? kinds = null, int? limit = null, CancellationToken cancellationToken = default)
    {
        var query = new List<string>();
        if (kinds?.ToList() is { Count: > 0 } list)
        {
            query.Add("kind=" + Uri.EscapeDataString(string.Join(",", list)));
        }
        if (limit is { } n)
        {
            query.Add($"limit={n}");
        }
        return Get<List<RuntimeEvent>>("/v1/events" + (query.Count == 0 ? "" : "?" + string.Join("&", query)), cancellationToken);
    }

    public async Task ClearEventsAsync(CancellationToken cancellationToken = default) => await Raw(HttpMethod.Delete, "/v1/events", cancellationToken: cancellationToken);

    // Logs

    public Task<LogIndex> LogsAsync(CancellationToken cancellationToken = default) => Get<LogIndex>("/v1/logs", cancellationToken: cancellationToken);

    public Task<string> ServiceLogAsync(string name, int lines = 300, CancellationToken cancellationToken = default) =>
        Text(HttpMethod.Get, $"/v1/logs/services/{Escape(name)}?lines={lines}", cancellationToken);

    public Task<List<LogEntry>> SiteLogAsync(string name, int limit = 200, CancellationToken cancellationToken = default) =>
        Get<List<LogEntry>>($"/v1/logs/sites/{Escape(name)}?limit={limit}", cancellationToken: cancellationToken);

    public async Task ClearSiteLogAsync(string name, CancellationToken cancellationToken = default) => await Raw(HttpMethod.Delete, $"/v1/logs/sites/{Escape(name)}", cancellationToken: cancellationToken);

    // Transport

    /// <summary>The time a request is given, recorded in its options (for diagnostics and tests).</summary>
    public static readonly HttpRequestOptionsKey<TimeSpan> TimeoutOption = new("Flock.Timeout");

    private static readonly TimeSpan DefaultTimeout = TimeSpan.FromSeconds(5);
    // A change reloads the daemon, which may start PHP: the first start after an install can take
    // several seconds while an antivirus scans it. Any request but a GET waits this long at least.
    private static readonly TimeSpan WriteTimeout = TimeSpan.FromSeconds(30);

    private Task<T> Get<T>(string path, CancellationToken cancellationToken = default) => Send<T>(HttpMethod.Get, path, cancellationToken: cancellationToken);

    private async Task<T> Send<T>(HttpMethod method, string path, object? body = null, TimeSpan? timeout = null, CancellationToken cancellationToken = default)
    {
        var data = await Raw(method, path, body, timeout, cancellationToken);
        // Go encodes nil slices as null; an empty list is what every caller wants then.
        if (data.Length == 0)
        {
            data = typeof(T).IsGenericType && typeof(T).GetGenericTypeDefinition() == typeof(List<>) ? "[]"u8.ToArray() : data;
        }
        try
        {
            return JsonSerializer.Deserialize<T>(data, FlockJson.Options) ?? throw new FlockException($"empty answer from {path}");
        }
        catch (JsonException e)
        {
            throw new FlockException($"Unexpected answer from {path}: {e.Message}", e);
        }
    }

    private async Task<string> Text(HttpMethod method, string path, CancellationToken cancellationToken = default) => Encoding.UTF8.GetString(await Raw(method, path, cancellationToken: cancellationToken));

    /// <summary>Sends one request, with the bounded retry rules for daemon restarts.</summary>
    private async Task<byte[]> Raw(HttpMethod method, string path, object? body = null, TimeSpan? timeout = null, CancellationToken cancellationToken = default)
    {
        if (cancellationToken.IsCancellationRequested)
        {
            throw new TaskCanceledException(null, null, cancellationToken);
        }
        var retried = false;
        while (true)
        {
            try
            {
                return await ExecuteAttempt(method, path, body, timeout, force: retried, cancellationToken);
            }
            catch (Exception error) when (!retried && CanRetry(method, error))
            {
                retried = true;
                if (error is HttpRequestException)
                {
                    await InvalidateConfig(cancellationToken);
                }
            }
            catch (HttpRequestException error)
            {
                await InvalidateConfig(cancellationToken);
                throw TerminalTransportFailure(method, error);
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
            {
                throw new TaskCanceledException(null, null, cancellationToken);
            }
        }
    }

    private static bool CanRetry(HttpMethod method, Exception error) => error switch
    {
        FlockServerException { StatusCode: 401 } => true,
        HttpRequestException => method == HttpMethod.Get,
        _ => false,
    };

    private async Task<byte[]> ExecuteAttempt(HttpMethod method, string path, object? body, TimeSpan? timeout, bool force, CancellationToken cancellationToken)
    {
        var limit = timeout ?? DefaultTimeout;
        if (method != HttpMethod.Get && limit < WriteTimeout)
        {
            limit = WriteTimeout;
        }
        using var timeoutCts = new CancellationTokenSource(limit, timeProvider);
        using var cts = CancellationTokenSource.CreateLinkedTokenSource(timeoutCts.Token, cancellationToken);
        try
        {
            var api = await LoadConfig(force, cts.Token);
            return await Attempt(method, path, body, api, limit, cts.Token);
        }
        catch (OperationCanceledException error) when (timeoutCts.IsCancellationRequested && !cancellationToken.IsCancellationRequested)
        {
            throw TimeoutFailure(method, path, error);
        }
    }

    private async Task<byte[]> Attempt(HttpMethod method, string path, object? body, ApiConfig api, TimeSpan limit, CancellationToken cancellationToken)
    {
        using var request = new HttpRequestMessage(method, api.Url.TrimEnd('/') + path);
        request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", api.Token);
        if (body is not null)
        {
            request.Content = new StringContent(JsonSerializer.Serialize(body, FlockJson.Options), Encoding.UTF8, "application/json");
        }

        request.Options.Set(TimeoutOption, limit);
        HttpResponseMessage response = await http.SendAsync(request, cancellationToken);

        using (response)
        {
            var data = await response.Content.ReadAsByteArrayAsync(cancellationToken);
            if (!response.IsSuccessStatusCode)
            {
                throw new FlockServerException((int)response.StatusCode, ErrorMessage(data));
            }
            return data;
        }
    }

    private static Exception TimeoutFailure(HttpMethod method, string path, Exception cause) =>
        method == HttpMethod.Get
            ? new FlockException($"Flock did not answer {method} {path} in time", cause)
            : new FlockRequestOutcomeUnknownException(cause);

    private static Exception TerminalTransportFailure(HttpMethod method, Exception cause) =>
        method == HttpMethod.Get ? new FlockNotRunningException(cause) : new FlockRequestOutcomeUnknownException(cause);

    private static string ErrorMessage(byte[] data)
    {
        try
        {
            using var doc = JsonDocument.Parse(data);
            if (doc.RootElement.ValueKind == JsonValueKind.Object && doc.RootElement.TryGetProperty("error", out var error))
            {
                return error.GetString() ?? "";
            }
        }
        catch (JsonException)
        {
        }
        var text = Encoding.UTF8.GetString(data);
        return text.Length > 500 ? text[..500] : text;
    }

    private async Task<ApiConfig> LoadConfig(bool force, CancellationToken cancellationToken)
    {
        await configLock.WaitAsync(cancellationToken);
        try
        {
            if (force)
            {
                config = null;
            }
            if (config is not null && !force)
            {
                return config;
            }
            try
            {
                var loaded = FlockJson.Decode<ApiConfig>(await File.ReadAllTextAsync(configPath, cancellationToken));
                if (string.IsNullOrWhiteSpace(loaded.Url) || string.IsNullOrWhiteSpace(loaded.Token)
                    || !Uri.TryCreate(loaded.Url, UriKind.Absolute, out _))
                {
                    throw new FlockNotRunningException(new InvalidDataException("api.json has no URL or token."));
                }
                try
                {
                    _ = new AuthenticationHeaderValue("Bearer", loaded.Token);
                }
                catch (FormatException e)
                {
                    throw new FlockNotRunningException(e);
                }
                return config = loaded;
            }
            catch (Exception e) when (e is IOException or UnauthorizedAccessException or JsonException or InvalidDataException)
            {
                throw new FlockNotRunningException(e);
            }
        }
        finally
        {
            configLock.Release();
        }
    }

    private async Task InvalidateConfig(CancellationToken cancellationToken)
    {
        await configLock.WaitAsync(cancellationToken);
        try
        {
            config = null;
        }
        finally
        {
            configLock.Release();
        }
    }

    private static string Escape(string segment) => Uri.EscapeDataString(segment);
}
