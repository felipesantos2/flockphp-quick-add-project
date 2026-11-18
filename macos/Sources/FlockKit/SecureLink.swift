import Foundation

/// A link from the page Flock serves for a linked site that isn't secured yet, asking to serve the
/// site over HTTPS: `flock://secure/<site>`, e.g. `flock://secure/watchtower`.
///
/// Any web page can open such a link, so like an `InstallLink` it changes nothing by itself: the
/// app asks the user to confirm (see `resolve`).
public struct SecureLink: Equatable, Sendable {
    /// The site's name ("watchtower").
    public var site: String

    public init(site: String) {
        self.site = site
    }

    public init?(url: URL) {
        guard url.scheme?.lowercased() == "flock", url.host?.lowercased() == "secure" else { return nil }
        let parts = url.path.split(separator: "/", omittingEmptySubsequences: true).map(String.init)
        guard parts.count == 1, Self.isSafe(parts[0]) else { return nil }
        site = parts[0].lowercased()
    }

    /// Letters, digits, dots and dashes only, so nothing odd ends up in a dialog or a request.
    private static func isSafe(_ part: String) -> Bool {
        !part.isEmpty && part.count <= 63
            && part.unicodeScalars.allSatisfy { CharacterSet.alphanumerics.contains($0) && $0.isASCII || $0 == "." || $0 == "-" }
    }

    /// What the link asks for, measured against the linked sites. Nil while they are loading.
    public func resolve(sites: [Status.Site]?) -> Resolution? {
        guard let sites else { return nil }
        guard let site = sites.first(where: { $0.name.lowercased() == self.site }) else {
            return .unknown(name: self.site)
        }
        return site.secured ? .secured(name: site.name, domain: site.domain) : .secure(name: site.name, domain: site.domain)
    }

    public enum Resolution: Equatable, Sendable {
        /// Can be secured once the user confirms.
        case secure(name: String, domain: String)
        /// Already served over HTTPS: nothing to do.
        case secured(name: String, domain: String)
        /// No site has that name.
        case unknown(name: String)
    }
}

extension SecureLink.Resolution {
    public var title: String {
        switch self {
        case .secure(let name, _): String(localized: "links.secure.secure_title \(name)")
        case .secured(let name, _): String(localized: "links.secure.already_secured_title \(name)")
        case .unknown(let name): String(localized: "links.secure.unknown_title \(name)")
        }
    }

    public var message: String {
        switch self {
        case .secure(_, let domain): String(localized: "links.secure.secure_message \(domain)")
        case .secured(_, let domain): String(localized: "links.secure.already_secured_message \(domain)")
        case .unknown: String(localized: "links.secure.unknown_message")
        }
    }

    /// The site to secure once the user confirms, when there is one.
    public var confirmation: String? {
        if case .secure(let name, _) = self { name } else { nil }
    }
}

/// Any flock:// link the app handles.
public enum FlockLink: Equatable, Sendable {
    case install(InstallLink)
    case secure(SecureLink)
    /// `flock://update`, from `flock update`: check for a new version of the app.
    case update
    /// `flock://site/<name>`: show a site (or worktree) in Settings → Sites, e.g. from a notification.
    case site(String)

    public init?(url: URL) {
        if let link = InstallLink(url: url) {
            self = .install(link)
        } else if let link = SecureLink(url: url) {
            self = .secure(link)
        } else if url.scheme?.lowercased() == "flock", url.host?.lowercased() == "update", url.path.trimmingCharacters(in: CharacterSet(charactersIn: "/")).isEmpty {
            self = .update
        } else if url.scheme?.lowercased() == "flock", url.host?.lowercased() == "site",
                  case let name = url.path.trimmingCharacters(in: CharacterSet(charactersIn: "/")), !name.isEmpty, !name.contains("/") {
            self = .site(name)
        } else {
            return nil
        }
    }
}
