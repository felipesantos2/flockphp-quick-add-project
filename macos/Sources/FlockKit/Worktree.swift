import Foundation

/// How Flock set up a git worktree it serves under its site (see `Status.Site.worktree`).
public struct WorktreeSetup: Decodable, Sendable, Equatable {
    public var state: State
    /// What a running setup does now, e.g. "composer install".
    public var step: String
    /// Why the setup failed.
    public var error: String?
    /// The name of its service log.
    public var log: String
    /// The database mode asked for; nil leaves it to the project's flock.yml.
    public var dbMode: DatabaseMode?
    /// The worktree's own database; empty when it uses the site's.
    public var database: String

    public enum State: String, Decodable, Sendable {
        case queued, running, ready, failed, unknown

        public init(from decoder: Decoder) throws {
            self = State(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .unknown
        }
    }

    /// The setup is waiting or running.
    public var busy: Bool { state == .queued || state == .running }

    enum CodingKeys: String, CodingKey {
        case state, step, error, log, dbMode, database
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        state = try c.decodeIfPresent(State.self, forKey: .state) ?? .unknown
        step = try c.decodeIfPresent(String.self, forKey: .step) ?? ""
        error = try c.decodeIfPresent(String.self, forKey: .error)
        log = try c.decodeIfPresent(String.self, forKey: .log) ?? ""
        dbMode = (try c.decodeIfPresent(String.self, forKey: .dbMode)).flatMap(DatabaseMode.init(rawValue:))
        database = try c.decodeIfPresent(String.self, forKey: .database) ?? ""
    }
}

/// A worktree's database: the site's, a copy of it, or an empty one with the migrations run.
public enum DatabaseMode: String, Codable, Sendable, CaseIterable, Identifiable {
    case shared, copy, fresh

    public var id: String { rawValue }

    public var title: String {
        switch self {
        case .shared: String(localized: "worktrees.database_shared")
        case .copy: String(localized: "worktrees.database_copy")
        case .fresh: String(localized: "worktrees.database_fresh")
        }
    }
}

/// A new worktree: its folder, its branch (Flock's name for it when none was given), and its setup.
public struct WorktreeCreated: Decodable, Sendable {
    public var path: String
    public var branch: String?
    public var setup: WorktreeSetup
}

/// A site's git branches, for where a new worktree's branch starts.
public struct Branches: Decodable, Sendable, Equatable {
    /// The branch the site's checkout is on; empty when its HEAD is detached.
    public var current: String
    /// The repository's default branch (origin's HEAD, else main or master); empty when unknown.
    public var `default`: String
    public var local: [String]
    /// Remote-tracking branches (origin/main), as git last fetched them.
    public var remote: [String]

    public init(current: String = "", default: String = "", local: [String] = [], remote: [String] = []) {
        self.current = current
        self.default = `default`
        self.local = local
        self.remote = remote
    }

    enum CodingKeys: String, CodingKey {
        case current, `default`, local, remote
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        current = try c.decodeIfPresent(String.self, forKey: .current) ?? ""
        `default` = try c.decodeIfPresent(String.self, forKey: .default) ?? ""
        local = try c.decodeIfPresent([String].self, forKey: .local) ?? []
        remote = try c.decodeIfPresent([String].self, forKey: .remote) ?? []
    }

    /// The local branches in the order a menu offers them: the current one, the default one, then
    /// the others by name.
    public var ordered: [String] {
        var out: [String] = []
        for name in [current, `default`] + local where local.contains(name) && !out.contains(name) {
            out.append(name)
        }
        return out
    }
}

extension Status.Site {
    /// A git worktree of another site (`parent`).
    public var isWorktree: Bool { parent != nil }

    /// What a worktree is called: its branch, else the name it's served under.
    public var worktreeLabel: String {
        if !branch.isEmpty { return branch }
        return name.split(separator: ".").first.map(String.init) ?? name
    }
}

extension Array where Element == Status.Site {
    /// The sites without the worktrees, which go under their site (see `worktrees(of:)`).
    public var topLevel: [Status.Site] { filter { !$0.isWorktree } }

    /// The worktrees of the site named name.
    public func worktrees(of name: String) -> [Status.Site] { filter { $0.parent == name } }

    /// The sites as lists show them: each followed by its worktrees.
    public var nested: [Status.Site] { topLevel.flatMap { [$0] + worktrees(of: $0.name) } }
}
