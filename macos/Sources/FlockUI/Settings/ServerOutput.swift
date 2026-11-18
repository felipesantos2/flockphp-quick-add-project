import FlockKit
import SwiftUI

/// The output of a server Flock runs (a Node.js site's dev server, a proxy's command): the last
/// 300 lines of its service log, refreshed every second while `live`, with Clear and Open Log.
struct ServerOutput: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openWindow) private var openWindow
    /// The service log's name.
    let log: String
    /// The server runs or starts, so its output grows.
    let live: Bool

    private let textState = State<String>(initialValue: "")

    /// What the refresh depends on: another log, or the server starting or stopping.
    private struct Refresh: Hashable {
        let log: String
        let live: Bool
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text("sites.output").foregroundStyle(.secondary)
                Spacer()
                Button("logs.clear") {
                    Task {
                        try? await model.client.clearServiceLog(log)
                        textState.wrappedValue = ""
                    }
                }
                Button("sites.open_log") {
                    model.logs.requested = .service(log)
                    openWindow.activating(WindowID.logs)
                }
            }
            .controlSize(.small)
            ScrollViewReader { proxy in
                ScrollView {
                    Text(textState.wrappedValue.isEmpty ? " " : textState.wrappedValue)
                        .font(.system(.caption, design: .monospaced))
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(8)
                    Color.clear.frame(height: 1).id("end")
                }
                .background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 6))
                .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(.separator))
                .onChange(of: textState.wrappedValue, initial: true) { proxy.scrollTo("end", anchor: .bottom) }
            }
            .frame(height: 220)
        }
        .task(id: Refresh(log: log, live: live)) { await refresh() }
    }

    /// Reads the log once, and every second while the server is live (until the pane goes away).
    private func refresh() async {
        let client = model.client
        repeat {
            if let text = try? await client.serviceLog(log, lines: 300), text != textState.wrappedValue {
                textState.wrappedValue = text
            }
            if !live { return }
            try? await Task.sleep(for: .seconds(1))
        } while !Task.isCancelled
    }
}

/// A Node.js site's (or worktree's) dev server: its state, Start, Stop and Restart, the command it
/// runs, and its output.
struct NodeDevServerSection: View {
    @Environment(AppModel.self) private var model
    let site: Status.Site

    /// Start, Stop or Restart is on its way to the daemon.
    private let busyState = State<Bool>(initialValue: false)

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            VStack(alignment: .leading, spacing: 10) {
                Text("sites.dev_server").font(.subheadline.weight(.semibold))
                if let server = site.nodeServer {
                    let state = Self.state(server)
                    row("general.status") {
                        VStack(alignment: .leading, spacing: 4) {
                            HStack(spacing: 6) {
                                StateDot(color: state.color)
                                Text(state.text).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
                            }
                            if server.state != .failed, let problem = server.problem {
                                Text(problem).font(.caption).foregroundStyle(.secondary)
                                    .textSelection(.enabled)
                                    .fixedSize(horizontal: false, vertical: true)
                            }
                        }
                    }
                    HStack(spacing: 8) {
                        switch server.state {
                        case .sleeping, .failed:
                            Button("services.start") { run(stop: false) }
                        case .starting:
                            Button("sites.share.stop") { run(stop: true) }
                        case .running:
                            Button("sites.share.stop") { run(stop: true) }
                            Button("menu.restart") { run(stop: false) }
                        }
                        if busyState.wrappedValue {
                            ProgressView().controlSize(.mini)
                        }
                    }
                    .disabled(busyState.wrappedValue)
                    .controlSize(.small)
                    row("sites.command") {
                        Text(server.command).font(.callout.monospaced()).foregroundStyle(.secondary)
                            .textSelection(.enabled)
                    }
                }
                Text("sites.server.flock_starts_the_dev_server")
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let server = site.nodeServer {
                ServerOutput(log: server.log, live: server.state == .starting || server.state == .running)
            }
        }
    }

    /// A labelled row, lined up like the panes' other settings.
    private func row<Content: View>(_ label: LocalizedStringKey, @ViewBuilder content: () -> Content) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            Text(label).foregroundStyle(.secondary)
                .frame(width: 80, alignment: .leading)
            content()
            Spacer(minLength: 0)
        }
    }

    /// The dot and text of a dev server's state.
    static func state(_ server: Status.SiteNodeServer) -> (color: Color, text: String) {
        switch server.state {
        case .sleeping: (StateDot.stopped, String(localized: "sites.server.sleeping"))
        case .starting: (StateDot.busy, String(localized: "worktrees.job.starting"))
        case .running: (StateDot.running, String(localized: "sites.server.running_on_port \(String(server.port))"))
        case .failed: (StateDot.failed, server.problem.map { String(localized: "sites.server.stopped_because \($0)") } ?? String(localized: "services.postgres.stopped"))
        }
    }

    /// Starts (or restarts) or stops the dev server, by the site's (or worktree's) own name.
    private func run(stop: Bool) {
        let store = model.settings
        let name = site.name
        busyState.wrappedValue = true
        Task {
            await store.perform { client in
                if stop {
                    _ = try await client.stopSite(name)
                } else {
                    _ = try await client.restartSite(name)
                }
            }
            busyState.wrappedValue = false
        }
    }
}

/// The state of what Flock runs for a row (Settings → Sites, the panel): a Node.js site's dev server,
/// a proxy's server. PHP sites have none.
extension Status.Site {
    /// The dev server's dot and its state as help; nil for a PHP site.
    var stateDot: (color: Color, help: String)? {
        guard isNode, let server = nodeServer else { return nil }
        let state = NodeDevServerSection.state(server)
        return (state.color, state.text)
    }

    /// What the row warns about: the site's problem, or what needs a decision.
    var rowProblem: String? { problem ?? notice }
}

extension Status.Proxy {
    /// Its command's state when it has one (failed, on its way), else whether something answers.
    var stateDot: (color: Color, help: String) {
        if let command {
            switch command.state {
            case .failed: return (StateDot.failed, command.problem.map { String(localized: "sites.server.failed_because \($0)") } ?? String(localized: "sites.server.failed"))
            case .starting: return (StateDot.busy, String(localized: "worktrees.job.starting"))
            case .stopping: return (StateDot.busy, String(localized: "sites.server.stopping"))
            case .running, .stopped: break
            }
        }
        return running ? (StateDot.running, server ?? String(localized: "sites.proxy.running")) : (StateDot.stopped, String(localized: "sites.proxy.not_running"))
    }
}

/// A row's problem as an orange ⚠, the text as help.
struct ProblemIcon: View {
    let text: String

    var body: some View {
        Image(systemName: "exclamationmark.triangle.fill").font(.caption).foregroundStyle(.orange)
            .help(text)
    }
}

/// A server's state as a dot: grey stopped or sleeping, amber on the way, green running, red failed.
struct StateDot: View {
    let color: Color

    var body: some View {
        Circle().fill(color).frame(width: 7, height: 7)
    }

    static let stopped = Color.secondary.opacity(0.5)
    static let busy = Color.orange
    static let running = Color.green
    static let failed = Color.red
}

/// The help text under a proxy's command fields.
let proxyCommandsHelp = String(localized: "sites.proxy.command_help")

/// The Start, Stop and Folder fields of a proxy's commands, in a Form.
struct ProxyCommandsFields: View {
    let start: Binding<String>
    let stop: Binding<String>
    let dir: Binding<String>

    // The labels are in the UI font like the form's other fields; only what's typed is monospaced.
    var body: some View {
        LabeledContent("services.start") {
            TextField("services.start", text: start, prompt: Text(verbatim: "npm run dev"))
                .labelsHidden()
                .font(.body.monospaced())
        }
        LabeledContent("sites.share.stop") {
            VStack(alignment: .leading, spacing: 3) {
                TextField("sites.share.stop", text: stop, prompt: Text(verbatim: "docker compose stop"))
                    .labelsHidden()
                    .font(.body.monospaced())
                Text("sites.proxy.stop_command_hint")
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        LabeledContent("general.folder") {
            HStack(spacing: 8) {
                TextField("general.folder", text: dir)
                    .labelsHidden()
                    .font(.body.monospaced())
                Button("common.choose") {
                    let from = URL(fileURLWithPath: dir.wrappedValue)
                    if let url = SettingsStore.chooseDirectories(prompt: String(localized: "sites.server.choose"), multiple: false, in: from).first {
                        dir.wrappedValue = url.path
                    }
                }
            }
        }
    }

    /// The folder new commands run in until another is chosen.
    static var home: String { FileManager.default.homeDirectoryForCurrentUser.path }
}

/// A sheet's form, as tall as it is up to `maxHeight`, scrolling beyond it. A sheet sizes itself to
/// its content and can grow past its window, so the height is capped here: with the sheet's title
/// and buttons (outside this), a sheet stays within the Settings window at its minimum size
/// (810 × 480).
struct ScrollingForm<Content: View>: View {
    var maxHeight: CGFloat = 300
    @ViewBuilder let content: () -> Content

    /// The form's own height, measured.
    private let heightState = State<CGFloat>(initialValue: 0)

    var body: some View {
        ScrollView {
            Form { content() }
                .padding(.trailing, 12) // room for the scroller
                .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { heightState.wrappedValue = $0 }
        }
        .frame(height: min(max(heightState.wrappedValue, 1), maxHeight))
    }
}

/// Edits the commands that start and stop a proxy's server, and their folder.
struct ProxyCommandSheet: View {
    let proxy: Status.Proxy
    let save: (ProxyChange) -> Void
    let cancel: () -> Void

    private let commandState: State<String>
    private let dirState: State<String>
    private let stopState: State<String>

    init(proxy: Status.Proxy, save: @escaping (ProxyChange) -> Void, cancel: @escaping () -> Void) {
        self.proxy = proxy
        self.save = save
        self.cancel = cancel
        commandState = State(initialValue: proxy.command?.command ?? "")
        dirState = State(initialValue: proxy.command?.dir ?? ProxyCommandsFields.home)
        stopState = State(initialValue: proxy.command?.stopCommand ?? "")
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("sites.proxy.commands_title \(proxy.name)").font(.headline)
            ScrollingForm {
                ProxyCommandsFields(start: commandState.projectedValue, stop: stopState.projectedValue, dir: dirState.projectedValue)
            }
            HStack {
                if proxy.command != nil {
                    // An empty start command removes the commands and their folder.
                    Button("sites.proxy.remove_commands", role: .destructive) { save(ProxyChange(command: "")) }
                }
                Spacer()
                Button("common.cancel", role: .cancel) { cancel() }
                    .keyboardShortcut(.cancelAction)
                Button("common.save") {
                    let command = commandState.wrappedValue.trimmingCharacters(in: .whitespaces)
                    save(command.isEmpty
                         ? ProxyChange(command: "")
                         : ProxyChange(command: command, dir: dirState.wrappedValue,
                                       stopCommand: stopState.wrappedValue.trimmingCharacters(in: .whitespaces)))
                }
                .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 520)
    }
}

/// Adds a proxy: its name and upstream, and optionally the commands that start and stop its server.
struct AddProxySheet: View {
    /// name, upstream, then the start command, its folder and the stop command (nil without a start command).
    let add: (String, String, String?, String?, String?) -> Void
    let cancel: () -> Void
    let tld: String

    private let nameState = State<String>(initialValue: "")
    private let upstreamState = State<String>(initialValue: "")
    private let commandsShownState = State<Bool>(initialValue: false)
    private let commandState = State<String>(initialValue: "")
    private let stopState = State<String>(initialValue: "")
    private let dirState = State<String>(initialValue: ProxyCommandsFields.home)

    var body: some View {
        let name = nameState.wrappedValue.trimmingCharacters(in: .whitespaces)
        let upstream = upstreamState.wrappedValue.trimmingCharacters(in: .whitespaces)
        VStack(alignment: .leading, spacing: 12) {
            Text("sites.add_proxy_title").font(.headline)
            ScrollingForm {
                TextField("sites.proxy_name", text: nameState.projectedValue, prompt: Text(verbatim: "my-app → my-app.\(tld)"))
                TextField("sites.upstream", text: upstreamState.projectedValue, prompt: Text("sites.upstream_placeholder_short"))
                    .help("sites.upstream_port_means")
                DisclosureGroup(isExpanded: commandsShownState.projectedValue) {
                    ProxyCommandsFields(start: commandState.projectedValue, stop: stopState.projectedValue, dir: dirState.projectedValue)
                } label: {
                    // The whole row opens it, not only the disclosure arrow.
                    Text("sites.proxy.commands_optional")
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .contentShape(Rectangle())
                        .onTapGesture {
                            withAnimation { commandsShownState.wrappedValue = !commandsShownState.wrappedValue }
                        }
                }
            }
            HStack {
                Spacer()
                Button("common.cancel", role: .cancel) { cancel() }
                    .keyboardShortcut(.cancelAction)
                Button("common.add") {
                    let command = commandState.wrappedValue.trimmingCharacters(in: .whitespaces)
                    if command.isEmpty {
                        add(name, upstream, nil, nil, nil)
                    } else {
                        let stop = stopState.wrappedValue.trimmingCharacters(in: .whitespaces)
                        add(name, upstream, command, dirState.wrappedValue, stop.isEmpty ? nil : stop)
                    }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(name.isEmpty || upstream.isEmpty)
            }
        }
        .padding(20)
        .frame(width: 520)
    }
}
