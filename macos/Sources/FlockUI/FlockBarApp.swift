import AppKit
import FlockKit
import SwiftUI

/// Note on `State`: with only the Command Line Tools installed, `@State` cannot be used —
/// the macOS SDK expands it as a macro whose plugin ships with Xcode only. The property
/// wrapper type itself works, so views declare `State` values explicitly instead.
public struct FlockBarApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    private let modelState = State<AppModel>(initialValue: AppModel())
    private var model: AppModel {
        get { modelState.wrappedValue }
        nonmutating set { modelState.wrappedValue = newValue }
    }

    public init() {}

    /// Every scene gets the app's locale (AppLanguage.locale): the system's would format dates in
    /// another language than the windows when Settings → General picks one.
    public var body: some Scene {
        MenuBarExtra {
            PanelView()
                .environment(model)
                .environment(\.locale, AppLanguage.locale)
        } label: {
            MenuBarLabel(model: model)
        }
        .menuBarExtraStyle(.window)

        Window("services.category.mail", id: WindowID.mail) {
            MailWindow()
                .environment(model)
                .environment(\.locale, AppLanguage.locale)
        }
        .defaultSize(width: 980, height: 640)

        Window("menu.dumps", id: WindowID.dumps) {
            DumpsWindow()
                .environment(model)
                .environment(\.locale, AppLanguage.locale)
        }
        .defaultSize(width: 900, height: 600)

        Window("dumps.event_kind.logs", id: WindowID.logs) {
            LogsWindow()
                .environment(model)
                .environment(\.locale, AppLanguage.locale)
        }
        .defaultSize(width: 980, height: 640)

        Window("settings.window_title", id: WindowID.settings) {
            SettingsWindow()
                .environment(model)
                .environment(\.locale, AppLanguage.locale)
        }
        .defaultSize(width: 860, height: 560)
        .windowStyle(.hiddenTitleBar)
        .windowResizability(.contentMinSize)

        Window("setup.window_title", id: WindowID.setup) {
            SetupWindow()
                .environment(model)
                .environment(\.locale, AppLanguage.locale)
        }
        .windowResizability(.contentSize)
        .defaultPosition(.center)
    }
}

/// Opens the main window when the app is started by hand (Finder, Spotlight, Launchpad), also
/// while it already runs. The panel belongs to the menu bar icon; the login item stays quiet.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    static let reopen = Notification.Name("dev.flock.reopen")
    static let launched = Notification.Name("dev.flock.launched")

    /// Whether the launch asked for the main window, until the menu bar label takes it. Decided here
    /// and kept, because the label can appear before or after the launch's Apple event arrives.
    private(set) static var pendingMain = false

    static func takePendingMain() -> Bool {
        defer { pendingMain = false }
        return pendingMain
    }

    /// The launch's Apple event only arrives by now; in applicationWillFinishLaunching it is nil.
    /// The login item's launch carries 'prdt':'lgit' and stays quiet.
    func applicationDidFinishLaunching(_ notification: Notification) {
        let event = NSAppleEventManager.shared().currentAppleEvent
        let launchedAtLogin = event?.eventID == kAEOpenApplication
            && event?.paramDescriptor(forKeyword: keyAEPropData)?.enumCodeValue == keyAELaunchedAsLogInItem
        guard !launchedAtLogin else { return }
        Self.pendingMain = true
        NotificationCenter.default.post(name: Self.launched, object: nil)
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        NotificationCenter.default.post(name: Self.reopen, object: nil)
        return false
    }

    static let openLink = Notification.Name("dev.flock.openLink")

    /// The flock:// link macOS handed over last, until the menu bar label takes it. Kept here
    /// because a link that starts the app can arrive before any view listens.
    private(set) static var pendingLink: FlockLink?

    static func takePendingLink() -> FlockLink? {
        defer { pendingLink = nil }
        return pendingLink
    }

    /// Opens a link from inside the app (a notification), as if macOS had handed it over.
    static func open(_ link: FlockLink) {
        pendingLink = link
        NotificationCenter.default.post(name: openLink, object: nil)
    }

    /// flock:// links (CFBundleURLTypes in build-app.sh), e.g. from the website's install buttons,
    /// the page served for a site that isn't secured, or `flock update`.
    func application(_ application: NSApplication, open urls: [URL]) {
        guard let link = urls.lazy.compactMap(FlockLink.init(url:)).first else { return }
        Self.pendingLink = link
        NotificationCenter.default.post(name: Self.openLink, object: nil)
    }
}

/// The menu bar icon. It is the only view that exists at launch, so it also opens the main
/// window when started by hand, and Settings → Services (or PHP, Node) or Sites for a flock:// link.
private struct MenuBarLabel: View {
    let model: AppModel
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        LogoMark(mode: model.logoMark)
            .task {
                openPending()
            }
            .onReceive(NotificationCenter.default.publisher(for: AppDelegate.launched)) { _ in
                openPending()
            }
            .onReceive(NotificationCenter.default.publisher(for: AppDelegate.reopen)) { _ in
                openMain()
            }
            .onReceive(NotificationCenter.default.publisher(for: AppDelegate.openLink)) { _ in
                openPending()
            }
    }

    /// A pending link opens what it points to, otherwise the main window if the launch asked for it.
    private func openPending() {
        let main = AppDelegate.takePendingMain()
        if let link = AppDelegate.takePendingLink() {
            open(link)
        } else if main {
            openMain()
        }
    }

    /// The setup until Flock is set up, then Settings.
    private func openMain() {
        openWindow.activating(model.needsSetup ? WindowID.setup : WindowID.settings)
    }

    /// Before the setup an install link waits: Services (or PHP, Node) asks about it once Flock is set up. An
    /// update link opens Settings only once Flock is set up; Sparkle's window shows the check.
    private func open(_ link: FlockLink) {
        model.handle(link)
        if link == .update, model.needsSetup {
            return
        }
        openMain()
    }
}

enum WindowID {
    static let mail = "mail"
    static let dumps = "dumps"
    static let logs = "logs"
    static let settings = "settings"
    static let setup = "setup"
}

extension OpenWindowAction {
    /// Agent apps (no Dock icon) don't come to the front on their own when a window opens.
    @MainActor
    func activating(_ id: String) {
        callAsFunction(id: id)
        NSApp.activate()
    }
}
