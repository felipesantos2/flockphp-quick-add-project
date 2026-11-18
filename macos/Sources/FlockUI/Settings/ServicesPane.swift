import AppKit
import FlockKit
import SwiftUI

/// Grouped list of services with a detail column. Shows the installed
/// services or every service Flock offers; PostgreSQL and the catalog services (MySQL, Valkey,
/// Redis, Meilisearch, ...) are installed, updated and switched from their detail.
struct ServicesPane: View {
    @Environment(AppModel.self) private var model
    @AppStorage("services.filter") private var filter = ServiceFilter.all
    private let confirmStopState = State<Service?>(initialValue: nil)
    private var confirmStop: Service? {
        get { confirmStopState.wrappedValue }
        nonmutating set { confirmStopState.wrappedValue = newValue }
    }

    private static let categoryOrder = ["Core", "Database", "Cache & Queue", "Search", "Storage", "Mail", "Debugging"]

    var body: some View {
        let store = model.settings
        let services = visible(store.services)
        let selected = services.first { $0.id == model.servicesSelection } ?? services.first

        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .bottom, spacing: 12) {
                PaneHeader(title: "settings.pane.services", subtitle: "services.subtitle")
                Picker("services.show", selection: $filter) {
                    Text("common.installed").tag(ServiceFilter.installed)
                    Text("services.all").tag(ServiceFilter.all)
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .fixedSize()
                .padding(.trailing, 20)
                .padding(.bottom, 6)
                .help("services.filter_tooltip")
            }

            HStack(spacing: 0) {
                List(selection: Binding<Service.ID?>(get: { selected?.id }, set: { model.servicesSelection = $0 })) {
                    ForEach(categories(of: services), id: \.self) { category in
                        Section(Self.title(of: category)) {
                            ForEach(services.filter { $0.category == category }) { service in
                                ServiceRow(service: service, activity: activity(of: service), locked: locked(service)) {
                                    model.servicesSelection = service.id
                                } toggle: {
                                    if service.running {
                                        confirmStop = service
                                    } else {
                                        let id = service.id
                                        Task { await store.performOnServices { try await $0.startService(id) } }
                                    }
                                }
                                .tag(service.id)
                            }
                        }
                    }
                    if filter == .installed, hiddenCount(store.services) > 0 {
                        Button {
                            filter = .all
                        } label: {
                            Label("services.more_services", systemImage: "plus.circle")
                        }
                        .buttonStyle(.link)
                        .padding(.vertical, 4)
                        .help("services.show_every_service_flock_offers")
                    }
                }
                .listStyle(.inset)
                // Wide enough for "Nainstalovat…" next to a name and its state.
                .frame(width: 250)

                Divider()

                Group {
                    if let selected {
                        detail(selected)
                    } else {
                        ContentUnavailableView("services.no_services", systemImage: "server.rack")
                    }
                }
                .frame(minWidth: 340, maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
                .background(Color(nsColor: .controlBackgroundColor).opacity(0.5))
            }
            .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(.separator))
            .clipShape(RoundedRectangle(cornerRadius: 8))
            .padding(.horizontal, 20)
            .padding(.vertical, 12)
        }
        .confirmationDialog("services.stop_question \(confirmStop?.name ?? "")", isPresented: Binding(get: { confirmStop != nil }, set: { if !$0 { confirmStop = nil } }),
                            presenting: confirmStop) { service in
            Button("sites.share.stop", role: .destructive) {
                let id = service.id
                Task { await store.performOnServices { try await $0.stopService(id) } }
            }
            Button("common.cancel", role: .cancel) {}
        } message: { service in
            Text("services.your_sites_cant_use_service \(service.name)")
        }
        // A flock://install link (AppModel.handle): nothing installs until the user confirms here.
        .alert(linkRequest?.title ?? "", isPresented: Binding(get: { linkRequest != nil }, set: { if !$0 { model.pendingInstall = nil } }),
               presenting: linkRequest) { request in
            if let install = request.confirmation {
                Button("common.install") { self.install(install) }
                Button("common.cancel", role: .cancel) {}
            } else {
                Button("common.ok") {}
            }
        } message: { request in
            Text(request.message)
        }
        // Versions and the catalog load when the pane appears, then follow the status (AppModel);
        // while something installs, builds or switches they are polled every 2 seconds.
        .task { await store.refreshInstalls() }
        .task(id: store.installsBusy) {
            guard store.installsBusy else { return }
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(2))
                if Task.isCancelled { break }
                await store.refreshInstalls()
            }
        }
    }

    @ViewBuilder
    private func detail(_ service: Service) -> some View {
        switch service.id {
        case "postgres": PostgresDetail(service: service)
        default:
            if service.isOnRequest {
                CatalogDetail(service: service)
            } else {
                ServiceDetail(service: service)
            }
        }
    }

    /// What a pending flock://install link asks for, once the services and versions are loaded.
    /// The PHP and Node panes answer links for PHP and Node.js.
    private var linkRequest: InstallLink.Resolution? {
        let store = model.settings
        guard let link = model.pendingInstall, link.service != "php", link.service != "node" else { return nil }
        return link.resolve(services: store.services, catalog: store.catalog, postgres: store.postgres)
    }

    private func install(_ install: InstallLink.Install) {
        let store = model.settings
        let id = install.service
        let line = install.line
        if id == "postgres" {
            Task { await store.performOnPostgres(line, .installing) { try await $0.installPostgres(line) } }
        } else {
            Task { await store.performOnCatalog(id) { try await $0.installCatalogService(id, line: line) } }
        }
    }

    /// The services the filter shows: with "Installed", the built-in ones, the installed ones and
    /// those installing (and the selected one, so picking a service from elsewhere still works).
    private func visible(_ services: [Service]) -> [Service] {
        guard filter == .installed else { return services }
        return services.filter { service in
            !service.isOnRequest || service.installed || activity(of: service) == .working || activity(of: service) == .failed
                || service.id == model.servicesSelection
        }
    }

    private func hiddenCount(_ services: [Service]) -> Int {
        services.count - visible(services).count
    }

    /// What the row shows next to the name: work in progress, or an available update.
    private func activity(of service: Service) -> ServiceRow.Activity {
        let store = model.settings
        switch service.id {
        case "postgres":
            if store.postgres?.isBusy == true || !store.postgresPending.isEmpty {
                return .working
            }
            return store.postgres?.hasUpdate == true ? .update : .none
        default:
            guard let entry = store.catalog?[service.id] else { return .none }
            if entry.job?.isRunning == true || store.catalogPending.contains(service.id) { return .working }
            if entry.job?.isFailed == true { return .failed }
            return entry.lines.contains(where: \.update) ? .update : .none
        }
    }

    /// Start/Stop waits while the service is being installed or switched.
    private func locked(_ service: Service) -> Bool {
        let store = model.settings
        return store.isBusy || activity(of: service) == .working
    }

    /// The categories come from the daemon in English; the ones Flock has are shown translated.
    private static func title(of category: String) -> String {
        switch category {
        case "Core": String(localized: "services.category.core")
        case "Database": String(localized: "services.category.database")
        case "Cache & Queue": String(localized: "services.category.cache_queue")
        case "Search": String(localized: "services.category.search")
        case "Storage": String(localized: "services.category.storage")
        case "Mail": String(localized: "services.category.mail")
        case "Debugging": String(localized: "services.category.debugging")
        default: category
        }
    }

    private func categories(of services: [Service]) -> [String] {
        let present = Set(services.map(\.category))
        let ordered = Self.categoryOrder.filter(present.contains)
        return ordered + present.subtracting(ordered).sorted()
    }
}

/// Which services the Services pane lists.
enum ServiceFilter: String {
    case installed, all
}

private struct ServiceRow: View {
    /// `working`: the service is being installed, updated or switched.
    enum Activity { case none, update, working, failed }

    let service: Service
    let activity: Activity
    let locked: Bool
    let select: () -> Void
    let toggle: () -> Void

    var body: some View {
        HStack(spacing: 10) {
            StatusDot(on: service.running)
            VStack(alignment: .leading, spacing: 1) {
                HStack(spacing: 5) {
                    Text(service.name).fontWeight(.medium).lineLimit(1)
                    switch activity {
                    case .update:
                        Circle().fill(Color.accentColor).frame(width: 6, height: 6)
                            .help("versions.update_available")
                    case .failed:
                        Image(systemName: "exclamationmark.circle.fill").font(.caption).foregroundStyle(.red)
                            .help("services.last_install_failed")
                    case .none, .working:
                        EmptyView()
                    }
                }
                Text(detail).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                if !service.problem.isEmpty {
                    Label(service.problem, systemImage: "exclamationmark.triangle.fill")
                        .font(.caption).foregroundStyle(.orange)
                        .lineLimit(3)
                        .help(service.problem)
                }
            }
            Spacer(minLength: 4)
            if activity == .working {
                ProgressView().controlSize(.small)
            } else if service.installed {
                if service.stoppable {
                    Button(service.running ? "sites.share.stop" : "services.start", action: toggle)
                        .controlSize(.small)
                        .disabled(locked)
                }
            } else if service.isInstallable, service.available {
                Button("services.install_ellipsis", action: select)
                    .controlSize(.small)
                    .fixedSize()
            }
        }
        .padding(.vertical, 3)
        .opacity(service.available ? 1 : 0.5)
        .help(service.available ? "" : service.unavailable)
    }

    private var detail: String {
        if !service.installed {
            guard service.isInstallable else { return "" }
            return service.available ? String(localized: "common.not_installed") : String(localized: "services.catalog.not_available_on_macos")
        }
        var parts: [String] = []
        if !service.version.isEmpty {
            parts.append(service.version)
        }
        if service.port > 0 {
            parts.append(String(localized: "services.port_number \(String(service.port))"))
        }
        if !service.running {
            parts.append(String(localized: "services.postgres.stopped"))
        }
        return parts.joined(separator: " · ")
    }
}

extension Service {
    /// The web server and DNS ports are tied to certificates and /etc/resolver; `flock install` changes them.
    var hasConfigurablePort: Bool { installed && port > 0 && id != "web" && id != "dns" }

    /// The web server and DNS ports: shown, but only `flock install` changes them.
    var hasFixedPort: Bool { installed && port > 0 && (id == "web" || id == "dns") }

    /// PostgreSQL and the catalog services are installed from their detail.
    var isInstallable: Bool { id == "postgres" || isOnRequest }

    /// A catalog service (MySQL, Valkey, ...): installed on request, its detail is a CatalogDetail.
    var isOnRequest: Bool {
        !["web", "dns", "hosts", "mail", "dumps", "postgres"].contains(id)
    }
}

/// The detail of a service without installs: problem, port, environment, connection and logs.
private struct ServiceDetail: View {
    let service: Service

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                HStack(spacing: 8) {
                    StatusDot(on: service.running)
                    Text(service.name).font(.headline)
                    Spacer()
                }
                ServiceInfo(service: service)
            }
            .padding(16)
        }
    }
}

/// What every installed service shows: its problem, what it does (core services), port, TablePlus,
/// environment, connection and log.
struct ServiceInfo: View {
    @Environment(AppModel.self) private var model
    let service: Service
    private let editingState = State<Bool>(initialValue: false)

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            // Why it isn't working; the broken DNS server's explanation below already includes it.
            if !service.problem.isEmpty && !(service.id == "dns" && dnsBroken) {
                Label(service.problem, systemImage: "exclamationmark.triangle.fill")
                    .font(.callout).foregroundStyle(.orange)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }

            if let explanation {
                Text(explanation)
                    .font(.callout).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            if service.hasConfigurablePort {
                row("mail.port") {
                    Text(String(service.port)).monospacedDigit().foregroundStyle(.secondary)
                    Button("sites.change") { editingState.wrappedValue = true }
                        .controlSize(.small)
                }
            } else if service.hasFixedPort {
                row("mail.port") {
                    Text(String(localized: "services.port_set_by_flock_install \(String(service.port))")).monospacedDigit().foregroundStyle(.secondary)
                }
            }

            if service.isDatabase, let url = URL(string: service.url),
               let tablePlus = NSWorkspace.shared.urlForApplication(withBundleIdentifier: "com.tinyapp.TablePlus") {
                row("common.tableplus") {
                    Button("common.open") {
                        NSWorkspace.shared.open([url], withApplicationAt: tablePlus, configuration: NSWorkspace.OpenConfiguration())
                    }
                    .controlSize(.small)
                }
            }

            if !service.log.isEmpty {
                row("services.log") {
                    Button("common.open") {
                        let file = FlockPaths.root.appendingPathComponent("log/\(service.log).log")
                        NSWorkspace.shared.open(file)
                    }
                    .controlSize(.small)
                }
            }

            if !service.env.isEmpty {
                VStack(alignment: .leading, spacing: 6) {
                    Text("mail.environment_variables_title_case").font(.subheadline.weight(.medium))
                    CodeBlock(text: service.env)
                }
            }

            if !service.url.isEmpty {
                VStack(alignment: .leading, spacing: 6) {
                    Text("services.connection").font(.subheadline.weight(.medium))
                    CodeBlock(text: service.url)
                }
            }
        }
        .sheet(isPresented: editingState.projectedValue) {
            PortSheet(service: service) { port in
                let id = service.id
                Task { await model.settings.performOnServices { try await $0.setServicePort(id, port: port) } }
            }
        }
    }

    private var tld: String {
        let tld = model.status?.dns.tld ?? ""
        return tld.isEmpty ? "test" : tld
    }

    /// Flock's DNS server counts as broken when names under the TLD don't reach it.
    private var dnsBroken: Bool { !service.running || !service.problem.isEmpty }

    /// What the core services do, as on Windows (ServicesPane.Detail): they have nothing else to show.
    private var explanation: String? {
        switch service.id {
        case "web":
            return service.running
                ? String(localized: "services.web_running \(tld)")
                : String(localized: "services.web_down")
        case "dns":
            if !dnsBroken {
                return String(localized: "services.macos_sends_every_name_under \(tld) \(tld) \(tld) \(tld)")
            }
            let problem = service.problem.isEmpty ? String(localized: "services.dns_server_isnt_running") : service.problem
            return String(localized: "general.doesnt_resolve_macos_has_no \(tld) \(problem) \(tld)")
        default:
            return nil
        }
    }

    private func row(_ title: LocalizedStringKey, @ViewBuilder trailing: () -> some View) -> some View {
        HStack {
            Text(title).font(.subheadline.weight(.medium))
            Spacer()
            trailing()
        }
    }
}

/// Changes a service's port.
private struct PortSheet: View {
    let service: Service
    let save: (Int) -> Void
    @Environment(\.dismiss) private var dismiss
    private let portState: State<String>

    init(service: Service, save: @escaping (Int) -> Void) {
        self.service = service
        self.save = save
        portState = State(initialValue: String(service.port))
    }

    private var port: Int? {
        Int(portState.wrappedValue).flatMap { (1024...65535).contains($0) ? $0 : nil }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("services.service_port \(service.name)").font(.headline)
            Form {
                TextField("mail.port", text: portState.projectedValue)
            }
            Text("services.port_details")
                .font(.caption).foregroundStyle(.secondary)
            HStack {
                Spacer()
                Button("common.cancel", role: .cancel) { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("common.save") {
                    if let port {
                        save(port)
                    }
                    dismiss()
                }
                .keyboardShortcut(.defaultAction)
                .disabled(port == nil || port == service.port)
            }
        }
        .padding(20)
        .frame(width: 340)
    }
}
