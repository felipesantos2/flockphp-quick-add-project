import Foundation

/// A link from the website or the docs that asks to install a service:
/// `flock://install/<service>` or `flock://install/<service>/<version line>`, e.g.
/// `flock://install/mysql/8.4` or `flock://install/postgres/18`. PHP and Node.js take the same
/// form (`flock://install/php/8.5`, `flock://install/node/24`); the page the daemon serves for a
/// site whose PHP is missing links to it.
///
/// Any web page can open such a link, so it never installs anything by itself: the app shows
/// what it would install and waits for the user to confirm (see `resolve`).
public struct InstallLink: Equatable, Sendable {
    public var service: String
    /// The version line ("8.4", a PostgreSQL major); nil for the newest.
    public var line: String?

    public init(service: String, line: String? = nil) {
        self.service = service
        self.line = line
    }

    public init?(url: URL) {
        guard url.scheme?.lowercased() == "flock", url.host?.lowercased() == "install" else { return nil }
        let parts = url.path.split(separator: "/", omittingEmptySubsequences: true).map(String.init)
        guard (1...2).contains(parts.count), Self.isSafe(parts[0], maxLength: 32) else { return nil }
        if parts.count == 2, !Self.isSafe(parts[1], maxLength: 16) {
            return nil
        }
        service = parts[0].lowercased()
        line = parts.count == 2 ? parts[1] : nil
    }

    /// Letters, digits, dots and dashes only, so nothing odd ends up in a dialog or a request.
    private static func isSafe(_ part: String, maxLength: Int) -> Bool {
        !part.isEmpty && part.count <= maxLength
            && part.unicodeScalars.allSatisfy { CharacterSet.alphanumerics.contains($0) && $0.isASCII || $0 == "." || $0 == "-" }
    }

    /// What the link asks for, measured against what this Flock offers and has installed.
    /// Nil while the services and versions are still loading.
    public func resolve(services: [Service], catalog: ServiceCatalog?, postgres: PostgresVersions?) -> Resolution? {
        guard !services.isEmpty else { return nil }
        guard let service = services.first(where: { $0.id == self.service }) else {
            return .unknown(name: self.service)
        }
        guard service.available else {
            return .unavailable(name: service.name, reason: service.unavailable)
        }

        if service.id == "postgres" {
            guard let postgres else { return nil }
            let wanted = line.flatMap { line in postgres.majors.first { $0.major == line } } ?? (line == nil ? postgres.majors.first : nil)
            guard let major = wanted else { return .unknownVersion(name: service.name, line: line ?? "") }
            if major.isInstalled {
                return .installed(name: "PostgreSQL \(major.major)")
            }
            return .install(Install(service: service.id, name: service.name, line: major.major, label: major.major, version: major.latest))
        }

        guard let catalog else { return nil }
        guard let entry = catalog[service.id] else { return .unknown(name: service.name) }
        let wanted = line.flatMap { line in entry.lines.first { $0.id == line } } ?? (line == nil ? entry.lines.first : nil)
        guard let target = wanted else { return .unknownVersion(name: entry.name, line: line ?? "") }
        if target.installed {
            return .installed(name: "\(entry.name) \(target.label)")
        }
        return .install(Install(service: entry.id, name: entry.name, line: target.id, label: target.label, version: target.latest))
    }

    /// What a link for PHP asks for, measured against the PHP versions. Without a version it
    /// means the newest one that isn't a pre-release. Nil while the versions are still loading.
    public func resolve(php catalog: PHPCatalog?) -> Resolution? {
        guard let catalog else { return nil }
        let wanted: PHPRelease?
        if let line {
            wanted = catalog.versions.first { $0.minor == line }
        } else {
            wanted = Self.newest(catalog.versions.filter { !$0.prerelease && !$0.latest.isEmpty }, by: \.minor)
        }
        // A version without a build in the channel is only listed while it's installed.
        guard let release = wanted, release.isInstalled || !release.latest.isEmpty else {
            return .noBuild(name: "PHP", line: line ?? "")
        }
        if release.isInstalled {
            return .installed(name: "PHP \(release.minor)", pane: "PHP")
        }
        if release.job?.isRunning == true {
            return .installing(name: "PHP \(release.minor)")
        }
        return .install(Install(service: "php", name: "PHP", line: release.minor, label: release.minor, version: release.latest))
    }

    /// What a link for Node.js asks for, measured against the Node.js versions; `installing` are
    /// the majors an install already runs for (the first run's default, or one started from a link).
    /// Without a version it means the newest listed major (the versions don't say which are LTS). Nil while the versions are still loading.
    public func resolve(node catalog: NodeCatalog?, installing: Set<String> = []) -> Resolution? {
        guard let catalog else { return nil }
        let wanted: NodeRelease?
        if let line {
            wanted = catalog.versions.first { $0.major == line }
        } else {
            wanted = Self.newest(catalog.versions.filter { !$0.latest.isEmpty }, by: \.major)
        }
        guard let release = wanted, release.isInstalled || !release.latest.isEmpty else {
            return .noBuild(name: "Node.js", line: line ?? "")
        }
        if release.isInstalled {
            return .installed(name: "Node.js \(release.major)", pane: "Node")
        }
        if installing.contains(release.major) {
            return .installing(name: "Node.js \(release.major)")
        }
        return .install(Install(service: "node", name: "Node.js", line: release.major, label: release.major, version: release.latest))
    }

    /// The newest of `items` by a version number ("8.10" is newer than "8.9").
    private static func newest<T>(_ items: [T], by version: KeyPath<T, String>) -> T? {
        items.max { $0[keyPath: version].compare($1[keyPath: version], options: .numeric) == .orderedAscending }
    }

    public enum Resolution: Equatable, Sendable {
        /// Can be installed once the user confirms.
        case install(Install)
        /// Already installed: nothing to do. `pane` is the Settings pane that lists it.
        case installed(name: String, pane: String = "Services")
        /// An install of it already runs: nothing to do.
        case installing(name: String)
        /// The service doesn't run on this platform.
        case unavailable(name: String, reason: String)
        /// This Flock doesn't know the service.
        case unknown(name: String)
        /// This Flock doesn't offer that version line.
        case unknownVersion(name: String, line: String)
        /// This Flock has no build of that PHP or Node.js version for this computer, or doesn't
        /// know the version yet; `line` is empty when the link named none and none is offered.
        case noBuild(name: String, line: String)
    }

    public struct Install: Equatable, Sendable {
        public var service: String
        public var name: String
        /// The line to pass to the daemon ("8.4", "18").
        public var line: String
        /// How the line reads ("8.4 LTS").
        public var label: String
        /// The release that gets installed; empty when not known yet.
        public var version: String
    }
}

extension InstallLink.Resolution {
    public var title: String {
        switch self {
        case .install(let install): String(localized: "links.install.install_title \(install.name) \(install.label)")
        case .installed(let name, _): String(localized: "links.install.already_installed_title \(name)")
        case .installing(let name): String(localized: "links.install.installing_title \(name)")
        case .unavailable(let name, _): String(localized: "links.install.unavailable_title \(name)")
        case .unknown(let name): String(localized: "links.install.unknown_title \(name)")
        case .unknownVersion(let name, let line): String(localized: "links.install.flock_doesnt_offer_name_version \(name) \(line)")
        case .noBuild(let name, let line): line.isEmpty ? String(localized: "links.install.unknown_title \(name)") : String(localized: "links.install.flock_doesnt_offer_name_version \(name) \(line)")
        }
    }

    public var message: String {
        switch self {
        case .install(let install):
            let what = install.version.isEmpty ? "\(install.name) \(install.label)" : "\(install.name) \(install.version)"
            switch install.service {
            case "php": return String(localized: "links.install.link_asked_flock_to_install_sites \(what)")
            case "node": return String(localized: "links.install.link_asked_flock_to_install_projects \(what)")
            default: return String(localized: "links.install.link_asked_flock_to_install \(what)")
            }
        case .installed(_, let pane):
            // The Settings pane by its name in the app's language; PHP and Node read the same in all.
            let name = pane == "Services" ? String(localized: "settings.pane.services") : pane
            return String(localized: "links.install.already_installed_message \(name)")
        case .installing:
            return String(localized: "links.install.its_ready_in_a_moment")
        case .unavailable(_, let reason):
            return reason.isEmpty ? String(localized: "links.install.unavailable_message") : reason
        case .unknown, .unknownVersion:
            return String(localized: "links.install.newer_flock")
        case .noBuild:
            return String(localized: "links.install.no_build")
        }
    }

    /// The confirmation, when there is something to install.
    public var confirmation: InstallLink.Install? {
        if case .install(let install) = self { install } else { nil }
    }
}
