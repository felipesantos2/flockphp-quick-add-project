import AppKit
import FlockKit
import SwiftUI

/// The extensions of one PostgreSQL major: Flock's own (each a download of its own) and the folder
/// for yours. Installed per major, since upstream doesn't build every extension for every major.
struct PostgresExtensionsSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let major: String

    private let stateState = State<PostgresExtensions?>(initialValue: nil)
    private let errorState = State<String?>(initialValue: nil)
    private let pendingState = State<[String: String]>(initialValue: [:])

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(alignment: .top, spacing: 12) {
                SettingsIcon(symbol: "puzzlepiece.extension.fill", color: .blue, size: 40)
                VStack(alignment: .leading, spacing: 4) {
                    Text("services.postgres.postgresql_version_extensions \(major)").font(.headline)
                    Text("services.postgres.after_installing_one_run_create")
                        .font(.callout).foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }

            if let state = stateState.wrappedValue {
                VStack(spacing: 0) {
                    ForEach(Array(state.extensions.enumerated()), id: \.element.id) { index, ext in
                        if index > 0 {
                            Divider().padding(.leading, 10)
                        }
                        row(ext)
                    }
                }
                .card(padding: 0)

                customFolder(state)
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.vertical, 30)
            }

            if let error = errorState.wrappedValue {
                Label(error, systemImage: "exclamationmark.triangle")
                    .font(.caption).foregroundStyle(.red)
                    .fixedSize(horizontal: false, vertical: true)
            }

            HStack {
                Spacer()
                Button("common.done") { dismiss() }
                    .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 520)
        .task {
            // What the daemon has at once; then the newest manifest (it fetches only hourly), so a
            // package published meanwhile doesn't show as "not available".
            await load()
            await model.settings.checkForUpdates()
            await load()
        }
        .task(id: isBusy) {
            // Follows operations (also started elsewhere: the CLI, another window) every second,
            // and otherwise what is put into the extensions folder.
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(isBusy ? 1 : 3))
                await load()
            }
        }
    }

    private var isBusy: Bool { stateState.wrappedValue?.isBusy == true }

    private func busy(_ ext: PostgresExtension) -> String {
        if !ext.busy.isEmpty { return ext.busy }
        return pendingState.wrappedValue[ext.name] ?? ""
    }

    // MARK: Rows

    private func row(_ ext: PostgresExtension) -> some View {
        HStack(spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(ext.title).fontWeight(.medium)
                    if ext.isInstalled {
                        Text(ext.installed).monospacedDigit().foregroundStyle(.secondary)
                        if ext.update, !ext.latest.isEmpty {
                            Text("common.to_version \(ext.latest)").monospacedDigit().foregroundStyle(Color.accentColor)
                        }
                        if !ext.yanked.isEmpty {
                            Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                                .help("services.postgres.this_build_was_withdrawn_flock \(ext.yanked)")
                        }
                    }
                    if ext.prerelease {
                        Badge(text: "versions.prerelease", color: .orange)
                    }
                }
                .font(.callout)
                Text(caption(ext))
                    .font(.caption).foregroundStyle(.secondary)
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .opacity(ext.supported ? 1 : 0.5)

            Spacer(minLength: 6)

            let busy = busy(ext)
            if !busy.isEmpty {
                HStack(spacing: 6) {
                    ProgressView().controlSize(.small)
                    Text(Self.describe(busy)).font(.caption).foregroundStyle(.secondary)
                }
            } else {
                actions(ext)
            }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
        .help(ext.license.isEmpty ? "" : String(localized: "services.postgres.license_license \(ext.license)"))
    }

    /// "Installing…" for what the daemon (or this sheet) reports an extension is doing.
    private static func describe(_ busy: String) -> String {
        switch busy {
        case "installing": String(localized: "common.installing")
        case "updating": String(localized: "common.updating")
        case "uninstalling": String(localized: "common.uninstalling")
        case "waiting": String(localized: "services.postgres.waiting")
        default: "\(busy.prefix(1).uppercased())\(busy.dropFirst())…"
        }
    }

    private func caption(_ ext: PostgresExtension) -> String {
        if !ext.supported {
            return String(localized: "services.postgres.not_available_for_postgresql_version \(major)")
        }
        var parts = [ext.description]
        if ext.discontinued {
            parts.append(String(localized: "services.postgres.no_longer_updated_for_postgresql \(major)"))
        }
        if ext.isInstalled {
            parts.append("CREATE EXTENSION \(ext.creates);")
        } else if ext.preload {
            parts.append(String(localized: "services.postgres.postgresql_version_restarts_to_load \(major)"))
        }
        return parts.joined(separator: " · ")
    }

    /// Every row stays usable while another one works: the daemon queues the operations, so several
    /// extensions can be installed in a row (the queued ones are "waiting").
    @ViewBuilder
    private func actions(_ ext: PostgresExtension) -> some View {
        if !ext.isInstalled {
            Button("common.install") { run(ext, "installing") { try await $0.installPostgresExtension(major, ext.name) } }
                .controlSize(.small)
                .disabled(!ext.supported)
                .help(ext.size > 0 ? "services.postgres.downloads_macos \(ext.title) \(ext.latest) \(Formats.bytes(ext.size))" : "services.postgres.downloads \(ext.title)")
        } else {
            HStack(spacing: 4) {
                if ext.update {
                    Button("common.update") { run(ext, "updating") { try await $0.updatePostgresExtension(major, ext.name) } }
                        .buttonStyle(.borderedProminent)
                        .controlSize(.small)
                        .help("services.postgres.installs \(ext.title) \(ext.latest)")
                }
                Menu {
                    Button("services.postgres.uninstall_name \(ext.title)", role: .destructive) {
                        run(ext, "uninstalling") { try await $0.uninstallPostgresExtension(major, ext.name) }
                    }
                    .help("services.postgres.databases_that_created_it_cant")
                } label: {
                    Image(systemName: "ellipsis.circle")
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
                .help("services.more")
            }
        }
    }

    // MARK: Your own

    private func customFolder(_ state: PostgresExtensions) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            if !state.custom.isEmpty {
                VStack(spacing: 0) {
                    ForEach(Array(state.custom.enumerated()), id: \.element.id) { index, ext in
                        if index > 0 {
                            Divider().padding(.leading, 10)
                        }
                        customRow(ext)
                    }
                }
                .card(padding: 0)
            }
            folderHint(state)
        }
    }

    /// One of your extensions: read only, with what to run or what is in the way.
    private func customRow(_ ext: PostgresCustomExtension) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 6) {
                Text(ext.name).fontWeight(.medium)
                if !ext.version.isEmpty {
                    Text(ext.version).monospacedDigit().foregroundStyle(.secondary)
                }
                Badge(text: "services.postgres.yours")
            }
            .font(.callout)
            if ext.linked {
                Text(verbatim: "CREATE EXTENSION \(ext.name);")
                    .font(.caption).foregroundStyle(.secondary)
                    .textSelection(.enabled)
            } else {
                Label(ext.problem, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption).foregroundStyle(.orange)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
    }

    private func folderHint(_ state: PostgresExtensions) -> some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "folder").foregroundStyle(.secondary)
            VStack(alignment: .leading, spacing: 4) {
                Text("services.postgres.your_own_extensions_for_postgresql \(major)")
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Button("services.postgres.open_extensions_folder") {
                    NSWorkspace.shared.open(URL(fileURLWithPath: state.customDir, isDirectory: true))
                }
                .controlSize(.small)
                .disabled(state.customDir.isEmpty)
            }
        }
    }

    // MARK: Loading

    private func load() async {
        switch await model.settings.postgresExtensions(major) {
        case let .success(fresh):
            if fresh != stateState.wrappedValue {
                stateState.wrappedValue = fresh
            }
        case let .failure(error):
            errorState.wrappedValue = error.localizedDescription
        }
    }

    private func run(_ ext: PostgresExtension, _ pending: String, _ change: @escaping @Sendable (FlockClient) async throws -> PostgresExtensions) {
        let name = ext.name
        // Behind another operation it waits; the daemon reports the same once polled.
        let others = isBusy || !pendingState.wrappedValue.isEmpty
        pendingState.wrappedValue[name] = others ? "waiting" : pending
        errorState.wrappedValue = nil
        Task {
            let result = await model.settings.performOnPostgresExtension(change)
            pendingState.wrappedValue[name] = nil
            switch result {
            case let .success(fresh):
                stateState.wrappedValue = fresh
            case let .failure(error):
                errorState.wrappedValue = error.localizedDescription
                await load()
            }
        }
    }
}
