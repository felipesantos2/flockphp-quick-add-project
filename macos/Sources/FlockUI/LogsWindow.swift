import AppKit
import FlockKit
import SwiftUI

struct LogsWindow: View {
    @Environment(AppModel.self) private var model
    @AppStorage("logs.autoRefresh") private var autoRefresh = false
    private let selectionState = State<LogStore.Source?>(initialValue: nil)
    private var selection: LogStore.Source? {
        get { selectionState.wrappedValue }
        nonmutating set { selectionState.wrappedValue = newValue }
    }
    private let queryState = State<String>(initialValue: "")
    private var query: String {
        get { queryState.wrappedValue }
        nonmutating set { queryState.wrappedValue = newValue }
    }
    private let levelState = State<LogLevelFilter>(initialValue: .all)
    private var level: LogLevelFilter {
        get { levelState.wrappedValue }
        nonmutating set { levelState.wrappedValue = newValue }
    }
    private let confirmClearState = State<String?>(initialValue: nil)
    private var confirmClear: String? {
        get { confirmClearState.wrappedValue }
        nonmutating set { confirmClearState.wrappedValue = newValue }
    }

    var body: some View {
        let store = model.logs

        // HSplitView instead of NavigationSplitView: the latter floats the sidebar and lets the
        // detail column's toolbar background bleed under its edge on current macOS.
        HSplitView {
            List(selection: selectionState.projectedValue) {
                Section("settings.pane.sites") {
                    ForEach(store.index.sites, id: \.self) { site in
                        Label(site, systemImage: "globe").tag(LogStore.Source.site(site))
                    }
                }
                Section("settings.pane.services") {
                    ForEach(store.index.services, id: \.self) { service in
                        Label(service, systemImage: "gearshape").tag(LogStore.Source.service(service))
                    }
                }
            }
            .frame(minWidth: 200, idealWidth: 240, maxWidth: 340)
            Group {
                if let selection, store.loaded != selection {
                    ProgressView("common.loading").frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    switch selection {
                    case let .site(name):
                        SiteLogView(entries: LogFilter.entries(store.entries, level: level, query: query),
                                    filtered: !store.entries.isEmpty, site: name)
                    case .service:
                        ServiceLogView(text: store.serviceText.isEmpty ? String(localized: "common.empty_parenthesized") : LogFilter.lines(store.serviceText, query: query))
                    case nil:
                        ContentUnavailableView("logs.pick_log", systemImage: "doc.text.magnifyingglass")
                    }
                }
            }
            .frame(minWidth: 420, maxWidth: .infinity, maxHeight: .infinity)
        }
        .searchable(text: queryState.projectedValue, placement: .toolbar, prompt: "logs.search_logs")
        .toolbar {
            ToolbarItemGroup {
                Picker("logs.level", selection: levelState.projectedValue) {
                    ForEach(LogLevelFilter.allCases) { Text($0.title).tag($0) }
                }
                .pickerStyle(.menu)
                .fixedSize()
                .help("logs.show_only_entries_of_this")
                .disabled({ if case .site = selection { false } else { true } }())

                Toggle(isOn: $autoRefresh) {
                    Label("logs.auto_refresh", systemImage: "arrow.triangle.2.circlepath")
                }
                .help(autoRefresh ? "logs.reloading_the_log_every_2" : "logs.reload_the_log_every_2_seconds")

                Button("mail.refresh", systemImage: "arrow.clockwise") {
                    Task {
                        await store.loadIndex()
                        await reload()
                    }
                }
                if case let .site(name) = selection {
                    Button("logs.clear", systemImage: "trash") {
                        confirmClear = name
                    }
                    .disabled(store.entries.isEmpty)
                }
            }
        }
        .confirmationDialog("logs.clear_question \(confirmClear ?? "")", isPresented: Binding(get: { confirmClear != nil }, set: { if !$0 { confirmClear = nil } }),
                            presenting: confirmClear) { site in
            Button("logs.clear", role: .destructive) {
                Task { await store.clear(site: site) }
            }
            Button("common.cancel", role: .cancel) {}
        } message: { site in
            Text("logs.deletes_the_laravel_log_files \(site)")
        }
        .safeAreaInset(edge: .bottom) {
            ErrorBanner(message: store.error)
        }
        .task {
            await store.loadIndex()
        }
        .task(id: selection) {
            await reload()
        }
        .task(id: store.requested) {
            guard let source = store.requested else { return }
            store.requested = nil
            // The log may be new since the list loaded.
            await store.loadIndex()
            selection = source
        }
        .task(id: AutoRefresh(source: selection, on: autoRefresh)) {
            guard autoRefresh else { return }
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(2))
                if !Task.isCancelled, isVisible {
                    await reload()
                }
            }
        }
    }

    private struct AutoRefresh: Equatable {
        let source: LogStore.Source?
        let on: Bool
    }

    /// The Logs window is on screen (not minimized, hidden or covered entirely).
    private var isVisible: Bool {
        NSApp.windows.contains { $0.identifier?.rawValue.contains(WindowID.logs) == true && $0.occlusionState.contains(.visible) }
    }

    private func reload() async {
        if let selection {
            await model.logs.load(selection)
        }
    }
}

private struct SiteLogView: View {
    let entries: [LogEntry]
    /// There are entries, and the search or level filter hides them all.
    let filtered: Bool
    let site: String

    var body: some View {
        if entries.isEmpty {
            if filtered {
                ContentUnavailableView.search
            } else {
                ContentUnavailableView("logs.no_entries", systemImage: "checkmark.circle",
                                       description: Text("logs.storage_logs_of_site_is \(site)"))
            }
        } else {
            List(entries) { entry in
                LogEntryRow(entry: entry)
            }
        }
    }
}

/// A service's log, scrolled to its newest lines (at the bottom) when it loads or changes.
private struct ServiceLogView: View {
    let text: String

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView([.vertical, .horizontal]) {
                VStack(alignment: .leading, spacing: 0) {
                    Text(text)
                        .font(.system(.callout, design: .monospaced))
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .topLeading)
                    Color.clear.frame(height: 1).id("end")
                }
                .padding(12)
            }
            .onAppear { proxy.scrollTo("end", anchor: .bottom) }
            .onChange(of: text) { proxy.scrollTo("end", anchor: .bottom) }
        }
    }
}

private struct LogEntryRow: View {
    let entry: LogEntry
    private let expandedState = State<Bool>(initialValue: false)
    private var expanded: Bool {
        get { expandedState.wrappedValue }
        nonmutating set { expandedState.wrappedValue = newValue }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text(entry.level.uppercased())
                    .font(.caption2.weight(.bold))
                    .foregroundStyle(.white)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(levelColor, in: RoundedRectangle(cornerRadius: 4))
                Text(entry.time, format: .dateTime.year().month().day().hour().minute().second())
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
                if !entry.env.isEmpty {
                    Text(entry.env).font(.caption).foregroundStyle(.secondary)
                }
            }
            Text(entry.message)
                .font(.callout)
                .lineLimit(expanded ? nil : 3)
                .textSelection(.enabled)
            if !entry.details.isEmpty {
                DisclosureGroup("logs.details", isExpanded: expandedState.projectedValue) {
                    let details = Text(entry.details)
                        .font(.system(.caption, design: .monospaced))
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    // Stack traces run to hundreds of lines; keep the list scrollable around them.
                    if entry.details.reduce(0, { $1 == "\n" ? $0 + 1 : $0 }) > 20 {
                        ScrollView([.vertical, .horizontal]) { details }
                            .frame(height: 280)
                    } else {
                        details
                    }
                }
                .font(.caption)
            }
        }
        .padding(.vertical, 4)
    }

    private var levelColor: Color {
        switch entry.level {
        case "debug": .gray
        case "info": .blue
        case "notice": .teal
        case "warning": .orange
        default: .red
        }
    }
}
