import Foundation

/// GET /v1/status
public struct Status: Decodable, Sendable, Equatable {
    public var version: String
    public var pid: Int
    public var startedAt: Date
    public var dns: DNSStatus
    public var httpPort: Int
    public var httpsPort: Int
    public var mail: String
    public var postgres: String?
    /// The installed catalog services (MySQL, Redis, ...).
    public var extras: [Extra]
    /// Xdebug detection: requests that ask for Xdebug go to a PHP pool that loads it.
    public var xdebug: Bool
    /// Sites with PhpStorm breakpoints send every request to Xdebug.
    public var xdebugBreakpoints: Bool
    /// Capture is on: dumps (and, where the PHP agent runs, queries, logs, views, jobs and
    /// requests) go to Flock.
    public var dumps: Bool
    /// Event kinds ("query", "log", "view", "job", "request") that are not captured.
    public var captureOff: [String]
    /// dump() output also shows in the page or terminal; dd() always does.
    public var dumpsInPage: Bool
    public var mailCount: Int
    public var dumpCount: Int
    /// Captured events (queries, logs, ...), see GET /v1/events.
    public var eventCount: Int
    /// Installed PHP versions whose build has no agent extension: they only capture dumps.
    public var captureUnsupported: [String]
    public var sites: [Site]
    /// Names served by a server of their own, e.g. a Node.js dev server (`flock proxy`).
    public var proxies: [Proxy]
    public var processes: [Process]
    public var warnings: [String]
    /// The channel config.json follows: "stable", "beta" or "experimental" (`flock channel`).
    public var channel: String
    /// The first run's installs of the newest PHP and Node.js LTS, which become the defaults;
    /// nil once both defaults are set.
    public var defaults: Defaults?

    public struct DNSStatus: Decodable, Sendable, Equatable {
        public var tld: String
        public var addr: String
        /// Every name under the TLD resolves (any subdomain). Always true on macOS while the DNS
        /// server runs; on Windows a company DNS policy can override Flock's rule.
        public var wildcard: Bool
        public var wildcardProblem: String
        /// Subdomain levels below a site (and its aliases) that are served.
        public var maxDepth: Int

        public init(tld: String, addr: String, wildcard: Bool = false, wildcardProblem: String = "", maxDepth: Int = 4) {
            self.tld = tld
            self.addr = addr
            self.wildcard = wildcard
            self.wildcardProblem = wildcardProblem
            self.maxDepth = maxDepth
        }

        enum CodingKeys: String, CodingKey {
            case tld, addr, wildcard, wildcardProblem, maxDepth
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            tld = try c.decodeIfPresent(String.self, forKey: .tld) ?? ""
            addr = try c.decodeIfPresent(String.self, forKey: .addr) ?? ""
            wildcard = try c.decodeIfPresent(Bool.self, forKey: .wildcard) ?? !addr.isEmpty
            wildcardProblem = try c.decodeIfPresent(String.self, forKey: .wildcardProblem) ?? ""
            maxDepth = try c.decodeIfPresent(Int.self, forKey: .maxDepth) ?? 4
        }
    }

    /// A name served by a server of its own rather than by PHP, e.g. a Node.js dev server: every
    /// request, WebSocket upgrades included, passes to `upstream` with its Host header.
    public struct Proxy: Decodable, Sendable, Equatable, Identifiable {
        public var name: String
        public var domain: String
        /// Where requests go, e.g. http://127.0.0.1:3000.
        public var upstream: String
        public var secured: Bool
        public var url: String?
        /// Why it isn't served, e.g. a site of the same name serves its domain.
        public var problem: String?
        /// Something answers at the upstream; `server` names it when it runs on this Mac, e.g.
        /// "Node.js 22.23.4" for one of Flock's Node.js versions.
        public var running: Bool
        public var server: String?

        public var id: String { name }
    }

    /// One of a site's extra domains: a subdomain ("tenant1") or an alias ("my-app-2.test").
    public struct SiteDomain: Decodable, Sendable, Equatable, Identifiable {
        /// As stored, and as the API takes it for removal.
        public var entry: String
        /// The full name, e.g. tenant1.app.test.
        public var host: String
        public var alias: Bool
        public var url: String?
        /// Why it isn't served, e.g. another site uses it.
        public var problem: String?

        public var id: String { entry }
    }

    public struct Site: Decodable, Sendable, Equatable, Identifiable {
        public var name: String
        public var domain: String
        public var path: String
        /// The PHP version the site uses; `phpSource` says why.
        public var php: String
        public var phpSource: VersionSource
        /// What the project pins (in `phpFile`), also when an isolation wins over it; empty when nothing is pinned.
        public var phpPinned: String
        public var phpFile: String
        /// The Node.js version (usually a major) the site uses; `nodeSource` says why.
        public var node: String?
        public var nodeSource: VersionSource
        public var nodePinned: String
        public var nodeFile: String
        /// A .nvmrc / .node-version that .flockrc's node= wins over, e.g. ".nvmrc: 20".
        public var nodeShadowed: String
        public var secured: Bool
        public var parked: Bool
        public var url: String?
        public var problem: String?
        /// Something that needs a decision but isn't broken, e.g. that no document root was found.
        public var notice: String?
        /// The document root relative to `path` ("." for the project folder); `rootSource` says why:
        /// set on this machine (isolated), in the project's .flockrc, or detected (default).
        public var root: String
        public var rootSource: VersionSource
        /// What the project's .flockrc says (in `rootFile`), also when this machine's root wins over it.
        public var rootPinned: String
        public var rootFile: String
        /// Why Flock serves its page instead of the site, and which folders could be the root.
        public var rootState: RootState
        public var rootSuggestions: [String]
        public var domains: [SiteDomain]
        /// PhpStorm breakpoints send every request of the site to Xdebug.
        public var debugging: Bool
        /// What runs the site's PHP: its version's PHP-FPM pool, or FrankenPHP.
        public var server: Server
        /// FrankenPHP's worker mode.
        public var worker: Worker
        /// The project has Laravel Octane installed, so it can run in worker mode.
        public var octaneAvailable: Bool
        /// The site's FrankenPHP process, when `server` is FrankenPHP. Until it runs, PHP-FPM serves the site.
        public var frankenPHP: SiteFrankenPHP?
        /// Whether the project uses Laravel Boost and has Flock's guideline.
        public var boost: Boost
        /// The site's tunnel (`flock share`), while there is one.
        public var share: ShareStatus?
        /// Set on a git worktree of another site: that site's name, whose settings it uses.
        public var parent: String?
        /// A worktree's branch; empty when its HEAD is detached.
        public var branch: String
        /// How Flock set the worktree up; nil when it hasn't.
        public var worktree: WorktreeSetup?

        public var id: String { name }

        /// The site is reachable at a public share URL right now.
        public var shared: Bool { share?.state == .running }

        /// FrankenPHP needs PHP 8.2 or newer.
        public var supportsFrankenPHP: Bool { Self.frankenPHPSupports(php) }

        /// "8.2" and newer ("8.4", "8.4.14", "9.0"). A version that can't be read doesn't block it
        /// (the daemon checks too).
        public static func frankenPHPSupports(_ version: String) -> Bool {
            let parts = version.split(separator: ".").map { Int($0) }
            guard parts.count >= 2, let major = parts[0], let minor = parts[1] else { return true }
            return (major, minor) >= (8, 2)
        }

        /// The file pinning PHP sits in the site's own folder (so it can be unpinned there).
        public var phpPinnedHere: Bool { !phpFile.isEmpty && Self.inFolder(phpFile, path) }
        public var nodePinnedHere: Bool { !nodeFile.isEmpty && Self.inFolder(nodeFile, path) }
        public var rootPinnedHere: Bool { !rootPinned.isEmpty }

        /// A folder as a document root of the site: relative to its path ("src/www", "." for the
        /// project folder itself), or nil when the folder isn't in the project.
        public func root(for folder: URL) -> String? {
            let project = URL(fileURLWithPath: path).resolvingSymlinksInPath().standardizedFileURL.pathComponents
            let chosen = folder.resolvingSymlinksInPath().standardizedFileURL.pathComponents
            guard chosen.count >= project.count, Array(chosen.prefix(project.count)) == project else { return nil }
            let rest = chosen.dropFirst(project.count)
            return rest.isEmpty ? "." : rest.joined(separator: "/")
        }

        private static func inFolder(_ file: String, _ folder: String) -> Bool {
            URL(fileURLWithPath: file).deletingLastPathComponent().standardizedFileURL.path == URL(fileURLWithPath: folder).standardizedFileURL.path
        }

        enum CodingKeys: String, CodingKey {
            case name, domain, path, php, phpSource, phpPinned, phpFile, node, nodeSource, nodePinned, nodeFile, nodeShadowed
            case secured, parked, url, problem, notice, root, rootSource, rootPinned, rootFile, rootState, rootSuggestions
            case domains, debugging, server, worker, octaneAvailable, boost, share, parent, branch, worktree
            case frankenPHP = "frankenphp"
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            name = try c.decode(String.self, forKey: .name)
            domain = try c.decodeIfPresent(String.self, forKey: .domain) ?? ""
            path = try c.decodeIfPresent(String.self, forKey: .path) ?? ""
            php = try c.decodeIfPresent(String.self, forKey: .php) ?? ""
            phpSource = try c.decodeIfPresent(VersionSource.self, forKey: .phpSource) ?? .default
            phpPinned = try c.decodeIfPresent(String.self, forKey: .phpPinned) ?? ""
            phpFile = try c.decodeIfPresent(String.self, forKey: .phpFile) ?? ""
            node = try c.decodeIfPresent(String.self, forKey: .node)
            nodeSource = try c.decodeIfPresent(VersionSource.self, forKey: .nodeSource) ?? .default
            nodePinned = try c.decodeIfPresent(String.self, forKey: .nodePinned) ?? ""
            nodeFile = try c.decodeIfPresent(String.self, forKey: .nodeFile) ?? ""
            nodeShadowed = try c.decodeIfPresent(String.self, forKey: .nodeShadowed) ?? ""
            secured = try c.decodeIfPresent(Bool.self, forKey: .secured) ?? false
            parked = try c.decodeIfPresent(Bool.self, forKey: .parked) ?? false
            url = try c.decodeIfPresent(String.self, forKey: .url)
            problem = try c.decodeIfPresent(String.self, forKey: .problem)
            notice = try c.decodeIfPresent(String.self, forKey: .notice)
            root = try c.decodeIfPresent(String.self, forKey: .root) ?? "."
            rootSource = try c.decodeIfPresent(VersionSource.self, forKey: .rootSource) ?? .default
            rootPinned = try c.decodeIfPresent(String.self, forKey: .rootPinned) ?? ""
            rootFile = try c.decodeIfPresent(String.self, forKey: .rootFile) ?? ""
            rootState = try c.decodeIfPresent(RootState.self, forKey: .rootState) ?? .ok
            rootSuggestions = try c.decodeIfPresent([String].self, forKey: .rootSuggestions) ?? []
            domains = try c.decodeIfPresent([SiteDomain].self, forKey: .domains) ?? []
            debugging = try c.decodeIfPresent(Bool.self, forKey: .debugging) ?? false
            server = try c.decodeIfPresent(Server.self, forKey: .server) ?? .fastcgi
            worker = try c.decodeIfPresent(Worker.self, forKey: .worker) ?? .classic
            octaneAvailable = try c.decodeIfPresent(Bool.self, forKey: .octaneAvailable) ?? false
            frankenPHP = try c.decodeIfPresent(SiteFrankenPHP.self, forKey: .frankenPHP)
            boost = try c.decodeIfPresent(Boost.self, forKey: .boost) ?? .none
            share = try c.decodeIfPresent(ShareStatus.self, forKey: .share)
            parent = try c.decodeIfPresent(String.self, forKey: .parent).flatMap { $0.isEmpty ? nil : $0 }
            branch = try c.decodeIfPresent(String.self, forKey: .branch) ?? ""
            worktree = try c.decodeIfPresent(WorktreeSetup.self, forKey: .worktree)
        }
    }

    /// Why a site isn't served from its document root; the raw values are the API's.
    public enum RootState: String, Decodable, Sendable, Hashable {
        case ok = ""
        /// The root set on this machine or in .flockrc isn't a folder.
        case missing
        /// A Composer project without an index.php in public/, www/, web/, webroot/ or its own folder.
        case notFound = "not_found"
        /// The root has neither an index.php nor an index.html (yet).
        case empty

        public init(from decoder: Decoder) throws {
            self = RootState(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .ok
        }
    }

    /// A project's Laravel Boost guidelines and Flock's guideline in them; the raw values are the API's.
    public enum Boost: String, Decodable, Sendable, Hashable {
        /// The project doesn't use Boost.
        case none = ""
        /// Uses Boost, without Flock's guideline.
        case missing
        case current
        /// Has an older version of Flock's guideline.
        case outdated
        /// Has a flock.md Flock didn't write; Flock leaves it alone.
        case custom

        public init(from decoder: Decoder) throws {
            self = Boost(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .none
        }
    }

    /// What runs a site's PHP; the raw values are the API's.
    public enum Server: String, Decodable, Sendable, Hashable {
        /// The PHP version's PHP-FPM pool (php-cgi on Windows).
        case fastcgi
        case frankenphp

        public init(from decoder: Decoder) throws {
            self = Server(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .fastcgi
        }
    }

    /// FrankenPHP's worker mode; the raw values are the API's.
    public enum Worker: String, Decodable, Sendable, Hashable {
        case classic = ""
        /// Laravel Octane keeps the app booted in workers.
        case octane

        public init(from decoder: Decoder) throws {
            self = Worker(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .classic
        }
    }

    /// A site's FrankenPHP process.
    public struct SiteFrankenPHP: Decodable, Sendable, Equatable {
        public var state: State
        /// FrankenPHP, e.g. "1.13.0"; empty until installed.
        public var version: String
        /// The embedded PHP, e.g. "8.4.14".
        public var phpVersion: String
        public var port: Int
        /// While installing: 0...1, or nil when unknown.
        public var progress: Double?
        /// Why it doesn't run (failed, unavailable).
        public var problem: String
        /// The name of its service log.
        public var log: String

        public enum State: String, Decodable, Sendable {
            /// Serving the site.
            case running
            /// The build for the site's PHP version is downloading.
            case installing
            /// See `problem`; PHP-FPM serves the site.
            case failed
            /// No build for this platform or PHP version; see `problem`.
            case unavailable
            case unknown

            public init(from decoder: Decoder) throws {
                self = State(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .unknown
            }
        }

        enum CodingKeys: String, CodingKey { case state, version, phpVersion, port, progress, problem, log }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            state = try c.decodeIfPresent(State.self, forKey: .state) ?? .unknown
            version = try c.decodeIfPresent(String.self, forKey: .version) ?? ""
            phpVersion = try c.decodeIfPresent(String.self, forKey: .phpVersion) ?? ""
            port = try c.decodeIfPresent(Int.self, forKey: .port) ?? 0
            let raw = try c.decodeIfPresent(Double.self, forKey: .progress) ?? -1
            progress = raw < 0 ? nil : min(raw, 1)
            problem = try c.decodeIfPresent(String.self, forKey: .problem) ?? ""
            log = try c.decodeIfPresent(String.self, forKey: .log) ?? ""
        }
    }

    /// Where a site's PHP or Node.js version comes from, in this order: set for the site in Flock
    /// on this machine (`flock isolate`), pinned in the project (`flock pin`), Flock's default.
    public enum VersionSource: String, Decodable, Sendable {
        case isolated, project, `default`

        public init(from decoder: Decoder) throws {
            self = VersionSource(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .default
        }
    }

    public struct Process: Decodable, Sendable, Equatable, Identifiable {
        public var name: String
        public var up: Bool
        public var restarts: Int
        public var lastError: String?

        public var id: String { name }
    }

    /// An installed catalog service, for the menu bar panel.
    public struct Extra: Decodable, Sendable, Equatable, Identifiable {
        public var id: String
        public var name: String
        public var version: String
        public var port: Int
        public var enabled: Bool
        public var up: Bool

        enum CodingKeys: String, CodingKey { case id, name, version, port, enabled, up }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            id = try c.decode(String.self, forKey: .id)
            name = try c.decodeIfPresent(String.self, forKey: .name) ?? id
            version = try c.decodeIfPresent(String.self, forKey: .version) ?? ""
            port = try c.decodeIfPresent(Int.self, forKey: .port) ?? 0
            enabled = try c.decodeIfPresent(Bool.self, forKey: .enabled) ?? true
            up = try c.decodeIfPresent(Bool.self, forKey: .up) ?? false
        }
    }

    /// The first run's default PHP and Node.js installs.
    public struct Defaults: Decodable, Sendable, Equatable {
        public var php: DefaultInstall
        public var node: DefaultInstall

        public init(php: DefaultInstall = DefaultInstall(), node: DefaultInstall = DefaultInstall()) {
            self.php = php
            self.node = node
        }

        enum CodingKeys: String, CodingKey { case php, node }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            php = try c.decodeIfPresent(DefaultInstall.self, forKey: .php) ?? DefaultInstall()
            node = try c.decodeIfPresent(DefaultInstall.self, forKey: .node) ?? DefaultInstall()
        }
    }

    /// One of the first run's installs: the newest PHP minor or Node.js LTS major, until it is the default.
    public struct DefaultInstall: Decodable, Sendable, Equatable {
        /// "8.5", "24"; empty until the package manifest says which.
        public var version: String
        public var state: State
        /// Why it failed; the user tries again from Settings → PHP or Node.
        public var error: String

        public init(version: String = "", state: State = .done, error: String = "") {
            self.version = version
            self.state = state
            self.error = error
        }

        /// Not installed yet, but on its way.
        public var isPending: Bool { state == .waiting || state == .installing }

        public enum State: String, Decodable, Sendable {
            /// No package manifest yet (e.g. offline).
            case waiting
            case installing
            /// See `error`.
            case failed
            /// The default is set.
            case done
            case unknown

            public init(from decoder: Decoder) throws {
                self = State(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .unknown
            }
        }

        enum CodingKeys: String, CodingKey { case version, state, error }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            version = try c.decodeIfPresent(String.self, forKey: .version) ?? ""
            state = try c.decodeIfPresent(State.self, forKey: .state) ?? .unknown
            error = try c.decodeIfPresent(String.self, forKey: .error) ?? ""
        }
    }

    /// Something needs attention: a warning, or a supervised process that is down.
    public var isDegraded: Bool {
        !warnings.isEmpty || processes.contains { !$0.up }
    }

    enum CodingKeys: String, CodingKey {
        case version, pid, startedAt, dns, httpPort, httpsPort, mail, postgres, extras
        case xdebug, xdebugBreakpoints, dumps, captureOff, dumpsInPage, mailCount, dumpCount, eventCount, captureUnsupported
        case sites, proxies, processes, warnings, channel, defaults
    }

    // Go omits empty slices (`omitempty`) or sends null; treat both as empty.
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        version = try c.decode(String.self, forKey: .version)
        pid = try c.decode(Int.self, forKey: .pid)
        startedAt = try c.decode(Date.self, forKey: .startedAt)
        dns = try c.decodeIfPresent(DNSStatus.self, forKey: .dns) ?? DNSStatus(tld: "", addr: "")
        httpPort = try c.decodeIfPresent(Int.self, forKey: .httpPort) ?? 0
        httpsPort = try c.decodeIfPresent(Int.self, forKey: .httpsPort) ?? 0
        mail = try c.decodeIfPresent(String.self, forKey: .mail) ?? ""
        postgres = try c.decodeIfPresent(String.self, forKey: .postgres)
        extras = try c.decodeIfPresent([Extra].self, forKey: .extras) ?? []
        xdebug = try c.decodeIfPresent(Bool.self, forKey: .xdebug) ?? false
        xdebugBreakpoints = try c.decodeIfPresent(Bool.self, forKey: .xdebugBreakpoints) ?? false
        dumps = try c.decodeIfPresent(Bool.self, forKey: .dumps) ?? false
        captureOff = try c.decodeIfPresent([String].self, forKey: .captureOff) ?? []
        dumpsInPage = try c.decodeIfPresent(Bool.self, forKey: .dumpsInPage) ?? false
        mailCount = try c.decodeIfPresent(Int.self, forKey: .mailCount) ?? 0
        dumpCount = try c.decodeIfPresent(Int.self, forKey: .dumpCount) ?? 0
        eventCount = try c.decodeIfPresent(Int.self, forKey: .eventCount) ?? 0
        captureUnsupported = try c.decodeIfPresent([String].self, forKey: .captureUnsupported) ?? []
        sites = try c.decodeIfPresent([Site].self, forKey: .sites) ?? []
        proxies = try c.decodeIfPresent([Proxy].self, forKey: .proxies) ?? []
        processes = try c.decodeIfPresent([Process].self, forKey: .processes) ?? []
        warnings = try c.decodeIfPresent([String].self, forKey: .warnings) ?? []
        channel = try c.decodeIfPresent(String.self, forKey: .channel) ?? "stable"
        defaults = try c.decodeIfPresent(Defaults.self, forKey: .defaults)
    }
}

/// An entry of GET /v1/mail.
public struct MailSummary: Decodable, Sendable, Equatable, Identifiable, Hashable {
    public var id: String
    public var inbox: String
    public var from: String
    public var to: [String]
    public var subject: String
    public var date: Date
    public var size: Int

    enum CodingKeys: String, CodingKey { case id, inbox, from, to, subject, date, size }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        inbox = try c.decodeIfPresent(String.self, forKey: .inbox) ?? ""
        from = try c.decodeIfPresent(String.self, forKey: .from) ?? ""
        to = try c.decodeIfPresent([String].self, forKey: .to) ?? []
        subject = try c.decodeIfPresent(String.self, forKey: .subject) ?? ""
        date = try c.decodeIfPresent(Date.self, forKey: .date) ?? .distantPast
        size = try c.decodeIfPresent(Int.self, forKey: .size) ?? 0
    }
}

/// GET /v1/mail/{id}
public struct MailDetail: Decodable, Sendable, Equatable {
    public var id: String
    public var inbox: String
    public var from: String
    public var to: [String]
    public var cc: [String]
    public var subject: String
    public var date: Date
    public var html: String
    public var text: String
    public var headers: [Header]
    public var attachments: [Attachment]

    public struct Header: Decodable, Sendable, Equatable, Hashable {
        public var name: String
        public var value: String
    }

    public struct Attachment: Decodable, Sendable, Equatable, Identifiable {
        public var index: Int
        public var filename: String
        public var contentType: String
        public var size: Int

        public var id: Int { index }
    }

    enum CodingKeys: String, CodingKey {
        case id, inbox, from, to, cc, subject, date, html, text, headers, attachments
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        inbox = try c.decodeIfPresent(String.self, forKey: .inbox) ?? ""
        from = try c.decodeIfPresent(String.self, forKey: .from) ?? ""
        to = try c.decodeIfPresent([String].self, forKey: .to) ?? []
        cc = try c.decodeIfPresent([String].self, forKey: .cc) ?? []
        subject = try c.decodeIfPresent(String.self, forKey: .subject) ?? ""
        date = try c.decodeIfPresent(Date.self, forKey: .date) ?? .distantPast
        html = try c.decodeIfPresent(String.self, forKey: .html) ?? ""
        text = try c.decodeIfPresent(String.self, forKey: .text) ?? ""
        headers = try c.decodeIfPresent([Header].self, forKey: .headers) ?? []
        attachments = try c.decodeIfPresent([Attachment].self, forKey: .attachments) ?? []
    }
}

/// An entry of GET /v1/dumps.
public struct Dump: Decodable, Sendable, Equatable, Identifiable, Hashable {
    public var id: String
    public var time: Date
    public var site: String
    public var source: Source?
    public var request: Request?
    public var commandLine: String?
    /// Shared by every dump of one HTTP request or CLI run.
    public var group: String
    public var label: String?
    public var text: String
    /// Dump body only; render it after the shared header from `FlockClient.dumpHeader()`.
    public var html: String

    public struct Source: Decodable, Sendable, Equatable, Hashable {
        public var file: String
        public var line: Int
        public var name: String

        public init(file: String, line: Int, name: String = "") {
            self.file = file
            self.line = line
            self.name = name
        }

        enum CodingKeys: String, CodingKey { case file, line, name }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            file = try c.decodeIfPresent(String.self, forKey: .file) ?? ""
            line = try c.decodeIfPresent(Int.self, forKey: .line) ?? 0
            name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        }
    }

    public struct Request: Decodable, Sendable, Equatable, Hashable {
        public var method: String
        public var uri: String
    }

    /// "Controller.php:42" — the last path components keep list rows short.
    public var shortSource: String? {
        guard let source, !source.file.isEmpty else { return nil }
        let parts = source.file.split(separator: "/").suffix(2).joined(separator: "/")
        return source.line > 0 ? "\(parts):\(source.line)" : parts
    }

    enum CodingKeys: String, CodingKey { case id, time, site, source, request, commandLine, group, label, text, html }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        time = try c.decodeIfPresent(Date.self, forKey: .time) ?? .distantPast
        site = try c.decodeIfPresent(String.self, forKey: .site) ?? ""
        source = try c.decodeIfPresent(Source.self, forKey: .source)
        request = try c.decodeIfPresent(Request.self, forKey: .request)
        commandLine = try c.decodeIfPresent(String.self, forKey: .commandLine)
        group = try c.decodeIfPresent(String.self, forKey: .group) ?? ""
        label = try c.decodeIfPresent(String.self, forKey: .label)
        text = try c.decodeIfPresent(String.self, forKey: .text) ?? ""
        html = try c.decodeIfPresent(String.self, forKey: .html) ?? ""
    }
}

/// An entry of GET /v1/events: a query, log message, rendered view, queue job or finished request
/// that Flock's PHP agent captured. Decode it with `FlockJSON.eventDecoder()`, which keeps the
/// keys of a log message's context as the app wrote them.
public struct Event: Decodable, Sendable, Equatable, Identifiable, Hashable {
    public enum Kind: String, Sendable, CaseIterable, Identifiable {
        case query, log, view, job, request

        public var id: String { rawValue }

        /// "Queries", "Logs", ... for menus and settings.
        public var title: String {
            switch self {
            case .query: "Queries"
            case .log: "Logs"
            case .view: "Views"
            case .job: "Jobs"
            case .request: "Requests"
            }
        }
    }

    public var id: String
    /// nil for a kind this version of the app doesn't know; such events are left out.
    public var kind: Kind?
    public var time: Date
    public var site: String
    /// Shared with the dumps and other events of the same HTTP request or CLI run.
    public var group: String
    public var request: Dump.Request?
    public var commandLine: String?
    /// The first frame outside vendor/ (where a query was run, a job dispatched, ...).
    public var source: Dump.Source?

    // query
    public var sql: String
    public var bindings: [JSONValue]
    public var connection: String
    /// Queries, finished jobs and requests.
    public var durationMs: Double?

    // log
    public var level: String
    public var message: String
    public var context: [String: JSONValue]

    // view
    public var view: String
    public var path: String

    // job
    public var job: String
    public var queue: String
    /// "processing", "processed" or "failed".
    public var state: String
    public var exception: String

    // request
    public var statusCode: Int?

    enum CodingKeys: String, CodingKey {
        case id, kind, time, site, group, request, source, sql, bindings, connection, level, message, context
        case view, path, job, queue, state, exception
        case commandLine = "command_line"
        case durationMs = "duration_ms"
        case statusCode = "status_code"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        kind = (try? c.decodeIfPresent(String.self, forKey: .kind)).flatMap { $0.flatMap(Kind.init(rawValue:)) }
        time = try c.decodeIfPresent(Date.self, forKey: .time) ?? .distantPast
        site = try c.decodeIfPresent(String.self, forKey: .site) ?? ""
        group = try c.decodeIfPresent(String.self, forKey: .group) ?? ""
        request = try? c.decodeIfPresent(Dump.Request.self, forKey: .request)
        commandLine = try? c.decodeIfPresent(String.self, forKey: .commandLine)
        source = try? c.decodeIfPresent(Dump.Source.self, forKey: .source)
        sql = (try? c.decodeIfPresent(String.self, forKey: .sql)) ?? ""
        bindings = (try? c.decodeIfPresent([JSONValue].self, forKey: .bindings)) ?? []
        connection = (try? c.decodeIfPresent(String.self, forKey: .connection)) ?? ""
        durationMs = try? c.decodeIfPresent(Double.self, forKey: .durationMs)
        level = (try? c.decodeIfPresent(String.self, forKey: .level)) ?? ""
        message = (try? c.decodeIfPresent(String.self, forKey: .message)) ?? ""
        // PHP encodes an empty array as [], so an empty context may come as a list.
        context = (try? c.decodeIfPresent([String: JSONValue].self, forKey: .context)) ?? [:]
        view = (try? c.decodeIfPresent(String.self, forKey: .view)) ?? ""
        path = (try? c.decodeIfPresent(String.self, forKey: .path)) ?? ""
        job = (try? c.decodeIfPresent(String.self, forKey: .job)) ?? ""
        queue = (try? c.decodeIfPresent(String.self, forKey: .queue)) ?? ""
        state = (try? c.decodeIfPresent(String.self, forKey: .state)) ?? ""
        exception = (try? c.decodeIfPresent(String.self, forKey: .exception)) ?? ""
        statusCode = try? c.decodeIfPresent(Int.self, forKey: .statusCode)
    }
}

/// GET /v1/logs
public struct LogIndex: Decodable, Sendable, Equatable {
    public var services: [String]
    public var sites: [String]

    enum CodingKeys: String, CodingKey { case services, sites }

    public init(services: [String] = [], sites: [String] = []) {
        self.services = services
        self.sites = sites
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        services = try c.decodeIfPresent([String].self, forKey: .services) ?? []
        sites = try c.decodeIfPresent([String].self, forKey: .sites) ?? []
    }
}

/// An entry of GET /v1/logs/sites/{name}: one parsed Laravel log record.
public struct LogEntry: Decodable, Sendable, Equatable, Identifiable {
    public var time: Date
    public var env: String
    public var level: String
    public var message: String
    public var details: String

    /// Entries have no id on the wire; time + message is unique enough for list diffing.
    public var id: String { "\(time.timeIntervalSince1970)|\(level)|\(message)" }

    enum CodingKeys: String, CodingKey { case time, env, level, message, details }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        time = try c.decodeIfPresent(Date.self, forKey: .time) ?? .distantPast
        env = try c.decodeIfPresent(String.self, forKey: .env) ?? ""
        level = try c.decodeIfPresent(String.self, forKey: .level)?.lowercased() ?? "info"
        message = try c.decodeIfPresent(String.self, forKey: .message) ?? ""
        details = try c.decodeIfPresent(String.self, forKey: .details) ?? ""
    }
}

/// One template entry from GET /v1/apps/templates (apps.conf).
public struct AppTemplate: Decodable, Sendable, Equatable, Identifiable {
    public var name: String
    public var requiresDb: Bool

    public var id: String { name }

    public static let builtInDefaults: [AppTemplate] = [
        AppTemplate(name: "blank"),
        AppTemplate(name: "php"),
        AppTemplate(name: "wordpress", requiresDb: true),
    ]

    public init(name: String, requiresDb: Bool = false) {
        self.name = name
        self.requiresDb = requiresDb
    }

    enum CodingKeys: String, CodingKey { case name, requiresDb }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        requiresDb = try c.decodeIfPresent(Bool.self, forKey: .requiresDb) ?? false
    }
}

/// POST /v1/apps.
public struct CreateAppRequest: Encodable, Sendable, Equatable {
    public var name: String
    public var parentDir: String
    public var type: String
    public var db: Bool
    public var dbService: String?
    public var php: String?
    public var secured: Bool

    public init(
        name: String,
        parentDir: String,
        type: String = "php",
        db: Bool = false,
        dbService: String? = nil,
        php: String? = nil,
        secured: Bool = false
    ) {
        self.name = name
        self.parentDir = parentDir
        self.type = type
        self.db = db
        self.dbService = dbService
        self.php = php
        self.secured = secured
    }

    enum CodingKeys: String, CodingKey {
        case name
        case parentDir = "parent_dir"
        case type
        case db
        case dbService = "db_service"
        case php
        case secured
    }
}
