import FlockKit
import SwiftUI

struct GeneralPane: View {
    @Environment(AppModel.self) private var model
    private let loginErrorState = State<String?>(initialValue: nil)
    private var loginError: String? {
        get { loginErrorState.wrappedValue }
        nonmutating set { loginErrorState.wrappedValue = newValue }
    }
    private let selectedParkedState = State<String?>(initialValue: nil)
    private var selectedParked: String? {
        get { selectedParkedState.wrappedValue }
        nonmutating set { selectedParkedState.wrappedValue = newValue }
    }
    /// The project folder waiting for the removal to be confirmed.
    private let confirmUnparkState = State<String?>(initialValue: nil)
    private var confirmUnpark: String? {
        get { confirmUnparkState.wrappedValue }
        nonmutating set { confirmUnparkState.wrappedValue = newValue }
    }
    private let launchAtLoginState = State<Bool>(initialValue: LoginItem.isEnabled)
    private var launchAtLogin: Bool {
        get { launchAtLoginState.wrappedValue }
        nonmutating set { launchAtLoginState.wrappedValue = newValue }
    }

    var body: some View {
        let store = model.settings

        VStack(spacing: 0) {
            PaneHeader(title: "general.title", subtitle: "general.subtitle")
            Form {
                Section("general.daemon") {
                    if let status = model.status {
                        LabeledContent("general.status") {
                            HStack(spacing: 6) {
                                StatusDot(on: !status.isDegraded)
                                Text(status.isDegraded ? "menu.status.needs_attention" : "sites.proxy.running")
                            }
                        }
                        LabeledContent("general.version", value: status.version)
                        LabeledContent("general.process", value: "pid \(status.pid)")
                    }
                    LabeledContent {
                        Button {
                            Task { await model.restart() }
                        } label: {
                            BusyLabel(title: "general.restart_daemon", busyTitle: "general.restarting", isBusy: model.isRestarting)
                        }
                        .disabled(model.isRestarting)
                    } label: {
                        Text(verbatim: "")
                    }
                }

                if let config = store.config {
                    Section {
                        LabeledContent {
                            Text("common.wildcard_domain \(config.tld)")
                        } label: {
                            Text("general.domain")
                            Text(domainExplanation(config.tld))
                        }
                    } header: {
                        Text("general.network")
                    } footer: {
                        // Sites are served on 80 and 443; other ports are only set by editing config.json.
                        if config.ports.http != 80 || config.ports.https != 443 {
                            Text("general.ports \(String(config.ports.http)) \(String(config.ports.https))")
                                .font(.caption).foregroundStyle(.secondary)
                        }
                    }

                    Section("general.data") {
                        LabeledContent("general.folder") {
                            HStack {
                                Text(config.root).lineLimit(1).truncationMode(.middle).foregroundStyle(.secondary)
                                Button("general.show_in_finder") { reveal(config.root) }
                            }
                        }
                    }
                }

                Section("general.startup") {
                    Toggle("general.open_flocks_menu_bar_app", isOn: Binding(get: { launchAtLogin }, set: { setLaunchAtLogin($0) }))
                    if let loginError {
                        Text(loginError).font(.caption).foregroundStyle(.orange)
                    }
                }

                Section {
                    LanguagePicker()
                }

                Section {
                    appUpdates
                    if let config = store.config {
                        Toggle(isOn: Binding(
                            get: { config.channel == "beta" },
                            set: { on in Task { await store.perform { _ = try await $0.updateSettings(SettingsChange(channel: on ? "beta" : "stable")) } } }
                        )) {
                            Text("general.beta")
                            Text("general.beta_description")
                        }
                        .disabled(config.channel == "experimental")
                        if config.channel == "experimental" {
                            Label("general.experimental_channel_packages_that_arent", systemImage: "flask.fill")
                                .font(.caption).foregroundStyle(.orange)
                        }
                    }
                } header: {
                    Text("general.updates")
                }

                if let config = store.config {
                    Section {
                        List(selection: selectedParkedState.projectedValue) {
                            ForEach(config.parked, id: \.self) { path in
                                Label(path, systemImage: "folder").tag(path)
                            }
                        }
                        .frame(minHeight: 80)
                        .overlay {
                            if config.parked.isEmpty {
                                Text("general.no_project_folders").foregroundStyle(.secondary)
                            }
                        }
                        HStack(spacing: 0) {
                            Button {
                                for url in SettingsStore.chooseDirectories(prompt: String(localized: "common.add"), multiple: true) {
                                    Task { await store.perform { _ = try await $0.park(url.path) } }
                                }
                            } label: { Image(systemName: "plus").frame(width: 22, height: 18) }
                            Button {
                                confirmUnpark = selectedParked
                            } label: { Image(systemName: "minus").frame(width: 22, height: 18) }
                            .disabled(selectedParked == nil)
                            Spacer()
                        }
                        .buttonStyle(.borderless)
                    } header: {
                        Text("general.project_folders")
                    } footer: {
                        Text("general.project_folders_description \(config.tld)")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
            .formStyle(.grouped)
            .scrollContentBackground(.hidden)
        }
        .confirmationDialog("general.remove_project_folder_question \(confirmUnpark.map { URL(fileURLWithPath: $0).lastPathComponent } ?? "")",
                            isPresented: Binding(get: { confirmUnpark != nil }, set: { if !$0 { confirmUnpark = nil } }),
                            presenting: confirmUnpark) { path in
            Button("common.remove", role: .destructive) {
                if selectedParked == path {
                    selectedParked = nil
                }
                Task { await store.perform { _ = try await $0.unpark(path) } }
            }
            Button("common.cancel", role: .cancel) {}
        } message: { path in
            Text("general.sites_in_path_are_no \(path)")
        }
    }

    /// The app's version, what the last check found, and the button to check or update.
    private var appUpdates: some View {
        let updater = model.updater
        return LabeledContent {
            HStack(spacing: 8) {
                Text(updateState(updater)).foregroundStyle(.secondary)
                if updater.isEnabled {
                    Button(updater.availableVersion.map { String(localized: "general.update_to \($0)") } ?? String(localized: "general.check_for_updates")) {
                        updater.checkForUpdates()
                    }
                    .disabled(updater.isChecking)
                }
            }
        } label: {
            Text("common.flock")
            Text("general.version_number \(Updater.appVersion)")
        }
    }

    private func updateState(_ updater: Updater) -> String {
        if !updater.isEnabled {
            return String(localized: "general.updates_are_off_in_development")
        }
        if updater.isChecking {
            return String(localized: "versions.checking")
        }
        if let version = updater.availableVersion {
            return String(localized: "services.version_available \(version)")
        }
        return updater.upToDate ? String(localized: "versions.up_to_date") : ""
    }

    /// How names under the TLD resolve: Flock's DNS server through /etc/resolver, which has no
    /// fallback on macOS (Windows also writes site domains to the hosts file).
    private func domainExplanation(_ tld: String) -> String {
        guard let dns = model.status?.dns else { return "" }
        if dns.wildcard {
            return String(localized: "general.every_name_any_subdomain_included \(tld)")
        }
        let problem = dns.wildcardProblem.isEmpty ? String(localized: "general.flocks_dns_server_isnt_running") : dns.wildcardProblem
        return String(localized: "general.doesnt_resolve_macos_has_no \(tld) \(problem) \(tld)")
    }

    private func setLaunchAtLogin(_ on: Bool) {
        loginError = LoginItem.set(on)
        launchAtLogin = LoginItem.isEnabled
    }
}
