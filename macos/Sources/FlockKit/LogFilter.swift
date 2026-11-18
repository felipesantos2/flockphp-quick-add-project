import Foundation

/// The Logs window's level filter, over the levels laravellog parses (Monolog's, lower-cased).
public enum LogLevelFilter: String, CaseIterable, Identifiable, Sendable {
    case all, error, warning, info

    public var id: String { rawValue }

    public var title: String {
        switch self {
        case .all: String(localized: "services.all")
        case .error: String(localized: "logs.error_and_above")
        case .warning: String(localized: "logs.warning_and_above")
        case .info: String(localized: "logs.info_and_above")
        }
    }

    /// Monolog's levels, least severe first.
    static let severity = ["debug", "info", "notice", "warning", "error", "critical", "alert", "emergency"]

    /// Whether an entry of this level passes. Levels Flock doesn't know count as errors, so
    /// nothing unusual is hidden by a filter.
    public func includes(_ level: String) -> Bool {
        guard self != .all else { return true }
        let minimum = Self.severity.firstIndex(of: rawValue) ?? 0
        let rank = Self.severity.firstIndex(of: level.lowercased()) ?? Self.severity.firstIndex(of: "error")!
        return rank >= minimum
    }
}

public enum LogFilter {
    /// Entries of at least the level whose message or details contain the query (case-insensitive).
    public static func entries(_ entries: [LogEntry], level: LogLevelFilter, query: String) -> [LogEntry] {
        let query = query.trimmingCharacters(in: .whitespaces)
        return entries.filter { entry in
            level.includes(entry.level)
                && (query.isEmpty || entry.message.localizedCaseInsensitiveContains(query) || entry.details.localizedCaseInsensitiveContains(query))
        }
    }

    /// The lines of a service log that contain the query (case-insensitive); all of them without one.
    public static func lines(_ text: String, query: String) -> String {
        let query = query.trimmingCharacters(in: .whitespaces)
        guard !query.isEmpty else { return text }
        return text.split(separator: "\n", omittingEmptySubsequences: false)
            .filter { $0.localizedCaseInsensitiveContains(query) }
            .joined(separator: "\n")
    }
}
