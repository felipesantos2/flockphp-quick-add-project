import AppKit
import FlockKit
import Observation

/// Configuration and services for the Settings window.
@MainActor
@Observable
final class SettingsStore {
    private let client: FlockClient
    /// Lets the menu bar status catch up after a change.
    @ObservationIgnored var onChange: (() async -> Void)?

    private(set) var config: FlockConfig?
    private(set) var services: [Service] = []
    /// PHP versions from php.net with install state; loaded by the PHP pane.
    private(set) var php: PHPCatalog?
    /// FrankenPHP builds per PHP minor; loaded by the PHP pane.
    private(set) var frankenPHP: FrankenPHPState?
    /// Node.js majors from the package manifest with install state; loaded by the Node pane.
    private(set) var node: NodeCatalog?
    /// PostgreSQL majors with install state; loaded by the Services pane.
    private(set) var postgres: PostgresVersions?
    /// MySQL, MariaDB, Redis, MongoDB, Meilisearch, Typesense, RustFS; loaded by the Services pane.
    private(set) var catalog: ServiceCatalog?
    /// The AI clients Flock's MCP server can be registered with; loaded by the AI pane.
    private(set) var mcp: MCPState?
    /// Catalog services this window asked to install that the daemon hasn't reported a job for yet.
    private(set) var catalogPending: Set<String> = []
    /// PostgreSQL operations this window started that haven't answered yet, by major.
    private(set) var postgresPending: [String: PostgresMajor.Busy] = [:]
    /// Node.js majors a flock://install link installs that the daemon hasn't answered for yet
    /// (the Node pane's rows track the installs started there).
    private(set) var nodePending: Set<String> = []
    private var mutationError: String?
    private var refreshError: String?
    var error: String? {
        get { mutationError ?? refreshError }
        set {
            mutationError = newValue
            if newValue == nil {
                refreshError = nil
            }
        }
    }
    private(set) var isBusy = false
    var isVisible = false

    init(client: FlockClient) {
        self.client = client
    }

    func refresh() async {
        do {
            let (config, services) = try await (client.config(), client.services())
            if config != self.config {
                self.config = config
            }
            if services != self.services {
                self.services = services
            }
            refreshError = nil
        } catch {
            refreshError = error.localizedDescription
        }
    }

    func refreshIfVisible() async {
        if isVisible {
            await refresh()
        }
    }

    enum MutationOutcome: Equatable, Sendable {
        case succeeded
        case unknown
        case failed
    }

    /// Runs a change against the daemon, then reloads everything that may depend on it.
    @discardableResult
    func perform(_ change: @Sendable (FlockClient) async throws -> Void) async -> MutationOutcome {
        isBusy = true
        defer { isBusy = false }
        let outcome: MutationOutcome
        do {
            try await change(client)
            error = nil
            outcome = .succeeded
        } catch FlockError.outcomeUnknown {
            self.error = FlockError.outcomeUnknown.localizedDescription
            outcome = .unknown
        } catch {
            self.error = error.localizedDescription
            outcome = .failed
        }
        await refresh()
        await onChange?()
        return outcome
    }

    /// Reloads the AI clients (only the AI pane lists them).
    func refreshMCP() async {
        do {
            let fresh = try await client.mcp()
            if fresh != mcp {
                mcp = fresh
            }
        } catch {
            refreshError = error.localizedDescription
        }
    }

    /// Registers or removes Flock with an AI client and adopts the list the daemon returns.
    func performOnMCP(_ change: @Sendable (FlockClient) async throws -> MCPState) async {
        isBusy = true
        defer { isBusy = false }
        do {
            mcp = try await change(client)
            error = nil
        } catch {
            self.error = error.localizedDescription
            await refreshMCP()
        }
    }

    /// Changes services and adopts the list the daemon returns.
    func performOnServices(_ change: @Sendable (FlockClient) async throws -> [Service]) async {
        isBusy = true
        defer { isBusy = false }
        do {
            services = try await change(client)
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
        await onChange?()
    }

    /// Reloads the PHP catalog (not part of `refresh`, it is only needed by the PHP pane).
    func refreshPHP() async {
        do {
            let fresh = try await client.phpVersions()
            if fresh != php {
                php = fresh
            }
        } catch {
            refreshError = error.localizedDescription
        }
    }

    /// Runs a PHP action and adopts the catalog it returns; installs also change config and status.
    func performOnPHP(_ change: @Sendable (FlockClient) async throws -> PHPCatalog) async {
        isBusy = true
        defer { isBusy = false }
        do {
            php = try await change(client)
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
        await refresh()
        await onChange?()
    }

    /// Reloads the FrankenPHP builds (only the PHP pane lists them).
    func refreshFrankenPHP() async {
        do {
            let fresh = try await client.frankenPHP()
            if fresh != frankenPHP {
                frankenPHP = fresh
            }
        } catch {
            refreshError = error.localizedDescription
        }
    }

    /// Runs a FrankenPHP action and adopts the builds it returns; installs reload sites waiting for them.
    func performOnFrankenPHP(_ change: @escaping @Sendable (FlockClient) async throws -> FrankenPHPState) async {
        do {
            frankenPHP = try await change(client)
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
        await onChange?()
    }

    /// Reloads the Node.js catalog (only the Node pane needs it).
    func refreshNode() async {
        do {
            let fresh = try await client.nodeVersions()
            if fresh != node {
                node = fresh
            }
        } catch {
            refreshError = error.localizedDescription
        }
    }

    /// Runs a Node.js action and adopts the catalog it returns.
    func performOnNode(_ change: @Sendable (FlockClient) async throws -> NodeCatalog) async {
        do {
            node = try await change(client)
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
        await refresh()
    }

    /// Installs a Node.js major for a flock://install link; the install answers when it's done.
    func installNode(_ major: String) async {
        nodePending.insert(major)
        defer { nodePending.remove(major) }
        await performOnNode { try await $0.installNodeMajor(major) }
    }

    // MARK: PostgreSQL versions and the catalog (Services pane)

    /// Reloads PostgreSQL versions and the catalog; the Services pane needs them.
    func refreshInstalls() async {
        do {
            async let postgres = client.postgresVersions()
            async let catalog = client.serviceCatalog()
            let fresh = try await (postgres, catalog)
            adopt(fresh.0)
            adopt(fresh.1)
        } catch {
            refreshError = error.localizedDescription
        }
    }

    /// Reloads them only when the Services pane has loaded them before.
    func refreshInstallsIfLoaded() async {
        if isVisible, postgres != nil || catalog != nil {
            await refreshInstalls()
        }
    }

    /// A PostgreSQL operation, or a catalog install, runs — so the Services pane polls.
    var installsBusy: Bool {
        !postgresPending.isEmpty || (postgres?.isBusy ?? false) || !catalogPending.isEmpty || (catalog?.hasRunningJob ?? false)
    }

    /// What a major is doing: the daemon's state, or what this window just asked for while the
    /// daemon hasn't reported it yet.
    func busy(_ major: PostgresMajor) -> PostgresMajor.Busy {
        major.isBusy ? major.busy : (postgresPending[major.major] ?? .idle)
    }

    /// Runs a PostgreSQL action (installs can take minutes) and adopts the versions it returns.
    /// Polling during the request shows the daemon's own busy state.
    func performOnPostgres(_ major: String, _ pending: PostgresMajor.Busy, _ change: @escaping @Sendable (FlockClient) async throws -> PostgresVersions) async {
        postgresPending[major] = pending
        defer { postgresPending[major] = nil }
        do {
            adopt(try await change(client))
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
        await refresh()
        await onChange?()
    }

    /// The extensions of a major, for its Extensions sheet.
    func postgresExtensions(_ major: String) async -> Result<PostgresExtensions, Error> {
        do {
            return .success(try await client.postgresExtensions(major))
        } catch {
            return .failure(error)
        }
    }

    /// Installs, updates or removes an extension; the versions are reloaded afterwards, since a
    /// major's row names its extensions.
    func performOnPostgresExtension(_ change: @escaping @Sendable (FlockClient) async throws -> PostgresExtensions) async -> Result<PostgresExtensions, Error> {
        let result: Result<PostgresExtensions, Error>
        do {
            result = .success(try await change(client))
        } catch {
            result = .failure(error)
        }
        await refreshInstalls()
        await onChange?()
        return result
    }

    /// "Check for Updates": fetches the package manifest, for PostgreSQL and the catalog at once.
    func checkForUpdates() async {
        do {
            async let postgres = client.checkPostgres()
            async let catalog = client.checkServiceCatalog()
            let fresh = try await (postgres, catalog)
            adopt(fresh.0)
            adopt(fresh.1)
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// Runs a catalog action for service `id` and adopts the catalog it returns.
    func performOnCatalog(_ id: String, _ change: @escaping @Sendable (FlockClient) async throws -> ServiceCatalog) async {
        catalogPending.insert(id)
        defer { catalogPending.remove(id) }
        do {
            adopt(try await change(client))
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
        await refresh()
        await onChange?()
    }

    func catalogLog(_ id: String) async -> String {
        (try? await client.catalogLog(id)) ?? ""
    }

    private func adopt(_ fresh: PostgresVersions) {
        if fresh != postgres {
            postgres = fresh
        }
    }

    private func adopt(_ fresh: ServiceCatalog) {
        if fresh != catalog {
            catalog = fresh
        }
    }

    func phpInstallLog(_ minor: String) async -> String {
        (try? await client.phpInstallLog(minor)) ?? ""
    }

    /// The PHP extensions a site needs, or why the daemon couldn't tell.
    func siteExtensions(_ site: String) async -> Result<ExtensionReport, Error> {
        do {
            return .success(try await client.siteExtensions(site: site))
        } catch {
            return .failure(error)
        }
    }

    /// Adds `ext-<name>` to a site's composer.json, then refreshes the sites (a problem may be gone).
    func requireExtension(site: String, name: String) async -> Result<ExtensionReport, Error> {
        let result: Result<ExtensionReport, Error>
        do {
            result = .success(try await client.requireExtension(site: site, name: name))
        } catch {
            result = .failure(error)
        }
        await onChange?()
        return result
    }

    /// Lets the user pick folders; empty when cancelled.
    static func chooseDirectories(prompt: String, multiple: Bool, in directory: URL? = nil, message: String? = nil) -> [URL] {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = multiple
        panel.prompt = prompt
        panel.directoryURL = directory
        if let message {
            panel.message = message
        }
        NSApp.activate()
        return panel.runModal() == .OK ? panel.urls : []
    }
}
