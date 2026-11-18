import AppKit
import FlockKit
import SwiftUI

struct MailPane: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        let store = model.settings
        let mail = store.services.first { $0.id == "mail" }

        VStack(spacing: 0) {
            PaneHeader(title: "services.category.mail", subtitle: "mail.flock_catches_every_email_your")
            Form {
                Section("mail.smtp_server") {
                    LabeledContent("mail.host", value: "127.0.0.1")
                    LabeledContent("mail.port", value: String(store.config?.ports.smtp ?? mail?.port ?? 0))
                    LabeledContent("general.status") {
                        HStack(spacing: 6) {
                            StatusDot(on: mail?.running ?? false)
                            Text(mail?.running == true ? "sites.proxy.running" : "services.postgres.stopped")
                        }
                    }
                }
                Section {
                    CodeBlock(text: mail?.env ?? "")
                } header: {
                    Text("mail.environment_variables_title_case")
                } footer: {
                    Text("mail.username_inbox")
                        .font(.caption).foregroundStyle(.secondary)
                }
                Section {
                    LabeledContent("mail.caught") {
                        HStack {
                            Text(String(model.status?.mailCount ?? 0))
                            Button("mail.open") { openWindow.activating(WindowID.mail) }
                        }
                    }
                }
            }
            .formStyle(.grouped)
            .scrollContentBackground(.hidden)
        }
    }
}

struct DumpsPane: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openWindow) private var openWindow
    @AppStorage("dumps.editor") private var editorID = Editor.phpstorm.rawValue

    var body: some View {
        let capturing = model.status?.dumps == true
        let off = Set(model.status?.captureOff ?? [])
        let unsupported = model.status?.captureUnsupported ?? []

        VStack(spacing: 0) {
            PaneHeader(title: "menu.dumps", subtitle: "dumps.send_dump_and_dd_output")
            Form {
                Section {
                    Toggle("dumps.capture_short", isOn: Binding(get: { capturing }, set: { on in Task { await model.setDumps(on, from: .settings) } }))
                    LabeledContent("dumps.captured") {
                        HStack {
                            Text(verbatim: "\(Self.dumps(model.status?.dumpCount ?? 0)), \(Self.events(model.status?.eventCount ?? 0))")
                            Button("dumps.open") { openWindow.activating(WindowID.dumps) }
                        }
                    }
                } footer: {
                    Text("dumps.capture_description")
                        .font(.caption).foregroundStyle(.secondary)
                }
                Section {
                    ForEach(Event.Kind.allCases) { kind in
                        Toggle(kind.title, isOn: Binding(get: { !off.contains(kind.rawValue) },
                                                         set: { on in Task { await model.setCapture(kind, on, from: .settings) } }))
                    }
                    Toggle("dumps.show_dump_output_in_the", isOn: Binding(get: { model.status?.dumpsInPage == true },
                                                                             set: { on in Task { await model.setDumpsInPage(on, from: .settings) } }))
                } header: {
                    Text("dumps.while_capturing")
                } footer: {
                    let list = unsupported.formatted(.list(type: .and).locale(AppLanguage.locale))
                    // The subject is a list, not a count: one version or several.
                    Text(unsupported.isEmpty
                        ? "dumps.dd_always_shows_in_the"
                        : unsupported.count == 1
                        ? "dumps.dd_always_shows_in_the_page_too \(list)"
                        : "dumps.dd_always_shows_in_the_page \(list)")
                        .font(.caption).foregroundStyle(.secondary)
                }
                .disabled(!capturing)
                Section("dumps.sources") {
                    Picker("dumps.open_files_in", selection: $editorID) {
                        ForEach(Editor.allCases) { Text($0.name).tag($0.rawValue) }
                    }
                }
            }
            .formStyle(.grouped)
            .scrollContentBackground(.hidden)
        }
    }
}

extension DumpsPane {
    // One apart in code as well: builds without the compiled catalog (swift run) have no plural rules.
    static func dumps(_ count: Int) -> String {
        count == 1 ? String(localized: "dumps.n1_dump") : String(localized: "dumps.n_dumps \(count)")
    }

    static func events(_ count: Int) -> String {
        count == 1 ? String(localized: "dumps.n1_event") : String(localized: "app.n_events \(count)")
    }
}

struct DebuggerPane: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        let enabled = model.status?.xdebug == true
        let breakpoints = model.status?.xdebugBreakpoints == true
        let debugging = (model.status?.sites ?? []).filter(\.debugging).map(\.domain)
        let versions = model.settings.config?.php ?? []

        VStack(spacing: 0) {
            PaneHeader(title: "settings.pane.debugger", subtitle: "debugger.subtitle")
            Form {
                Section {
                    Toggle("debugger.detect_requests", isOn: Binding(get: { enabled }, set: { on in Task { await model.setXdebug(on, from: .settings) } }))
                    Toggle("debugger.detect_breakpoints", isOn: Binding(get: { breakpoints }, set: { on in Task { await model.setXdebugBreakpoints(on) } }))
                        .disabled(!enabled)
                } footer: {
                    Text("debugger.xdebug_loads_only_for_requests")
                        .font(.caption).foregroundStyle(.secondary)
                }
                if enabled && breakpoints && !debugging.isEmpty {
                    Section("debugger.debugging_now") {
                        ForEach(debugging, id: \.self) { domain in
                            Label(domain, systemImage: "ladybug.fill").foregroundStyle(.orange)
                        }
                    }
                }
                Section("debugger.connect_ide") {
                    LabeledContent("debugger.client_port", value: "9003")
                    LabeledContent("debugger.mode", value: "debug, develop")
                    VStack(alignment: .leading, spacing: 4) {
                        Text("debugger.start_session")
                        Text("debugger.set_a_breakpoint_in_phpstorm")
                            .font(.callout)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                Section("debugger.availability") {
                    ForEach(versions) { php in
                        LabeledContent("common.php_version \(php.version)") {
                            if php.xdebugAvailable {
                                Label("debugger.available", systemImage: "checkmark.circle.fill").foregroundStyle(.green)
                            } else {
                                Label("debugger.not_in_build", systemImage: "xmark.circle").foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            }
            .formStyle(.grouped)
            .scrollContentBackground(.hidden)
        }
    }
}

struct AboutPane: View {
    @Environment(AppModel.self) private var model
    private let uninstallingState = State<Bool>(initialValue: false)

    var body: some View {
        VStack(spacing: 14) {
            Spacer()
            AppIconView(size: 88)
            Text("common.flock").font(.largeTitle.weight(.semibold))
            Text("about.tagline")
                .foregroundStyle(.secondary)
            Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 6) {
                GridRow {
                    Text("about.app").foregroundStyle(.secondary)
                    Text(Updater.appVersion)
                }
                GridRow {
                    Text("general.daemon").foregroundStyle(.secondary)
                    Text(model.status?.version ?? String(localized: "about.daemon_not_running"))
                }
            }
            .font(.callout)
            HStack {
                Button("about.data_folder_title_case") { reveal(FlockPaths.root.path) }
                Button("about.flock_docs") { NSWorkspace.shared.open(URL(string: "https://flockphp.com/docs")!) }
                Button("common.discord") { NSWorkspace.shared.open(URL(string: "https://discord.flockphp.com")!) }
                Button("about.license") { NSWorkspace.shared.open(URL(string: "https://github.com/flock-php/flock/blob/main/LICENSE")!) }
            }
            VStack(spacing: 8) {
                Text("about.support_description")
                    .font(.callout).foregroundStyle(.secondary)
                HStack {
                    Button("about.star_on_github", systemImage: "star") { NSWorkspace.shared.open(URL(string: "https://github.com/flock-php/flock")!) }
                    Button("about.sponsor", systemImage: "heart") { NSWorkspace.shared.open(URL(string: "https://github.com/sponsors/flock-php")!) }
                }
            }
            .padding(.top, 6)
            Button("about.found_a_bug_report_an") { NSWorkspace.shared.open(issueURL) }
                .buttonStyle(.link)
                .font(.callout)
            Text("about.license_description")
                .font(.caption).foregroundStyle(.secondary)
            Spacer()
            VStack(spacing: 6) {
                Button("about.uninstall_flock_ellipsis") { uninstallingState.wrappedValue = true }
                    .disabled(model.installation.uninstaller == nil)
                Text("about.stops_flock_and_removes_it")
                    .font(.caption).foregroundStyle(.secondary)
            }
            .padding(.bottom, 20)
        }
        .frame(maxWidth: .infinity)
        .sheet(isPresented: uninstallingState.projectedValue) {
            UninstallSheet()
        }
    }

    /// A blank GitHub issue with the versions filled in, so reports say what they run on.
    private var issueURL: URL {
        var components = URLComponents(string: "https://github.com/flock-php/flock/issues/new")!
        let body = "\n\n---\nApp: \(Updater.appVersion)\nDaemon: \(model.status?.version ?? "not running")\nOS: macOS \(ProcessInfo.processInfo.operatingSystemVersionString)\n"
        components.queryItems = [URLQueryItem(name: "body", value: body)]
        return components.url!
    }
}

/// Asks whether to uninstall (and whether to delete the user's data too), runs
/// `flock uninstall --keep-app` with its output, then removes the app and quits.
private struct UninstallSheet: View {
    enum Stage { case confirm, running, done, failed }

    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss

    private let stageState = State<Stage>(initialValue: .confirm)
    private var stage: Stage {
        get { stageState.wrappedValue }
        nonmutating set { stageState.wrappedValue = newValue }
    }
    private let purgeState = State<Bool>(initialValue: false)
    private let outputState = State<String>(initialValue: "")
    private var output: String {
        get { outputState.wrappedValue }
        nonmutating set { outputState.wrappedValue = newValue }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            switch stage {
            case .confirm:
                Text("about.uninstall_question").font(.headline)
                Text("about.flock_stops_and_is_removed")
                    .fixedSize(horizontal: false, vertical: true)
                Text("about.uninstall_keeps \(FlockPaths.root.path)")
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Toggle("about.also_delete_data", isOn: purgeState.projectedValue)
                    .toggleStyle(.checkbox)
                HStack {
                    Spacer()
                    Button("common.cancel", role: .cancel) { dismiss() }
                        .keyboardShortcut(.cancelAction)
                    Button("common.uninstall", role: .destructive) { Task { await run() } }
                }

            case .running:
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text("about.uninstalling_flock").font(.headline)
                }
                log

            case .done:
                Text("about.flock_was_uninstalled").font(.headline)
                Text(model.installedAppURL == nil
                    ? "about.this_copy_of_the_app"
                    : "about.quitting_moves_flock_app_to")
                    .fixedSize(horizontal: false, vertical: true)
                log
                HStack {
                    Spacer()
                    Button("menu.quit_flock") { model.removeAppAndQuit() }
                        .keyboardShortcut(.defaultAction)
                }

            case .failed:
                Text("about.uninstalling_didnt_finish").font(.headline)
                Text(output.contains("administrator permission was declined")
                    ? "about.password_prompt_was_cancelled_so"
                    : "setup.failed")
                    .fixedSize(horizontal: false, vertical: true)
                log
                HStack {
                    Spacer()
                    Button("common.close") { dismiss() }
                        .keyboardShortcut(.cancelAction)
                    Button("setup.try_again") { Task { await run() } }
                        .keyboardShortcut(.defaultAction)
                }
            }
        }
        .padding(20)
        .frame(width: 520)
        .interactiveDismissDisabled(stage == .running)
    }

    private func run() async {
        output = ""
        stage = .running
        let result = await model.uninstall(purge: purgeState.wrappedValue) { line in
            Task { @MainActor in
                if stage == .running {
                    output += line + "\n"
                }
            }
        }
        output = result.output
        stage = result.ok ? .done : .failed
    }

    private var log: some View {
        ScrollView([.vertical, .horizontal]) {
            Text(verbatim: output.isEmpty ? " " : output)
                .font(.system(.caption, design: .monospaced))
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(10)
        }
        .defaultScrollAnchor(.bottom)
        .frame(height: 180)
        .background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 6))
        .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(.separator))
    }
}
