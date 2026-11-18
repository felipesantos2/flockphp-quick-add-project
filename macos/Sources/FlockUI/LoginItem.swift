import Foundation
import ServiceManagement

/// Flock's menu bar app as a login item: the setup's checkbox and Settings → General.
enum LoginItem {
    static var isEnabled: Bool { SMAppService.mainApp.status == .enabled }

    /// Turns it on or off. Returns what to tell the user when that didn't work, or still needs
    /// their approval in System Settings.
    static func set(_ on: Bool) -> String? {
        do {
            if on {
                try SMAppService.mainApp.register()
            } else {
                try SMAppService.mainApp.unregister()
            }
        } catch {
            return String(localized: "general.could_not_change_the_login \(error.localizedDescription)")
        }
        return SMAppService.mainApp.status == .requiresApproval
            ? String(localized: "general.allow_flock_in_system_settings")
            : nil
    }
}
