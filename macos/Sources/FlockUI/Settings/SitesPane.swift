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
    /// The site the Share sheet is open for.
    private let sharingState = State<String?>(initialValue: nil)
    private var sharing: String? {
        get { sharingState.wrappedValue }
        nonmutating set { sharingState.wrappedValue = newValue }
    }

    /// The Add Proxy alert is open; its name and upstream typed so far.
    private let addingProxyState = State<Bool>(initialValue: false)
    private let newProxyNameState = State<String>(initialValue: "")
    private let newProxyUpstreamState = State<String>(initialValue: "")
    /// The proxy whose upstream is being changed, and the upstream typed so far.
    private let editingUpstreamState = State<String?>(initialValue: nil)
    private var editingUpstream: String? {
        get { editingUpstreamState.wrappedValue }
        nonmutating set { editingUpstreamState.wrappedValue = newValue }
    }
    private let upstreamDraftState = State<String>(initialValue: "")
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
                PaneHeader(title: "Sites", subtitle: "Sites you added one by one, plus every folder inside your project folders.")
                if creatingAppState.wrappedValue {
                    ProgressView().controlSize(.small).padding(.bottom, 8)
                }
                Button("New App…") { openNewAppSheet() }
                    .disabled(creatingAppState.wrappedValue)
                    .help("Create a new project from a template and link it as a site")
                    .padding(.bottom, 6)
                Button("Add Proxy…") { addingProxyState.wrappedValue = true }
                    .help("Serve a server of its own, e.g. a Node.js dev server on a port, at a .\(model.status?.dns.tld ?? "test") domain")
                    .padding(.bottom, 6)
                Button("Add Site…") {
                    for url in SettingsStore.chooseDirectories(prompt: "Add", multiple: false) {
                        Task { await store.perform { _ = try await $0.linkSite(path: url.path) } }
                    }
                }
                .padding(.trailing, 20)
                .padding(.bottom, 6)
            }

            if sites.isEmpty && proxies.isEmpty {
                ContentUnavailableView("No sites", systemImage: "globe",
                                       description: Text("Add a single site here, or add a project folder in General."))
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
                            Section("Proxies") {
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
            Button("Cancel", role: .cancel) {}
        } message: { action in
            Text(action.message)
        }
        .alert("Add a domain to \(adding ?? "")", isPresented: Binding(get: { adding != nil }, set: { if !$0 { adding = nil } })) {
            TextField("tenant1, a.b or \(adding ?? "app")-2.\(model.status?.dns.tld ?? "test")", text: newDomainState.projectedValue)
            Button("Add") {
                let name = adding ?? ""
                let domain = newDomainState.wrappedValue.trimmingCharacters(in: .whitespaces)
                newDomainState.wrappedValue = ""
                if !name.isEmpty, !domain.isEmpty {
                    Task { await store.perform { _ = try await $0.addDomain(site: name, domain: domain) } }
                }
            }
            Button("Cancel", role: .cancel) { newDomainState.wrappedValue = "" }
        } message: {
            Text(domainHelp)
        }
        .alert("Add a proxy", isPresented: addingProxyState.projectedValue) {
            TextField("Name, e.g. my-app", text: newProxyNameState.projectedValue)
            TextField("Port, host:port or URL, e.g. 3000", text: newProxyUpstreamState.projectedValue)
            Button("Add") {
                let name = newProxyNameState.wrappedValue.trimmingCharacters(in: .whitespaces)
                let upstream = newProxyUpstreamState.wrappedValue.trimmingCharacters(in: .whitespaces)
                newProxyNameState.wrappedValue = ""
                newProxyUpstreamState.wrappedValue = ""
                if !name.isEmpty, !upstream.isEmpty {
                    selectionState.wrappedValue = name
                    Task { await store.perform { _ = try await $0.proxy(name: name, upstream: upstream) } }
                }
            }
            Button("Cancel", role: .cancel) {
                newProxyNameState.wrappedValue = ""
                newProxyUpstreamState.wrappedValue = ""
            }
        } message: {
            Text("Flock serves <name>.\(model.status?.dns.tld ?? "test") from a server of its own, e.g. a Next.js, Nuxt or Vite dev server. A port means 127.0.0.1.")
        }
        .alert("Change the upstream of \(editingUpstream ?? "")", isPresented: Binding(get: { editingUpstream != nil }, set: { if !$0 { editingUpstream = nil } })) {
            TextField("Port, host:port or URL", text: upstreamDraftState.projectedValue)
            Button("Change") {
                let name = editingUpstream ?? ""
                let upstream = upstreamDraftState.wrappedValue.trimmingCharacters(in: .whitespaces)
                if !name.isEmpty, !upstream.isEmpty {
                    Task { await store.perform { _ = try await $0.proxy(name: name, upstream: upstream) } }
                }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("A port means 127.0.0.1; give a host and port (localhost:5173) or a URL (https://127.0.0.1:8443) for anything else.")
        }
        // A flock://secure link (AppModel.handle): nothing changes until the user confirms here.
        .alert(linkRequest?.title ?? "", isPresented: Binding(get: { linkRequest != nil }, set: { if !$0 { model.pendingSecure = nil } }),
               presenting: linkRequest) { request in
            if let name = request.confirmation {
                Button("Secure") {
                    Task { await store.perform { _ = try await $0.updateSite(name, SiteChange(secured: true)) } }
                }
                Button("Cancel", role: .cancel) {}
            } else {
                Button("OK") {}
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
            return "Every subdomain up to \(depth) levels below the site already works. Add a full name like \(adding ?? "app")-2.\(dns?.tld ?? "test") for an alias, or a subdomain (tenant1, a.b) so it is listed here and also resolves on Windows machines without wildcard DNS."
        }
        return "Wildcard subdomains don't work here (\(dns?.wildcardProblem ?? "")), so add each subdomain you use (tenant1, a.b). A full name adds an alias."
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
                Image(systemName: "exclamationmark.triangle.fill").font(.caption).foregroundStyle(.orange)
                    .help(problem)
            }
            if site.shared {
                Badge(text: "Shared", color: .green)
                    .help("Shared at \(site.share?.url ?? "")")
            }
            if site.secured {
                Image(systemName: "lock.fill").font(.caption).foregroundStyle(.secondary)
                    .help("Served over HTTPS")
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
                Text("\(proxy.domain) → \(upstreamLabel(proxy.upstream))").font(.caption).foregroundStyle(.secondary)
                    .lineLimit(1).truncationMode(.middle)
            }
            Spacer(minLength: 4)
            if let problem = proxy.problem {
                Image(systemName: "exclamationmark.triangle.fill").font(.caption).foregroundStyle(.orange)
                    .help(problem)
            }
            Circle()
                .fill(proxy.running ? Color.green : Color.secondary.opacity(0.5))
                .frame(width: 6, height: 6)
                .help(proxy.running ? proxy.server ?? "Running" : "Not running")
            if proxy.secured {
                Image(systemName: "lock.fill").font(.caption).foregroundStyle(.secondary)
                    .help("Served over HTTPS")
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
                        Badge(text: "Proxy", color: .secondary)
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
                        .help("Open \(proxy.url ?? proxy.domain)")
                        .disabled(proxy.url == nil)
                    Button {
                        confirm = ConfirmAction(
                            title: "Remove \(proxy.name)?", button: "Remove", destructive: true,
                            message: "Flock stops serving \(proxy.domain). The server at \(proxy.upstream) keeps running.",
                            run: { [name = proxy.name] client in try await client.removeProxy(name) })
                    } label: { Image(systemName: "minus.circle") }
                        .help("Remove proxy")
                }
                .buttonStyle(.borderless)
            }
            if let problem = proxy.problem {
                Label(problem, systemImage: "exclamationmark.triangle.fill")
                    .font(.callout).foregroundStyle(.orange)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }

            section("Proxy") {
                field("Server") {
                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 6) {
                            Circle()
                                .fill(proxy.running ? Color.green : Color.secondary.opacity(0.5))
                                .frame(width: 7, height: 7)
                            Text(proxy.running ? proxy.server ?? "Running" : "Not running")
                        }
                        if !proxy.running {
                            Text("Nothing answers at \(upstreamLabel(proxy.upstream)) yet.")
                                .font(.caption).foregroundStyle(.secondary)
                        }
                    }
                }
                field("HTTPS") {
                    Toggle("HTTPS", isOn: Binding(
                        get: { proxy.secured },
                        set: { on in Task { await store.perform { _ = try await $0.secureProxy(proxy.name, on) } } }
                    ))
                    .labelsHidden()
                    .toggleStyle(.switch)
                    .controlSize(.small)
                }
                field("Upstream") {
                    HStack(spacing: 8) {
                        Text(proxy.upstream).font(.callout.monospaced()).textSelection(.enabled)
                        Button("Change…") {
                            upstreamDraftState.wrappedValue = upstreamLabel(proxy.upstream)
                            editingUpstream = proxy.name
                        }
                        .controlSize(.small)
                    }
                }
                Text("Every request passes to the upstream with its Host header, WebSocket upgrades (hot reload) included. Flock doesn't start the server, so start it as usual. Until it answers, \(proxy.domain) shows a page saying so.")
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
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

    /// What a site's list row warns about: the site, its document root, one of its domains, or its FrankenPHP.
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
        if site.server == .frankenphp, let franken = site.frankenPHP, franken.state == .failed || franken.state == .unavailable,
           !franken.problem.isEmpty {
            return franken.problem
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
                            Badge(text: "Project folder", color: .secondary)
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
                        .help("Open \(site.url ?? site.domain)")
                        .disabled(site.url == nil)
                    Button { reveal(site.path) } label: { Image(systemName: "folder") }
                        .help("Show in Finder")
                    Button { extensionsState.wrappedValue = site } label: { Image(systemName: "puzzlepiece.extension") }
                        .help("PHP extensions")
                    pinMenu(site)
                    if !site.parked {
                        Button {
                            confirm = ConfirmAction(
                                title: "Remove \(site.name)?", button: "Remove", destructive: true,
                                message: "Flock stops serving \(site.domain). The project in \(site.path) is not touched.",
                                run: { [name = site.name] client in try await client.unlinkSite(name) })
                        } label: { Image(systemName: "minus.circle") }
                            .help("Remove site")
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

            section("Site") {
                field("HTTPS") {
                    Toggle("HTTPS", isOn: Binding(
                        get: { site.secured },
                        set: { on in Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(secured: on)) } } }
                    ))
                    .labelsHidden()
                    .toggleStyle(.switch)
                    .controlSize(.small)
                }
                field("Domains") {
                    VStack(alignment: .leading, spacing: 3) {
                        ForEach(site.domains) { domain in
                            HStack(spacing: 4) {
                                if let problem = domain.problem {
                                    Label("\(domain.host): \(problem)", systemImage: "exclamationmark.triangle.fill")
                                        .foregroundStyle(.orange)
                                        .fixedSize(horizontal: false, vertical: true)
                                } else if let url = domain.url.flatMap(URL.init(string:)) {
                                    Link(domain.alias ? "\(domain.host) (alias)" : domain.host, destination: url)
                                } else {
                                    Text(domain.host)
                                }
                                Button {
                                    confirm = ConfirmAction(
                                        title: "Remove \(domain.host)?", button: "Remove", destructive: true,
                                        message: "\(domain.host) is no longer served by \(site.name).",
                                        run: { [name = site.name, entry = domain.entry] client in try await client.removeDomain(site: name, domain: entry) })
                                } label: { Image(systemName: "minus.circle") }
                                    .buttonStyle(.borderless)
                                    .help("Remove \(domain.host)")
                            }
                        }
                        Button("Add Domain…") { adding = site.name }
                            .buttonStyle(.link)
                            .help("Extra domains: subdomains and aliases")
                    }
                    .font(.callout)
                }
                if let dns = model.status?.dns, !dns.wildcard, !dns.wildcardProblem.isEmpty {
                    Text("Subdomains only work when you add them here: \(dns.wildcardProblem).")
                        .font(.caption).foregroundStyle(.orange)
                        .fixedSize(horizontal: false, vertical: true)
                }
                field("Root") {
                    rootPicker(site)
                }
                rootHints(site)
            }

            section("Share") {
                shareFields(site)
            }

            section("Versions") {
                field("PHP") {
                    versionPicker(label: "PHP", current: site.php, source: site.phpSource, pinned: site.phpPinned, file: site.phpFile,
                                  defaultVersion: store.config?.defaultPHP ?? "", installed: phpVersions) { version in
                        Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(php: version)) } }
                    }
                }
                field("Node.js") {
                    versionPicker(label: "Node", current: major(site.node ?? ""), source: site.nodeSource, pinned: major(site.nodePinned), file: site.nodeFile,
                                  defaultVersion: store.config?.defaultNode ?? "", installed: nodeMajors) { version in
                        Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(node: version)) } }
                    }
                }
                hints(site)
                Text("A version picked here applies on this machine only. To set it for everyone working on the project, pin it (\(Image(systemName: "pin"))): PHP goes into .flockrc, Node.js into .nvmrc.")
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            section("Server") {
                serverFields(site)
            }

            if site.boost == .missing || site.boost == .outdated {
                section("Laravel Boost") {
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
    private func section<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(title).font(.subheadline.weight(.semibold))
            content()
        }
    }

    /// A labelled setting: the label in a fixed column, so the controls line up.
    private func field<Content: View>(_ label: String, @ViewBuilder content: () -> Content) -> some View {
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
        let follow = !pinned.isEmpty ? "Use \(fileName(file)) (\(label) \(pinned))"
            : defaultVersion.isEmpty ? "Default" : "Default (\(label) \(defaultVersion))"
        let versions = source == .isolated && !current.isEmpty && !installed.contains(current) ? installed + [current] : installed
        let help: String = switch source {
        case .isolated: "\(label) \(current) is set for this site on this machine (isolated)."
        case .project: "The project pins \(label) \(pinned) in \(fileName(file))."
        case .default: "The site follows Flock's default \(label) version."
        }
        return Picker(label, selection: Binding(
            get: { source == .isolated ? current : "" },
            set: { change($0) }
        )) {
            Text(follow).tag("")
            Divider()
            ForEach(versions, id: \.self) { version in
                Text("\(label) \(version)").tag(version)
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
        case (_, true, _): "Use .flockrc (\(rootLabel(site.rootPinned)))"
        case (.default, _, .notFound): "Automatic (none found)"
        case (.default, _, _): "Automatic (\(rootLabel(site.root)))"
        default: "Automatic"
        }
        var folders = site.rootSuggestions
        if site.rootSource == .isolated, !folders.contains(site.root) {
            folders.append(site.root)
        }
        let help: String = switch site.rootSource {
        case .isolated: "\(rootLabel(site.root)) is set for this site on this machine."
        case .project: "The project sets \(rootLabel(site.root)) in .flockrc."
        case .default: "Flock serves the first of public/, www/, web/ and webroot/ with an index.php, else the project folder."
        }
        return Picker("Document root", selection: Binding(
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
            Text("Choose Folder…").tag(Self.chooseRootTag)
        }
        .labelsHidden()
        .fixedSize()
        .help(help)
    }

    /// Asks for a folder in the project and serves the site from it on this machine.
    private func chooseRoot(_ site: Status.Site) {
        let store = model.settings
        let start = URL(fileURLWithPath: site.path)
        guard let folder = SettingsStore.chooseDirectories(prompt: "Choose", multiple: false, in: start,
                                                           message: "Choose the folder with the app's index.php in \(site.name).").first else { return }
        guard let root = site.root(for: folder) else {
            store.error = "\(folder.path) isn't in \(site.path). The document root must be a folder in the project."
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
            Text("No public/, www/, web/ or webroot/ with an index.php was found, so Flock shows a page instead of the site. Choose the folder with the app's index.php.")
                .font(.caption).foregroundStyle(.orange)
                .fixedSize(horizontal: false, vertical: true)
        case .missing:
            Text("Choose the folder with the app's index.php, or create \(site.root).")
                .font(.caption).foregroundStyle(.orange)
                .fixedSize(horizontal: false, vertical: true)
        case .empty:
            Text("\(rootLabel(site.root)) has no index.php or index.html, so Flock shows a page instead of the site until it does.")
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        case .ok:
            if site.rootSource == .isolated, site.rootPinnedHere {
                HStack(spacing: 4) {
                    Text("\(rootLabel(site.root)) is set here; the project sets \(rootLabel(site.rootPinned)) in .flockrc.")
                    Button("Use .flockrc") {
                        Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(root: "")) } }
                    }
                    .buttonStyle(.link)
                }
                .font(.caption).foregroundStyle(.orange)
            }
        }
        Text("The folder Flock serves the site from. One picked here applies on this machine only; pin it (\(Image(systemName: "pin"))) to write it into .flockrc for everyone.")
            .font(.caption).foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
    }

    /// "src/www", or "Project folder" for ".".
    private func rootLabel(_ root: String) -> String { root == "." ? "Project folder" : root }

    /// What runs the site's PHP (PHP-FPM or FrankenPHP); on FrankenPHP also its mode (classic or
    /// Laravel Octane), its state, Restart and its log.
    @ViewBuilder
    private func serverFields(_ site: Status.Site) -> some View {
        let store = model.settings
        field("Runs on") {
            Picker("Server", selection: Binding(
                get: { site.server },
                set: { server in Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(server: server.rawValue)) } } }
            )) {
                Text("PHP-FPM").tag(Status.Server.fastcgi)
                Text("FrankenPHP").tag(Status.Server.frankenphp)
                    .disabled(!site.supportsFrankenPHP && site.server != .frankenphp)
            }
            .labelsHidden()
            .fixedSize()
        }

        if site.server == .frankenphp {
            field("Mode") {
                Picker("Mode", selection: Binding(
                    get: { site.worker },
                    set: { worker in Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(worker: worker.rawValue)) } } }
                )) {
                    Text("Classic").tag(Status.Worker.classic)
                    Text("Laravel Octane").tag(Status.Worker.octane)
                        .disabled(!site.octaneAvailable && site.worker != .octane)
                }
                .labelsHidden()
                .fixedSize()
                .help(site.octaneAvailable ? "" : "Install Laravel Octane in the project (composer require laravel/octane) to use worker mode.")
            }

            frankenPHPState(site)

            HStack(spacing: 8) {
                Button("Restart") {
                    restarting = site.name
                    Task {
                        await store.perform { _ = try await $0.restartSite(site.name) }
                        restarting = nil
                    }
                }
                .disabled(site.frankenPHP?.state != .running || restarting == site.name)
                .help("Restart FrankenPHP, e.g. so Octane's workers load changed code")
                Button("Show Log") {
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
             ? "FrankenPHP needs PHP 8.2 or newer. Requests that ask for Xdebug still go to PHP-FPM. For worker mode, install Laravel Octane in the project (composer require laravel/octane)."
             : "FrankenPHP needs PHP 8.2 or newer. Requests that ask for Xdebug still go to PHP-FPM.")
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
                    Text("PHP-FPM serves the site until it's ready.")
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
                    Text("PHP-FPM serves the site meanwhile.")
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
        let text = "Running FrankenPHP \(franken.version) with PHP \(franken.phpVersion)"
        return site.worker == .octane ? text + " · worker mode" : text
    }

    /// "Downloading FrankenPHP for PHP 8.4… 42%"; without a percentage while the size is unknown.
    private func downloadingText(_ site: Status.Site, _ franken: Status.SiteFrankenPHP) -> String {
        let text = "Downloading FrankenPHP for PHP \(site.php)…"
        guard let progress = franken.progress else { return text }
        return "\(text) \(Int((progress * 100).rounded()))%"
    }

    /// The name of the site's FrankenPHP log, a service log in the Logs window.
    private func frankenPHPLog(_ site: Status.Site) -> String {
        if let log = site.frankenPHP?.log, !log.isEmpty {
            return log
        }
        return "frankenphp-\(site.name)"
    }

    /// Notes when versions disagree: an isolation over the project's pin, .flockrc over .nvmrc.
    @ViewBuilder
    private func hints(_ site: Status.Site) -> some View {
        let store = model.settings
        if site.phpSource == .isolated, !site.phpPinned.isEmpty, site.phpPinned != site.php {
            HStack(spacing: 4) {
                Text("PHP \(site.php) is isolated here; the project pins \(site.phpPinned) in \(fileName(site.phpFile)).")
                Button("Use \(fileName(site.phpFile))") {
                    Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(php: "")) } }
                }
                .buttonStyle(.link)
            }
            .font(.caption).foregroundStyle(.orange)
        }
        if site.nodeSource == .isolated, !site.nodePinned.isEmpty, major(site.nodePinned) != major(site.node ?? "") {
            HStack(spacing: 4) {
                Text("Node \(site.node ?? "") is isolated here; the project pins \(site.nodePinned) in \(fileName(site.nodeFile)).")
                Button("Use \(fileName(site.nodeFile))") {
                    Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(node: "")) } }
                }
                .buttonStyle(.link)
            }
            .font(.caption).foregroundStyle(.orange)
        }
        if !site.nodeShadowed.isEmpty {
            Text(".flockrc's Node \(site.nodePinned) wins over \(site.nodeShadowed).")
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
                    Text(share.state == .building ? "Building assets…" : "Starting the tunnel…")
                    Spacer(minLength: 8)
                    Button("Stop") { stop() }
                }
                .font(.callout)
            case .running:
                runningShare(site, share, stop: stop)
            case .failed:
                VStack(alignment: .leading, spacing: 8) {
                    Label(share.error ?? "Sharing failed.", systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                        .textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                    HStack(spacing: 8) {
                        Button("Try Again") { startShare(site.name, allow: share.allow, debug: share.debug) }
                        Button("Dismiss") { stop() }
                    }
                }
                .font(.callout)
            }
        } else {
            VStack(alignment: .leading, spacing: 6) {
                Button("Share…") { sharing = site.name }
                    .disabled(site.url == nil)
                Text("Show the site at a temporary public address to preview work in progress, never for production. Visitors don't get debug pages, dumps or the dev tools.")
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
                Button("Copy") {
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
                    .accessibilityLabel("QR code of \(url)")
                    .help("Right-click to copy or save the QR code, or drag it out")
                    .contextMenu {
                        Button("Copy Image") { copyQR(data, image) }
                        Button("Save Image…") { saveQR(data, site: site.name) }
                    }
                    .onDrag { qrProvider(data, site: site.name) }
            }
            VStack(alignment: .leading, spacing: 2) {
                if let expires = share.expiresAt {
                    Text("Ends at \(expires.formatted(date: .omitted, time: .shortened))")
                }
                if !share.allow.isEmpty {
                    Text("Only for: \(share.allow.joined(separator: ", "))")
                }
                if share.debug {
                    Text("Debug mode is on: visitors see stack traces")
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
                    Button(share.rebuilding ? "Rebuilding…" : "Rebuild Assets") {
                        Task { await store.perform { _ = try await $0.rebuildShare(site: site.name) } }
                    }
                    .disabled(share.rebuilding)
                    .help("Runs npm run build again, so visitors see your latest changes when they reload")
                }
                Button("Stop Sharing") { stop() }
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
                 ? "The project uses Laravel Boost. Adding Flock to its guidelines tells AI agents that Flock serves the app and picks its PHP version."
                 : "Flock's guideline in the project's Boost guidelines is outdated.")
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            Button(site.boost == .missing ? "Add Flock to its guidelines…" : "Update it…") {
                confirm = ConfirmAction(
                    title: site.boost == .missing ? "Add Flock to \(site.name)'s Boost guidelines?" : "Update Flock's guideline in \(site.name)?",
                    button: site.boost == .missing ? "Add" : "Update",
                    message: "Writes .ai/guidelines/flock.md in \(site.path) and runs php artisan boost:update, so AI agents know Flock serves the app and picks its PHP version. Commit it with the rest of .ai/guidelines. This changes files in your project.",
                    run: { [name = site.name] client in try await client.addBoostGuideline(site: name) })
            }
            .buttonStyle(.link)
        }
        .font(.caption)
    }

    private func pinMenu(_ site: Status.Site) -> some View {
        let nodeMajor = major(site.node ?? "")
        let nodeFile = site.nodePinnedHere ? fileName(site.nodeFile) : ".nvmrc"
        let canPinPHP = !(site.phpSource == .project && site.phpPinnedHere)
        let canPinNode = !nodeMajor.isEmpty && !(site.nodeSource == .project && site.nodePinnedHere)
        let canPinRoot = (site.rootState == .ok || site.rootState == .empty) && site.rootSource != .project

        return Menu {
            if canPinPHP {
                Button("Pin PHP \(site.php) to Project…") {
                    confirm = ConfirmAction(
                        title: "Pin PHP \(site.php) to \(site.name)?", button: "Pin",
                        message: "Writes php=\(site.php) to .flockrc in \(site.path). Commit it, and everyone using Flock on this project gets PHP \(site.php). This changes a file in your project.",
                        run: { [name = site.name, php = site.php] client in try await client.pin(site: name, tool: "php", version: php) })
                }
            }
            if canPinNode {
                Button("Pin Node \(nodeMajor) to Project…") {
                    confirm = ConfirmAction(
                        title: "Pin Node \(nodeMajor) to \(site.name)?", button: "Pin",
                        message: "Writes \(nodeMajor) to \(nodeFile) in \(site.path). Commit it, and Flock, nvm and other Node tools use Node \(nodeMajor) for this project. This changes a file in your project.",
                        run: { [name = site.name] client in try await client.pin(site: name, tool: "node", version: nodeMajor) })
                }
            }
            if canPinRoot {
                Button("Pin Document Root (\(rootLabel(site.root))) to Project…") {
                    confirm = ConfirmAction(
                        title: "Pin the document root \(rootLabel(site.root)) to \(site.name)?", button: "Pin",
                        message: "Writes root=\(site.root) to .flockrc in \(site.path). Commit it, and everyone using Flock on this project serves it from \(rootLabel(site.root)). This changes a file in your project.",
                        run: { [name = site.name, root = site.root] client in try await client.pin(site: name, tool: "root", version: root) })
                }
            }
            if site.phpPinnedHere || site.nodePinnedHere || site.rootPinnedHere {
                Divider()
            }
            if site.phpPinnedHere {
                Button("Unpin PHP (\(fileName(site.phpFile)))…") {
                    confirm = ConfirmAction(
                        title: "Unpin PHP from \(site.name)?", button: "Unpin",
                        message: "Removes the PHP version from \(site.phpFile). The site then uses Flock's default PHP. This changes files in your project.",
                        run: { [name = site.name] client in try await client.unpin(site: name, tool: "php") })
                }
            }
            if site.nodePinnedHere {
                Button("Unpin Node (\(fileName(site.nodeFile)))…") {
                    confirm = ConfirmAction(
                        title: "Unpin Node from \(site.name)?", button: "Unpin",
                        message: "Removes the Node.js version from .flockrc and deletes .nvmrc / .node-version in \(site.path). The site then uses Flock's default Node.js. This changes files in your project.",
                        run: { [name = site.name] client in try await client.unpin(site: name, tool: "node") })
                }
            }
            if site.rootPinnedHere {
                Button("Unpin Document Root (.flockrc)…") {
                    confirm = ConfirmAction(
                        title: "Unpin the document root from \(site.name)?", button: "Unpin",
                        message: "Removes root= from .flockrc in \(site.path). Flock then finds the document root itself. This changes files in your project.",
                        run: { [name = site.name] client in try await client.unpin(site: name, tool: "root") })
                }
            }
        } label: {
            Image(systemName: "pin")
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .help("Pin versions and the document root to the project")
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
            Text("Share \(site)").font(.headline)
            VStack(alignment: .leading, spacing: 4) {
                Text("Only these people (optional)")
                TextField("colleague@acme.com, *@client.com", text: allowState.projectedValue)
                    .textFieldStyle(.roundedBorder)
            }
            Text("Anyone with the link can open the site unless you list email addresses — Cloudflare then emails them a code. The link stops working after 8 hours or when you stop sharing.")
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            VStack(alignment: .leading, spacing: 4) {
                Toggle("Show error details to visitors", isOn: debugState.projectedValue)
                Text("Turns the app's debug mode on for this share, so visitors see stack traces. Only for people you trust.")
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack {
                Spacer()
                Button("Cancel", role: .cancel) { cancel() }
                    .keyboardShortcut(.cancelAction)
                Button("Share") { share(ShareStatus.parseAllow(allowState.wrappedValue), debugState.wrappedValue) }
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
        Task {
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
        let fallbackName = appSlug(req.name)
        Task {
            defer { creatingAppState.wrappedValue = false }
            await model.settings.perform { client in
                _ = try await client.createApp(req)
            }
            if model.settings.error == nil {
                let created = model.status?.sites.first { $0.path == targetPath }
                selectionState.wrappedValue = created?.name ?? fallbackName
            }
        }
    }
}

/// Hostname label matching `config.SiteName` on the daemon.
private func appSlug(_ name: String) -> String {
    let mapped = name.lowercased().map { c -> Character in
        (c.isASCII && (c.isLetter || c.isNumber || c == "-")) ? c : "-"
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
        let valid = !rawName.isEmpty && !rawFolder.isEmpty
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
                 ? "\(URL(fileURLWithPath: rawFolder).appendingPathComponent(rawName).path) → \(scheme)://\(appSlug(rawName)).\(tld)"
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
