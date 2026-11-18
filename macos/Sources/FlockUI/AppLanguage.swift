import AppKit
import SwiftUI

/// The language of the app's windows, chosen in Settings → General (or the setup). macOS picks the
/// localization at launch from AppleLanguages in the app's own defaults, which is also what System
/// Settings → Language & Region → Applications sets; without it, the user's languages decide. The
/// choice is read back from there, so both stay the same. A change applies when Flock starts again.
struct AppLanguage: Hashable, Identifiable {
    /// "system", or the code of one of appLanguages.
    let rawValue: String

    /// The user's languages decide.
    static let system = AppLanguage(code: "system")

    /// System, then the languages Flock is translated to (Languages.swift, generated from lang/).
    static let allCases = [AppLanguage.system] + appLanguages.map { AppLanguage(code: $0.code) }

    private init(code: String) {
        rawValue = code
    }

    /// One of allCases; nil for a language Flock doesn't have.
    init?(rawValue: String) {
        guard let language = Self.allCases.first(where: { $0.rawValue == rawValue }) else { return nil }
        self = language
    }

    var id: String { rawValue }

    /// The picker's names: each language in itself, so it can be found from any language.
    var name: String {
        if self == .system {
            return String(localized: "general.language_system")
        }
        return appLanguages.first { $0.code == rawValue }?.name ?? rawValue
    }

    /// The app's own defaults; UserDefaults.standard would also answer from NSGlobalDomain.
    private static var domain: String { Bundle.main.bundleIdentifier ?? ProcessInfo.processInfo.processName }

    /// What AppleLanguages in the app's defaults asks for; System without it, or for a language
    /// Flock doesn't have.
    static var selected: AppLanguage {
        let languages = UserDefaults.standard.persistentDomain(forName: domain)?["AppleLanguages"] as? [String]
        guard let first = languages?.first else { return .system }
        // A regional variant Flock has ("pt-BR"), else the language ("de-AT" is "de").
        let code = first.split(whereSeparator: { $0 == "-" || $0 == "_" }).first.map(String.init) ?? first
        let match = AppLanguage(rawValue: first.replacingOccurrences(of: "_", with: "-")) ?? AppLanguage(rawValue: code.lowercased())
        guard let language = match, language != .system else { return .system }
        return language
    }

    static func select(_ language: AppLanguage) {
        let defaults = UserDefaults.standard
        if language == .system {
            defaults.removeObject(forKey: "AppleLanguages")
        } else {
            defaults.set([language.rawValue], forKey: "AppleLanguages")
        }
    }

    /// The language the app runs in since it started.
    static let running = Bundle.main.preferredLocalizations.first ?? "en"

    /// The localization the app would start in with this choice. For System, the app's own
    /// AppleLanguages when it names a language Flock doesn't have (System Settings sets it per app),
    /// else the user's languages (NSGlobalDomain).
    var localization: String {
        guard self == .system else { return rawValue }
        let own = UserDefaults.standard.persistentDomain(forName: Self.domain)?["AppleLanguages"] as? [String]
        let global = UserDefaults.standard.persistentDomain(forName: UserDefaults.globalDomain)?["AppleLanguages"] as? [String] ?? []
        return Bundle.preferredLocalizations(from: Bundle.main.localizations, forPreferences: own ?? global).first ?? "en"
    }

    /// Dates, counts and lists in the language the app runs in, with the user's region and formats.
    /// Locale.current keeps the user's language when AppleLanguages names another one.
    static let locale: Locale = {
        let current = Locale.current
        guard current.language.languageCode?.identifier != running else { return current }
        var components = Locale.Components(locale: current)
        components.languageComponents = Locale.Language.Components(identifier: running)
        return Locale(components: components)
    }()

    /// Starts Flock again once this process has quit, then quits; Flock keeps running when the
    /// relaunch can't be started. The daemon keeps running either way.
    @MainActor
    static func relaunch() throws {
        let bundle = Bundle.main.bundleURL
        let target = bundle.pathExtension == "app" ? bundle.path : Bundle.main.executablePath ?? bundle.path
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/bin/sh")
        // `open` for the app (a launch by hand opens Settings again), the binary for a development build.
        process.arguments = ["-c", "while kill -0 \"$1\" 2>/dev/null; do sleep 0.2; done; if [ -d \"$2\" ]; then open \"$2\"; else \"$2\" & fi",
                             "relaunch", String(ProcessInfo.processInfo.processIdentifier), target]
        try process.run()
        NSApp.terminate(nil)
    }
}

/// The Language picker, and Relaunch Flock once the choice differs from the language running. In
/// Settings → General and in the setup, so it can be changed before Flock runs.
struct LanguagePicker: View {
    private let selectedState = State<AppLanguage>(initialValue: AppLanguage.selected)
    private let errorState = State<String?>(initialValue: nil)

    var body: some View {
        let selected = selectedState.wrappedValue
        Picker("general.language", selection: Binding(get: { selected }, set: {
            AppLanguage.select($0)
            selectedState.wrappedValue = AppLanguage.selected
        })) {
            ForEach(AppLanguage.allCases) { Text($0.name).tag($0) }
        }
        if selected.localization != AppLanguage.running {
            HStack {
                Text("general.language_restart_note")
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Spacer()
                Button("general.relaunch_flock") {
                    do {
                        try AppLanguage.relaunch()
                    } catch {
                        errorState.wrappedValue = error.localizedDescription
                    }
                }
            }
            if let error = errorState.wrappedValue {
                Text(error).font(.caption).foregroundStyle(.orange)
            }
        }
    }
}
