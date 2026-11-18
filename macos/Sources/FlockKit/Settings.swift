import Foundation

/// GET /v1/config — the persisted configuration plus what is installed.
public struct FlockConfig: Decodable, Sendable, Equatable {
    public var tld: String
    public var defaultPHP: String
    public var defaultNode: String
    public var xdebug: Bool
    public var dumps: Bool
    /// Event kinds that are not captured (see `Status.captureOff`).
    public var captureOff: [String]
    public var dumpsInPage: Bool
    /// The channel of prebuilt packages: "stable", "beta" or "experimental" (`flock channel`).
    public var channel: String
    public var root: String
    public var ports: Ports
    public var parked: [String]
    public var php: [PHPVersion]
    public var node: [String]

    public struct Ports: Decodable, Sendable, Equatable {
        public var http: Int
        public var https: Int
        public var dns: Int
        public var smtp: Int
        public var postgres: Int
        public var dumps: Int
        /// Where the PHP agent sends what it captures.
        public var agent: Int
        public var api: Int

        enum CodingKeys: String, CodingKey { case http, https, dns, smtp, postgres, dumps, agent, api }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            func port(_ key: CodingKeys) throws -> Int { try c.decodeIfPresent(Int.self, forKey: key) ?? 0 }
            http = try port(.http)
            https = try port(.https)
            dns = try port(.dns)
            smtp = try port(.smtp)
            postgres = try port(.postgres)
            dumps = try port(.dumps)
            agent = try port(.agent)
            api = try port(.api)
        }
    }

    public struct PHPVersion: Decodable, Sendable, Equatable, Identifiable {
        public var version: String
        public var fullVersion: String
        public var ini: String
        public var xdebugAvailable: Bool

        public var id: String { version }

        enum CodingKeys: String, CodingKey { case version, fullVersion, ini, xdebugAvailable }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            version = try c.decode(String.self, forKey: .version)
            fullVersion = try c.decodeIfPresent(String.self, forKey: .fullVersion) ?? version
            ini = try c.decodeIfPresent(String.self, forKey: .ini) ?? ""
            xdebugAvailable = try c.decodeIfPresent(Bool.self, forKey: .xdebugAvailable) ?? false
        }
    }

    // convertFromSnakeCase turns default_php into defaultPhp, so these keys are spelled out.
    enum CodingKeys: String, CodingKey {
        case tld, xdebug, dumps, captureOff, dumpsInPage, root, ports, parked, php, node
        case defaultPHP = "defaultPhp"
        case channel
        case defaultNode
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        tld = try c.decodeIfPresent(String.self, forKey: .tld) ?? "test"
        defaultPHP = try c.decodeIfPresent(String.self, forKey: .defaultPHP) ?? ""
        defaultNode = try c.decodeIfPresent(String.self, forKey: .defaultNode) ?? ""
        xdebug = try c.decodeIfPresent(Bool.self, forKey: .xdebug) ?? false
        dumps = try c.decodeIfPresent(Bool.self, forKey: .dumps) ?? false
        captureOff = try c.decodeIfPresent([String].self, forKey: .captureOff) ?? []
        dumpsInPage = try c.decodeIfPresent(Bool.self, forKey: .dumpsInPage) ?? false
        channel = try c.decodeIfPresent(String.self, forKey: .channel) ?? "stable"
        root = try c.decodeIfPresent(String.self, forKey: .root) ?? ""
        ports = try c.decode(Ports.self, forKey: .ports)
        parked = try c.decodeIfPresent([String].self, forKey: .parked) ?? []
        php = try c.decodeIfPresent([PHPVersion].self, forKey: .php) ?? []
        node = try c.decodeIfPresent([String].self, forKey: .node) ?? []
    }

    /// Node majors that are installed, newest first ("22.23.3" → "22").
    public var nodeMajors: [String] {
        var seen = Set<String>()
        return node.compactMap { $0.split(separator: ".").first.map(String.init) }
            .filter { seen.insert($0).inserted }
    }
}

/// GET /v1/services — one background service (web server, database, mail catcher, ...).
public struct Service: Decodable, Sendable, Equatable, Identifiable {
    public var id: String
    public var name: String
    public var category: String
    /// What it is, for services installed on request; may be empty.
    public var description: String
    /// False for a service Flock can't run on this platform; `unavailable` says why.
    public var available: Bool
    public var unavailable: String
    public var version: String
    public var port: Int
    public var installed: Bool
    public var enabled: Bool
    public var running: Bool
    public var stoppable: Bool
    public var env: String
    public var log: String
    public var url: String
    /// Why it isn't working, when that isn't simply "stopped".
    public var problem: String

    enum CodingKeys: String, CodingKey {
        case id, name, category, description, available, unavailable, version, port, installed, enabled, running, stoppable, env, log, url, problem
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? id
        category = try c.decodeIfPresent(String.self, forKey: .category) ?? "Other"
        description = try c.decodeIfPresent(String.self, forKey: .description) ?? ""
        available = try c.decodeIfPresent(Bool.self, forKey: .available) ?? true
        unavailable = try c.decodeIfPresent(String.self, forKey: .unavailable) ?? ""
        version = try c.decodeIfPresent(String.self, forKey: .version) ?? ""
        port = try c.decodeIfPresent(Int.self, forKey: .port) ?? 0
        installed = try c.decodeIfPresent(Bool.self, forKey: .installed) ?? false
        enabled = try c.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        running = try c.decodeIfPresent(Bool.self, forKey: .running) ?? false
        stoppable = try c.decodeIfPresent(Bool.self, forKey: .stoppable) ?? false
        env = try c.decodeIfPresent(String.self, forKey: .env) ?? ""
        log = try c.decodeIfPresent(String.self, forKey: .log) ?? ""
        url = try c.decodeIfPresent(String.self, forKey: .url) ?? ""
        problem = try c.decodeIfPresent(String.self, forKey: .problem) ?? ""
    }

    /// Is this a database that a GUI client like TablePlus can open?
    public var isDatabase: Bool { category == "Database" && !url.isEmpty }
}

/// Body of POST /v1/settings; nil fields are left out and stay unchanged.
public struct SettingsChange: Encodable, Sendable {
    public var xdebug: Bool?
    public var xdebugBreakpoints: Bool?
    public var dumps: Bool?
    /// Replaces the list of event kinds that are not captured.
    public var captureOff: [String]?
    public var dumpsInPage: Bool?
    public var defaultPHP: String?
    public var defaultNode: String?
    /// "stable" or "beta" ("experimental" is set with `flock channel`).
    public var channel: String?

    public init(xdebug: Bool? = nil, xdebugBreakpoints: Bool? = nil, dumps: Bool? = nil, captureOff: [String]? = nil, dumpsInPage: Bool? = nil,
                defaultPHP: String? = nil, defaultNode: String? = nil, channel: String? = nil) {
        self.xdebug = xdebug
        self.xdebugBreakpoints = xdebugBreakpoints
        self.dumps = dumps
        self.captureOff = captureOff
        self.dumpsInPage = dumpsInPage
        self.defaultPHP = defaultPHP
        self.defaultNode = defaultNode
        self.channel = channel
    }

    enum CodingKeys: String, CodingKey {
        case xdebug, dumps, channel
        case xdebugBreakpoints = "xdebug_breakpoints"
        case captureOff = "capture_off"
        case dumpsInPage = "dumps_in_page"
        case defaultPHP = "default_php"
        case defaultNode = "default_node"
    }
}

/// Body of PATCH /v1/sites/{name}; nil fields are left out. An empty `php` means "use the default".
/// `server` is "fastcgi" or "frankenphp", `worker` "" (classic) or "octane".
public struct SiteChange: Encodable, Sendable {
    /// "php" or "node": what the site is.
    public var type: String?
    public var secured: Bool?
    public var php: String?
    public var node: String?
    public var server: String?
    public var worker: String?
    /// The document root on this machine, relative to the project ("" lets the project or detection decide).
    public var root: String?
    /// Runs Laravel Reverb for the site, or stops it.
    public var reverb: Bool?

    public init(type: String? = nil, secured: Bool? = nil, php: String? = nil, node: String? = nil, server: String? = nil, worker: String? = nil, root: String? = nil,
                reverb: Bool? = nil) {
        self.type = type
        self.secured = secured
        self.php = php
        self.node = node
        self.server = server
        self.worker = worker
        self.root = root
        self.reverb = reverb
    }
}

/// Body of PATCH /v1/proxies/{name}; nil fields are left out. An empty `command` removes the
/// command, its folder and its stop command.
public struct ProxyChange: Encodable, Sendable {
    public var upstream: String?
    public var secured: Bool?
    public var command: String?
    /// The absolute folder the command runs in.
    public var dir: String?
    /// For a command that starts the server in the background: the command that stops it.
    public var stopCommand: String?

    enum CodingKeys: String, CodingKey {
        case upstream, secured, command, dir
        case stopCommand = "stop_command"
    }

    public init(upstream: String? = nil, secured: Bool? = nil, command: String? = nil, dir: String? = nil, stopCommand: String? = nil) {
        self.upstream = upstream
        self.secured = secured
        self.command = command
        self.dir = dir
        self.stopCommand = stopCommand
    }
}
