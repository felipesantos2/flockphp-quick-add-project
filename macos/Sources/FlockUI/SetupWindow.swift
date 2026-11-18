import FlockKit
import SwiftUI

/// First start after installing: explains what setting Flock up does, makes sure no other local
/// environment (Herd, Valet, ...) holds Flock's ports, runs the bundled `flock install` (two
/// password prompts) and shows its output. The Windows app's SetupWindow.
struct SetupWindow: View {
    enum Stage { case intro, checking, conflicts, running, done, failed }

    @Environment(AppModel.self) private var model
    @Environment(\.dismissWindow) private var dismissWindow
    @Environment(\.openWindow) private var openWindow

    private let stageState = State<Stage>(initialValue: .intro)
    private var stage: Stage {
        get { stageState.wrappedValue }
        nonmutating set { stageState.wrappedValue = newValue }
    }
    private let outputState = State<String>(initialValue: "")
    private var output: String {
        get { outputState.wrappedValue }
        nonmutating set { outputState.wrappedValue = newValue }
    }
    /// The programs to quit before Flock can be set up, by app.
    private let conflictsState = State<[Installation.ConflictingApp]>(initialValue: [])
    private var conflicts: [Installation.ConflictingApp] {
        get { conflictsState.wrappedValue }
        nonmutating set { conflictsState.wrappedValue = newValue }
    }
    private let declinedState = State<Bool>(initialValue: false)
    private var declined: Bool {
        get { declinedState.wrappedValue }
        nonmutating set { declinedState.wrappedValue = newValue }
    }
    /// The first run's installs as this window last saw them in the status.
    private let seenDefaultsState = State<Status.Defaults?>(initialValue: nil)
    private var seenDefaults: Status.Defaults? {
        get { seenDefaultsState.wrappedValue }
        nonmutating set { seenDefaultsState.wrappedValue = newValue }
    }
    /// Applied once setup succeeds; Settings → General changes it later.
    private let launchAtLoginState = State<Bool>(initialValue: true)
    private var launchAtLogin: Bool {
        get { launchAtLoginState.wrappedValue }
        nonmutating set { launchAtLoginState.wrappedValue = newValue }
    }
    private let loginProblemState = State<String?>(initialValue: nil)
    private var loginProblem: String? {
        get { loginProblemState.wrappedValue }
        nonmutating set { loginProblemState.wrappedValue = newValue }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            switch stage {
            case .intro:
                header(Text("setup.welcome"), Text("setup.flock_runs_your_laravel_sites"))
                Form {
                    Section {
                        item(Text("setup.background"), Text("setup.flock_daemon_serves_your_sites"), symbol: "server.rack")
                        item(Text("setup.terminal_title"), Text("setup.terminal_description"), symbol: "terminal")
                        item(Text("setup.trusted_https_title"), Text("setup.macos_asks_for_your_password_twice"), symbol: "lock.fill")
                    }
                    Section {
                        Toggle("general.open_flocks_menu_bar_app", isOn: Binding(get: { launchAtLogin }, set: { launchAtLogin = $0 }))
                            .toggleStyle(.checkbox)
                    }
                    // Settings → General has it too, but only once Flock is set up.
                    Section {
                        LanguagePicker()
                    }
                }
                .formStyle(.grouped)
                .scrollContentBackground(.hidden)
                .scrollDisabled(true)
                buttons {
                    Button("setup.not_now") { dismissWindow(id: WindowID.setup) }
                        .keyboardShortcut(.cancelAction)
                    Button("setup.window_title") { Task { await run() } }
                        .keyboardShortcut(.defaultAction)
                }

            case .checking:
                header(Text("setup.checking_ports"), nil)
                ProgressView().progressViewStyle(.linear).padding(.horizontal, 32).padding(.bottom, 28)

            case .conflicts:
                header(Text("setup.quit_others"), Text("setup.flock_needs_these_ports_and \(model.status?.dns.tld ?? "test")"))
                Form {
                    Section {
                        ForEach(conflicts) { app in
                            item(Text(app.title), Text(app.detail), symbol: "exclamationmark.triangle.fill", color: .orange)
                        }
                    }
                }
                .formStyle(.grouped)
                .scrollContentBackground(.hidden)
                .scrollDisabled(true)
                buttons {
                    Button("setup.not_now") { dismissWindow(id: WindowID.setup) }
                        .keyboardShortcut(.cancelAction)
                    Button("setup.check_again_title_case") { Task { await run() } }
                        .keyboardShortcut(.defaultAction)
                }

            case .running:
                header(Text("setup.setting_up"), Text("setup.macos_asks_for_your_password"))
                ProgressView().progressViewStyle(.linear).padding(.horizontal, 32).padding(.bottom, 12)
                log

            case .done:
                header(Text("setup.ready"), nil)
                Form {
                    Section("setup.next_steps") {
                        if let loginProblem {
                            item(Text("setup.open_flock_at_login"), Text(loginProblem), symbol: "exclamationmark.triangle")
                        }
                        runtimes
                        item(Text("setup.new_terminal"), Text("setup.new_terminal_description"), symbol: "terminal")
                        item(Text("setup.link_project"), Text("setup.link_project_description"), symbol: "globe")
                        item(Text("setup.php_and_node"),Text("setup.php_and_node_description"), symbol: "chevron.left.forwardslash.chevron.right")
                    }
                }
                .formStyle(.grouped)
                .scrollContentBackground(.hidden)
                .scrollDisabled(true)
                // The status stops listing the installs once both are done; the row keeps their versions.
                .onChange(of: model.status?.defaults, initial: true) { _, new in
                    if let new {
                        seenDefaults = new
                    }
                }
                Text("setup.star")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.horizontal, 32)
                buttons {
                    Button("setup.open_settings") {
                        dismissWindow(id: WindowID.setup)
                        showSettingsPane()
                        openWindow.activating(WindowID.settings)
                    }
                    Button("common.done") { dismissWindow(id: WindowID.setup) }
                        .keyboardShortcut(.defaultAction)
                }

            case .failed:
                header(Text("setup.did_not_finish"), declined
                    ? Text("setup.password_prompt_was_cancelled_flock")
                    : Text("setup.failed"))
                log
                buttons {
                    Button("common.close") { dismissWindow(id: WindowID.setup) }
                        .keyboardShortcut(.cancelAction)
                    Button("setup.try_again") { Task { await run() } }
                        .keyboardShortcut(.defaultAction)
                }
            }
        }
        .frame(width: 560)
        .fixedSize(horizontal: false, vertical: true)
    }

    private func run() async {
        stage = .checking
        conflicts = Installation.group(await model.conflicts())
        guard conflicts.isEmpty else {
            stage = .conflicts
            return
        }

        output = ""
        stage = .running
        let result = await model.install { line in
            // Lines arrive from the pipe's thread; late ones lose to the final output below.
            Task { @MainActor in
                if stage == .running {
                    output += line + "\n"
                }
            }
        }
        output = result.output
        declined = result.permissionDeclined
        if result.ok && launchAtLogin != LoginItem.isEnabled {
            loginProblem = LoginItem.set(launchAtLogin)
        }
        stage = result.ok ? .done : .failed
    }

    private func header(_ title: Text, _ text: Text?) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            AppIconView(size: 60)
            title.font(.title.weight(.semibold))
            if let text {
                text.foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(.horizontal, 32)
        .padding(.top, 28)
        .padding(.bottom, 12)
    }

    /// One of the first run's installs, which become the default PHP and Node.js.
    private struct Runtime {
        let name: String
        /// The Settings pane that installs it again.
        let pane: SettingsPane
        let install: Status.DefaultInstall

        /// "PHP 8.5", or "PHP" while the version isn't known yet.
        var label: String { install.version.isEmpty ? name : "\(name) \(install.version)" }
    }

    /// The first run's installs of the newest PHP and Node.js LTS (Status.defaults): in progress,
    /// waiting for a download, failed, or done. Shown only when this setup installs them; once the
    /// status stops listing them, both are done.
    @ViewBuilder
    private var runtimes: some View {
        if let defaults = model.status?.defaults ?? seenDefaults.map(Self.finished) {
            let all = [Runtime(name: "PHP", pane: .php, install: defaults.php), Runtime(name: "Node.js", pane: .node, install: defaults.node)]
            let pending = all.filter { $0.install.isPending }
            ForEach(all.filter { $0.install.state == .failed }, id: \.name) { runtime in
                item(Text("setup.name_couldnt_be_installed \(runtime.label)"),
                     runtime.install.error.isEmpty
                         ? Text("setup.try_again_in_settings_to \(runtime.pane.title)")
                         : Text("setup.try_again_in_settings_to_macos \(runtime.install.error) \(runtime.pane.title)"),
                     symbol: "exclamationmark.triangle.fill", color: .orange)
            }
            // One install or both (PHP and Node.js): the sentences differ in more than a word.
            let names = Self.names(pending)
            if pending.contains(where: { $0.install.state == .installing }) {
                row(Text("setup.installing \(names)"), pending.count > 1
                    ? Text("setup.flock_makes_them_the_default_macos")
                    : Text("setup.flock_makes_it_the_default")) {
                    ProgressView().controlSize(.small)
                }
            } else if !pending.isEmpty {
                row(Text("setup.waiting_to_install_names \(names)"), pending.count > 1
                    ? Text("setup.installed_when_downloadable_both \(names)")
                    : Text("setup.installed_when_downloadable \(names)")) {
                    ProgressView().controlSize(.small)
                }
            }
            if all.allSatisfy({ $0.install.state == .done }) {
                item(Text("setup.names_are_installed \(Self.names(all))"), Text("setup.theyre_the_defaults_for_your"), symbol: "checkmark.circle.fill", color: .green)
            }
        }
    }

    /// The installs as last seen, once the status no longer lists them: both defaults are set.
    private static func finished(_ seen: Status.Defaults) -> Status.Defaults {
        Status.Defaults(php: Status.DefaultInstall(version: seen.php.version), node: Status.DefaultInstall(version: seen.node.version))
    }

    /// The pane "Open Settings" shows: the one a link waited for during the setup, else the pane to
    /// retry a failed default in, else the one Settings showed last.
    private func showSettingsPane() {
        if let link = model.pendingInstall {
            model.handle(.install(link))
        } else if model.pendingSecure != nil {
            model.showSettings(.sites)
        } else if let defaults = model.status?.defaults, defaults.php.state == .failed {
            model.showSettings(.php)
        } else if model.status?.defaults?.node.state == .failed {
            model.showSettings(.node)
        }
    }

    /// "PHP 8.5 and Node.js 24".
    private static func names(_ runtimes: [Runtime]) -> String {
        runtimes.map(\.label).formatted(.list(type: .and).locale(AppLanguage.locale))
    }

    private func item(_ title: Text, _ text: Text, symbol: String, color: Color = .accentColor) -> some View {
        row(title, text) {
            Image(systemName: symbol).foregroundStyle(color)
        }
    }

    private func row(_ title: Text, _ text: Text, @ViewBuilder icon: () -> some View) -> some View {
        Label {
            VStack(alignment: .leading, spacing: 2) {
                title
                text.font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
        } icon: {
            icon().frame(width: 22)
        }
        .padding(.vertical, 2)
    }

    private var log: some View {
        ScrollViewReader { proxy in
            ScrollView([.vertical, .horizontal]) {
                VStack(alignment: .leading, spacing: 0) {
                    Text(verbatim: output.isEmpty ? " " : output)
                        .font(.system(.caption, design: .monospaced))
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Color.clear.frame(height: 1).id("end")
                }
                .padding(10)
            }
            .frame(height: 220)
            .background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 6))
            .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(.separator))
            .padding(.horizontal, 32)
            .padding(.bottom, stage == .running ? 28 : 0)
            .onChange(of: output) { proxy.scrollTo("end", anchor: .bottom) }
        }
    }

    private func buttons(@ViewBuilder _ content: () -> some View) -> some View {
        HStack {
            Spacer()
            content()
        }
        .padding(.horizontal, 32)
        .padding(.top, 8)
        .padding(.bottom, 24)
    }
}
