import FlockKit
import SwiftUI

/// The panes of the Settings window, in sidebar order.
enum SettingsPane: String, CaseIterable, Identifiable {
    case general, sites, php, node, services, mail, dumps, debugger, ai, about

    var id: String { rawValue }

    var title: String {
        switch self {
        case .general: String(localized: "general.title")
        case .sites: String(localized: "settings.pane.sites")
        case .php: "PHP"
        case .node: "Node"
        case .services: String(localized: "settings.pane.services")
        case .mail: String(localized: "services.category.mail")
        case .dumps: String(localized: "menu.dumps")
        case .debugger: String(localized: "settings.pane.debugger")
        case .ai: String(localized: "settings.pane.ai")
        case .about: String(localized: "settings.pane.about")
        }
    }

    var symbol: String {
        switch self {
        case .general: "gearshape.fill"
        case .sites: "globe"
        case .php: "chevron.left.forwardslash.chevron.right"
        case .node: "hexagon.fill"
        case .services: "server.rack"
        case .mail: "envelope.fill"
        case .dumps: "ladybug.fill"
        case .debugger: "ant.fill"
        case .ai: "sparkles"
        case .about: "info"
        }
    }

    var color: Color {
        switch self {
        case .general: .gray
        case .sites: .blue
        case .php: .indigo
        case .node: .green
        case .services: .orange
        case .mail: .cyan
        case .dumps: .pink
        case .debugger: .red
        case .ai: .purple
        case .about: .secondary
        }
    }
}

/// Settings: a sidebar of panes and the selected pane on the right.
/// A plain HStack instead of NavigationSplitView, which lets the toolbar background
/// bleed under its floating sidebar on current macOS.
struct SettingsWindow: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openWindow) private var openWindow
    private var pane: SettingsPane {
        get { model.settingsPane }
        nonmutating set { model.settingsPane = newValue }
    }

    var body: some View {
        let store = model.settings

        HStack(spacing: 0) {
            List(selection: Binding<SettingsPane?>(get: { pane }, set: { if let new = $0 { pane = new } })) {
                ForEach(SettingsPane.allCases) { item in
                    Label {
                        Text(item.title)
                    } icon: {
                        SettingsIcon(symbol: item.symbol, color: item.color)
                    }
                    .tag(item)
                    .padding(.vertical, 2)
                }
            }
            .listStyle(.sidebar)
            .frame(width: 200)
            .safeAreaPadding(.top, 38) // clear the traffic lights

            Divider()

            VStack(spacing: 0) {
                content
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
                // The daemon's buttons here (Start, Restart) report through the model; "not running"
                // is already what the pane shows.
                SettingsError(message: (store.error == model.error ? nil : store.error) ?? model.actionError) {
                    store.error = nil
                    model.actionError = nil
                }
            }
            .background(Color(nsColor: .windowBackgroundColor))
        }
        .ignoresSafeArea(.container, edges: .top)
        .frame(minWidth: 830, minHeight: 480)
        .task {
            store.isVisible = true
            await store.refresh()
        }
        .onDisappear { store.isVisible = false }
    }

    @ViewBuilder
    private var content: some View {
        // About, and General once Flock is set up (its language, updates and login item don't need
        // the daemon), also show while the daemon isn't running.
        if model.status == nil, pane != .about, pane != .general || model.needsSetup {
            if model.needsSetup {
                ContentUnavailableView {
                    Label { Text("settings.flock_isnt_set_up_yet") } icon: { LogoMark(size: 44) }
                } description: {
                    Text("settings.set_flock_up_to_run")
                } actions: {
                    Button("menu.set_up_flock") { openWindow.activating(WindowID.setup) }
                }
            } else {
                ContentUnavailableView {
                    Label { Text("app.not_running") } icon: { LogoMark(size: 44) }
                } description: {
                    Text(model.error ?? String(localized: "settings.start_daemon_to_change"))
                } actions: {
                    Button("menu.start_flock") { Task { await model.restart() } }
                        .disabled(model.isRestarting)
                }
            }
        } else {
            switch pane {
            case .general: GeneralPane()
            case .sites: SitesPane()
            case .php: PHPPane()
            case .node: NodePane()
            case .services: ServicesPane()
            case .mail: MailPane()
            case .dumps: DumpsPane()
            case .debugger: DebuggerPane()
            case .ai: AIPane()
            case .about: AboutPane()
            }
        }
    }
}
