import AppKit
import FlockKit
import SwiftUI

/// PostgreSQL: install a major, see the one that runs, switch between installed majors (each
/// keeps its own databases), update and uninstall, and pick each major's extensions. The
/// command-line tools come with every major.
struct PostgresDetail: View {
    @Environment(AppModel.self) private var model
    let service: Service

    private let switchState = State<PostgresMajor?>(initialValue: nil)
    private let uninstallState = State<PostgresMajor?>(initialValue: nil)
    private let extensionsState = State<PostgresMajor?>(initialValue: nil)
    private let installWithState = State<PostgresMajor?>(initialValue: nil)

    var body: some View {
        let store = model.settings

        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                if let versions = store.postgres {
                    DetailHeader(title: "PostgreSQL", symbol: "cylinder.split.1x2.fill", color: .blue,
                                 status: headerStatus(versions), running: versions.hasInstalled ? versions.running : nil)

                    if versions.hasInstalled {
                        if let active = versions.activeMajor {
                            ActiveServerCard(versions: versions, major: active, service: service, locked: locked)
                        }
                        versionList(versions)
                    } else {
                        EmptyPostgres(versions: versions, locked: locked)
                    }

                    commandLineTools

                    if versions.hasInstalled {
                        Divider()
                        ServiceInfo(service: service)
                    }
                } else {
                    DetailHeader(title: "PostgreSQL", symbol: "cylinder.split.1x2.fill", color: .blue, status: String(localized: "common.loading"))
                    ProgressView().frame(maxWidth: .infinity).padding(.top, 30)
                }
            }
            .padding(16)
        }
        .alert(item: switchState.projectedValue) { target in
            switchAlert(target)
        }
        .sheet(item: uninstallState.projectedValue) { major in
            UninstallPostgresSheet(major: major, versions: store.postgres) { deleteData in
                let id = major.major
                Task { await store.performOnPostgres(id, .uninstalling) { try await $0.uninstallPostgres(id, deleteData: deleteData) } }
            }
        }
        .sheet(item: extensionsState.projectedValue) { major in
            PostgresExtensionsSheet(major: major.major)
        }
        .alert("services.postgres.also_install_the_extensions_of \(store.postgres?.active ?? "")",
               isPresented: Binding(get: { installWithState.wrappedValue != nil }, set: { if !$0 { installWithState.wrappedValue = nil } }),
               presenting: installWithState.wrappedValue) { target in
            let id = target.major
            let extensions = store.postgres?.activeMajor?.extensions ?? []
            Button("services.postgres.install_with_extensions") {
                Task { await store.performOnPostgres(id, .installing) { try await $0.installPostgres(id, extensions: extensions) } }
            }
            .keyboardShortcut(.defaultAction)
            Button("services.postgres.postgresql_only") {
                Task { await store.performOnPostgres(id, .installing) { try await $0.installPostgres(id) } }
            }
            Button("common.cancel", role: .cancel) {}
        } message: { target in
            let names = PostgresExtensions.titles(store.postgres?.activeMajor?.extensions ?? [])
            Text("services.postgres.extensions_for_postgresql_version_where \(names) \(target.major)")
        }
    }

    /// Installs another major; when the active one has extensions, asks whether it gets them too.
    private func install(_ major: PostgresMajor) {
        if model.settings.postgres?.activeMajor?.extensions.isEmpty == false {
            installWithState.wrappedValue = major
            return
        }
        let id = major.major
        Task { await model.settings.performOnPostgres(id, .installing) { try await $0.installPostgres(id) } }
    }

    /// Something runs on a major: other PostgreSQL actions wait.
    private var locked: Bool {
        model.settings.postgres?.isBusy == true || !model.settings.postgresPending.isEmpty
    }

    private func headerStatus(_ versions: PostgresVersions) -> String {
        if let busy = versions.majors.first(where: { model.settings.busy($0) != .idle }) {
            return model.settings.busy(busy).describe(busy.major)
        }
        guard let active = versions.activeMajor else {
            return versions.hasInstalled ? String(localized: "services.postgres.no_version_selected") : String(localized: "common.not_installed")
        }
        let state = versions.running ? String(localized: "sites.proxy.running") : (versions.enabled ? String(localized: "sites.proxy.not_running") : String(localized: "services.postgres.stopped"))
        return "\(state) · " + String(localized: "services.postgres.postgresql_on_port \(active.major) \(String(versions.port))")
    }

    // MARK: Versions

    private func versionList(_ versions: PostgresVersions) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            DetailSectionTitle(title: "versions.versions") {
                CheckForUpdatesLine(checkedAt: versions.checkedAt, error: versions.checkError, disabled: locked) {
                    await model.settings.checkForUpdates()
                }
            }

            VStack(spacing: 0) {
                ForEach(Array(versions.majors.enumerated()), id: \.element.id) { index, major in
                    if index > 0 {
                        Divider().padding(.leading, 46)
                    }
                    PostgresMajorRow(major: major, busy: model.settings.busy(major), locked: locked,
                                     activeMajor: versions.active) {
                        switchState.wrappedValue = major
                    } uninstall: {
                        uninstallState.wrappedValue = major
                    } extensions: {
                        extensionsState.wrappedValue = major
                    } install: {
                        install(major)
                    }
                }
            }
            .card(padding: 0)

            if !versions.checkError.isEmpty {
                Label("services.postgres.couldnt_check_for_new_releases \(versions.checkError)", systemImage: "exclamationmark.triangle")
                    .font(.caption).foregroundStyle(.secondary)
                    .lineLimit(2)
                    .help(versions.checkError)
            }
            Text("services.postgres.side_by_side")
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    private func switchAlert(_ target: PostgresMajor) -> Alert {
        let current = model.settings.postgres?.active ?? ""
        let own = target.hasData
            ? String(localized: "services.postgres.postgresql_version_uses_its_own \(target.major)")
            : String(localized: "services.postgres.postgresql_version_starts_with_a \(target.major)")
        let message = current.isEmpty
            ? own
            : String(localized: "services.postgres.postgresql_stops_its_databases_stay \(current)") + " " + own
        let id = target.major
        return Alert(
            title: Text("services.postgres.switch_to_postgresql_version \(target.major)"),
            message: Text(message),
            primaryButton: .default(Text("worktrees.switch")) {
                Task { await model.settings.performOnPostgres(id, .switching) { try await $0.activatePostgres(id) } }
            },
            secondaryButton: .cancel()
        )
    }

    // MARK: Command-line tools

    private var commandLineTools: some View {
        Label("services.postgres.psql_pg_dump_and_the",
              systemImage: "terminal")
            .font(.caption).foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
    }
}

extension PostgresMajor.Busy {
    /// "Installing PostgreSQL 17…"
    func describe(_ major: String) -> String {
        switch self {
        case .idle: ""
        case .installing: String(localized: "services.postgres.installing_postgresql_version \(major)")
        case .updating: String(localized: "services.postgres.updating_postgresql_version \(major)")
        case .switching: String(localized: "services.postgres.switching_to_postgresql_version \(major)")
        case .uninstalling: String(localized: "services.postgres.uninstalling_postgresql_version \(major)")
        case let .other(raw): "\(raw.prefix(1).uppercased())\(raw.dropFirst()) PostgreSQL \(major)…"
        }
    }

    /// "Installing…", for a row.
    var short: String {
        switch self {
        case .idle: ""
        case .installing: String(localized: "common.installing")
        case .updating: String(localized: "common.updating")
        case .switching: String(localized: "common.switching")
        case .uninstalling: String(localized: "common.uninstalling")
        case let .other(raw): "\(raw.prefix(1).uppercased())\(raw.dropFirst())…"
        }
    }
}

/// The version that runs: state, version, port, data size and its update.
private struct ActiveServerCard: View {
    @Environment(AppModel.self) private var model
    let versions: PostgresVersions
    let major: PostgresMajor
    let service: Service
    let locked: Bool

    var body: some View {
        let busy = model.settings.busy(major)

        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text("common.postgresql_version \(major.major)").font(.title2.weight(.semibold))
                Text(major.installed).font(.title3.monospacedDigit()).foregroundStyle(.secondary)
                Spacer()
                statePill(busy)
            }

            fact("services.postgres.databases", major.hasData ? Formats.bytes(major.dataSize) : String(localized: "services.postgres.none_yet"))

            if !major.yanked.isEmpty {
                Label(major.update ? "services.postgres.this_build_was_withdrawn_update \(major.yanked)" : "services.postgres.withdrawn \(major.yanked)",
                      systemImage: "exclamationmark.triangle.fill")
                    .font(.callout).foregroundStyle(.orange)
                    .help("services.postgres.withdrawn_tooltip")
            }

            if major.update, !major.latest.isEmpty {
                Divider()
                UpdateStrip(title: major.latest == major.installed ? String(localized: "services.postgres.new_build_of_postgresql_is \(major.latest)") : String(localized: "services.postgres.postgresql_is_available \(major.latest)"),
                            note: String(localized: "services.postgres.your_databases_stay_postgresql_restarts"),
                            help: String(localized: "services.postgres.installs_and_restarts_postgresql_your \(major.latest) \(major.major)"),
                            busy: busy == .updating, disabled: locked) {
                    let id = major.major
                    Task { await model.settings.performOnPostgres(id, .updating) { try await $0.updatePostgres(id) } }
                }
            }
        }
        .card(padding: 14)
    }

    @ViewBuilder
    private func statePill(_ busy: PostgresMajor.Busy) -> some View {
        let switching = versions.majors.contains { model.settings.busy($0) == .switching }
        if busy != .idle || switching {
            HStack(spacing: 5) {
                ProgressView().controlSize(.mini)
                Text(switching ? String(localized: "common.switching") : busy.short)
            }
            .font(.caption.weight(.medium)).foregroundStyle(.secondary)
        } else {
            Badge(text: "services.active")
        }
    }

    private func fact(_ title: LocalizedStringKey, _ value: String) -> some View {
        HStack(spacing: 6) {
            Text(title).foregroundStyle(.secondary)
            Text(value).monospacedDigit()
        }
        .font(.callout)
    }
}

/// One major in the version list.
private struct PostgresMajorRow: View {
    @Environment(AppModel.self) private var model
    let major: PostgresMajor
    let busy: PostgresMajor.Busy
    let locked: Bool
    let activeMajor: String
    let switchTo: () -> Void
    let uninstall: () -> Void
    let extensions: () -> Void
    let install: () -> Void

    var body: some View {
        HStack(spacing: 10) {
            Text(major.major)
                .font(.system(.title3, design: .rounded).weight(.semibold).monospacedDigit())
                .foregroundStyle(major.isInstalled ? .primary : .tertiary)
                .frame(width: 26, alignment: .center)

            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    if major.isInstalled {
                        Text(major.installed).monospacedDigit()
                        if major.update, !major.latest.isEmpty {
                            Text(major.latest == major.installed ? "services.postgres.to_new_build" : "common.to_version \(major.latest)").monospacedDigit().foregroundStyle(Color.accentColor)
                                .help(major.latest == major.installed ? "services.postgres.new_build_of_postgresql_is \(major.latest)" : "services.postgres.postgresql_is_available \(major.latest)")
                        }
                        if !major.yanked.isEmpty {
                            Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                                .help("services.postgres.this_build_was_withdrawn_flock \(major.yanked)")
                        }
                    } else {
                        Text("common.not_installed").foregroundStyle(.secondary)
                    }
                    if major.prerelease {
                        Badge(text: "versions.prerelease", color: .orange)
                            .help("services.postgres.postgresql_isnt_released_yet_it \(major.latest)")
                    }
                    if major.active, major.isInstalled {
                        Badge(text: "services.active")
                    }
                }
                .font(.callout)
                caption
            }

            Spacer(minLength: 6)

            if busy != .idle {
                HStack(spacing: 6) {
                    ProgressView().controlSize(.small)
                    Text(busy.short).font(.caption).foregroundStyle(.secondary)
                }
            } else {
                actions
            }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
    }

    @ViewBuilder
    private var caption: some View {
        if major.isInstalled && !major.extensions.isEmpty {
            Text(PostgresExtensions.titles(major.extensions))
                .font(.caption)
                .lineLimit(1)
                .foregroundStyle(.secondary)
                .help("services.postgres.extensions_installed_for_postgresql \(major.major)")
        } else if !major.isInstalled && major.hasData {
            Text("services.postgres.kept \(Formats.bytes(major.dataSize))")
                .font(.caption)
                .lineLimit(1)
                .foregroundStyle(.tertiary)
                .help("services.postgres.databases_of_postgresql_are_kept \(major.major)")
        }
    }

    @ViewBuilder
    private var actions: some View {
        let store = model.settings
        if !major.isInstalled {
            Button("common.install", action: install)
            .controlSize(.small)
            .disabled(locked)
            .help(major.latest.isEmpty ? "services.postgres.downloads_the_newest_postgresql \(major.major)" : "services.postgres.downloads_postgresql \(major.latest)")
        } else {
            HStack(spacing: 4) {
                // The active major's update is in the card above.
                if major.update, !major.latest.isEmpty, !major.active {
                    // "Update": the row shows the version it goes to ("17.10 → 17.11").
                    Button("common.update") {
                        let id = major.major
                        Task { await store.performOnPostgres(id, .updating) { try await $0.updatePostgres(id) } }
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.small)
                    .disabled(locked)
                    .help("services.postgres.update_tooltip \(major.latest) \(major.installed)")
                }
                if !major.active {
                    Button("services.switch_ellipsis", action: switchTo)
                        .controlSize(.small)
                        .disabled(locked)
                        .help("services.postgres.stops_postgresql_and_runs_with \(activeMajor) \(major.major)")
                }
                // An icon, like ⋯: with Update and Switch, a third labelled button doesn't fit.
                Button(action: extensions) {
                    Image(systemName: "puzzlepiece.extension")
                }
                .buttonStyle(.borderless)
                .accessibilityLabel("services.postgres.extensions")
                .help("services.postgres.extensions_of_postgresql_version_postgis \(major.major)")
                Menu {
                    if major.update, !major.latest.isEmpty {
                        Button("general.update_to \(major.latest)") {
                            let id = major.major
                            Task { await store.performOnPostgres(id, .updating) { try await $0.updatePostgres(id) } }
                        }
                        .help("services.postgres.your_databases_stay")
                    }
                    if !major.active {
                        Button("services.postgres.switch_to_postgresql \(major.major)", action: switchTo)
                    }
                    if major.update || !major.active {
                        Divider()
                    }
                    Button("services.postgres.uninstall_postgresql \(major.major)", role: .destructive, action: uninstall)
                } label: {
                    Image(systemName: "ellipsis.circle")
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
                .help("services.more")
                .disabled(locked)
            }
        }
    }
}

/// The first PostgreSQL: the version and port, and Install, as for the catalog services.
private struct EmptyPostgres: View {
    @Environment(AppModel.self) private var model
    let versions: PostgresVersions
    let locked: Bool

    private let majorState = State<String?>(initialValue: nil)
    private let portState = State<String?>(initialValue: nil)

    var body: some View {
        let store = model.settings
        let installing = versions.majors.first { store.busy($0) != .idle }
        let kept = versions.majors.filter(\.hasData)

        if let installing {
            InstallInvitation(symbol: "cylinder.split.1x2.fill", color: .blue, title: String(localized: "services.postgres.installing_postgresql \(installing.major)"),
                              message: String(localized: "services.postgres.downloading_postgresql_and_creating_its \(installing.latest.isEmpty ? installing.major : installing.latest)")) {
                ProgressView().progressViewStyle(.linear).frame(maxWidth: 260)
            } footer: {
                Text("common.it_keeps_going_when_you")
            }
        } else {
            InstallInvitation(symbol: "cylinder.split.1x2.fill", color: .blue, title: String(localized: "services.postgres.install_postgresql"),
                              message: String(localized: "services.postgres.database_for_your_laravel_apps")) {
                ServiceInstallForm(
                    name: "PostgreSQL",
                    options: versions.majors.map { ServiceInstallForm.Option(id: $0.major, label: optionLabel($0), latest: $0.latest) },
                    version: Binding(get: { selectedMajor }, set: { majorState.wrappedValue = $0 }),
                    portText: Binding(get: { portText }, set: { portState.wrappedValue = $0 }),
                    note: nil,
                    busy: locked,
                    install: install
                )
            } footer: {
                if !kept.isEmpty {
                    Text("services.postgres.databases_of_postgresql_are_still \(kept.map(\.major).joined(separator: ", "))")
                }
            }
        }
    }

    private var selectedMajor: String? { majorState.wrappedValue ?? versions.majors.first?.major }

    private var portText: String { portState.wrappedValue ?? String(versions.port) }

    /// "17 (17.6)", or "17 (17.6) — your databases" when its databases are kept.
    private func optionLabel(_ major: PostgresMajor) -> String {
        let label = major.latest.isEmpty ? major.major : "\(major.major) (\(major.latest))"
        return major.hasData ? String(localized: "services.postgres.your_databases \(label)") : label
    }

    /// The port belongs to PostgreSQL, not to a version, so it is set before the install.
    private func install() {
        guard let major = selectedMajor, let port = ServiceInstallForm.port(portText) else { return }
        let store = model.settings
        let current = versions.port
        Task {
            if port != current {
                await store.performOnServices { try await $0.setServicePort("postgres", port: port) }
                guard store.error == nil else { return }
            }
            await store.performOnPostgres(major, .installing) { try await $0.installPostgres(major) }
        }
    }
}

/// Confirms uninstalling a major, optionally with its databases.
private struct UninstallPostgresSheet: View {
    let major: PostgresMajor
    let versions: PostgresVersions?
    let uninstall: (_ deleteData: Bool) -> Void
    @Environment(\.dismiss) private var dismiss
    private let deleteDataState = State<Bool>(initialValue: false)

    var body: some View {
        let deleteData = deleteDataState.wrappedValue

        VStack(alignment: .leading, spacing: 14) {
            HStack(alignment: .top, spacing: 12) {
                SettingsIcon(symbol: "cylinder.split.1x2.fill", color: .blue, size: 40)
                VStack(alignment: .leading, spacing: 6) {
                    Text("services.postgres.uninstall_postgresql_version \(major.major)").font(.headline)
                    Text(message)
                        .font(.callout).foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }

            if major.hasData {
                VStack(alignment: .leading, spacing: 4) {
                    Toggle("services.postgres.also_delete_databases \(Formats.bytes(major.dataSize))", isOn: deleteDataState.projectedValue)
                        .toggleStyle(.checkbox)
                    Text(deleteData ? "services.postgres.every_database_of_postgresql_is \(major.major)"
                                    : "services.postgres.its_databases_stay_so_installing \(major.major)")
                        .font(.caption)
                        .foregroundStyle(deleteData ? AnyShapeStyle(Color.red) : AnyShapeStyle(.secondary))
                        .padding(.leading, 20)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .padding(.leading, 52)
            }

            HStack {
                Spacer()
                Button("common.cancel", role: .cancel) { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button(deleteData ? "services.postgres.uninstall_and_delete" : "common.uninstall", role: .destructive) {
                    uninstall(deleteData)
                    dismiss()
                }
                // Deleting databases needs a deliberate click, not Return.
                .keyboardShortcut(deleteData ? nil : .defaultAction)
                .tint(deleteData ? .red : nil)
            }
        }
        .padding(20)
        .frame(width: 440)
    }

    private var message: String {
        guard major.active else {
            return String(localized: "services.postgres.removes_the_postgresql_version_server \(major.installed)")
        }
        let next = versions?.majors.first { $0.isInstalled && $0.major != major.major }
        if let next {
            return String(localized: "services.postgres.postgresql_version_stops_and_flock \(major.major) \(next.major)")
        }
        return String(localized: "services.postgres.postgresql_version_stops_and_your \(major.major)")
    }
}
