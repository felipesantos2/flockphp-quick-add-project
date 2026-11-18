import AppKit
import FlockKit
import SwiftUI

struct MailWindow: View {
    @Environment(AppModel.self) private var model
    private let selectionState = State<MailSummary.ID?>(initialValue: nil)
    private var selection: MailSummary.ID? {
        get { selectionState.wrappedValue }
        nonmutating set { selectionState.wrappedValue = newValue }
    }
    private let confirmClearState = State<Bool>(initialValue: false)
    private var confirmClear: Bool {
        get { confirmClearState.wrappedValue }
        nonmutating set { confirmClearState.wrappedValue = newValue }
    }

    var body: some View {
        let store = model.mail

        // HSplitView instead of NavigationSplitView: the latter floats the sidebar and lets the
        // detail column's toolbar background bleed under its edge on current macOS.
        HSplitView {
            List(store.messages, selection: selectionState.projectedValue) { message in
                MailRow(message: message)
            }
            .overlay {
                if store.messages.isEmpty {
                    ContentUnavailableView("mail.no_mail", systemImage: "envelope.open")
                }
            }
            .frame(minWidth: 260, idealWidth: 320, maxWidth: 340)
            Group {
                if let selection {
                    MailDetailView(id: selection)
                        .id(selection)
                } else if store.env.isEmpty {
                    ContentUnavailableView("mail.no_message_selected", systemImage: "envelope")
                } else {
                    MailSetupView(env: store.env)
                }
            }
            .frame(minWidth: 420, maxWidth: .infinity, maxHeight: .infinity)
        }
        .toolbar {
            ToolbarItemGroup {
                Button("mail.refresh", systemImage: "arrow.clockwise") {
                    Task { await store.refresh() }
                }
                Button("mail.clear_all", systemImage: "trash") {
                    confirmClear = true
                }
                .disabled(store.messages.isEmpty)
            }
        }
        .confirmationDialog("mail.delete_all_question", isPresented: confirmClearState.projectedValue) {
            Button("mail.delete_all", role: .destructive) {
                selection = nil
                Task { await store.clear() }
            }
        }
        .safeAreaInset(edge: .bottom) {
            ErrorBanner(message: store.error)
        }
        .task {
            store.isVisible = true
            await store.refresh()
        }
        .onDisappear { store.isVisible = false }
        .onChange(of: store.messages) {
            // Cleared elsewhere (the CLI, another inbox's clear): drop the selection and its detail.
            if let selection, !store.messages.contains(where: { $0.id == selection }) {
                self.selection = nil
            }
        }
    }
}

/// What a project's .env needs for Flock to catch its mail, with a copy button; shown while no
/// message is selected.
private struct MailSetupView: View {
    let env: String

    var body: some View {
        VStack(spacing: 12) {
            Image(systemName: "envelope")
                .font(.system(size: 40))
                .foregroundStyle(.secondary)
            Text("mail.catch_title").font(.title2.weight(.semibold))
            Text("mail.catch_description")
                .foregroundStyle(.secondary)
            CodeBlock(text: env)
            Text("mail.username_inbox")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: 420)
        .padding(24)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

private struct MailRow: View {
    let message: MailSummary

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack {
                Text(message.from).font(.callout.weight(.medium)).lineLimit(1)
                Spacer()
                Text(message.date, format: .relative(presentation: .named))
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Text(message.subject.isEmpty ? String(localized: "mail.no_subject") : message.subject).lineLimit(1)
            HStack(spacing: 4) {
                Text(message.inbox)
                    .font(.caption2)
                    .padding(.horizontal, 5)
                    .background(Color.accentColor.opacity(0.18), in: Capsule())
                Text(message.to.joined(separator: ", "))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .padding(.vertical, 3)
    }
}

private struct MailDetailView: View {
    enum Mode: String, CaseIterable, Identifiable {
        case html = "HTML", text = "Text", raw = "Raw", headers = "Headers"
        var id: Self { self }

        var title: String {
            switch self {
            case .html: "HTML"
            case .text: String(localized: "mail.detail.text")
            case .raw: String(localized: "mail.detail.raw")
            case .headers: String(localized: "mail.detail.headers")
            }
        }
    }

    let id: String

    @Environment(AppModel.self) private var model
    private let detailState = State<MailDetail?>(initialValue: nil)
    private var detail: MailDetail? {
        get { detailState.wrappedValue }
        nonmutating set { detailState.wrappedValue = newValue }
    }
    private let rawState = State<Result<String, RawError>?>(initialValue: nil)
    private var raw: Result<String, RawError>? {
        get { rawState.wrappedValue }
        nonmutating set { rawState.wrappedValue = newValue }
    }

    struct RawError: Error {
        let message: String
    }
    private let modeState = State<Mode>(initialValue: Mode.html)
    private var mode: Mode {
        get { modeState.wrappedValue }
        nonmutating set { modeState.wrappedValue = newValue }
    }
    private let errorState = State<String?>(initialValue: nil)
    private var error: String? {
        get { errorState.wrappedValue }
        nonmutating set { errorState.wrappedValue = newValue }
    }

    var body: some View {
        Group {
            if let detail {
                VStack(alignment: .leading, spacing: 0) {
                    header(detail)
                    Divider()
                    content(detail)
                }
            } else if let error {
                ContentUnavailableView("mail.could_not_load", systemImage: "exclamationmark.triangle", description: Text(error))
            } else {
                ProgressView()
            }
        }
        .task {
            do {
                let loaded = try await model.mail.detail(id)
                detail = loaded
                if loaded.html.isEmpty {
                    mode = .text
                }
            } catch {
                self.error = error.localizedDescription
            }
        }
        .task(id: mode) {
            // Loaded once; a failure is shown, and switching back to Raw tries again.
            if mode == .raw, raw == nil || (try? raw?.get()) == nil {
                do {
                    raw = .success(try await model.mail.raw(id))
                } catch {
                    raw = .failure(RawError(message: error.localizedDescription))
                }
            }
        }
    }

    private func header(_ detail: MailDetail) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(detail.subject.isEmpty ? String(localized: "mail.no_subject") : detail.subject).font(.title3.weight(.semibold))
            Grid(alignment: .leading, horizontalSpacing: 8, verticalSpacing: 2) {
                field("mail.detail.from", detail.from)
                field("mail.detail.to", detail.to.joined(separator: ", "))
                if !detail.cc.isEmpty {
                    field("mail.detail.cc", detail.cc.joined(separator: ", "))
                }
                field("mail.detail.date", detail.date.formatted(Date.FormatStyle(date: .abbreviated, time: .standard).locale(AppLanguage.locale)))
                field("mail.detail.inbox", detail.inbox)
            }
            .font(.callout)

            if !detail.attachments.isEmpty {
                ScrollView(.horizontal) {
                    HStack {
                        ForEach(detail.attachments) { attachment in
                            Button {
                                Task { await model.mail.save(attachment, of: detail.id) }
                            } label: {
                                Label("common.name_with_detail \(attachment.filename) \(Formats.bytes(Int64(attachment.size)))", systemImage: "paperclip")
                            }
                            .help("mail.save_attachment")
                        }
                    }
                    .padding(.vertical, 2)
                }
                .scrollIndicators(.automatic)
                .controlSize(.small)
            }

            Picker("mail.view", selection: modeState.projectedValue) {
                ForEach(Mode.allCases) { Text($0.title).tag($0) }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .frame(maxWidth: 360)
        }
        .padding(14)
        .textSelection(.enabled)
    }

    private func field(_ name: LocalizedStringKey, _ value: String) -> some View {
        GridRow {
            Text(name).foregroundStyle(.secondary)
            Text(value)
        }
    }

    @ViewBuilder
    private func content(_ detail: MailDetail) -> some View {
        switch mode {
        case .html:
            if detail.html.isEmpty {
                ContentUnavailableView("mail.no_html_part", systemImage: "doc.richtext")
            } else {
                WebView(html: detail.html)
                    .background(Color.white)
            }
        case .text:
            MonospacedText(detail.text.isEmpty ? String(localized: "mail.no_text_part") : detail.text)
        case .raw:
            switch raw {
            case let .success(text):
                MonospacedText(text)
            case let .failure(error):
                ContentUnavailableView("mail.could_not_load_the_raw_message", systemImage: "exclamationmark.triangle", description: Text(error.message))
            case nil:
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        case .headers:
            Table(detail.headers) {
                TableColumn("sites.proxy_name", value: \.name).width(min: 120, ideal: 180, max: 260)
                TableColumn("mail.value", value: \.value)
            }
        }
    }
}

extension MailDetail.Header: Identifiable {
    public var id: String { name + "\u{0}" + value }
}

/// Scrollable, selectable monospaced text.
struct MonospacedText: View {
    let text: String

    init(_ text: String) {
        self.text = text
    }

    var body: some View {
        ScrollView([.vertical, .horizontal]) {
            Text(text)
                .font(.system(.callout, design: .monospaced))
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .topLeading)
                .padding(12)
        }
    }
}

/// A red strip shown at the bottom of a window when the last request failed.
struct ErrorBanner: View {
    let message: String?

    var body: some View {
        if let message {
            Label(message, systemImage: "exclamationmark.triangle.fill")
                .font(.callout)
                .foregroundStyle(.white)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(8)
                .background(.red.opacity(0.85))
        }
    }
}
