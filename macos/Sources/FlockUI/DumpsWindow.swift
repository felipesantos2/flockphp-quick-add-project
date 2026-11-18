import AppKit
import FlockKit
import SwiftUI

/// A terminal-like timeline: newest request on top, its dumps, queries, logs, views and jobs one
/// after another, no sidebar.
struct DumpsWindow: View {
    @Environment(AppModel.self) private var model
    @AppStorage("dumps.latestOnly") private var latestOnly = true
    @AppStorage("dumps.editor") private var editorID = Editor.phpstorm.rawValue

    private let queryState = State<String>(initialValue: "")
    private var query: String {
        get { queryState.wrappedValue }
        nonmutating set { queryState.wrappedValue = newValue }
    }

    private let confirmClearState = State<Bool>(initialValue: false)
    private var confirmClear: Bool {
        get { confirmClearState.wrappedValue }
        nonmutating set { confirmClearState.wrappedValue = newValue }
    }

    private var editor: Editor { Editor(rawValue: editorID) ?? .phpstorm }

    var body: some View {
        let store = model.dumps
        let capturing = model.status?.dumps == true
        let all = DumpTimeline.merge(dumps: store.dumps, events: store.events)
        let visible = DumpTimeline.filter(latestOnly ? DumpTimeline.latestGroup(of: all) : all, query: query)

        Group {
            if visible.isEmpty {
                ContentUnavailableView {
                    Label(all.isEmpty ? "dumps.nothing_captured_yet_sentence_case" : "dumps.nothing_matches_sentence_case", systemImage: "ladybug")
                } description: {
                    Text(capturing
                        ? "dumps.empty_capturing"
                        : "dumps.capture_is_off_turn_it")
                } actions: {
                    if !capturing {
                        Button("dumps.start_capturing") { Task { await model.setDumps(true, from: .dumps) } }
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                WebView(html: DumpTimeline.html(items: visible, header: store.header, editor: editor),
                        allowsJavaScript: true, blocksRemoteContent: true)
            }
        }
        .searchable(text: queryState.projectedValue, placement: .toolbar, prompt: "dumps.search_dumps_queries_logs")
        .toolbar {
            ToolbarItemGroup {
                Toggle(isOn: Binding(get: { capturing }, set: { on in Task { await model.setDumps(on, from: .dumps) } })) {
                    Label(capturing ? "dumps.capturing" : "dumps.capture_dumps", systemImage: "antenna.radiowaves.left.and.right")
                }
                .help(capturing ? "dumps.stop_capturing_dumps_queries_logs" : "dumps.capture_dumps_queries_logs_views")
                .symbolEffect(.pulse, isActive: capturing)
                .disabled(model.status == nil)

                captureMenu

                Toggle(isOn: $latestOnly) {
                    Label("dumps.latest_only", systemImage: "clock.arrow.circlepath")
                }
                .help(latestOnly ? "dumps.showing_the_latest_request_only" : "dumps.showing_all_requests")

                Menu {
                    Picker("dumps.open_sources_in", selection: $editorID) {
                        ForEach(Editor.allCases) { Text($0.name).tag($0.rawValue) }
                    }
                    .pickerStyle(.inline)
                } label: {
                    Label("dumps.editor", systemImage: "chevron.left.forwardslash.chevron.right")
                }
                .help("dumps.editor_tooltip")

                Button("logs.clear", systemImage: "trash") {
                    confirmClear = true
                }
                .disabled(all.isEmpty)
            }
        }
        .navigationTitle(visible.isEmpty ? "menu.dumps" : "dumps.dumps \(String(visible.count))")
        .confirmationDialog("dumps.clear_everything_captured", isPresented: confirmClearState.projectedValue) {
            Button("logs.clear", role: .destructive) {
                Task { await store.clear() }
            }
            Button("common.cancel", role: .cancel) {}
        } message: {
            Text("dumps.every_captured_dump_query_log")
        }
        .safeAreaInset(edge: .bottom) {
            VStack(spacing: 0) {
                if capturing, let versions = model.status?.captureUnsupported, !versions.isEmpty {
                    DumpsOnlyNote(versions: versions)
                }
                ErrorBanner(message: store.error)
            }
        }
        .task {
            store.isVisible = true
            await store.refresh()
        }
        .onDisappear { store.isVisible = false }
    }

    /// What is captured besides dumps, and whether dump() also shows in the page.
    private var captureMenu: some View {
        let off = Set(model.status?.captureOff ?? [])
        return Menu {
            Section("dumps.capture_short") {
                ForEach(Event.Kind.allCases) { kind in
                    Toggle(kind.title, isOn: Binding(get: { !off.contains(kind.rawValue) },
                                                     set: { on in Task { await model.setCapture(kind, on) } }))
                }
            }
            Divider()
            Toggle("dumps.show_dump_in_page_too", isOn: Binding(get: { model.status?.dumpsInPage == true },
                                                         set: { on in Task { await model.setDumpsInPage(on) } }))
        } label: {
            Label("dumps.capture_options", systemImage: "line.3.horizontal.decrease.circle")
        }
        .help("dumps.what_is_captured_besides_dumps")
        .disabled(model.status == nil)
    }
}

/// PHP versions whose build lacks Flock's agent extension only capture dumps.
struct DumpsOnlyNote: View {
    let versions: [String]

    var body: some View {
        let list = versions.formatted(.list(type: .and).locale(AppLanguage.locale))
        // The subject is a list, not a count: one version or several.
        Label(versions.count == 1
              ? "dumps.php_only_captures_dumps_its \(list)"
              : "dumps.php_only_capture_dumps_their \(list)",
              systemImage: "info.circle")
            .font(.callout)
            .foregroundStyle(.secondary)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(8)
            .background(.bar)
    }
}
