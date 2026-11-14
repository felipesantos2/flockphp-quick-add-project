import Foundation

/// Where Flock keeps its files (FLOCK_HOME overrides it, like the CLI).
public enum FlockPaths {
    public static var root: URL {
        if let home = ProcessInfo.processInfo.environment["FLOCK_HOME"], !home.isEmpty {
            return URL(fileURLWithPath: home, isDirectory: true)
        }
        return FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Application Support/Flock", isDirectory: true)
    }

    public static var apiJSON: URL { root.appendingPathComponent("api.json") }
    public static var flockBinary: URL { root.appendingPathComponent("bin/flock") }
}

/// Contents of api.json, written by the daemon.
public struct APIConfig: Decodable, Sendable, Equatable {
    public var url: URL
    public var token: String
}

public enum FlockError: LocalizedError, Equatable {
    case notRunning
    case server(status: Int, message: String)

    public var errorDescription: String? {
        switch self {
        case .notRunning: "Flock is not running"
        case let .server(status, message): message.isEmpty ? "Flock answered with HTTP \(status)" : message
        }
    }
}

/// Talks to the daemon's local HTTP API.
public actor FlockClient {
    private let configPath: URL
    private let session: URLSession
    private var config: APIConfig?

    /// Default timeout of a request; slow endpoints pass their own.
    static let defaultTimeout: TimeInterval = 5
    /// Any request but a GET waits this long at least: a change reloads the daemon, which may start
    /// PHP, and the first start after an install can take several seconds while it is scanned.
    static let writeTimeout: TimeInterval = 30
    /// Changing PHP versions waits for the daemon's work, like the Go CLI (2 minutes).
    static let phpChangeTimeout: TimeInterval = 120
    /// Installing or updating a PostgreSQL major downloads it first (10 minutes).
    static let postgresInstallTimeout: TimeInterval = 600
    /// Switching or uninstalling a PostgreSQL major stops and starts servers (2 minutes).
    static let postgresChangeTimeout: TimeInterval = 120
    /// Restarting a FrankenPHP site waits for its process to come back (1 minute, like the Go CLI).
    static let siteRestartTimeout: TimeInterval = 60
    /// Registering with Claude Code runs its CLI.
    static let mcpChangeTimeout: TimeInterval = 30
    /// Adding Flock's Boost guideline runs `php artisan boost:update`, which boots the app.
    static let boostTimeout: TimeInterval = 120
    /// Creating an app may download and extract an archive like WordPress (the Go CLI allows 2 minutes).
    static let appCreateTimeout: TimeInterval = 180

    public init(configPath: URL = FlockPaths.apiJSON) {
        self.init(configPath: configPath, configuration: .ephemeral)
    }

    /// Tests pass a configuration with a stub URLProtocol.
    init(configPath: URL, configuration: URLSessionConfiguration) {
        self.configPath = configPath
        // Each request sets its own timeout; the session's must not cut the slow ones short.
        configuration.timeoutIntervalForRequest = 600
        configuration.connectionProxyDictionary = [:] // never send localhost traffic through a proxy
        self.session = URLSession(configuration: configuration)
    }

    // MARK: Endpoints

    public func status() async throws -> Status {
        try await decode(Status.self, "GET", "/v1/status")
    }

    /// Changes only the given settings and returns the new status.
    public func updateSettings(xdebug: Bool? = nil, xdebugBreakpoints: Bool? = nil, dumps: Bool? = nil) async throws -> Status {
        try await updateSettings(SettingsChange(xdebug: xdebug, xdebugBreakpoints: xdebugBreakpoints, dumps: dumps))
    }

    /// Changes only the non-nil settings and returns the new status.
    public func updateSettings(_ change: SettingsChange) async throws -> Status {
        try await decode(Status.self, "POST", "/v1/settings", body: try JSONEncoder().encode(change))
    }

    public func config() async throws -> FlockConfig {
        try await decode(FlockConfig.self, "GET", "/v1/config")
    }

    public func services() async throws -> [Service] {
        try await decode([Service]?.self, "GET", "/v1/services") ?? []
    }

    public func startService(_ id: String) async throws -> [Service] {
        try await decode([Service]?.self, "POST", "/v1/services/\(escape(id))/start") ?? []
    }

    public func stopService(_ id: String) async throws -> [Service] {
        try await decode([Service]?.self, "POST", "/v1/services/\(escape(id))/stop") ?? []
    }

    public func setServicePort(_ id: String, port: Int) async throws -> [Service] {
        try await decode([Service]?.self, "PATCH", "/v1/services/\(escape(id))", body: try JSONEncoder().encode(["port": port])) ?? []
    }

    /// Links a directory as a site (name defaults to the directory name on the daemon side).
    public func linkSite(path: String, name: String? = nil, php: String? = nil, secured: Bool = false) async throws -> Status {
        struct Body: Encodable { var path: String; var name: String?; var php: String?; var secured: Bool }
        return try await decode(Status.self, "POST", "/v1/sites", body: try JSONEncoder().encode(Body(path: path, name: name, php: php, secured: secured)))
    }

    /// Lists available templates from apps.conf.
    public func appTemplates() async throws -> [AppTemplate] {
        struct Response: Decodable { var templates: [AppTemplate]? }
        return try await decode(Response.self, "GET", "/v1/apps/templates").templates ?? []
    }

    /// Creates a new project directory from an apps.conf template and links it as a site.
    public func createApp(_ req: CreateAppRequest) async throws -> Status {
        try await decode(Status.self, "POST", "/v1/apps", body: try JSONEncoder().encode(req), timeout: Self.appCreateTimeout)
    }

    public func updateSite(_ name: String, _ change: SiteChange) async throws -> Status {
        try await decode(Status.self, "PATCH", "/v1/sites/\(escape(name))", body: try JSONEncoder().encode(change))
    }

    /// Restarts a FrankenPHP site's process, e.g. so Octane's workers load changed code.
    public func restartSite(_ name: String) async throws -> Status {
        try await decode(Status.self, "POST", "/v1/sites/\(escape(name))/restart", timeout: Self.siteRestartTimeout)
    }

    public func unlinkSite(_ name: String) async throws -> Status {
        try await decode(Status.self, "DELETE", "/v1/sites/\(escape(name))")
    }

    /// Serves <name>.test from upstream (a port, host:port or URL), adding the proxy or changing its
    /// upstream. A nil secured keeps the proxy's setting (HTTP for a new one).
    public func proxy(name: String, upstream: String, secured: Bool? = nil) async throws -> Status {
        struct Body: Encodable { var name: String; var upstream: String; var secured: Bool? }
        return try await decode(Status.self, "POST", "/v1/proxies", body: try JSONEncoder().encode(Body(name: name, upstream: upstream, secured: secured)))
    }

    /// Serves a proxy over HTTPS (on) or plain HTTP.
    public func secureProxy(_ name: String, _ on: Bool) async throws -> Status {
        try await decode(Status.self, "PATCH", "/v1/proxies/\(escape(name))", body: try JSONEncoder().encode(["secured": on]))
    }

    /// Stops serving a proxy.
    public func removeProxy(_ name: String) async throws -> Status {
        try await decode(Status.self, "DELETE", "/v1/proxies/\(escape(name))")
    }

    /// Adds an extra domain: a subdomain ("tenant1", "a.b") or an alias ("my-app-2.test").
    public func addDomain(site: String, domain: String) async throws -> Status {
        try await decode(Status.self, "POST", "/v1/sites/\(escape(site))/domains", body: try JSONEncoder().encode(["domain": domain]))
    }

    /// Removes an extra domain, given as `SiteDomain.entry` (or its full name).
    public func removeDomain(site: String, domain: String) async throws -> Status {
        try await decode(Status.self, "DELETE", "/v1/sites/\(escape(site))/domains/\(escape(domain))")
    }

    /// Writes a version (tool "php" or "node"; empty version: the one the site uses now) into the
    /// site's project files and lifts the site's isolation for that tool.
    public func pin(site: String, tool: String, version: String = "") async throws -> Status {
        try await decode(Status.self, "POST", "/v1/sites/\(escape(site))/pin", body: try JSONEncoder().encode(["tool": tool, "version": version]))
    }

    /// Removes a tool's version from the site's project files.
    public func unpin(site: String, tool: String) async throws -> Status {
        try await decode(Status.self, "DELETE", "/v1/sites/\(escape(site))/pin/\(escape(tool))")
    }

    /// Writes Flock's guideline into the site's Laravel Boost guidelines (.ai/guidelines/flock.md)
    /// and runs `php artisan boost:update`, so the agents' guideline files include it.
    public func addBoostGuideline(site: String) async throws -> Status {
        try await decode(Status.self, "POST", "/v1/sites/\(escape(site))/boost", timeout: Self.boostTimeout)
    }

    public func siteExtensions(site: String) async throws -> ExtensionReport {
        try await decode(ExtensionReport.self, "GET", "/v1/sites/\(escape(site))/extensions")
    }

    /// Adds `ext-<name>` to the site's composer.json with Composer (up to 5 minutes).
    public func requireExtension(site: String, name: String) async throws -> ExtensionReport {
        try await decode(ExtensionReport.self, "POST", "/v1/sites/\(escape(site))/extensions/\(escape(name))/require", timeout: 300)
    }

    // MARK: Sharing

    /// Shares a site at a temporary public URL; allow limits it to these emails (or *@domain).
    /// It returns at once: the share's progress shows on the site in `status()`.
    public func startShare(site: String, allow: [String], debug: Bool = false) async throws -> ShareStatus {
        struct Request: Encodable { let allow: [String]; let debug: Bool }
        return try await decode(ShareStatus.self, "POST", "/v1/shares/\(escape(site))", body: try JSONEncoder().encode(Request(allow: allow, debug: debug)))
    }

    /// Builds a running share's assets again, so visitors see the latest changes when they reload.
    public func rebuildShare(site: String) async throws -> ShareStatus {
        try await decode(ShareStatus.self, "POST", "/v1/shares/\(escape(site))/rebuild")
    }

    public func stopShare(site: String) async throws {
        _ = try await send("DELETE", "/v1/shares/\(escape(site))")
    }

    // MARK: Worktrees

    /// The git branches of a site, for where a new worktree's branch starts.
    public func siteBranches(_ site: String) async throws -> Branches {
        try await decode(Branches.self, "GET", "/v1/sites/\(escape(site))/branches")
    }

    /// Creates a git worktree of a site on branch (a new branch from `from`, the current commit by
    /// default; a branch that exists is checked out; an empty one gets a name of its own, like
    /// brave-otter) and starts its setup.
    public func createWorktree(site: String, branch: String, from: String = "", db: DatabaseMode? = nil) async throws -> WorktreeCreated {
        struct Request: Encodable { let site: String; let branch: String; let from: String; let db: String }
        let body = try JSONEncoder().encode(Request(site: site, branch: branch, from: from, db: db?.rawValue ?? ""))
        return try await decode(WorktreeCreated.self, "POST", "/v1/worktrees", body: body, timeout: 120)
    }

    /// How a worktree's setup goes, for following it.
    public func worktreeSetup(path: String) async throws -> WorktreeSetup {
        try await decode(WorktreeSetup.self, "GET", "/v1/worktrees/setup", query: [URLQueryItem(name: "path", value: path)])
    }

    /// Sets a worktree up again. It returns at once: the progress shows on the worktree in `status()`.
    public func setupWorktree(path: String, again: Bool = true) async throws -> WorktreeSetup {
        struct Request: Encodable { let path: String; let again: Bool }
        return try await decode(WorktreeSetup.self, "POST", "/v1/worktrees/setup", body: try JSONEncoder().encode(Request(path: path, again: again)))
    }

    /// Gives a worktree another database, dropping the one Flock made for it. It returns at once,
    /// like `setupWorktree`.
    public func switchWorktreeDatabase(path: String, to mode: DatabaseMode) async throws -> WorktreeSetup {
        struct Request: Encodable { let path: String; let db: String }
        return try await decode(WorktreeSetup.self, "POST", "/v1/worktrees/db", body: try JSONEncoder().encode(Request(path: path, db: mode.rawValue)))
    }

    /// Runs a worktree's before_delete hook, removes it with git and drops the database Flock made
    /// for it; force removes it also with changes or a failing hook. It returns what they printed.
    @discardableResult
    public func removeWorktree(path: String, force: Bool = false) async throws -> String {
        struct Request: Encodable { let path: String; let force: Bool }
        struct Response: Decodable { let output: String }
        let body = try JSONEncoder().encode(Request(path: path, force: force))
        return try await decode(Response.self, "POST", "/v1/worktrees/remove", body: body, timeout: 600).output
    }

    // MARK: AI clients

    /// The AI clients Flock's MCP server can be registered with.
    public func mcp() async throws -> MCPState {
        try await decode(MCPState.self, "GET", "/v1/mcp")
    }

    /// Registers Flock's MCP server with a client ("claude-code", "cursor", "vscode", "windsurf", "codex").
    public func registerMCP(_ client: String) async throws -> MCPState {
        try await decode(MCPState.self, "POST", "/v1/mcp/clients/\(escape(client))", timeout: Self.mcpChangeTimeout)
    }

    /// Removes Flock's MCP server from a client; nothing else in its configuration changes.
    public func unregisterMCP(_ client: String) async throws -> MCPState {
        try await decode(MCPState.self, "DELETE", "/v1/mcp/clients/\(escape(client))", timeout: Self.mcpChangeTimeout)
    }

    public func park(_ path: String) async throws -> Status {
        try await decode(Status.self, "POST", "/v1/parked", body: try JSONEncoder().encode(["path": path]))
    }

    public func unpark(_ path: String) async throws -> Status {
        try await decode(Status.self, "DELETE", "/v1/parked", body: try JSONEncoder().encode(["path": path]))
    }

    /// Downloads and installs a Node.js version ("20", "20.19" or exact); this can take a minute.
    public func installNode(_ version: String) async throws -> String {
        struct Installed: Decodable { var version: String }
        let data = try await send("POST", "/v1/node/install", body: try JSONEncoder().encode(["version": version]), timeout: 300)
        return try FlockJSON.decoder().decode(Installed.self, from: data).version
    }

    // MARK: Node.js versions

    public func nodeVersions() async throws -> NodeCatalog {
        try await decode(NodeCatalog.self, "GET", "/v1/node/versions", timeout: 60)
    }

    /// Installs the newest release of a major line, or updates it; a download of about 30 MB.
    public func installNodeMajor(_ major: String) async throws -> NodeCatalog {
        try await decode(NodeCatalog.self, "POST", "/v1/node/versions/\(escape(major))/install", timeout: 600)
    }

    /// Updates a line's npm to the newest version that supports it; its global packages stay.
    public func updateNpm(_ major: String) async throws -> NodeCatalog {
        try await decode(NodeCatalog.self, "POST", "/v1/node/versions/\(escape(major))/npm", timeout: 600)
    }

    public func uninstallNode(_ major: String) async throws -> NodeCatalog {
        try await decode(NodeCatalog.self, "DELETE", "/v1/node/versions/\(escape(major))")
    }

    /// Asks the daemon to fetch the package manifest (and npm's registry) now.
    public func checkNodeReleases() async throws -> NodeCatalog {
        try await decode(NodeCatalog.self, "POST", "/v1/node/check", timeout: 60)
    }

    // MARK: PHP versions

    public func phpVersions() async throws -> PHPCatalog {
        try await decode(PHPCatalog.self, "GET", "/v1/php/versions")
    }

    /// Starts installing (or updating to) the channel's newest release of a version; follow it with `phpVersions()`.
    public func installPHP(_ minor: String) async throws -> PHPCatalog {
        try await decode(PHPCatalog.self, "POST", "/v1/php/versions/\(escape(minor))/install", timeout: Self.phpChangeTimeout)
    }

    public func cancelPHPInstall(_ minor: String) async throws -> PHPCatalog {
        try await decode(PHPCatalog.self, "POST", "/v1/php/versions/\(escape(minor))/cancel", timeout: Self.phpChangeTimeout)
    }

    /// Forgets a failed install.
    public func dismissPHPJob(_ minor: String) async throws -> PHPCatalog {
        try await decode(PHPCatalog.self, "DELETE", "/v1/php/versions/\(escape(minor))/job")
    }

    public func uninstallPHP(_ minor: String) async throws -> PHPCatalog {
        try await decode(PHPCatalog.self, "DELETE", "/v1/php/versions/\(escape(minor))", timeout: Self.phpChangeTimeout)
    }

    /// Asks the daemon to fetch the binaries manifest now.
    public func checkPHPReleases() async throws -> PHPCatalog {
        try await decode(PHPCatalog.self, "POST", "/v1/php/check", timeout: 60)
    }

    public func phpInstallLog(_ minor: String) async throws -> String {
        String(decoding: try await send("GET", "/v1/php/versions/\(escape(minor))/log"), as: UTF8.self)
    }

    // MARK: FrankenPHP builds

    /// The builds per PHP minor; `.unavailable` when the daemon has no FrankenPHP (this platform,
    /// or a daemon from before it).
    public func frankenPHP() async throws -> FrankenPHPState {
        do {
            return try await decode(FrankenPHPState.self, "GET", "/v1/frankenphp")
        } catch FlockError.server(let status, _) where status == 404 || status == 501 {
            return .unavailable
        }
    }

    /// Asks the daemon to fetch the list of published builds now.
    public func checkFrankenPHP() async throws -> FrankenPHPState {
        try await decode(FrankenPHPState.self, "POST", "/v1/frankenphp/check", timeout: 60)
    }

    /// Starts installing, updating or retrying the build for a PHP minor in the background; poll for the job.
    public func installFrankenPHP(_ minor: String) async throws -> FrankenPHPState {
        try await decode(FrankenPHPState.self, "POST", "/v1/frankenphp/\(escape(minor))/install", timeout: 30)
    }

    public func cancelFrankenPHPJob(_ minor: String) async throws -> FrankenPHPState {
        try await decode(FrankenPHPState.self, "POST", "/v1/frankenphp/\(escape(minor))/cancel")
    }

    /// Dismisses a failed install.
    public func dismissFrankenPHPJob(_ minor: String) async throws -> FrankenPHPState {
        try await decode(FrankenPHPState.self, "DELETE", "/v1/frankenphp/\(escape(minor))/job")
    }

    /// Removes the build; the daemon refuses (409) while a site runs on it.
    public func uninstallFrankenPHP(_ minor: String) async throws -> FrankenPHPState {
        try await decode(FrankenPHPState.self, "DELETE", "/v1/frankenphp/\(escape(minor))", timeout: 60)
    }

    /// The tail of the current or last install's output; empty when there is none.
    public func frankenPHPLog(_ minor: String) async throws -> String {
        do {
            return String(decoding: try await send("GET", "/v1/frankenphp/\(escape(minor))/log"), as: UTF8.self)
        } catch FlockError.server(404, _) {
            return ""
        }
    }

    // MARK: PostgreSQL extensions

    /// The extensions of a major; creates its folder for your own extensions.
    public func postgresExtensions(_ major: String) async throws -> PostgresExtensions {
        try await decode(PostgresExtensions.self, "GET", "/v1/postgres/versions/\(escape(major))/extensions")
    }

    /// Downloads an extension for a major. A preloaded one (TimescaleDB) restarts that major when it runs.
    public func installPostgresExtension(_ major: String, _ name: String) async throws -> PostgresExtensions {
        try await decode(PostgresExtensions.self, "POST", "/v1/postgres/versions/\(escape(major))/extensions/\(escape(name))/install", timeout: Self.postgresInstallTimeout)
    }

    public func updatePostgresExtension(_ major: String, _ name: String) async throws -> PostgresExtensions {
        try await decode(PostgresExtensions.self, "POST", "/v1/postgres/versions/\(escape(major))/extensions/\(escape(name))/update", timeout: Self.postgresInstallTimeout)
    }

    public func uninstallPostgresExtension(_ major: String, _ name: String) async throws -> PostgresExtensions {
        try await decode(PostgresExtensions.self, "DELETE", "/v1/postgres/versions/\(escape(major))/extensions/\(escape(name))", timeout: Self.postgresChangeTimeout)
    }

    // MARK: PostgreSQL versions

    /// Cached on the daemon side; it never waits on the network.
    public func postgresVersions() async throws -> PostgresVersions {
        try await decode(PostgresVersions.self, "GET", "/v1/postgres/versions")
    }

    /// Asks the daemon to look for new PostgreSQL releases now.
    public func checkPostgres() async throws -> PostgresVersions {
        try await decode(PostgresVersions.self, "POST", "/v1/postgres/check", timeout: 60)
    }

    /// Downloads and installs the newest release of a major, with `extensions` where it has a build
    /// of them; the first one installed becomes active and starts.
    public func installPostgres(_ major: String, extensions: [String] = []) async throws -> PostgresVersions {
        let body = extensions.isEmpty ? nil : try JSONEncoder().encode(["extensions": extensions])
        return try await decode(PostgresVersions.self, "POST", "/v1/postgres/versions/\(escape(major))/install", body: body, timeout: Self.postgresInstallTimeout)
    }

    /// Installs the newest minor of a major; its databases stay, and it restarts when active.
    public func updatePostgres(_ major: String) async throws -> PostgresVersions {
        try await decode(PostgresVersions.self, "POST", "/v1/postgres/versions/\(escape(major))/update", timeout: Self.postgresInstallTimeout)
    }

    /// Makes an installed major the one that runs (with its own databases).
    public func activatePostgres(_ major: String) async throws -> PostgresVersions {
        try await decode(PostgresVersions.self, "POST", "/v1/postgres/versions/\(escape(major))/activate", timeout: Self.postgresChangeTimeout)
    }

    /// Uninstalls a major; its databases stay unless `deleteData` is set.
    public func uninstallPostgres(_ major: String, deleteData: Bool = false) async throws -> PostgresVersions {
        try await decode(PostgresVersions.self, "DELETE", "/v1/postgres/versions/\(escape(major))",
                         query: deleteData ? [URLQueryItem(name: "delete_data", value: "1")] : [], timeout: Self.postgresChangeTimeout)
    }

    // MARK: Catalog (MySQL, MariaDB, Redis, MongoDB, Meilisearch, Typesense, RustFS)

    public func serviceCatalog() async throws -> ServiceCatalog {
        try await decode(ServiceCatalog.self, "GET", "/v1/catalog")
    }

    /// Asks the daemon to look for new releases of the catalog services now.
    public func checkServiceCatalog() async throws -> ServiceCatalog {
        try await decode(ServiceCatalog.self, "POST", "/v1/catalog/check", timeout: 60)
    }

    /// Starts installing the newest release of `line` on `port` in the background (nil for the
    /// installed line or newest, and the configured or suggested port); poll for the job.
    public func installCatalogService(_ id: String, line: String? = nil, port: Int? = nil) async throws -> ServiceCatalog {
        let body = try JSONEncoder().encode(CatalogInstall(line: line ?? "", port: port ?? 0))
        return try await decode(ServiceCatalog.self, "POST", "/v1/catalog/\(escape(id))/install", body: body)
    }

    public func cancelCatalogJob(_ id: String) async throws -> ServiceCatalog {
        try await decode(ServiceCatalog.self, "POST", "/v1/catalog/\(escape(id))/cancel")
    }

    /// Dismisses a failed install.
    public func dismissCatalogJob(_ id: String) async throws -> ServiceCatalog {
        try await decode(ServiceCatalog.self, "DELETE", "/v1/catalog/\(escape(id))/job")
    }

    /// Runs an installed line of a catalog service instead of the active one, on the service's port.
    public func activateCatalogService(_ id: String, line: String) async throws -> ServiceCatalog {
        let body = try JSONEncoder().encode(["line": line])
        return try await decode(ServiceCatalog.self, "POST", "/v1/catalog/\(escape(id))/activate", body: body, timeout: 60)
    }

    /// Uninstalls a line of a catalog service (nil: every line); its data stays unless `deleteData`
    /// is set. When the active line goes, the newest other installed line takes over.
    public func uninstallCatalogService(_ id: String, line: String? = nil, deleteData: Bool = false) async throws -> ServiceCatalog {
        var query: [URLQueryItem] = []
        if let line {
            query.append(URLQueryItem(name: "line", value: line))
        }
        if deleteData {
            query.append(URLQueryItem(name: "delete_data", value: "1"))
        }
        return try await decode(ServiceCatalog.self, "DELETE", "/v1/catalog/\(escape(id))", query: query, timeout: 60)
    }

    /// The tail of the current or last install's output; empty when there is none.
    public func catalogLog(_ id: String) async throws -> String {
        do {
            return String(decoding: try await send("GET", "/v1/catalog/\(escape(id))/log"), as: UTF8.self)
        } catch FlockError.server(404, _) {
            return ""
        }
    }

    private struct CatalogInstall: Encodable {
        var line: String
        var port: Int
    }

    // MARK: Mail

    public func mail() async throws -> [MailSummary] {
        try await decode([MailSummary]?.self, "GET", "/v1/mail") ?? []
    }

    public func mail(id: String) async throws -> MailDetail {
        try await decode(MailDetail.self, "GET", "/v1/mail/\(escape(id))")
    }

    public func rawMail(id: String) async throws -> String {
        String(decoding: try await send("GET", "/v1/mail/\(escape(id))/raw"), as: UTF8.self)
    }

    public func attachment(mailID: String, index: Int) async throws -> Data {
        try await send("GET", "/v1/mail/\(escape(mailID))/attachments/\(index)")
    }

    public func clearMail(inbox: String? = nil) async throws {
        let query = inbox.map { [URLQueryItem(name: "inbox", value: $0)] } ?? []
        _ = try await send("DELETE", "/v1/mail", query: query)
    }

    public func dumps() async throws -> [Dump] {
        try await decode([Dump]?.self, "GET", "/v1/dumps") ?? []
    }

    /// Symfony's dump <script>/<style> header, shared by all dump bodies.
    public func dumpHeader() async throws -> String {
        String(decoding: try await send("GET", "/v1/dumps/header"), as: UTF8.self)
    }

    public func clearDumps() async throws {
        _ = try await send("DELETE", "/v1/dumps")
    }

    /// Queries, logs, views, jobs and requests captured by the PHP agent, newest first. Kinds this
    /// app doesn't know are left out; a daemon without the endpoint (older Flock) has none.
    public func events() async throws -> [Event] {
        do {
            let data = try await send("GET", "/v1/events")
            return (try FlockJSON.eventDecoder().decode([Event]?.self, from: data) ?? []).filter { $0.kind != nil }
        } catch FlockError.server(404, _) {
            return []
        }
    }

    public func clearEvents() async throws {
        do {
            _ = try await send("DELETE", "/v1/events")
        } catch FlockError.server(404, _) {}
    }

    public func logs() async throws -> LogIndex {
        try await decode(LogIndex.self, "GET", "/v1/logs")
    }

    public func serviceLog(_ name: String, lines: Int = 300) async throws -> String {
        let data = try await send("GET", "/v1/logs/services/\(escape(name))", query: [URLQueryItem(name: "lines", value: String(lines))])
        return String(decoding: data, as: UTF8.self)
    }

    public func siteLog(_ name: String, limit: Int = 200) async throws -> [LogEntry] {
        try await decode([LogEntry]?.self, "GET", "/v1/logs/sites/\(escape(name))", query: [URLQueryItem(name: "limit", value: String(limit))]) ?? []
    }

    public func clearSiteLog(_ name: String) async throws {
        _ = try await send("DELETE", "/v1/logs/sites/\(escape(name))")
    }

    // MARK: Transport

    private func decode<T: Decodable>(_ type: T.Type, _ method: String, _ path: String, query: [URLQueryItem] = [], body: Data? = nil, timeout: TimeInterval? = nil) async throws -> T {
        let data = try await send(method, path, query: query, body: body, timeout: timeout)
        return try FlockJSON.decoder().decode(T.self, from: data)
    }

    /// Sends a request; on a connection failure it re-reads api.json once and retries,
    /// because a restarted daemon may listen on a new port with a new token.
    private func send(_ method: String, _ path: String, query: [URLQueryItem] = [], body: Data? = nil, timeout: TimeInterval? = nil) async throws -> Data {
        do {
            return try await attempt(method, path, query: query, body: body, timeout: timeout, config: try loadConfig(force: false))
        } catch let error as URLError where error.code != .timedOut && error.code != .cancelled {
            config = nil
            do {
                return try await attempt(method, path, query: query, body: body, timeout: timeout, config: try loadConfig(force: true))
            } catch is URLError {
                throw FlockError.notRunning
            }
        } catch FlockError.server(401, _) {
            config = nil
            return try await attempt(method, path, query: query, body: body, timeout: timeout, config: try loadConfig(force: true))
        }
    }

    private func attempt(_ method: String, _ path: String, query: [URLQueryItem], body: Data?, timeout: TimeInterval?, config: APIConfig) async throws -> Data {
        // path is already percent-encoded (see escape); appendingPathComponent would encode the
        // escapes again ("my%20app" → "my%2520app").
        var components = URLComponents(url: config.url, resolvingAgainstBaseURL: false)!
        let basePath = components.percentEncodedPath
        components.percentEncodedPath = (basePath.hasSuffix("/") ? String(basePath.dropLast()) : basePath) + path
        if !query.isEmpty {
            components.queryItems = query
        }

        var request = URLRequest(url: components.url!)
        request.httpMethod = method
        request.timeoutInterval = method == "GET" ? timeout ?? Self.defaultTimeout : max(timeout ?? 0, Self.writeTimeout)
        request.setValue("Bearer \(config.token)", forHTTPHeaderField: "Authorization")
        if let body {
            request.httpBody = body
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }

        let (data, response) = try await session.data(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else {
            let message = (try? JSONDecoder().decode([String: String].self, from: data))?["error"]
                ?? String(decoding: data.prefix(500), as: UTF8.self)
            throw FlockError.server(status: status, message: message)
        }
        return data
    }

    private func loadConfig(force: Bool) throws -> APIConfig {
        if let config, !force {
            return config
        }
        guard let data = try? Data(contentsOf: configPath),
              let loaded = try? JSONDecoder().decode(APIConfig.self, from: data) else {
            throw FlockError.notRunning
        }
        config = loaded
        return loaded
    }

    private func escape(_ segment: String) -> String {
        segment.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(CharacterSet(charactersIn: "/"))) ?? segment
    }
}
