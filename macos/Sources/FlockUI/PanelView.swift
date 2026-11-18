import AppKit
import FlockKit
import SwiftUI

/// The menu bar panel: status, services, sites, toggles and window shortcuts.
struct PanelView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            header

            if let version = model.updater.availableVersion {
                updateRow(version)
            }

            if let message = model.actionError {
                errorBanner(message)
            }

            // Before the setup is done, a daemon that answers is the setup's (it may still fail).
            if let status = model.status, !model.needsSetup {
                if !status.warnings.isEmpty {
                    warnings(status.warnings)
                }
                Divider()
                services(status)
                Divider()
                sites(status.sites, proxies: status.proxies)
                Divider()
                toggles(status)
                Divider()
                shortcuts(status)
            } else {
                notRunning
            }

            Divider()
            footer
        }
        .padding(14)
        .frame(width: 340)
        .onAppear { model.refreshSetup() }
    }

    /// Opens a window and closes the panel, which otherwise stays on top of it.
    private func open(_ id: String) {
        closePanel()
        openWindow.activating(id)
    }

    /// MenuBarExtra has no API to close its panel; while it is used, it is the key window.
    private func closePanel() {
        if let panel = NSApp.keyWindow as? NSPanel {
            panel.close()
        }
    }

    // MARK: Sections

    private var header: some View {
        HStack(spacing: 8) {
            LogoMark(mode: model.logoMark, size: 24)
                // The stopped image is already dimmed.
                .foregroundStyle(model.isRunning && model.isDegraded ? Color.flockEmber : Color.flockMark)
            VStack(alignment: .leading, spacing: 1) {
                Text("common.flock").font(.headline)
                Text(model.headline).font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            if model.isRestarting || model.isUpdating {
                ProgressView().controlSize(.small)
            }
        }
    }

    /// A newer version of Flock; Update opens Sparkle's window to install it.
    private func updateRow(_ version: String) -> some View {
        HStack(spacing: 6) {
            Image(systemName: "arrow.down.circle.fill").foregroundStyle(Color.accentColor)
            Text("menu.update_available \(version)").font(.callout)
            Spacer(minLength: 0)
            Button("common.update") {
                closePanel()
                model.updater.checkForUpdates()
            }
            .controlSize(.small)
        }
        .padding(8)
        .background(Color.accentColor.opacity(0.12), in: RoundedRectangle(cornerRadius: 6))
    }

    /// The last failed action (restart, a toggle) until it is dismissed or an action succeeds.
    private func errorBanner(_ message: String) -> some View {
        HStack(alignment: .top, spacing: 6) {
            Image(systemName: "exclamationmark.octagon.fill").foregroundStyle(.red)
            Text(message)
                .font(.caption)
                .textSelection(.enabled)
                .lineLimit(8)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 0)
            Button {
                model.actionError = nil
            } label: {
                Image(systemName: "xmark").font(.caption2.weight(.bold))
            }
            .buttonStyle(.borderless)
            .help("sites.share.dismiss")
        }
        .padding(8)
        .background(.red.opacity(0.12), in: RoundedRectangle(cornerRadius: 6))
    }

    private func warnings(_ warnings: [String]) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            ForEach(warnings, id: \.self) { warning in
                Label(warning, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption)
                    .foregroundStyle(.orange)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    private func services(_ status: Status) -> some View {
        VStack(alignment: .leading, spacing: 5) {
            SectionTitle("settings.pane.services")
            ServiceRow(name: String(localized: "menu.web"), detail: "http :\(status.httpPort) · https :\(status.httpsPort)", up: true)
            if status.dns.wildcard {
                ServiceRow(name: "DNS", detail: "*.\(status.dns.tld)", up: true)
            } else if !status.dns.tld.isEmpty {
                ServiceRow(name: "DNS", detail: status.dns.wildcardProblem.isEmpty ? String(localized: "menu.doesnt_resolve \(status.dns.tld)") : status.dns.wildcardProblem, up: false)
                    .help(status.dns.wildcardProblem)
            }
            ServiceRow(name: String(localized: "services.category.mail"), detail: status.mail.isEmpty ? String(localized: "menu.stopped") : "smtp \(status.mail)", up: !status.mail.isEmpty)
            if let postgres = status.postgres {
                ServiceRow(name: "PostgreSQL", detail: postgres, up: status.processes.first { $0.name == "postgres" }?.up ?? false)
            }
            ForEach(status.extras) { extra in
                ServiceRow(name: extra.name, detail: extra.enabled ? String(localized: "menu.version_on_port \(extra.version) \(String(extra.port))") : String(localized: "menu.stopped"), up: extra.up)
            }
            notInstalled(status)
            ForEach(status.processes.filter { $0.name.hasPrefix("php") }) { process in
                ServiceRow(name: process.name, detail: processDetail(process), up: process.up)
            }
            ForEach(status.processes.filter { !$0.up && !$0.name.hasPrefix("php") }) { process in
                Text(verbatim: "\(process.name): \(process.lastError ?? String(localized: "menu.down"))")
                    .font(.caption2)
                    .foregroundStyle(.red)
                    .lineLimit(3)
            }
        }
    }

    /// Databases and caches are installed on request, from Settings → Services.
    @ViewBuilder
    private func notInstalled(_ status: Status) -> some View {
        if status.postgres == nil, status.extras.isEmpty {
            HStack(spacing: 6) {
                Circle().fill(Color.secondary.opacity(0.4)).frame(width: 7, height: 7)
                Text("menu.no_database_or_cache_installed")
                    .font(.caption).foregroundStyle(.secondary)
                Spacer()
                Button("services.install_ellipsis") {
                    model.showSettings(.services, service: "postgres")
                    open(WindowID.settings)
                }
                .buttonStyle(.link)
                .font(.caption)
                .help("menu.opens_settings_to_services")
            }
        }
    }

    private func processDetail(_ process: Status.Process) -> String {
        if !process.up {
            return process.lastError ?? String(localized: "menu.down")
        }
        return process.restarts > 0 ? String(localized: "menu.up_restarted \(String(process.restarts))") : String(localized: "menu.up")
    }

    private func sites(_ sites: [Status.Site], proxies: [Status.Proxy]) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            SectionTitle("settings.pane.sites")
            if sites.isEmpty && proxies.isEmpty {
                Text("menu.no_sites_yet_run_flock")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                SiteList(sites: sites, proxies: proxies) { model.open($0) } openProxy: { proxy in
                    if let text = proxy.url, let url = URL(string: text) { NSWorkspace.shared.open(url) }
                }
            }
        }
    }

    private func toggles(_ status: Status) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Toggle(isOn: Binding(get: { status.xdebug }, set: { on in Task { await model.setXdebug(on) } })) {
                Label("common.xdebug", systemImage: "ladybug")
            }
            Toggle(isOn: Binding(get: { status.dumps }, set: { on in Task { await model.setDumps(on) } })) {
                Label("menu.capture_dumps_and_queries", systemImage: "tray.and.arrow.down")
            }
        }
        .toggleStyle(.switch)
        .controlSize(.small)
    }

    private func shortcuts(_ status: Status) -> some View {
        HStack(spacing: 8) {
            ShortcutButton(title: "services.category.mail", symbol: "envelope", count: status.mailCount) {
                open(WindowID.mail)
            }
            ShortcutButton(title: "menu.dumps", symbol: "ladybug.fill", count: status.dumpCount) {
                open(WindowID.dumps)
            }
            ShortcutButton(title: "dumps.event_kind.logs", symbol: "doc.text.magnifyingglass", count: nil) {
                open(WindowID.logs)
            }
        }
    }

    @ViewBuilder
    private var notRunning: some View {
        if model.needsSetup {
            VStack(alignment: .leading, spacing: 8) {
                Text("menu.flock_isnt_set_up_on")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Button("menu.set_up_flock") {
                    open(WindowID.setup)
                }
                .buttonStyle(.borderedProminent)
            }
        } else {
            VStack(alignment: .leading, spacing: 8) {
                Text(model.isUpdating ? String(localized: "menu.updating") : model.error ?? String(localized: "app.not_running"))
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Button("menu.start_flock") {
                    Task { await model.restart() }
                }
                .disabled(model.isRestarting || model.isUpdating)
            }
        }
    }

    private var footer: some View {
        HStack {
            // Restart and Settings need the daemon; before the setup, Set Up Flock is the way on.
            if !model.needsSetup {
                Button("menu.restart") {
                    Task { await model.restart() }
                }
                .disabled(model.isRestarting || model.isUpdating || !model.isRunning)
                Button("menu.settings") {
                    open(WindowID.settings)
                }
                .keyboardShortcut(",")
            }
            Spacer()
            Button("menu.quit_flock_bar") {
                NSApp.terminate(nil)
            }
        }
        .buttonStyle(.borderless)
        .font(.callout)
    }
}

private struct SectionTitle: View {
    let title: LocalizedStringKey

    init(_ title: LocalizedStringKey) {
        self.title = title
    }

    var body: some View {
        Text(title)
            .textCase(.uppercase)
            .font(.caption2.weight(.semibold))
            .foregroundStyle(.secondary)
    }
}

private struct ServiceRow: View {
    let name: String
    let detail: String
    let up: Bool

    var body: some View {
        HStack(spacing: 6) {
            Circle()
                .fill(up ? Color.green : Color.red)
                .frame(width: 7, height: 7)
            Text(name).font(.callout)
            Spacer()
            Text(detail)
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.middle)
        }
    }
}

/// The panel sizes itself to its content's ideal size, and a ScrollView has none of its own, so
/// the list is as tall as its rows and scrolls past 240pt. A site's git worktrees are listed under
/// it once it's expanded.
private struct SiteList: View {
    let sites: [Status.Site]
    /// Proxies (`flock proxy`) follow the sites.
    var proxies: [Status.Proxy] = []
    let open: (Status.Site) -> Void
    var openProxy: (Status.Proxy) -> Void = { _ in }

    private let contentHeightState = State<CGFloat>(initialValue: 0)
    private var contentHeight: CGFloat {
        get { contentHeightState.wrappedValue }
        nonmutating set { contentHeightState.wrappedValue = newValue }
    }

    /// The sites whose worktrees are shown, by name, separated by newlines.
    @AppStorage("panel.expandedSites") private var expandedNames = ""

    private var expanded: Set<String> { Set(expandedNames.split(separator: "\n").map(String.init)) }

    private func toggle(_ name: String) {
        var names = expanded
        if names.remove(name) == nil { names.insert(name) }
        expandedNames = names.sorted().joined(separator: "\n")
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 2) {
                ForEach(sites.topLevel) { site in
                    let worktrees = sites.worktrees(of: site.name)
                    let isExpanded = expanded.contains(site.name)
                    SiteRow(site: site, worktrees: worktrees.count, expanded: isExpanded, toggle: { toggle(site.name) }) { open(site) }
                    if isExpanded {
                        ForEach(worktrees) { worktree in
                            WorktreeRow(worktree: worktree) { open(worktree) }
                        }
                    }
                }
                ForEach(proxies) { proxy in
                    ProxyRow(proxy: proxy) { openProxy(proxy) }
                }
            }
        }
        .onScrollGeometryChange(for: CGFloat.self, of: \.contentSize.height) { _, height in
            contentHeight = height
        }
        .frame(height: min(contentHeight, 240))
    }
}

private struct SiteRow: View {
    let site: Status.Site
    /// How many git worktrees the site has, and whether they're shown under it.
    var worktrees = 0
    var expanded = false
    var toggle: () -> Void = {}
    let open: () -> Void

    private let isHoveredState = State<Bool>(initialValue: false)
    private var isHovered: Bool {
        get { isHoveredState.wrappedValue }
        nonmutating set { isHoveredState.wrappedValue = newValue }
    }

    var body: some View {
        HStack(spacing: 2) {
            siteButton
            if worktrees > 0 {
                // A button of its own: the row still opens the site.
                Button(action: toggle) {
                    HStack(spacing: 2) {
                        Text(worktrees, format: .number).font(.caption2.monospacedDigit())
                        Image(systemName: expanded ? "chevron.down" : "chevron.right").font(.caption2.weight(.semibold))
                    }
                    .foregroundStyle(.secondary)
                    .frame(minWidth: 24, minHeight: 20)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                // One apart in code as well: builds without the compiled catalog (swift run) have no plural rules.
                .help(expanded ? "menu.hide_its_worktrees" : worktrees == 1 ? "menu.show_its_worktree" : "menu.show_its_worktrees \(worktrees)")
            }
        }
        .background(isHovered ? Color.primary.opacity(0.08) : .clear, in: RoundedRectangle(cornerRadius: 5))
        .onHover { isHovered = $0 }
    }

    private var siteButton: some View {
        Button(action: open) {
            HStack(spacing: 6) {
                Image(systemName: site.secured ? "lock.fill" : "globe")
                    .font(.caption)
                    .foregroundStyle(site.secured ? Color.green : Color.secondary)
                    .frame(width: 14)
                Text(site.domain).font(.callout)
                Spacer()
                if let problem = site.rowProblem {
                    ProblemIcon(text: problem)
                }
                if let dot = site.stateDot {
                    StateDot(color: dot.color).help(dot.help)
                }
                if site.debugging {
                    Label("services.category.debugging", systemImage: "ladybug.fill")
                        .labelStyle(.iconOnly)
                        .font(.caption2)
                        .foregroundStyle(.orange)
                }
                if site.shared {
                    Badge(text: "sites.shared", color: .green)
                        .help(site.share?.url ?? "")
                }
                if !site.isNode, site.server == .frankenphp {
                    Badge(text: site.worker == .octane ? "common.octane" : "common.frankenphp", color: .secondary)
                }
                Text(site.isNode ? "common.nodejs_version \(site.node ?? "")" : "common.php_version \(site.php)")
                    .font(.caption2.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            .padding(.vertical, 3)
            .padding(.horizontal, 6)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(site.url == nil)
        .help(help)
    }

    /// The row's tooltip: where a site without a document root is fixed, else its address.
    private var help: String {
        if site.rootState == .notFound {
            return String(localized: "menu.no_public_www_web_or \(site.path)")
        }
        return site.url ?? site.problem ?? site.path
    }
}

/// A proxy among the sites: its domain, and what answers at its upstream (the Node.js version when
/// it is one of Flock's) or that nothing does yet.
private struct ProxyRow: View {
    let proxy: Status.Proxy
    let open: () -> Void

    private let isHoveredState = State<Bool>(initialValue: false)
    private var isHovered: Bool {
        get { isHoveredState.wrappedValue }
        nonmutating set { isHoveredState.wrappedValue = newValue }
    }

    var body: some View {
        Button(action: open) {
            HStack(spacing: 6) {
                Image(systemName: proxy.secured ? "lock.fill" : "globe")
                    .font(.caption)
                    .foregroundStyle(proxy.secured ? Color.green : Color.secondary)
                    .frame(width: 14)
                Text(proxy.domain).font(.callout)
                Spacer()
                if let problem = proxy.problem {
                    ProblemIcon(text: problem)
                }
                StateDot(color: proxy.stateDot.color).help(proxy.stateDot.help)
                Text(proxy.running ? proxy.server ?? String(localized: "sites.proxy.running") : String(localized: "sites.proxy.not_running"))
                    .font(.caption2.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            .padding(.vertical, 3)
            .padding(.horizontal, 6)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(proxy.url == nil)
        .help(proxy.running ? String(localized: "common.from_to \(proxy.url ?? proxy.domain) \(proxy.upstream)") : String(localized: "sites.proxy.nothing_answers \(proxy.upstream)"))
        .background(isHovered ? Color.primary.opacity(0.08) : .clear, in: RoundedRectangle(cornerRadius: 5))
        .onHover { isHovered = $0 }
    }
}

/// A git worktree under its site: its branch, and how its setup goes.
private struct WorktreeRow: View {
    let worktree: Status.Site
    let open: () -> Void

    private let isHoveredState = State<Bool>(initialValue: false)
    private var isHovered: Bool {
        get { isHoveredState.wrappedValue }
        nonmutating set { isHoveredState.wrappedValue = newValue }
    }

    var body: some View {
        Button(action: open) {
            HStack(spacing: 6) {
                Image(systemName: worktree.secured ? "lock.fill" : "globe")
                    .font(.caption2)
                    .foregroundStyle(worktree.secured ? Color.green : Color.secondary)
                    .frame(width: 14)
                VStack(alignment: .leading, spacing: 1) {
                    Text(worktree.worktreeLabel).font(.callout)
                    if let setup = worktree.worktree, setup.busy {
                        HStack(spacing: 4) {
                            ProgressView().controlSize(.mini)
                            Text(setup.step.isEmpty ? String(localized: "worktrees.setting_up") : "\(setup.step.prefix(1).uppercased())\(setup.step.dropFirst())…")
                        }
                        .font(.caption2).foregroundStyle(.secondary)
                    } else if let setup = worktree.worktree, setup.state == .failed {
                        Text("worktrees.job.setup_failed \(setup.error ?? String(localized: "worktrees.see_log"))").font(.caption2).foregroundStyle(.red)
                    } else if let problem = worktree.problem {
                        Text(problem).font(.caption2).foregroundStyle(.red)
                    }
                }
                Spacer()
                if let setup = worktree.worktree, !setup.database.isEmpty {
                    Badge(text: setup.dbMode == .fresh ? "menu.fresh_db" : "menu.own_db", color: .secondary)
                        .help("worktrees.own_database_tooltip \(setup.database)")
                }
            }
            .padding(.vertical, 2)
            .padding(.leading, 22)
            .padding(.trailing, 6)
            .background(isHovered ? Color.primary.opacity(0.08) : .clear, in: RoundedRectangle(cornerRadius: 5))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(worktree.url == nil)
        .onHover { isHovered = $0 }
        .help(worktree.url ?? worktree.path)
    }
}

private struct ShortcutButton: View {
    let title: LocalizedStringKey
    let symbol: String
    let count: Int?
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(spacing: 3) {
                Image(systemName: symbol).font(.title3)
                HStack(spacing: 3) {
                    Text(title)
                    if let count, count > 0 {
                        Text(count, format: .number)
                            .font(.caption2.weight(.semibold))
                            .padding(.horizontal, 5)
                            .background(Color.accentColor.opacity(0.25), in: Capsule())
                    }
                }
                .font(.caption)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 6)
        }
        .buttonStyle(.bordered)
    }
}
