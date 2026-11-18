import AppKit
import FlockKit
import SwiftUI
import UniformTypeIdentifiers

/// The sites in a list, and the selected one's details beside it, as in Services: its address,
/// domains and document root, its PHP and Node.js versions and what serves it. Each version picker
/// offers the project's pinned version (or the default) first, then the installed versions, which
/// isolate the site on this machine; the document root picker works the same way. The pin menu
/// pins a version or the document root into the project or removes it again. Proxies (`flock proxy`)
/// follow the sites in a section of their own.
struct SitesPane: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openWindow) private var openWindow
    private let selectionState = State<Status.Site.ID?>(initialValue: nil)
    /// Whether the list has the keyboard focus, so the arrow keys move through it.
    private let listFocusState = FocusState<Bool>()
    private let confirmState = State<ConfirmAction?>(initialValue: nil)
    private var confirm: ConfirmAction? {
        get { confirmState.wrappedValue }
        nonmutating set { confirmState.wrappedValue = newValue }
    }
    /// The site a domain is being added to, and the name typed so far.
    private let addingState = State<String?>(initialValue: nil)
    private var adding: String? {
        get { addingState.wrappedValue }
        nonmutating set { addingState.wrappedValue = newValue }
    }
    private let newDomainState = State<String>(initialValue: "")
    /// The site whose PHP extensions are shown.
    private let extensionsState = State<Status.Site?>(initialValue: nil)
    /// The site whose FrankenPHP is restarting.
    private let restartingState = State<String?>(initialValue: nil)
    private var restarting: String? {
        get { restartingState.wrappedValue }
        nonmutating set { restartingState.wrappedValue = newValue }
    }
    /// The site whose Reverb server is restarting.
    private let restartingReverbState = State<String?>(initialValue: nil)
    private var restartingReverb: String? {
        get { restartingReverbState.wrappedValue }
        nonmutating set { restartingReverbState.wrappedValue = newValue }
    }
    /// The site the Share sheet is open for.
    private let sharingState = State<String?>(initialValue: nil)
    private var sharing: String? {
        get { sharingState.wrappedValue }
        nonmutating set { sharingState.wrappedValue = newValue }
    }

    /// The Add Proxy sheet is open.
    private let addingProxyState = State<Bool>(initialValue: false)
    /// The proxy whose upstream is being changed, and the upstream typed so far.
    private let editingUpstreamState = State<String?>(initialValue: nil)
    private var editingUpstream: String? {
        get { editingUpstreamState.wrappedValue }
        nonmutating set { editingUpstreamState.wrappedValue = newValue }
    }
    private let upstreamDraftState = State<String>(initialValue: "")
    /// The proxy whose command is being changed.
    private let editingCommandState = State<Status.Proxy?>(initialValue: nil)
    /// The proxy whose server is starting or stopping, while the daemon answers.
    private let proxyBusyState = State<String?>(initialValue: nil)
    private var proxyBusy: String? {
        get { proxyBusyState.wrappedValue }
        nonmutating set { proxyBusyState.wrappedValue = newValue }
    }
    /// Templates loaded for the New App sheet (non-nil while the sheet is open).
    private let newAppTemplatesState = State<[AppTemplate]?>(initialValue: nil)
    private let creatingAppState = State<Bool>(initialValue: false)

    var body: some View {
        let store = model.settings
        let sites = model.status?.sites ?? []
        let proxies = model.status?.proxies ?? []
        let phpVersions = store.config?.php.map(\.version) ?? []
        let nodeMajors = store.config?.nodeMajors ?? []

        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .bottom) {
                PaneHeader(title: "settings.pane.sites", subtitle: "sites.subtitle")
                if creatingAppState.wrappedValue {
                    ProgressView().controlSize(.small).padding(.bottom, 8)
                }
                Button("sites.new_app") { openNewAppSheet() }
                    .disabled(creatingAppState.wrappedValue)
                    .help("sites.new_app_tooltip")
                    .padding(.bottom, 6)
                Button("sites.add_proxy") { addingProxyState.wrappedValue = true }
                    .help("sites.serve_a_server_of_its \(model.status?.dns.tld ?? "test")")
                    .padding(.bottom, 6)
                Button("sites.add_site") {
                    for url in SettingsStore.chooseDirectories(prompt: String(localized: "common.add"), multiple: false) {
                        Task { await store.perform { _ = try await $0.linkSite(path: url.path) } }
                    }
                }
                .padding(.trailing, 20)
                .padding(.bottom, 6)
            }

            if sites.isEmpty && proxies.isEmpty {
                ContentUnavailableView("sites.no_sites", systemImage: "globe",
                                       description: Text("sites.no_sites_description"))
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                // The sites in a list, the selected one's details beside it, as in Services.
                // Site and proxy names never clash (the daemon refuses it), so one selection covers both.
                let selectedProxy = proxies.first { $0.id == selectionState.wrappedValue } ?? (sites.isEmpty ? proxies.first : nil)
                let selected = selectedProxy == nil ? sites.first { $0.id == selectionState.wrappedValue } ?? sites.first : nil
                let selectedID = selectedProxy?.id ?? selected?.id
                HStack(spacing: 0) {
                    // The details are rebuilt for each site, which takes the focus off the list; it is
                    // given back so the arrow keys keep moving through the sites.
                    List(selection: Binding<Status.Site.ID?>(get: { selectedID }, set: {
                        selectionState.wrappedValue = $0
                        listFocusState.wrappedValue = true
                    })) {
                        ForEach(sites.topLevel) { site in
                            siteRow(site).tag(site.id)
                            // Its git worktrees, under it.
                            ForEach(sites.worktrees(of: site.name)) { worktree in
                                worktreeRow(worktree).tag(worktree.id)
                            }
                        }
                        if !proxies.isEmpty {
                            Section("sites.proxies") {
                                ForEach(proxies) { proxy in
                                    proxyRow(proxy).tag(proxy.id)
                                }
                            }
                        }
                    }
                    .listStyle(.inset)
                    .focused(listFocusState.projectedValue)
                    .frame(width: 230)

                    Divider()

                    ScrollView {
                        Group {
                            if let selectedProxy {
                                proxyDetail(selectedProxy)
                            } else if let selected, selected.isWorktree {
                                WorktreeDetail(worktree: selected) { selectionState.wrappedValue = selected.parent }
                            } else if let selected {
                                detail(selected, phpVersions: phpVersions, nodeMajors: nodeMajors)
                            }
                        }
                        .padding(16)
                    }
                    // Another site's details start at the top.
                    .id(selectedID)
                    .frame(minWidth: 340, maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
                    .background(Color(nsColor: .controlBackgroundColor).opacity(0.5))
                }
                .onChange(of: model.pendingSite, initial: true) { _, name in
                    // A notification or link asks for this site: selected once.
                    if let name, sites.contains(where: { $0.name == name }) || proxies.contains(where: { $0.name == name }) {
                        selectionState.wrappedValue = name
                        model.pendingSite = nil
                    }
                }
                .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(.separator))
                .clipShape(RoundedRectangle(cornerRadius: 8))
                .padding(.horizontal, 20)
                .padding(.vertical, 12)
            }
        }
        .sheet(item: extensionsState.projectedValue) { site in
            SiteExtensionsSheet(site: site)
        }
        .sheet(item: editingCommandState.projectedValue) { proxy in
            ProxyCommandSheet(proxy: proxy) { change in
                editingCommandState.wrappedValue = nil
                Task { await store.perform { _ = try await $0.updateProxy(proxy.name, change) } }
            } cancel: {
                editingCommandState.wrappedValue = nil
            }
        }
        .sheet(isPresented: Binding(get: { sharing != nil }, set: { if !$0 { sharing = nil } })) {
            ShareSheet(site: sharing ?? "") { allow, debug in
                let name = sharing ?? ""
                sharing = nil
                startShare(name, allow: allow, debug: debug)
            } cancel: {
                sharing = nil
            }
        }
        .sheet(isPresented: Binding(get: { newAppTemplatesState.wrappedValue != nil },
                                    set: { if !$0 { newAppTemplatesState.wrappedValue = nil } })) {
            let defaultFolder = store.config?.parked.first ?? FileManager.default.homeDirectoryForCurrentUser.path
            let tld = model.status?.dns.tld.isEmpty == false ? (model.status?.dns.tld ?? "test") : "test"
            let hasRunningDb = model.status?.extras.contains { ($0.id == "mysql" || $0.id == "mariadb") && $0.up } ?? false
            NewAppSheet(
                templates: newAppTemplatesState.wrappedValue ?? AppTemplate.builtInDefaults,
                defaultFolder: defaultFolder,
                tld: tld,
                hasRunningDb: hasRunningDb
            ) { req in
                newAppTemplatesState.wrappedValue = nil
                createApp(req)
            } cancel: {
                newAppTemplatesState.wrappedValue = nil
            }
        }
        .confirmationDialog(confirm?.title ?? "", isPresented: Binding(get: { confirm != nil }, set: { if !$0 { confirm = nil } }), presenting: confirm) { action in
            Button(action.button, role: action.destructive ? .destructive : nil) {
                Task { await store.perform { _ = try await action.run($0) } }
            }
            Button("common.cancel", role: .cancel) {}
        } message: { action in
            Text(action.message)
        }
        .alert("sites.add_domain_title \(adding ?? "")", isPresented: Binding(get: { adding != nil }, set: { if !$0 { adding = nil } })) {
            TextField("sites.tenant1_a_b_or_site \(adding ?? "app") \(model.status?.dns.tld ?? "test")", text: newDomainState.projectedValue)
            Button("common.add") {
                let name = adding ?? ""
                let domain = newDomainState.wrappedValue.trimmingCharacters(in: .whitespaces)
                newDomainState.wrappedValue = ""
                if !name.isEmpty, !domain.isEmpty {
                    Task { await store.perform { _ = try await $0.addDomain(site: name, domain: domain) } }
                }
            }
            Button("common.cancel", role: .cancel) { newDomainState.wrappedValue = "" }
        } message: {
            Text(domainHelp)
        }
        .sheet(isPresented: addingProxyState.projectedValue) {
            AddProxySheet(add: { name, upstream, command, dir, stop in
                addingProxyState.wrappedValue = false
                selectionState.wrappedValue = name
                // With commands, one request makes the proxy and its commands.
                Task { await store.perform { _ = try await $0.proxy(name: name, upstream: upstream, command: command, dir: dir, stopCommand: stop) } }
            }, cancel: {
                addingProxyState.wrappedValue = false
            }, tld: model.status?.dns.tld ?? "test")
        }
        .alert("sites.change_upstream_title \(editingUpstream ?? "")", isPresented: Binding(get: { editingUpstream != nil }, set: { if !$0 { editingUpstream = nil } })) {
            TextField("sites.upstream_placeholder", text: upstreamDraftState.projectedValue)
            Button("sites.change_action") {
                let name = editingUpstream ?? ""
                let upstream = upstreamDraftState.wrappedValue.trimmingCharacters(in: .whitespaces)
                if !name.isEmpty, !upstream.isEmpty {
                    Task { await store.perform { _ = try await $0.proxy(name: name, upstream: upstream) } }
                }
            }
            Button("common.cancel", role: .cancel) {}
        } message: {
            Text("sites.change_upstream_details")
        }
        // A flock://secure link (AppModel.handle): nothing changes until the user confirms here.
        .alert(linkRequest?.title ?? "", isPresented: Binding(get: { linkRequest != nil }, set: { if !$0 { model.pendingSecure = nil } }),
               presenting: linkRequest) { request in
            if let name = request.confirmation {
                Button("sites.secure") {
                    Task { await store.perform { _ = try await $0.updateSite(name, SiteChange(secured: true)) } }
                }
                Button("common.cancel", role: .cancel) {}
            } else {
                Button("common.ok") {}
            }
        } message: { request in
            Text(request.message)
        }
    }

    /// What a pending flock://secure link asks for, once the sites are loaded.
    private var linkRequest: SecureLink.Resolution? {
        model.pendingSecure?.resolve(sites: model.status?.sites)
    }

    /// What adding a domain does on this machine.
    private var domainHelp: String {
        let dns = model.status?.dns
        let depth = dns?.maxDepth ?? 4
        if dns?.wildcard ?? true {
            let alias = "\(adding ?? "app")-2.\(dns?.tld ?? "test")"
            return String(localized: "sites.every_subdomain_up_to_levels \(depth) \(alias)")
        }
        let problem = dns?.wildcardProblem ?? ""
        return String(localized: "sites.wildcard_subdomains_dont_work_here \(problem)")
    }

    /// A site in the list: its name and address, a lock when it is secured, a badge while it is shared, a
    /// warning when something is wrong.
    private func siteRow(_ site: Status.Site) -> some View {
        HStack(spacing: 6) {
            VStack(alignment: .leading, spacing: 1) {
                Text(site.name).fontWeight(.medium).lineLimit(1)
                Text(site.domain).font(.caption).foregroundStyle(.secondary)
                    .lineLimit(1).truncationMode(.middle)
            }
            Spacer(minLength: 4)
            if let problem = problem(site) {
                ProblemIcon(text: problem)
            }
            if let dot = site.stateDot {
                StateDot(color: dot.color).help(dot.help)
            }
            if site.shared {
                Badge(text: "sites.shared", color: .green)
                    .help("menu.shared_at \(site.share?.url ?? "")")
            }
            if site.secured {
                Image(systemName: "lock.fill").font(.caption).foregroundStyle(.secondary)
                    .help("sites.served_over_https")
            }
        }
        .padding(.vertical, 2)
        .help(site.path)
    }

    /// A proxy in the list: its name and where it passes requests, a lock when it is secured, a
    /// warning when it isn't served.
    private func proxyRow(_ proxy: Status.Proxy) -> some View {
        HStack(spacing: 6) {
            VStack(alignment: .leading, spacing: 1) {
                Text(proxy.name).fontWeight(.medium).lineLimit(1)
                Text("common.from_to \(proxy.domain) \(upstreamLabel(proxy.upstream))").font(.caption).foregroundStyle(.secondary)
                    .lineLimit(1).truncationMode(.middle)
            }
            Spacer(minLength: 4)
            if let problem = proxy.problem {
                ProblemIcon(text: problem)
            }
            StateDot(color: proxy.stateDot.color).help(proxy.stateDot.help)
            if proxy.secured {
                Image(systemName: "lock.fill").font(.caption).foregroundStyle(.secondary)
                    .help("sites.served_over_https")
            }
        }
        .padding(.vertical, 2)
        .help(proxy.upstream)
    }

    /// An upstream without the http:// Flock adds: 127.0.0.1:3000.
    private func upstreamLabel(_ upstream: String) -> String {
        upstream.hasPrefix("http://") ? String(upstream.dropFirst("http://".count)) : upstream
    }

    /// The selected proxy: its address and actions, then its HTTPS and upstream.
    private func proxyDetail(_ proxy: Status.Proxy) -> some View {
        let store = model.settings
        return VStack(alignment: .leading, spacing: 20) {
            HStack(alignment: .top, spacing: 10) {
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        Text(proxy.name).font(.title3.weight(.semibold))
                        Badge(text: "sites.proxy", color: .secondary)
                    }
                    if let text = proxy.url, let url = URL(string: text) {
                        Link(text, destination: url).font(.callout)
                    }
                }
                Spacer(minLength: 8)
                HStack(spacing: 8) {
                    Button {
                        if let text = proxy.url, let url = URL(string: text) { NSWorkspace.shared.open(url) }
                    } label: { Image(systemName: "safari") }
                        .help("common.open_target \(proxy.url ?? proxy.domain)")
                        .disabled(proxy.url == nil)
                    Button {
                        confirm = ConfirmAction(
                            title: String(localized: "common.remove_question \(proxy.name)"), button: String(localized: "common.remove"), destructive: true,
                            message: proxy.command == nil
                                ? String(localized: "sites.remove_proxy_details \(proxy.domain) \(proxy.upstream)")
                                : String(localized: "sites.flock_stops_serving_domain_and \(proxy.domain)"),
                            run: { [name = proxy.name] client in try await client.removeProxy(name) })
                    } label: { Image(systemName: "minus.circle") }
                        .help("sites.remove_proxy")
                }
                .buttonStyle(.borderless)
            }
            if let problem = proxy.problem {
                Label(problem, systemImage: "exclamationmark.triangle.fill")
                    .font(.callout).foregroundStyle(.orange)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }

            section("sites.proxy") {
                field("sites.server") {
                    if let command = proxy.command {
                        proxyCommandState(proxy, command)
                    } else {
                        VStack(alignment: .leading, spacing: 2) {
                            HStack(spacing: 6) {
                                Circle()
                                    .fill(proxy.running ? Color.green : Color.secondary.opacity(0.5))
                                    .frame(width: 7, height: 7)
                                Text(proxy.running ? proxy.server ?? String(localized: "sites.proxy.running") : String(localized: "sites.proxy.not_running"))
                            }
                            if !proxy.running {
                                Text("sites.nothing_answers_yet \(upstreamLabel(proxy.upstream))")
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                        }
                    }
                }
                field("common.https") {
                    Toggle("common.https", isOn: Binding(
                        get: { proxy.secured },
                        set: { on in Task { await store.perform { _ = try await $0.secureProxy(proxy.name, on) } } }
                    ))
                    .labelsHidden()
                    .toggleStyle(.switch)
                    .controlSize(.small)
                }
                field("sites.upstream") {
                    HStack(spacing: 8) {
                        Text(proxy.upstream).font(.callout.monospaced()).textSelection(.enabled)
                        Button("sites.change") {
                            upstreamDraftState.wrappedValue = upstreamLabel(proxy.upstream)
                            editingUpstream = proxy.name
                        }
                        .controlSize(.small)
                    }
                }
                Text(proxy.command == nil
                     ? "sites.proxy_footnote_no_commands \(proxy.domain)"
                     : "sites.proxy_footnote_commands \(proxy.domain)")
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            section("sites.proxy.commands") {
                if let command = proxy.command {
                    field("services.start") {
                        Text(command.command).font(.callout.monospaced()).textSelection(.enabled)
                            .lineLimit(1).truncationMode(.middle)
                    }
                    if let stop = command.stopCommand, !stop.isEmpty {
                        field("sites.share.stop") {
                            Text(stop).font(.callout.monospaced()).textSelection(.enabled)
                                .lineLimit(1).truncationMode(.middle)
                        }
                    }
                    field("general.folder") {
                        Text(command.dir).font(.callout.monospaced()).textSelection(.enabled)
                            .lineLimit(1).truncationMode(.middle)
                            .help(command.dir)
                    }
                }
                Button(proxy.command == nil ? "sites.proxy.add_commands" : "sites.change") { editingCommandState.wrappedValue = proxy }
                    .controlSize(.small)
                Text(proxyCommandsHelp)
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            if let command = proxy.command {
                ServerOutput(log: command.log, live: command.state == .starting || command.state == .running || command.state == .stopping)
            }
        }
    }

    /// A proxy's server that Flock starts with its command: its state, and Start or Stop.
    private func proxyCommandState(_ proxy: Status.Proxy, _ command: Status.ProxyCommand) -> some View {
        let color: Color
        let text: String
        switch command.state {
        case .stopped: (color, text) = (StateDot.stopped, String(localized: "services.postgres.stopped"))
        case .starting: (color, text) = (StateDot.busy, String(localized: "worktrees.job.starting"))
        case .running: (color, text) = (StateDot.running, String(localized: "sites.proxy.running"))
        case .stopping: (color, text) = (StateDot.busy, String(localized: "sites.server.stopping"))
        case .failed: (color, text) = (StateDot.failed, command.problem.map { String(localized: "sites.server.failed_because \($0)") } ?? String(localized: "sites.server.failed"))
        }
        return VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                StateDot(color: color)
                Text(text).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
                switch command.state {
                case .stopped, .failed:
                    Button("services.start") { runProxy(proxy.name, start: true) }
                        .disabled(proxyBusy == proxy.name)
                case .starting, .running:
                    Button("sites.share.stop") { runProxy(proxy.name, start: false) }
                        .disabled(proxyBusy == proxy.name)
                case .stopping:
                    EmptyView()
                }
                if proxyBusy == proxy.name {
                    ProgressView().controlSize(.mini)
                }
            }
            .controlSize(.small)
            if command.state == .starting, let problem = command.problem {
                Text(problem).font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    /// Starts or stops a proxy's server.
    private func runProxy(_ name: String, start: Bool) {
        let store = model.settings
        proxyBusy = name
        Task {
            await store.perform { client in
                if start {
                    _ = try await client.startProxy(name)
                } else {
                    _ = try await client.stopProxy(name)
                }
            }
            proxyBusy = nil
        }
    }

    /// A worktree in the list, indented under its site: its branch, and how its setup goes.
    private func worktreeRow(_ worktree: Status.Site) -> some View {
        HStack(spacing: 6) {
            Image(systemName: "arrow.triangle.branch").font(.caption).foregroundStyle(.secondary)
            Text(worktree.worktreeLabel).lineLimit(1).truncationMode(.middle)
            Spacer(minLength: 4)
            WorktreeState(worktree: worktree)
        }
        .padding(.leading, 12)
        .padding(.vertical, 1)
        .help(worktree.path)
    }

    /// What a site's list row warns about: the site, its document root, one of its domains, its FrankenPHP
    /// or its Reverb server.
    private func problem(_ site: Status.Site) -> String? {
        if let problem = site.problem {
            return problem
        }
        if let notice = site.notice {
            return notice
        }
        if let domain = site.domains.first(where: { $0.problem != nil }) {
            return "\(domain.host): \(domain.problem ?? "")"
        }
        if !site.isNode, site.server == .frankenphp, let franken = site.frankenPHP, franken.state == .failed || franken.state == .unavailable,
           !franken.problem.isEmpty {
            return franken.problem
        }
        if !site.isNode, let reverb = site.reverb, reverb.state == .failed || reverb.state == .unavailable, !reverb.problem.isEmpty {
            return reverb.problem
        }
        return nil
    }

    /// The selected site: its address and actions, then Site, Versions and Server sections of labelled settings.
    private func detail(_ site: Status.Site, phpVersions: [String], nodeMajors: [String]) -> some View {
        let store = model.settings
        return VStack(alignment: .leading, spacing: 20) {
            HStack(alignment: .top, spacing: 10) {
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        Text(site.name).font(.title3.weight(.semibold))
                        if site.parked {
                            Badge(text: "sites.project_folder_badge", color: .secondary)
                        }
                    }
                    if let text = site.url, let url = URL(string: text) {
                        Link(text, destination: url).font(.callout)
                    }
                    Text(site.path).font(.caption).foregroundStyle(.tertiary)
                        .lineLimit(1).truncationMode(.middle)
                        .help(site.path)
                }
                Spacer(minLength: 8)
                HStack(spacing: 8) {
                    Button { model.open(site) } label: { Image(systemName: "safari") }
                        .help("common.open_target \(site.url ?? site.domain)")
                        .disabled(site.url == nil)
                    Button { reveal(site.path) } label: { Image(systemName: "folder") }
                        .help("general.show_in_finder")
                    if !site.isNode {
                        Button { extensionsState.wrappedValue = site } label: { Image(systemName: "puzzlepiece.extension") }
                            .help("sites.php_extensions")
                    }
                    pinMenu(site)
                    if !site.parked {
                        Button {
                            confirm = ConfirmAction(
                                title: String(localized: "common.remove_question \(site.name)"), button: String(localized: "common.remove"), destructive: true,
                                message: String(localized: "sites.flock_stops_serving_domain_the \(site.domain) \(site.path)"),
                                run: { [name = site.name] client in try await client.unlinkSite(name) })
                        } label: { Image(systemName: "minus.circle") }
                            .help("sites.remove_site")
                    }
                }
                .buttonStyle(.borderless)
            }
            if let problem = site.problem {
                Label(problem, systemImage: "exclamationmark.triangle.fill")
                    .font(.callout).foregroundStyle(.orange)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }

            section("sites.site") {
                field("sites.type") {
                    Picker("sites.type", selection: Binding(
                        get: { site.type },
                        set: { type in Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(type: type.rawValue)) } } }
                    )) {
                        Text("common.php").tag(Status.SiteType.php)
                        Text("common.nodejs").tag(Status.SiteType.node)
                    }
                    .labelsHidden()
                    .fixedSize()
                }
                field("common.https") {
                    Toggle("common.https", isOn: Binding(
                        get: { site.secured },
                        set: { on in Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(secured: on)) } } }
                    ))
                    .labelsHidden()
                    .toggleStyle(.switch)
                    .controlSize(.small)
                }
                field("sites.domains") {
                    VStack(alignment: .leading, spacing: 3) {
                        ForEach(site.domains) { domain in
                            HStack(spacing: 4) {
                                if let problem = domain.problem {
                                    Label("common.label_value \(domain.host) \(problem)", systemImage: "exclamationmark.triangle.fill")
                                        .foregroundStyle(.orange)
                                        .fixedSize(horizontal: false, vertical: true)
                                } else if let url = domain.url.flatMap(URL.init(string:)) {
                                    Link(domain.alias ? String(localized: "sites.alias \(domain.host)") : domain.host, destination: url)
                                } else {
                                    Text(domain.host)
                                }
                                Button {
                                    confirm = ConfirmAction(
                                        title: String(localized: "common.remove_question \(domain.host)"), button: String(localized: "common.remove"), destructive: true,
                                        message: String(localized: "sites.domain_is_no_longer_served \(domain.host) \(site.name)"),
                                        run: { [name = site.name, entry = domain.entry] client in try await client.removeDomain(site: name, domain: entry) })
                                } label: { Image(systemName: "minus.circle") }
                                    .buttonStyle(.borderless)
                                    .help("common.remove_target \(domain.host)")
                            }
                        }
                        Button("sites.add_domain") { adding = site.name }
                            .buttonStyle(.link)
                            .help("sites.extra_domains_subdomains_and_aliases")
                    }
                    .font(.callout)
                }
                if let dns = model.status?.dns, !dns.wildcard, !dns.wildcardProblem.isEmpty {
                    Text("sites.subdomains_only_here \(dns.wildcardProblem)")
                        .font(.caption).foregroundStyle(.orange)
                        .fixedSize(horizontal: false, vertical: true)
                }
                if !site.isNode {
                    field("sites.root") {
                        rootPicker(site)
                    }
                    rootHints(site)
                }
            }

            // The heading is a noun where the button ("Share") is a verb, in Czech.
            section("sites.share_section") {
                shareFields(site)
            }

            section("versions.versions") {
                if !site.isNode {
                    field("common.php") {
                        versionPicker(label: "PHP", current: site.php, source: site.phpSource, pinned: site.phpPinned, file: site.phpFile,
                                      defaultVersion: store.config?.defaultPHP ?? "", installed: phpVersions) { version in
                            Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(php: version)) } }
                        }
                    }
                }
                field("common.nodejs") {
                    versionPicker(label: "Node", current: major(site.node ?? ""), source: site.nodeSource, pinned: major(site.nodePinned), file: site.nodeFile,
                                  defaultVersion: store.config?.defaultNode ?? "", installed: nodeMajors) { version in
                        Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(node: version)) } }
                    }
                }
                hints(site)
                Text(site.isNode
                     ? "sites.version_picked_here_applies_on \(Image(systemName: "pin"))"
                     : "sites.version_picked_here_applies_on_this \(Image(systemName: "pin"))")
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            if site.isNode {
                NodeDevServerSection(site: site)
            } else {
                section("sites.server") {
                    serverFields(site)
                }
                section("sites.reverb.title") {
                    reverbFields(site)
                }
            }

            if site.boost == .missing || site.boost == .outdated {
                section("common.laravel_boost") {
                    boost(site)
                }
            }

            let worktrees = (model.status?.sites ?? []).worktrees(of: site.name)
            if !worktrees.isEmpty || isGitCheckout(site.path) {
                SiteWorktreesSection(site: site, worktrees: worktrees) { worktree in
                    selectionState.wrappedValue = worktree.id
                }
            }
        }
    }

    /// A section of the details: a heading and its settings.
    private func section<Content: View>(_ title: LocalizedStringKey, @ViewBuilder content: () -> Content) -> some View {
        section(Text(title), content: content)
    }

    private func section<Content: View>(_ title: Text, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            title.font(.subheadline.weight(.semibold))
            content()
        }
    }

    /// A labelled setting: the label in a fixed column, so the controls line up.
    private func field<Content: View>(_ label: LocalizedStringKey, @ViewBuilder content: () -> Content) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            Text(label).foregroundStyle(.secondary)
                .frame(width: 80, alignment: .leading)
            content()
            Spacer(minLength: 0)
        }
    }

    /// The project's pinned version ("Use .nvmrc (Node 20)") or the default first, which follow the
    /// project; then the installed versions, which isolate the site. The tag "" means "follow".
    private func versionPicker(label: String, current: String, source: Status.VersionSource, pinned: String, file: String,
                               defaultVersion: String, installed: [String], change: @escaping (String) -> Void) -> some View {
        // On the first run there's no default until Flock has installed one.
        let follow = !pinned.isEmpty ? String(localized: "sites.use_file_tool_version \(fileName(file)) \(label) \(pinned)")
            : defaultVersion.isEmpty ? String(localized: "sites.default") : String(localized: "sites.default_tool_version \(label) \(defaultVersion)")
        let versions = source == .isolated && !current.isEmpty && !installed.contains(current) ? installed + [current] : installed
        let help: String = switch source {
        case .isolated: String(localized: "sites.tool_version_is_set_for \(label) \(current)")
        case .project: String(localized: "sites.project_pins_tool_version_in \(label) \(pinned) \(fileName(file))")
        case .default: String(localized: "sites.version_default \(label)")
        }
        return Picker(label, selection: Binding(
            get: { source == .isolated ? current : "" },
            set: { change($0) }
        )) {
            Text(follow).tag("")
            Divider()
            ForEach(versions, id: \.self) { version in
                Text("common.name_and_version \(label) \(version)").tag(version)
            }
        }
        .labelsHidden()
        .fixedSize()
        .help(help)
    }

    /// The tag of the picker's "Choose Folder…" item, which no document root can be.
    private static let chooseRootTag = "\u{0}choose"

    /// The document root: the project's (.flockrc) or the detected one first, which follow the
    /// project; then the folders with an index.php Flock found and the one set here, which set the
    /// root on this machine; then any folder in the project. The tag "" means "follow".
    private func rootPicker(_ site: Status.Site) -> some View {
        let store = model.settings
        let follow: String = switch (site.rootSource, site.rootPinnedHere, site.rootState) {
        case (_, true, _): String(localized: "sites.use_flockrc_root \(rootLabel(site.rootPinned))")
        case (.default, _, .notFound): String(localized: "sites.root_automatic_none")
        case (.default, _, _): String(localized: "sites.root_automatic_found \(rootLabel(site.root))")
        default: String(localized: "sites.root_automatic")
        }
        var folders = site.rootSuggestions
        if site.rootSource == .isolated, !folders.contains(site.root) {
            folders.append(site.root)
        }
        let help: String = switch site.rootSource {
        case .isolated: String(localized: "sites.root_isolated \(rootLabel(site.root))")
        case .project: String(localized: "sites.root_pinned \(rootLabel(site.root))")
        case .default: String(localized: "sites.flock_serves_the_first_of")
        }
        return Picker("sites.document_root", selection: Binding(
            get: { site.rootSource == .isolated ? site.root : "" },
            set: { root in
                if root == Self.chooseRootTag {
                    chooseRoot(site)
                } else {
                    Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(root: root)) } }
                }
            }
        )) {
            Text(follow).tag("")
            if !folders.isEmpty {
                Divider()
                ForEach(folders, id: \.self) { folder in
                    Text(rootLabel(folder)).tag(folder)
                }
            }
            Divider()
            Text("sites.choose_folder").tag(Self.chooseRootTag)
        }
        .labelsHidden()
        .fixedSize()
        .help(help)
    }

    /// Asks for a folder in the project and serves the site from it on this machine.
    private func chooseRoot(_ site: Status.Site) {
        let store = model.settings
        let start = URL(fileURLWithPath: site.path)
        guard let folder = SettingsStore.chooseDirectories(prompt: String(localized: "sites.server.choose"), multiple: false, in: start,
                                                           message: String(localized: "sites.choose_the_folder_with_the \(site.name)")).first else { return }
        guard let root = site.root(for: folder) else {
            store.error = String(localized: "sites.choose_folder_in_project_details \(folder.path) \(site.path)")
            return
        }
        Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(root: root)) } }
    }

    /// Why the site isn't served from its root, and a root set here over the project's.
    @ViewBuilder
    private func rootHints(_ site: Status.Site) -> some View {
        let store = model.settings
        switch site.rootState {
        case .notFound:
            Text("sites.no_public_www_web_or")
                .font(.caption).foregroundStyle(.orange)
                .fixedSize(horizontal: false, vertical: true)
        case .missing:
            Text("sites.root_missing \(site.root)")
                .font(.caption).foregroundStyle(.orange)
                .fixedSize(horizontal: false, vertical: true)
        case .empty:
            Text("sites.root_no_index \(rootLabel(site.root))")
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        case .ok:
            if site.rootSource == .isolated, site.rootPinnedHere {
                HStack(spacing: 4) {
                    Text("sites.root_shadowed \(rootLabel(site.root)) \(rootLabel(site.rootPinned))")
                    Button("sites.use_flockrc") {
                        Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(root: "")) } }
                    }
                    .buttonStyle(.link)
                }
                .font(.caption).foregroundStyle(.orange)
            }
        }
        Text("sites.folder_flock_serves_the_site \(Image(systemName: "pin"))")
            .font(.caption).foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
    }

    /// "src/www", or "Project folder" for ".".
    private func rootLabel(_ root: String) -> String { root == "." ? String(localized: "sites.project_folder_badge") : root }

    /// What runs the site's PHP (PHP-FPM or FrankenPHP); on FrankenPHP also its mode (classic or
    /// Laravel Octane), its state, Restart and its log.
    @ViewBuilder
    private func serverFields(_ site: Status.Site) -> some View {
        let store = model.settings
        field("sites.runs_on") {
            Picker("sites.server", selection: Binding(
                get: { site.server },
                set: { server in Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(server: server.rawValue)) } } }
            )) {
                Text("common.php_fpm").tag(Status.Server.fastcgi)
                Text("common.frankenphp").tag(Status.Server.frankenphp)
                    .disabled(!site.supportsFrankenPHP && site.server != .frankenphp)
            }
            .labelsHidden()
            .fixedSize()
        }

        if site.server == .frankenphp {
            field("debugger.mode") {
                Picker("debugger.mode", selection: Binding(
                    get: { site.worker },
                    set: { worker in Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(worker: worker.rawValue)) } } }
                )) {
                    Text("sites.classic").tag(Status.Worker.classic)
                    Text("common.laravel_octane").tag(Status.Worker.octane)
                        .disabled(!site.octaneAvailable && site.worker != .octane)
                }
                .labelsHidden()
                .fixedSize()
                .help(site.octaneAvailable ? "" : String(localized: "sites.octane_tooltip"))
            }

            frankenPHPState(site)

            HStack(spacing: 8) {
                Button("menu.restart") {
                    restarting = site.name
                    Task {
                        await store.perform { _ = try await $0.restartSite(site.name) }
                        restarting = nil
                    }
                }
                .disabled(site.frankenPHP?.state != .running || restarting == site.name)
                .help("sites.restart_frankenphp_tooltip")
                Button("sites.show_log") {
                    model.logs.requested = .service(frankenPHPLog(site))
                    openWindow.activating(WindowID.logs)
                }
                if restarting == site.name {
                    ProgressView().controlSize(.mini)
                }
            }
            .controlSize(.small)
        }

        Text(site.server == .frankenphp && !site.octaneAvailable
             ? "sites.frankenphp_needs_php_8_2_or"
             : "sites.frankenphp_needs_php_8_2")
            .font(.caption).foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
    }

    /// The FrankenPHP process: running, downloading its build, or why PHP-FPM serves the site.
    @ViewBuilder
    private func frankenPHPState(_ site: Status.Site) -> some View {
        if let franken = site.frankenPHP {
            switch franken.state {
            case .running:
                Text(runningText(site, franken))
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            case .installing:
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        if let progress = franken.progress {
                            ProgressView(value: progress).frame(width: 60)
                        } else {
                            ProgressView().controlSize(.mini)
                        }
                        Text(downloadingText(site, franken))
                    }
                    Text("sites.php_fpm_serves_the_site_until")
                        .foregroundStyle(.secondary)
                }
                .font(.caption)
                .fixedSize(horizontal: false, vertical: true)
            case .failed, .unavailable:
                VStack(alignment: .leading, spacing: 2) {
                    if !franken.problem.isEmpty {
                        Label(franken.problem, systemImage: "exclamationmark.triangle.fill")
                            .foregroundStyle(.orange)
                            .textSelection(.enabled)
                    }
                    Text("sites.php_fpm_serves_the_site")
                        .foregroundStyle(.secondary)
                }
                .font(.caption)
                .fixedSize(horizontal: false, vertical: true)
            case .unknown:
                EmptyView()
            }
        }
    }

    /// "Running FrankenPHP 1.13.0 with PHP 8.4.14 · worker mode".
    private func runningText(_ site: Status.Site, _ franken: Status.SiteFrankenPHP) -> String {
        let text = String(localized: "sites.frankenphp_running \(franken.version) \(franken.phpVersion)")
        return site.worker == .octane ? text + " · " + String(localized: "sites.worker_mode_macos") : text
    }

    /// "Downloading FrankenPHP for PHP 8.4… 42%"; without a percentage while the size is unknown.
    private func downloadingText(_ site: Status.Site, _ franken: Status.SiteFrankenPHP) -> String {
        guard let progress = franken.progress else { return String(localized: "sites.downloading_frankenphp \(site.php)") }
        return String(localized: "sites.downloading_frankenphp_for_php \(site.php) \(String(Int((progress * 100).rounded())))")
    }

    /// The name of the site's FrankenPHP log, a service log in the Logs window.
    private func frankenPHPLog(_ site: Status.Site) -> String {
        if let log = site.frankenPHP?.log, !log.isEmpty {
            return log
        }
        return "frankenphp-\(site.name)"
    }

    /// Laravel Reverb for the site: Run Reverb, then its state, the .env lines the app needs, Restart
    /// and its output.
    @ViewBuilder
    private func reverbFields(_ site: Status.Site) -> some View {
        let store = model.settings
        let address = reverbAddress(site)
        field("sites.reverb.run") {
            Toggle("sites.reverb.run", isOn: Binding(
                get: { site.reverb != nil },
                set: { on in Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(reverb: on)) } } }
            ))
            .labelsHidden()
            .toggleStyle(.switch)
            .controlSize(.small)
            .disabled(!site.reverbAvailable && site.reverb == nil)
        }

        if let reverb = site.reverb {
            reverbState(reverb, address: address)

            if !reverb.env.isEmpty {
                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: 8) {
                        Text("sites.reverb.env_needs")
                            .font(.caption).foregroundStyle(.secondary)
                        Button("common.copy") {
                            NSPasteboard.general.clearContents()
                            NSPasteboard.general.setString(reverb.env.joined(separator: "\n"), forType: .string)
                        }
                        .controlSize(.small)
                    }
                    Text(reverb.env.joined(separator: "\n"))
                        .font(.callout.monospaced())
                        .textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }

            HStack(spacing: 8) {
                Button("menu.restart") {
                    restartingReverb = site.name
                    Task {
                        await store.perform { _ = try await $0.restartReverb(site.name) }
                        restartingReverb = nil
                    }
                }
                .disabled(reverb.state != .running || restartingReverb == site.name)
                .help("sites.reverb.restart_tooltip")
                if restartingReverb == site.name {
                    ProgressView().controlSize(.mini)
                }
            }
            .controlSize(.small)

            ServerOutput(log: reverb.log.isEmpty ? "reverb-\(site.name)" : reverb.log, live: reverb.state == .running)

            Text("sites.reverb.footnote")
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        } else {
            (site.reverbAvailable ? Text("sites.reverb.explanation \(address)") : Text("sites.reverb.install"))
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    /// Where browsers reach the site's Reverb server: the site's address as a WebSocket one,
    /// wss://acme.test/app (with its port when Flock serves another than 443), ws:// without HTTPS.
    private func reverbAddress(_ site: Status.Site) -> String {
        if let url = site.url {
            if url.hasPrefix("https://") { return "wss://\(url.dropFirst(8))/app" }
            if url.hasPrefix("http://") { return "ws://\(url.dropFirst(7))/app" }
        }
        return "\(site.secured ? "wss" : "ws")://\(site.domain)/app"
    }

    /// The Reverb server: running at its address, why it stopped, or why it can't run.
    @ViewBuilder
    private func reverbState(_ reverb: Status.SiteReverb, address: String) -> some View {
        switch reverb.state {
        case .running:
            HStack(spacing: 6) {
                StateDot(color: StateDot.running)
                Text("sites.reverb.running_at \(address)").textSelection(.enabled)
            }
        case .failed:
            HStack(spacing: 6) {
                StateDot(color: StateDot.failed)
                Text(reverb.problem.isEmpty ? String(localized: "sites.server.failed") : reverb.problem)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }
        case .unavailable:
            if !reverb.problem.isEmpty {
                Label(reverb.problem, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption).foregroundStyle(.orange)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }
        case .unknown:
            EmptyView()
        }
    }

    /// Notes when versions disagree: an isolation over the project's pin, .flockrc over .nvmrc.
    @ViewBuilder
    private func hints(_ site: Status.Site) -> some View {
        let store = model.settings
        if site.phpSource == .isolated, !site.phpPinned.isEmpty, site.phpPinned != site.php {
            HStack(spacing: 4) {
                Text("sites.php_version_is_isolated_here \(site.php) \(site.phpPinned) \(fileName(site.phpFile))")
                Button("sites.use_file \(fileName(site.phpFile))") {
                    Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(php: "")) } }
                }
                .buttonStyle(.link)
            }
            .font(.caption).foregroundStyle(.orange)
        }
        if site.nodeSource == .isolated, !site.nodePinned.isEmpty, major(site.nodePinned) != major(site.node ?? "") {
            HStack(spacing: 4) {
                Text("sites.node_version_is_isolated_here \(site.node ?? "") \(site.nodePinned) \(fileName(site.nodeFile))")
                Button("sites.use_file \(fileName(site.nodeFile))") {
                    Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(node: "")) } }
                }
                .buttonStyle(.link)
            }
            .font(.caption).foregroundStyle(.orange)
        }
        if !site.nodeShadowed.isEmpty {
            Text("sites.node_wins \(site.nodePinned) \(site.nodeShadowed)")
                .font(.caption).foregroundStyle(.orange)
        }
    }

    /// The site's share (`flock share`): Share… when there is none, then its progress, its URL with
    /// a QR code while it runs, or why it failed.
    @ViewBuilder
    private func shareFields(_ site: Status.Site) -> some View {
        let store = model.settings
        let stop: () -> Void = { _ = Task { await store.perform { try await $0.stopShare(site: site.name) } } }
        if let share = site.share {
            switch share.state {
            case .building, .starting, .unknown:
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text(share.state == .building ? "sites.share.building" : "sites.share.starting")
                    Spacer(minLength: 8)
                    Button("sites.share.stop") { stop() }
                }
                .font(.callout)
            case .running:
                runningShare(site, share, stop: stop)
            case .failed:
                VStack(alignment: .leading, spacing: 8) {
                    Label(share.error ?? String(localized: "sites.sharing_failed"), systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                        .textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                    HStack(spacing: 8) {
                        Button("setup.try_again") { startShare(site.name, allow: share.allow, debug: share.debug) }
                        Button("sites.share.dismiss") { stop() }
                    }
                }
                .font(.callout)
            }
        } else {
            VStack(alignment: .leading, spacing: 6) {
                Button("sites.share.share_ellipsis") { sharing = site.name }
                    .disabled(site.url == nil)
                Text("sites.share.explanation")
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    /// A running share: its URL, Copy, the QR code, when it ends, who may open it, and Rebuild / Stop.
    private func runningShare(_ site: Status.Site, _ share: ShareStatus, stop: @escaping () -> Void) -> some View {
        let store = model.settings
        let url = share.url ?? ""
        return VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 8) {
                if let link = URL(string: url) {
                    Link(url, destination: link)
                        .font(.callout)
                        .lineLimit(1).truncationMode(.middle)
                        .textSelection(.enabled)
                }
                Button("common.copy") {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(url, forType: .string)
                }
                .controlSize(.small)
            }
            if let data = share.qrPNG, let image = NSImage(data: data) {
                Image(nsImage: image)
                    .resizable()
                    .interpolation(.high)
                    .frame(width: 160, height: 160)
                    .accessibilityLabel("sites.qr_code_of_url \(url)")
                    .help("sites.share.qr_tooltip")
                    .contextMenu {
                        Button("sites.copy_image") { copyQR(data, image) }
                        Button("sites.save_image") { saveQR(data, site: site.name) }
                    }
                    .onDrag { qrProvider(data, site: site.name) }
            }
            VStack(alignment: .leading, spacing: 2) {
                if let expires = share.expiresAt {
                    Text("sites.share.ends_at \(expires.formatted(Date.FormatStyle(date: .omitted, time: .shortened).locale(AppLanguage.locale)))")
                }
                if !share.allow.isEmpty {
                    Text("sites.share.only_for \(share.allow.joined(separator: ", "))")
                }
                if share.debug {
                    Text("sites.share.debug_on")
                }
            }
            .font(.caption).foregroundStyle(.secondary)
            if let warning = share.warning {
                Label(warning, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption).foregroundStyle(.orange)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let error = share.rebuildError {
                Label(error, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption).foregroundStyle(.orange)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack(spacing: 8) {
                if share.canRebuild {
                    Button(share.rebuilding ? "sites.share.rebuilding" : "sites.rebuild_assets") {
                        Task { await store.perform { _ = try await $0.rebuildShare(site: site.name) } }
                    }
                    .disabled(share.rebuilding)
                    .help("sites.runs_npm_run_build_again")
                }
                Button("sites.stop_sharing") { stop() }
            }
        }
    }

    private func startShare(_ site: String, allow: [String], debug: Bool) {
        Task { await model.settings.perform { _ = try await $0.startShare(site: site, allow: allow, debug: debug) } }
    }

    /// Copies the share's QR code as PNG, and as TIFF for apps that only paste that.
    private func copyQR(_ data: Data, _ image: NSImage) {
        let item = NSPasteboardItem()
        item.setData(data, forType: .png)
        if let tiff = image.tiffRepresentation {
            item.setData(tiff, forType: .tiff)
        }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.writeObjects([item])
    }

    /// Asks where to save the share's QR code, then writes the PNG there.
    private func saveQR(_ data: Data, site: String) {
        let panel = NSSavePanel()
        panel.nameFieldStringValue = "\(site)-share-qr.png"
        panel.allowedContentTypes = [.png]
        guard panel.runModal() == .OK, let url = panel.url else { return }

        do {
            try data.write(to: url)
        } catch {
            model.settings.error = error.localizedDescription
        }
    }

    /// The QR code for dragging out: a .png file, so Finder, Mail and Slack all take it.
    private func qrProvider(_ data: Data, site: String) -> NSItemProvider {
        let file = FileManager.default.temporaryDirectory.appendingPathComponent("\(site)-share-qr.png")
        if (try? data.write(to: file)) != nil, let provider = NSItemProvider(contentsOf: file) {
            return provider
        }
        return NSItemProvider(item: data as NSData, typeIdentifier: UTType.png.identifier)
    }

    /// The project uses Laravel Boost, and Flock's guideline is missing from it or outdated.
    private func boost(_ site: Status.Site) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(site.boost == .missing
                 ? "sites.boost_missing"
                 : "sites.boost_outdated")
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            Button(site.boost == .missing ? "sites.boost_add" : "sites.boost_update") {
                confirm = ConfirmAction(
                    title: site.boost == .missing ? String(localized: "sites.boost_add_question \(site.name)") : String(localized: "sites.boost_update_question \(site.name)"),
                    button: site.boost == .missing ? String(localized: "common.add") : String(localized: "common.update"),
                    message: String(localized: "sites.writes_ai_guidelines_flock_md \(site.path)"),
                    run: { [name = site.name] client in try await client.addBoostGuideline(site: name) })
            }
            .buttonStyle(.link)
        }
        .font(.caption)
    }

    private func pinMenu(_ site: Status.Site) -> some View {
        let nodeMajor = major(site.node ?? "")
        let nodeFile = site.nodePinnedHere ? fileName(site.nodeFile) : ".nvmrc"
        // A Node.js app has no PHP version or document root to pin.
        let canPinPHP = !site.isNode && !(site.phpSource == .project && site.phpPinnedHere)
        let canPinNode = !nodeMajor.isEmpty && !(site.nodeSource == .project && site.nodePinnedHere)
        let canPinRoot = !site.isNode && (site.rootState == .ok || site.rootState == .empty) && site.rootSource != .project

        return Menu {
            if canPinPHP {
                Button("sites.pin_php_item \(site.php)") {
                    confirm = ConfirmAction(
                        title: String(localized: "sites.pin_php_question \(site.php) \(site.name)"), button: String(localized: "sites.pin"),
                        message: String(localized: "sites.writes_php_version_to_flockrc \(site.php) \(site.path) \(site.php)"),
                        run: { [name = site.name, php = site.php] client in try await client.pin(site: name, tool: "php", version: php) })
                }
            }
            if canPinNode {
                Button("sites.pin_node_item \(nodeMajor)") {
                    confirm = ConfirmAction(
                        title: String(localized: "sites.pin_node_question \(nodeMajor) \(site.name)"), button: String(localized: "sites.pin"),
                        message: String(localized: "sites.writes_version_to_file_in \(nodeMajor) \(nodeFile) \(site.path) \(nodeMajor)"),
                        run: { [name = site.name] client in try await client.pin(site: name, tool: "node", version: nodeMajor) })
                }
            }
            if canPinRoot {
                Button("sites.pin_root_item \(rootLabel(site.root))") {
                    confirm = ConfirmAction(
                        title: String(localized: "sites.pin_root_question \(rootLabel(site.root)) \(site.name)"), button: String(localized: "sites.pin"),
                        message: String(localized: "sites.writes_root_value_to_flockrc \(site.root) \(site.path) \(rootLabel(site.root))"),
                        run: { [name = site.name, root = site.root] client in try await client.pin(site: name, tool: "root", version: root) })
                }
            }
            if site.phpPinnedHere || site.nodePinnedHere || site.rootPinnedHere {
                Divider()
            }
            if site.phpPinnedHere {
                Button("sites.unpin_php_item \(fileName(site.phpFile))") {
                    confirm = ConfirmAction(
                        title: String(localized: "sites.unpin_php_question \(site.name)"), button: String(localized: "sites.unpin"),
                        message: String(localized: "sites.removes_the_php_version_from \(site.phpFile)"),
                        run: { [name = site.name] client in try await client.unpin(site: name, tool: "php") })
                }
            }
            if site.nodePinnedHere {
                Button("sites.unpin_node_item \(fileName(site.nodeFile))") {
                    confirm = ConfirmAction(
                        title: String(localized: "sites.unpin_node_question \(site.name)"), button: String(localized: "sites.unpin"),
                        message: String(localized: "sites.removes_the_node_js_version \(site.path)"),
                        run: { [name = site.name] client in try await client.unpin(site: name, tool: "node") })
                }
            }
            if site.rootPinnedHere {
                Button("sites.unpin_root_item") {
                    confirm = ConfirmAction(
                        title: String(localized: "sites.unpin_root_question \(site.name)"), button: String(localized: "sites.unpin"),
                        message: String(localized: "sites.removes_root_from_flockrc_in \(site.path)"),
                        run: { [name = site.name] client in try await client.unpin(site: name, tool: "root") })
                }
            }
        } label: {
            Image(systemName: "pin")
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .help("sites.pin_menu_tooltip")
        .disabled(!canPinPHP && !canPinNode && !canPinRoot && !site.phpPinnedHere && !site.nodePinnedHere && !site.rootPinnedHere)
    }

    private func fileName(_ path: String) -> String { URL(fileURLWithPath: path).lastPathComponent }

    /// A main git checkout, which can have worktrees (in a worktree, .git is a file).
    private func isGitCheckout(_ path: String) -> Bool {
        var isDirectory: ObjCBool = false
        return FileManager.default.fileExists(atPath: (path as NSString).appendingPathComponent(".git"), isDirectory: &isDirectory) && isDirectory.boolValue
    }

    /// "22.11.0" → "22"; pins and isolations are majors.
    private func major(_ version: String) -> String { String(version.split(separator: ".").first ?? "") }
}

/// A change waiting for confirmation: a pin or unpin (it changes files in the project), or a removal.
private struct ConfirmAction: Identifiable, Sendable {
    let title: String
    let button: String
    var destructive = false
    let message: String
    let run: @Sendable (FlockClient) async throws -> Status
    var id: String { title }
}

/// Asks who may open a share before it starts (anyone with the link, or only the emails listed) and
/// whether visitors see the app's debug mode.
private struct ShareSheet: View {
    let site: String
    let share: ([String], Bool) -> Void
    let cancel: () -> Void
    private let allowState = State<String>(initialValue: "")
    private let debugState = State<Bool>(initialValue: false)

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("sites.share.title \(site)").font(.headline)
            VStack(alignment: .leading, spacing: 4) {
                Text("sites.share.allow_header")
                TextField("sites.share.allow_placeholder", text: allowState.projectedValue)
                    .textFieldStyle(.roundedBorder)
            }
            Text("sites.share.allow_explanation")
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            VStack(alignment: .leading, spacing: 4) {
                Toggle("sites.share.show_errors", isOn: debugState.projectedValue)
                Text("sites.share.show_errors_explanation")
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack {
                Spacer()
                Button("common.cancel", role: .cancel) { cancel() }
                    .keyboardShortcut(.cancelAction)
                Button("sites.share.share") { share(ShareStatus.parseAllow(allowState.wrappedValue), debugState.wrappedValue) }
                    .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 420)
    }
}

private extension SitesPane {
    func openNewAppSheet() {
        guard !creatingAppState.wrappedValue else { return }
        creatingAppState.wrappedValue = true
        Task {
            defer { creatingAppState.wrappedValue = false }
            do {
                let list = try await model.client.appTemplates()
                newAppTemplatesState.wrappedValue = list.isEmpty ? AppTemplate.builtInDefaults : list
            } catch FlockError.server(status: 404, message: _) {
                newAppTemplatesState.wrappedValue = AppTemplate.builtInDefaults
            } catch {
                model.settings.error = error.localizedDescription
            }
        }
    }

    func createApp(_ req: CreateAppRequest) {
        creatingAppState.wrappedValue = true
        let targetPath = URL(fileURLWithPath: req.parentDir).appendingPathComponent(req.name).path
        let existedBefore = model.status?.sites.contains { $0.path == targetPath } ?? false
        let fallbackName = appSlug(req.name)
        Task {
            defer { creatingAppState.wrappedValue = false }
            let outcome = await model.settings.perform { client in
                _ = try await client.createApp(req)
            }
            switch outcome {
            case .succeeded:
                let created = model.status?.sites.first { $0.path == targetPath }
                selectionState.wrappedValue = created?.name ?? fallbackName
            case .unknown:
                if model.status?.sites.first(where: { $0.path == targetPath }) == nil {
                    await model.refresh()
                }
                if !existedBefore, let created = model.status?.sites.first(where: { $0.path == targetPath }) {
                    selectionState.wrappedValue = created.name
                }
            case .failed:
                break
            }
        }
    }
}

/// Hostname label matching `config.SiteName` on the daemon.
private func appSlug(_ name: String) -> String {
    let mapped = name.lowercased().unicodeScalars.map { s -> Character in
        ((s >= "a" && s <= "z") || (s >= "0" && s <= "9") || s == "-") ? Character(s) : "-"
    }
    return String(mapped).trimmingCharacters(in: CharacterSet(charactersIn: "-"))
}

private struct NewAppSheet: View {
    let templates: [AppTemplate]
    let tld: String
    let hasRunningDb: Bool
    let create: (CreateAppRequest) -> Void
    let cancel: () -> Void

    private let nameState = State<String>(initialValue: "")
    private let templateState: State<String>
    private let folderState: State<String>
    private let dbState = State<Bool>(initialValue: false)
    private let dbServiceState = State<String>(initialValue: "")
    private let securedState = State<Bool>(initialValue: false)

    init(
        templates: [AppTemplate],
        defaultFolder: String,
        tld: String,
        hasRunningDb: Bool,
        create: @escaping (CreateAppRequest) -> Void,
        cancel: @escaping () -> Void
    ) {
        self.templates = templates
        self.tld = tld
        self.hasRunningDb = hasRunningDb
        self.create = create
        self.cancel = cancel
        let initialTemplate = templates.first { $0.name.caseInsensitiveCompare("php") == .orderedSame }?.name
            ?? templates.first?.name ?? "php"
        self.templateState = State(initialValue: initialTemplate)
        self.folderState = State(initialValue: defaultFolder)
    }

    private var selectedTemplate: AppTemplate {
        templates.first { $0.name == templateState.wrappedValue } ?? AppTemplate(name: "php")
    }

    var body: some View {
        let tmpl = selectedTemplate
        let wantsDb = tmpl.requiresDb || dbState.wrappedValue
        let rawName = nameState.wrappedValue.trimmingCharacters(in: .whitespacesAndNewlines)
        let rawFolder = folderState.wrappedValue.trimmingCharacters(in: .whitespacesAndNewlines)
        let slug = appSlug(rawName)
        let valid = !slug.isEmpty && !rawFolder.isEmpty
        let scheme = securedState.wrappedValue ? "https" : "http"

        VStack(alignment: .leading, spacing: 12) {
            Text("New application").font(.headline)

            VStack(alignment: .leading, spacing: 4) {
                Text("Name")
                TextField("my-app", text: nameState.projectedValue)
                    .textFieldStyle(.roundedBorder)
            }

            VStack(alignment: .leading, spacing: 4) {
                Text("Template")
                Picker("Template", selection: templateState.projectedValue) {
                    ForEach(templates) { item in
                        Text(item.name).tag(item.name)
                    }
                }
                .labelsHidden()
            }

            VStack(alignment: .leading, spacing: 4) {
                Text("Folder")
                HStack(spacing: 8) {
                    TextField("Parent folder", text: folderState.projectedValue)
                        .textFieldStyle(.roundedBorder)
                    Button("Browse…") {
                        let initial = rawFolder.isEmpty ? nil : URL(fileURLWithPath: rawFolder)
                        if let chosen = SettingsStore.chooseDirectories(prompt: "Select", multiple: false, in: initial).first {
                            folderState.wrappedValue = chosen.path
                        }
                    }
                }
            }

            VStack(alignment: .leading, spacing: 6) {
                Toggle("Create MySQL or MariaDB database", isOn: Binding(
                    get: { wantsDb },
                    set: { dbState.wrappedValue = $0 }
                ))
                .disabled(tmpl.requiresDb)

                if wantsDb {
                    Picker("Database service", selection: dbServiceState.projectedValue) {
                        Text("Auto (MySQL or MariaDB)").tag("")
                        Text("MySQL").tag("mysql")
                        Text("MariaDB").tag("mariadb")
                    }
                    .labelsHidden()

                    if !hasRunningDb {
                        Text(tmpl.requiresDb
                             ? "This template requires MySQL or MariaDB to be running (start one in Services)."
                             : "No running MySQL or MariaDB service detected (start one in Services).")
                            .font(.caption)
                            .foregroundStyle(.orange)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
            }

            Toggle("Serve over HTTPS", isOn: securedState.projectedValue)

            Text(valid
                 ? "\(URL(fileURLWithPath: rawFolder).appendingPathComponent(rawName).path) → \(scheme)://\(slug).\(tld)"
                 : "Choose a name and folder to serve at \(scheme)://<name>.\(tld).")
                .font(.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)

            HStack {
                Spacer()
                Button("Cancel", role: .cancel) { cancel() }
                    .keyboardShortcut(.cancelAction)
                Button("Create") {
                    create(CreateAppRequest(
                        name: rawName,
                        parentDir: rawFolder,
                        type: tmpl.name,
                        db: wantsDb,
                        dbService: wantsDb && !dbServiceState.wrappedValue.isEmpty ? dbServiceState.wrappedValue : nil,
                        secured: securedState.wrappedValue
                    ))
                }
                .disabled(!valid)
                .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 440)
    }
}
