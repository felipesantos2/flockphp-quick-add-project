import AppKit
import FlockKit
import Observation

/// Shared state for the menu bar panel and all windows. Polls the daemon's status.
@MainActor
@Observable
final class AppModel {
    let client = FlockClient()
    let installation = Installation.current
    let updater = Updater()

    private(set) var status: Status?
    /// Why the status couldn't be read (the daemon isn't running); replaced by every poll.
    private(set) var error: String?
    /// The last failed user action (restart, a toggle). Polls leave it alone, so it stays until
    /// it is dismissed or the next user action succeeds.
    var actionError: String?
    private(set) var isRestarting = false

    /// Flock isn't set up for this user yet, and this app can do it (see Installation).
    var needsSetup: Bool
    /// The app is replacing the installed CLI with its bundled one.
    private(set) var isUpdating = false

    /// The pane the Settings window shows; the panel opens Settings at a pane.
    var settingsPane: SettingsPane = .general
    /// The service selected in Settings → Services.
    var servicesSelection: String?
    /// A site (or worktree) Settings → Sites is asked to select, e.g. from a notification.
    var pendingSite: String?
    /// Tells about worktree setups that failed where no window shows them.
    let worktreeNotifier = WorktreeNotifier()

    /// Points the Settings window at a pane (and a service of the Services pane).
    func showSettings(_ pane: SettingsPane, service: String? = nil) {
        settingsPane = pane
        if let service {
            servicesSelection = service
        }
    }

    /// A flock://install link waiting for the user to confirm it in Settings → Services (or PHP, Node).
    var pendingInstall: InstallLink?

    /// A flock://secure link waiting for the user to confirm it in Settings → Sites.
    var pendingSecure: SecureLink?

    /// Shows the service a link asks to install (PHP and Node.js have their own panes), or the sites
    /// for a link that asks to secure one; the pane then asks to confirm. Before the setup there are
    /// no sites to secure. An update link (`flock update`) checks for a new version of the app.
    func handle(_ link: FlockLink) {
        switch link {
        case .install(let link):
            pendingInstall = link
            switch link.service {
            case "php": showSettings(.php)
            case "node": showSettings(.node)
            default: showSettings(.services, service: link.service)
            }
        case .secure(let link):
            guard !needsSetup else { return }
            pendingSecure = link
            showSettings(.sites)
        case .update:
            showSettings(.general)
            updater.checkForUpdates()
        case .site(let name):
            pendingSite = name
            showSettings(.sites)
        }
    }

    let mail: MailStore
    let dumps: DumpStore
    let logs: LogStore
    let settings: SettingsStore

    @ObservationIgnored private var pollTask: Task<Void, Never>?

    /// UserDefaults key: hash of the CLI this app installed last (see Installation.needsUpdate).
    private static let installedFlockKey = "installedFlock"

    init() {
        mail = MailStore(client: client)
        dumps = DumpStore(client: client)
        logs = LogStore(client: client)
        settings = SettingsStore(client: client)
        needsSetup = installation.needsSetup
        startPolling()
        settings.onChange = { [weak self] in await self?.refresh() }

        if !needsSetup, installation.needsUpdate(recorded: UserDefaults.standard.string(forKey: Self.installedFlockKey)) {
            Task { await updateInstallation() }
        }
        updater.start()
    }

    var isRunning: Bool { status != nil }
    var isDegraded: Bool { status?.isDegraded ?? false }

    /// The logomark for the daemon's state: stopped is dimmed, a warning adds a badge.
    var logoMark: LogoMark.Mode {
        guard isRunning, !needsSetup else { return .stopped }
        return isDegraded ? .warning : .running
    }

    /// "Running · v1.2", "Needs attention · v1.2", "Not running" (or "Not set up", "Updating…").
    var headline: String {
        if isUpdating {
            return String(localized: "common.updating")
        }
        // Before the setup is done, a daemon that answers is the setup's (it may still fail).
        if needsSetup {
            return String(localized: "menu.status.not_set_up")
        }
        if let status {
            return "\(status.isDegraded ? String(localized: "menu.status.needs_attention") : String(localized: "sites.proxy.running")) · v\(status.version)"
        }
        return String(localized: "sites.proxy.not_running")
    }

    private func startPolling() {
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                await self?.refresh()
                try? await Task.sleep(for: .seconds(2))
            }
        }
    }

    func refresh() async {
        do {
            apply(try await client.status())
        } catch {
            status = nil
            self.error = error.localizedDescription
        }
    }

    private func apply(_ new: Status) {
        let previous = status
        status = new
        error = nil
        worktreeNotifier.check(previous: previous, new: new)
        updater.channel = new.channel
        Task { await settings.refreshIfVisible() }

        // Lists in open windows follow the counters in the status.
        if previous?.mailCount != new.mailCount {
            Task { await mail.refreshIfVisible() }
        }
        if previous?.dumpCount != new.dumpCount || previous?.eventCount != new.eventCount {
            Task { await dumps.refreshIfVisible() }
        }
        // PostgreSQL or a catalog service changed (maybe from the CLI): the Services pane's
        // versions follow.
        if previous != nil, previous?.postgres != new.postgres
            || previous?.extras.map(\.version) != new.extras.map(\.version) {
            Task { await settings.refreshInstallsIfLoaded() }
        }
    }

    /// Where a toggle's error is shown: the panel's banner, the Settings window's error bar or
    /// the Dumps window's.
    enum ErrorTarget {
        case panel, settings, dumps
    }

    func setXdebug(_ on: Bool, from target: ErrorTarget = .panel) async {
        await update(target) { try await $0.updateSettings(xdebug: on) }
    }

    func setXdebugBreakpoints(_ on: Bool, from target: ErrorTarget = .settings) async {
        await update(target) { try await $0.updateSettings(xdebugBreakpoints: on) }
    }

    func setDumps(_ on: Bool, from target: ErrorTarget = .panel) async {
        await update(target) { try await $0.updateSettings(dumps: on) }
    }

    /// Captures events of `kind` or stops capturing them; the other kinds stay as they are.
    func setCapture(_ kind: Event.Kind, _ on: Bool, from target: ErrorTarget = .dumps) async {
        var off = Set(status?.captureOff ?? [])
        if on { off.remove(kind.rawValue) } else { off.insert(kind.rawValue) }
        let list = Event.Kind.allCases.map(\.rawValue).filter(off.contains) + off.subtracting(Event.Kind.allCases.map(\.rawValue)).sorted()
        await update(target) { try await $0.updateSettings(SettingsChange(captureOff: list)) }
    }

    func setDumpsInPage(_ on: Bool, from target: ErrorTarget = .dumps) async {
        await update(target) { try await $0.updateSettings(SettingsChange(dumpsInPage: on)) }
    }

    private func update(_ target: ErrorTarget, _ change: @Sendable (FlockClient) async throws -> Status) async {
        do {
            apply(try await change(client))
            actionError = nil
            switch target {
            case .panel: break
            case .settings: settings.error = nil
            case .dumps: dumps.error = nil
            }
        } catch {
            switch target {
            case .panel: actionError = error.localizedDescription
            case .settings: settings.error = error.localizedDescription
            case .dumps: dumps.error = error.localizedDescription
            }
        }
    }

    /// Runs `flock restart` (which also starts a stopped daemon via launchd).
    func restart() async {
        isRestarting = true
        defer { isRestarting = false }

        let binary = FlockPaths.flockBinary
        guard FileManager.default.fileExists(atPath: binary.path) else {
            actionError = needsSetup
                ? String(localized: "app.flock_is_not_set_up")
                : String(localized: "app.flock_is_not_installed_for \(binary.path)")
            return
        }
        let result = await Installation.run(binary, ["restart"])
        actionError = result.ok ? nil : result.output
        await refresh()
    }

    /// Runs the bundled `flock install` after the app was updated. It restarts the daemon; macOS
    /// asks for the password only if DNS or the CA need setting up again.
    func updateInstallation() async {
        isUpdating = true
        let result = await installation.install()
        isUpdating = false
        recordInstalled()
        if !result.ok {
            let alert = NSAlert()
            alert.alertStyle = .warning
            alert.messageText = String(localized: "updates.could_not_update")
            alert.informativeText = "\(result.output)\n\n" + String(localized: "app.you_can_run_flock_install")
            NSApp.activate()
            alert.runModal()
        }
        await refresh()
    }

    /// Runs the bundled `flock install` for the setup window.
    func install(onLine: @escaping @Sendable (String) -> Void) async -> Installation.Result {
        isSettingUp = true
        let result = await installation.install(onLine: onLine)
        isSettingUp = false
        needsSetup = installation.needsSetup
        recordInstalled()
        if result.ok {
            actionError = nil
        }
        await refresh()
        return result
    }

    /// Other programs on Flock's ports, which the first setup waits for the user to quit.
    func conflicts() async -> [Installation.Conflict] {
        await installation.conflicts()
    }

    /// The setup window runs `flock install`, which registers and starts the daemon before its last steps.
    @ObservationIgnored private var isSettingUp = false

    /// Reads again whether Flock is set up, when the panel opens: `flock install` or `flock uninstall`
    /// in a terminal changes it while the app runs. Not during the app's own setup or update, which
    /// register the daemon before they are done.
    func refreshSetup() {
        guard !isSettingUp, !isUpdating, installation.needsSetup != needsSetup else { return }
        needsSetup = installation.needsSetup
    }

    /// Remembers the CLI this app installed, so later updates can tell it from a hand-installed one.
    /// After every `flock install`, also a failed one: it copies the CLI first, so one that failed
    /// later (e.g. at a declined password prompt) still installed it.
    private func recordInstalled() {
        guard let bundled = installation.bundledFlock else { return }
        let installed = Installation.hash(installation.installedFlock)
        if !installed.isEmpty, installed == Installation.hash(bundled) {
            UserDefaults.standard.set(installed, forKey: Self.installedFlockKey)
        }
    }

    /// Runs `flock uninstall --keep-app` (the app removes itself afterwards, see removeApp);
    /// purge also deletes the user's settings and databases.
    func uninstall(purge: Bool, onLine: @escaping @Sendable (String) -> Void) async -> Installation.Result {
        guard let flock = installation.uninstaller else {
            return Installation.Result(ok: false, output: String(localized: "app.flocks_cli_is_not_installed"))
        }
        let result = await Installation.run(flock, ["uninstall", "--keep-app"] + (purge ? ["--purge"] : []), onLine: onLine)
        needsSetup = installation.needsSetup
        await refresh()
        return result
    }

    /// Where this app runs from, when that is an Applications folder (not a development build).
    var installedAppURL: URL? {
        let app = Bundle.main.bundleURL
        let folders = [URL(fileURLWithPath: "/Applications"), FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Applications")]
        guard app.pathExtension == "app", folders.contains(where: { $0.standardizedFileURL.path == app.deletingLastPathComponent().standardizedFileURL.path }) else {
            return nil
        }
        return app
    }

    /// After uninstalling: forgets the app's preferences, moves the app to the Trash and quits.
    func removeAppAndQuit() {
        if let domain = Bundle.main.bundleIdentifier {
            UserDefaults.standard.removePersistentDomain(forName: domain)
        }
        if let app = installedAppURL {
            try? FileManager.default.trashItem(at: app, resultingItemURL: nil)
        }
        NSApp.terminate(nil)
    }

    func open(_ site: Status.Site) {
        guard let string = site.url, let url = URL(string: string) else { return }
        NSWorkspace.shared.open(url)
    }
}
