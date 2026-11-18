import AppKit
import FlockKit
import UserNotifications

/// Posts a notification when a worktree's setup fails and no window follows it: worktrees an
/// agent app or git created, or whose progress was hidden. Clicking it shows the worktree in
/// Settings → Sites. Notifications need the app bundle: without it (swift run, the screenshots
/// tool) nothing is posted.
@MainActor
final class WorktreeNotifier: NSObject, UNUserNotificationCenterDelegate {
    /// Worktrees whose setup a progress view shows, by path.
    private var followed: Set<String> = []
    private var authorized: Bool?

    private var center: UNUserNotificationCenter? {
        Bundle.main.bundleURL.pathExtension == "app" ? UNUserNotificationCenter.current() : nil
    }

    override init() {
        super.init()
        center?.delegate = self
    }

    func follow(_ path: String) { followed.insert(path) }
    func unfollow(_ path: String) { followed.remove(path) }

    /// Notifies of the worktrees whose setup failed since the last status.
    func check(previous: Status?, new: Status) {
        guard let previous else { return }
        let before = Dictionary(previous.sites.map { ($0.name, $0.worktree?.state) }, uniquingKeysWith: { a, _ in a })
        for site in new.sites where site.isWorktree && site.worktree?.state == .failed && !followed.contains(site.path) {
            // Not failed before, or not there at all: a setup can fail before a status shows it running.
            if (before[site.name] ?? nil) != .failed {
                post(site)
            }
        }
    }

    private func post(_ worktree: Status.Site) {
        guard let center else { return }
        Task {
            if authorized == nil {
                authorized = (try? await center.requestAuthorization(options: [.alert, .sound])) ?? false
            }
            guard authorized == true else { return }
            let content = UNMutableNotificationContent()
            content.title = String(localized: "worktrees.setup_failed_title \(worktree.worktreeLabel)")
            content.body = "\(worktree.parent ?? ""): \(worktree.worktree?.error ?? String(localized: "worktrees.see_log"))"
            content.userInfo = ["site": worktree.name]
            try? await center.add(UNNotificationRequest(identifier: "worktree-\(worktree.name)", content: content, trigger: nil))
        }
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse) async {
        guard let name = response.notification.request.content.userInfo["site"] as? String else { return }
        await MainActor.run { AppDelegate.open(.site(name)) }
    }

    /// Shown also while Flock is the active app.
    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification) async -> UNNotificationPresentationOptions {
        [.banner, .list, .sound]
    }
}
