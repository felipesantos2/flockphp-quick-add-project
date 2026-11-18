import FlockKit
import SwiftUI

/// Node.js majors from the package manifest, like the PHP pane: install, update, make default and uninstall
/// them, and update their npm. Exact versions a project pins (.nvmrc) are offered by `node` in
/// the terminal instead.
struct NodePane: View {
    @Environment(AppModel.self) private var model
    private let checkingState = State<Bool>(initialValue: false)
    private var checking: Bool {
        get { checkingState.wrappedValue }
        nonmutating set { checkingState.wrappedValue = newValue }
    }

    var body: some View {
        let store = model.settings

        VStack(spacing: 0) {
            PaneHeader(title: "common.node", subtitle: "node.subtitle")
            Form {
                if let catalog = store.node {
                    Section {
                        if catalog.versions.isEmpty {
                            Text("node.no_versions").foregroundStyle(.secondary)
                        }
                        ForEach(catalog.versions) { release in
                            NodeReleaseRow(release: release, installing: installing.contains(release.major))
                        }
                    } header: {
                        HStack {
                            Text("versions.versions")
                            Spacer()
                            checkedLine(catalog)
                        }
                    } footer: {
                        VStack(alignment: .leading, spacing: 6) {
                            if !catalog.checkError.isEmpty {
                                Label("versions.could_not_check_updates \(catalog.checkError)", systemImage: "exclamationmark.triangle.fill")
                                    .foregroundStyle(.orange)
                            }
                            Text("node.official_node_js_builds_the")
                                .foregroundStyle(.secondary)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                        .font(.caption)
                    }
                } else {
                    HStack {
                        ProgressView().controlSize(.small)
                        Text("node.loading_node_js_versions").foregroundStyle(.secondary)
                    }
                }
            }
            .formStyle(.grouped)
            .scrollContentBackground(.hidden)
        }
        // Also reloads when an install the rows didn't start finishes.
        .task(id: installing) { await store.refreshNode() }
        // A flock://install/node link (AppModel.handle): nothing installs until the user confirms here.
        .alert(linkRequest?.title ?? "", isPresented: Binding(get: { linkRequest != nil }, set: { if !$0 { model.pendingInstall = nil } }),
               presenting: linkRequest) { request in
            if let install = request.confirmation {
                Button("common.install") {
                    let major = install.line
                    Task { await store.installNode(major) }
                }
                Button("common.cancel", role: .cancel) {}
            } else {
                Button("common.ok") {}
            }
        } message: { request in
            Text(request.message)
        }
    }

    /// The majors an install runs for that no row started: the first run's default (which the
    /// versions don't show), or one a link asked for.
    private var installing: Set<String> {
        var majors = model.settings.nodePending
        if let node = model.status?.defaults?.node, node.state == .installing, !node.version.isEmpty {
            majors.insert(node.version)
        }
        return majors
    }

    /// What a pending flock://install link for Node.js asks for, once the versions are loaded.
    private var linkRequest: InstallLink.Resolution? {
        guard let link = model.pendingInstall, link.service == "node" else { return nil }
        return link.resolve(node: model.settings.node, installing: installing)
    }

    @ViewBuilder
    private func checkedLine(_ catalog: NodeCatalog) -> some View {
        HStack(spacing: 6) {
            if let checked = catalog.checkedAt {
                Text("node.checked \(checked, format: .relative(presentation: .named))")
            } else {
                Text("versions.not_checked")
            }
            Text(verbatim: "·")
            Button {
                checking = true
                Task {
                    await model.settings.performOnNode { try await $0.checkNodeReleases() }
                    checking = false
                }
            } label: {
                BusyLabel(title: "versions.check_now", busyTitle: "versions.checking", isBusy: checking)
            }
            .buttonStyle(.link)
            .disabled(checking)
        }
        .font(.caption)
        .foregroundStyle(.secondary)
        .textCase(nil)
    }
}

/// One Node.js line: name, version, badges, npm, state and the actions that fit it.
private struct NodeReleaseRow: View {
    @Environment(AppModel.self) private var model
    let release: NodeRelease
    /// An install the row didn't start runs for this major (see NodePane.installing).
    let installing: Bool

    // What the row is doing while the daemon works (installs answer when they're done).
    private let busyState = State<String?>(initialValue: nil)
    private var busy: String? {
        get { busyState.wrappedValue }
        nonmutating set { busyState.wrappedValue = newValue }
    }

    /// The row's own work, or an install it didn't start, which shows the same way.
    private var step: String? {
        busy ?? (installing ? String(localized: "versions.downloading_node \(release.latest)") : nil)
    }
    private let confirmUninstallState = State<Bool>(initialValue: false)
    private var confirmUninstall: Bool {
        get { confirmUninstallState.wrappedValue }
        nonmutating set { confirmUninstallState.wrappedValue = newValue }
    }

    var body: some View {
        HStack(alignment: .center, spacing: 10) {
            SettingsIcon(symbol: "hexagon.fill", color: release.isInstalled ? .green : .gray, size: 26)

            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text("common.node_version \(release.major)").font(.body.weight(.semibold)).fixedSize()
                    Text(versionText).font(.callout.monospacedDigit()).foregroundStyle(.secondary).fixedSize()
                    if release.isDefault { Badge(text: "sites.default") }
                }
                detail
            }

            Spacer(minLength: 12)
            // The buttons keep their whole titles (longer in Czech and Spanish).
            actions
                .fixedSize()
                .disabled(step != nil)
        }
        .padding(.vertical, 3)
        .confirmationDialog("node.uninstall_node_version \(release.major)", isPresented: confirmUninstallState.projectedValue) {
            Button("common.uninstall", role: .destructive) {
                run(String(localized: "common.uninstalling")) { try await $0.uninstallNode(release.major) }
            }
        } message: {
            Text("node.uninstall_details \(release.major)")
        }
    }

    /// The installed version; an update's version is on its Update to button.
    private var versionText: String {
        release.isInstalled ? release.installed : release.latest
    }

    /// The status line under the name.
    @ViewBuilder
    private var detail: some View {
        if let step {
            HStack(spacing: 6) {
                ProgressView().controlSize(.mini)
                Text(step)
            }
            .font(.caption).foregroundStyle(.secondary)
        } else {
            // Versions never wrap; a newer npm gets a line of its own, as the row has no room left
            // next to Update npm and Make Default in Czech and Spanish.
            VStack(alignment: .leading, spacing: 1) {
                HStack(spacing: 0) {
                    Text(release.update ? "versions.update_available" : (release.isInstalled ? "versions.up_to_date" : "common.not_installed"))
                        .foregroundStyle(release.update ? Color.blue : Color.secondary)
                    if !release.npm.isEmpty {
                        Text(verbatim: " · npm \(release.npm)").foregroundStyle(.secondary)
                    }
                }
                if !release.npm.isEmpty, !release.npmLatest.isEmpty {
                    Text("versions.npm_available \(release.npmLatest)").foregroundStyle(.blue)
                }
            }
            .font(.caption)
            .fixedSize()
        }
    }

    @ViewBuilder
    private var actions: some View {
        if !release.isInstalled {
            Button {
                run(String(localized: "versions.downloading_node \(release.latest)")) { try await $0.installNodeMajor(release.major) }
            } label: {
                Label("common.install", systemImage: "arrow.down.circle")
            }
            .help("node.downloads_node_js \(release.latest)")
        } else {
            if release.update {
                Button {
                    run(String(localized: "versions.downloading_node \(release.latest)")) { try await $0.installNodeMajor(release.major) }
                } label: {
                    Label("general.update_to \(release.latest)", systemImage: "arrow.down.circle")
                }
                .buttonStyle(.borderedProminent)
                .help("node.downloads_node_js_new_then \(release.latest) \(release.installed)")
            }
            if !release.npmLatest.isEmpty {
                Button("node.update_npm") {
                    run(String(localized: "node.updating_npm_to_version \(release.npmLatest)")) { try await $0.updateNpm(release.major) }
                }
                .help("node.installs_npm_the_newest_npm \(release.npmLatest) \(release.installed) \(release.major)")
            }
            if !release.isDefault {
                Button("node.make_default") {
                    busy = String(localized: "node.making_node_version_the_default \(release.major)")
                    Task {
                        await model.settings.perform { _ = try await $0.updateSettings(SettingsChange(defaultNode: release.major)) }
                        await model.settings.refreshNode()
                        busy = nil
                    }
                }
            }
            Menu {
                Button("common.uninstall_ellipsis", role: .destructive) { confirmUninstall = true }
                    .disabled(release.isDefault)
            } label: {
                Image(systemName: "ellipsis.circle")
            }
            .menuStyle(.borderlessButton)
            .menuIndicator(.hidden)
            .fixedSize()
            .help("services.more")
        }
    }

    private func run(_ step: String, _ action: @escaping @Sendable (FlockClient) async throws -> NodeCatalog) {
        busy = step
        Task {
            await model.settings.performOnNode(action)
            busy = nil
        }
    }
}
