import AppKit
import FlockKit
import SwiftUI

// Building blocks of the service details: cards, the empty "install" state, an install job with
// its progress, and its log.

extension View {
    /// A grouped box like the ones in System Settings.
    func card(padding: CGFloat = 12) -> some View {
        self
            .padding(padding)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(Color(nsColor: .separatorColor).opacity(0.6)))
    }
}

/// The name of a service with its icon and a status line, at the top of its detail.
struct DetailHeader: View {
    let title: String
    let symbol: String
    let color: Color
    let status: String
    var running: Bool?

    var body: some View {
        HStack(spacing: 10) {
            SettingsIcon(symbol: symbol, color: color, size: 34)
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.title3.weight(.semibold))
                HStack(spacing: 5) {
                    if let running {
                        StatusDot(on: running)
                    }
                    Text(status).font(.callout).foregroundStyle(.secondary)
                }
            }
            Spacer()
        }
    }
}

/// A section title inside a detail, with optional trailing content.
struct DetailSectionTitle<Trailing: View>: View {
    let title: LocalizedStringKey
    @ViewBuilder var trailing: Trailing

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            Text(title).font(.subheadline.weight(.semibold))
            Spacer()
            trailing.font(.caption).foregroundStyle(.secondary)
        }
    }
}

extension DetailSectionTitle where Trailing == EmptyView {
    init(_ title: LocalizedStringKey) {
        self.title = title
        trailing = EmptyView()
    }
}

/// A button's label that, while busy, shows a spinner and what the button is doing ("Checking…").
struct BusyLabel: View {
    let title: LocalizedStringKey
    let busyTitle: LocalizedStringKey
    let isBusy: Bool

    var body: some View {
        if isBusy {
            HStack(spacing: 5) {
                ProgressView().controlSize(.mini)
                Text(busyTitle)
            }
        } else {
            Text(title)
        }
    }
}

/// "Last checked 3 hours ago · Check for Updates", with the check's error behind a warning icon.
struct CheckForUpdatesLine: View {
    let checkedAt: Date?
    let error: String
    let disabled: Bool
    let check: () async -> Void
    private let checkingState = State<Bool>(initialValue: false)

    var body: some View {
        HStack(spacing: 5) {
            if !error.isEmpty {
                Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                    .help("common.last_check_failed \(error)")
            }
            if let checkedAt {
                Text("versions.last_checked \(checkedAt, format: .relative(presentation: .named))")
            } else {
                Text("versions.not_checked")
            }
            Text(verbatim: "·")
            if checkingState.wrappedValue {
                ProgressView().controlSize(.mini)
                Text("versions.checking")
            } else {
                Button("general.check_for_updates") {
                    checkingState.wrappedValue = true
                    Task {
                        await check()
                        checkingState.wrappedValue = false
                    }
                }
                .buttonStyle(.link)
                .disabled(disabled)
            }
        }
        .lineLimit(1)
    }
}

enum Formats {
    static func bytes(_ count: Int64) -> String {
        count.formatted(.byteCount(style: .file).locale(AppLanguage.locale))
    }

    /// "Nov 2030".
    static func monthYear(_ date: Date) -> String {
        date.formatted(.dateTime.month(.abbreviated).year().locale(AppLanguage.locale))
    }

    /// "4:05" or "1:02:03".
    static func elapsed(since start: Date, now: Date) -> String {
        let seconds = max(0, Int(now.timeIntervalSince(start)))
        let (h, m, s) = (seconds / 3600, seconds / 60 % 60, seconds % 60)
        return h > 0 ? String(format: "%d:%02d:%02d", h, m, s) : String(format: "%d:%02d", m, s)
    }
}

/// An install that runs in the background: phase, progress, elapsed time, the log and Cancel.
struct ComponentJobProgress: View {
    let title: String
    let job: ComponentJob
    let showLog: () -> Void
    let cancel: () -> Void

    init(title: String, job: ComponentJob, showLog: @escaping () -> Void, cancel: @escaping () -> Void) {
        self.title = title
        self.job = job
        self.showLog = showLog
        self.cancel = cancel
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .firstTextBaseline) {
                Text(title).font(.callout.weight(.medium))
                Spacer()
                if let started = job.startedAt {
                    TimelineView(.periodic(from: .now, by: 1)) { context in
                        Text(Formats.elapsed(since: started, now: context.date))
                            .font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                    }
                }
            }
            if let progress = job.progress {
                ProgressView(value: progress)
            } else {
                ProgressView().progressViewStyle(.linear)
            }
            HStack(spacing: 10) {
                Text("common.it_keeps_going_when_you")
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Spacer(minLength: 4)
                Button("sites.show_log", action: showLog).buttonStyle(.link).font(.caption)
                Button("common.cancel", action: cancel).controlSize(.small)
            }
        }
    }
}

/// A failed install: what went wrong, the log, Try Again and Dismiss.
struct ComponentJobFailure: View {
    /// "MySQL", "Valkey".
    let name: String
    let job: ComponentJob
    let showLog: () -> Void
    let retry: () -> Void
    let dismiss: () -> Void

    init(name: String, job: ComponentJob, showLog: @escaping () -> Void, retry: @escaping () -> Void, dismiss: @escaping () -> Void) {
        self.name = name
        self.job = job
        self.showLog = showLog
        self.retry = retry
        self.dismiss = dismiss
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .top, spacing: 8) {
                Image(systemName: "exclamationmark.octagon.fill").foregroundStyle(.red)
                VStack(alignment: .leading, spacing: 3) {
                    Text(job.action == .update ? "common.couldnt_update \(name)" : "common.couldnt_install \(name)")
                        .font(.callout.weight(.semibold))
                    Text(job.error.isEmpty ? String(localized: "common.install_stopped_with_an_error") : job.error)
                        .font(.caption).foregroundStyle(.secondary)
                        .textSelection(.enabled)
                        .lineLimit(4)
                        .fixedSize(horizontal: false, vertical: true)
                        .help(job.error)
                }
            }
            HStack(spacing: 8) {
                Button("sites.show_log", action: showLog).buttonStyle(.link).font(.caption)
                Spacer()
                Button("sites.share.dismiss", action: dismiss).controlSize(.small)
                Button("setup.try_again", action: retry).controlSize(.small).buttonStyle(.borderedProminent)
            }
        }
        .padding(10)
        .background(Color.red.opacity(0.08), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
    }
}

/// "X is available", what updating means, and the Update button.
struct UpdateStrip: View {
    let title: String
    let note: String
    let help: String
    var busy = false
    var disabled = false
    let update: () -> Void

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: "arrow.up.circle.fill").foregroundStyle(Color.accentColor).font(.title3)
            VStack(alignment: .leading, spacing: 1) {
                Text(title).font(.callout.weight(.medium))
                if !note.isEmpty {
                    Text(note).font(.caption).foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            Spacer(minLength: 6)
            if busy {
                ProgressView().controlSize(.small)
            } else {
                Button("common.update", action: update)
                    .buttonStyle(.borderedProminent)
                    .controlSize(.small)
                    .disabled(disabled)
                    .help(help)
            }
        }
    }
}

/// The tail of an install's output, refreshed while it runs and the sheet is open.
struct JobLogSheet: View {
    @Environment(\.dismiss) private var dismiss
    let title: String
    let running: () -> Bool
    let load: () async -> String
    private let textState = State<String>(initialValue: "")

    var body: some View {
        let text = textState.wrappedValue

        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 8) {
                Text(title).font(.headline)
                if running() {
                    ProgressView().controlSize(.small)
                }
                Spacer()
                Button("common.copy") {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(text, forType: .string)
                }
                .disabled(text.isEmpty)
                Button("common.done") { dismiss() }.keyboardShortcut(.defaultAction)
            }

            ScrollViewReader { proxy in
                ScrollView {
                    Text(text.isEmpty ? String(localized: "worktrees.job.no_log_yet") : text)
                        .font(.system(.caption, design: .monospaced))
                        .foregroundStyle(text.isEmpty ? .secondary : .primary)
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(10)
                    Color.clear.frame(height: 1).id("end")
                }
                .background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 6))
                .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(.separator))
                .onChange(of: text) { proxy.scrollTo("end", anchor: .bottom) }
            }
        }
        .padding(16)
        .frame(width: 720, height: 460)
        .task {
            while !Task.isCancelled {
                let log = await load()
                if log != textState.wrappedValue {
                    textState.wrappedValue = log
                }
                if !running(), !log.isEmpty {
                    break
                }
                try? await Task.sleep(for: .seconds(2))
            }
        }
    }
}

/// What a detail shows before something is installed: what it is, and one obvious button.
struct InstallInvitation<Actions: View, Footer: View>: View {
    let symbol: String
    let color: Color
    let title: String
    let message: String
    @ViewBuilder var actions: Actions
    @ViewBuilder var footer: Footer

    var body: some View {
        VStack(spacing: 12) {
            SettingsIcon(symbol: symbol, color: color, size: 52)
                .shadow(color: color.opacity(0.25), radius: 8, y: 3)
            VStack(spacing: 4) {
                Text(title).font(.title3.weight(.semibold))
                Text(message)
                    .font(.callout).foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .fixedSize(horizontal: false, vertical: true)
            }
            actions
                .padding(.top, 2)
            footer
                .font(.caption).foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .fixedSize(horizontal: false, vertical: true)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 22)
        .padding(.horizontal, 18)
        .card(padding: 0)
    }
}

