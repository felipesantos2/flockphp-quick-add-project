import CryptoKit
import Foundation

/// Setting Flock up from the app. build-app.sh puts the flock CLI next to the app's executable;
/// `flock install` copies it to the data dir, registers the daemon's LaunchAgent and, with one
/// password prompt, sets up DNS and trusts the CA. Development builds (`swift run`) have no
/// bundled CLI and leave installing to the terminal.
public struct Installation: Sendable {
    /// The CLI shipped with this app, or nil for a development build.
    public var bundledFlock: URL?
    /// Where `flock install` puts the CLI.
    public var installedFlock: URL
    /// The daemon's LaunchAgent, written by `flock install`.
    public var launchAgent: URL

    public init(bundledFlock: URL?, installedFlock: URL = FlockPaths.flockBinary, launchAgent: URL = Installation.defaultLaunchAgent) {
        self.bundledFlock = bundledFlock
        self.installedFlock = installedFlock
        self.launchAgent = launchAgent
    }

    /// The installation of the running app.
    public static var current: Installation {
        let bundled = Bundle.main.executableURL?.deletingLastPathComponent().appendingPathComponent("flock")
        return Installation(bundledFlock: bundled.flatMap { FileManager.default.isExecutableFile(atPath: $0.path) ? $0 : nil })
    }

    public static var defaultLaunchAgent: URL {
        FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/LaunchAgents/dev.flock.daemon.plist")
    }

    /// The CLI is installed and the daemon is registered to start at login.
    public var isSetUp: Bool {
        FileManager.default.fileExists(atPath: installedFlock.path) && FileManager.default.fileExists(atPath: launchAgent.path)
    }

    /// Flock isn't set up for this user yet, and this app can do it.
    public var needsSetup: Bool { bundledFlock != nil && !isSetUp }

    /// The installed CLI should be replaced by the bundled one: it differs, and the installed one is
    /// what this app installed last time (`recorded`, or nothing is recorded yet). A CLI installed
    /// by hand from another build is left alone.
    public func needsUpdate(recorded: String?) -> Bool {
        guard let bundledFlock, isSetUp else { return false }
        let installed = Self.hash(installedFlock)
        if installed == Self.hash(bundledFlock) {
            return false
        }
        return recorded?.isEmpty ?? true || recorded == installed
    }

    /// SHA-256 of a file, hex; empty when it can't be read.
    public static func hash(_ url: URL) -> String {
        guard let data = try? Data(contentsOf: url, options: .mappedIfSafe) else { return "" }
        return SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }

    /// Output of a CLI run.
    public struct Result: Sendable, Equatable {
        public var ok: Bool
        public var output: String

        public init(ok: Bool, output: String) {
            self.ok = ok
            self.output = output
        }

        /// The password dialog was cancelled (runPrivileged in internal/cli/install_darwin.go).
        public var permissionDeclined: Bool { output.contains("administrator permission was declined") }
    }

    /// Runs `flock install` from the bundled CLI, passing each output line to onLine.
    public func install(onLine: (@Sendable (String) -> Void)? = nil) async -> Result {
        guard let bundledFlock else {
            return Result(ok: false, output: String(localized: "setup.no_bundled_cli"))
        }
        return await Self.run(bundledFlock, ["install"], onLine: onLine)
    }

    /// Another program on one of Flock's ports, or answering for the TLD (`flock conflicts --json`).
    /// The first setup waits until it is quit.
    public struct Conflict: Decodable, Sendable, Equatable, Hashable {
        public var port: Int?
        /// The resolver file another program set up for the TLD.
        public var file: String?
        /// What Flock needs the port for: "HTTP", "*.test DNS".
        public var service: String
        /// "Herd (nginx-arm64, pid 812)".
        public var holder: String
        /// What to quit: "Herd", or the program when no app is known.
        public var app: String
        /// "quit Herd", "run `brew services stop nginx`".
        public var hint: String?
        public var message: String

        public init(port: Int? = nil, file: String? = nil, service: String, holder: String, app: String, hint: String? = nil, message: String = "") {
            self.port = port
            self.file = file
            self.service = service
            self.holder = holder
            self.app = app
            self.hint = hint
            self.message = message
        }
    }

    /// One app to quit and what it is in the way of.
    public struct ConflictingApp: Sendable, Equatable, Identifiable {
        public var app: String
        public var hint: String?
        public var conflicts: [Conflict]
        public var id: String { app }

        /// "Quit Herd", "Run `brew services stop nginx`".
        public var title: String {
            guard let hint, let first = hint.first else { return String(localized: "app.conflict.quit \(app)") }
            return first.uppercased() + hint.dropFirst()
        }

        /// "It uses port 80 (HTTP) and port 443 (HTTPS), and answers for *.test."
        public var detail: String {
            let holder = conflicts.first?.holder ?? app
            let ports = Self.joined(conflicts.compactMap { c in c.port.map { String(localized: "app.conflict.port \(String($0)) \(c.service)") } })
            let dns = conflicts.first(where: { $0.file != nil }).map { "\($0.service.replacingOccurrences(of: " DNS", with: "")) (\($0.file!))" }
            let text = switch (ports.isEmpty, dns) {
            case (false, let dns?): String(localized: "app.uses_and_answers_for \(holder) \(ports) \(dns)")
            case (false, nil): String(localized: "app.program_uses_ports \(holder) \(ports)")
            case (true, let dns?): String(localized: "app.answers_for \(holder) \(dns)")
            case (true, nil): "\(holder)."
            }
            return text.prefix(1).uppercased() + text.dropFirst()
        }

        private static func joined(_ items: [String]) -> String {
            items.count < 2 ? items.joined() : String(localized: "common.list.and \(items.dropLast().joined(separator: ", ")) \(items.last!)")
        }
    }

    /// The conflicts grouped by the app to quit, in the order they were found.
    public static func group(_ conflicts: [Conflict]) -> [ConflictingApp] {
        var apps: [ConflictingApp] = []
        for c in conflicts {
            if let i = apps.firstIndex(where: { $0.app == c.app }) {
                apps[i].conflicts.append(c)
            } else {
                apps.append(ConflictingApp(app: c.app, hint: c.hint, conflicts: [c]))
            }
        }
        return apps
    }

    /// Asks the bundled CLI which other programs use Flock's ports. Empty when there are none, and
    /// when the check itself fails: `flock install` checks again and reports them.
    public func conflicts() async -> [Conflict] {
        guard let bundledFlock else { return [] }
        let result = await Self.run(bundledFlock, ["conflicts", "--json"])
        struct Response: Decodable { var conflicts: [Conflict] }
        guard result.ok, let response = try? FlockJSON.decoder().decode(Response.self, from: Data(result.output.utf8)) else {
            return []
        }
        return response.conflicts
    }

    /// The CLI to uninstall with: the bundled one, else the installed one.
    public var uninstaller: URL? {
        bundledFlock ?? (FileManager.default.isExecutableFile(atPath: installedFlock.path) ? installedFlock : nil)
    }

    /// Runs a flock command without a terminal (stdin is /dev/null, so it asks for the administrator
    /// password with macOS' dialog), streaming stdout and stderr line by line.
    public static func run(_ binary: URL, _ arguments: [String], onLine: (@Sendable (String) -> Void)? = nil) async -> Result {
        await withCheckedContinuation { continuation in
            let process = Process()
            process.executableURL = binary
            process.arguments = arguments
            process.standardInput = FileHandle.nullDevice
            let pipe = Pipe()
            process.standardOutput = pipe
            process.standardError = pipe

            let collector = LineCollector(onLine: onLine)
            pipe.fileHandleForReading.readabilityHandler = { handle in
                let data = handle.availableData
                if data.isEmpty {
                    handle.readabilityHandler = nil
                    collector.finish()
                } else {
                    collector.append(data)
                }
            }
            process.terminationHandler = { process in
                // Wait for the pipe to drain: the handler sees end-of-file once the CLI's output is read.
                collector.waitUntilFinished()
                continuation.resume(returning: Result(ok: process.terminationStatus == 0, output: collector.output))
            }
            do {
                try process.run()
            } catch {
                pipe.fileHandleForReading.readabilityHandler = nil
                continuation.resume(returning: Result(ok: false, output: String(localized: "app.could_not_run \(binary.path) \(error.localizedDescription)")))
            }
        }
    }
}

/// Splits streamed output into lines; called from the pipe's and the process' threads.
private final class LineCollector: @unchecked Sendable {
    private let lock = NSCondition()
    private var buffer = Data()
    private var text = ""
    private var finished = false
    private let onLine: (@Sendable (String) -> Void)?

    init(onLine: (@Sendable (String) -> Void)?) {
        self.onLine = onLine
    }

    func append(_ data: Data) {
        lock.lock()
        buffer.append(data)
        var lines: [String] = []
        while let newline = buffer.firstIndex(of: UInt8(ascii: "\n")) {
            lines.append(String(decoding: buffer[buffer.startIndex..<newline], as: UTF8.self))
            buffer.removeSubrange(buffer.startIndex...newline)
        }
        lines.forEach { text += $0 + "\n" }
        lock.unlock()
        lines.forEach { onLine?($0) }
    }

    func finish() {
        lock.lock()
        var last: String?
        if !buffer.isEmpty {
            last = String(decoding: buffer, as: UTF8.self)
            text += last!
            buffer.removeAll()
        }
        finished = true
        lock.broadcast()
        lock.unlock()
        if let last {
            onLine?(last)
        }
    }

    func waitUntilFinished() {
        lock.lock()
        // A child that left the pipe open (a daemon it started) must not hold the result forever.
        let deadline = Date().addingTimeInterval(5)
        while !finished, lock.wait(until: deadline) {}
        lock.unlock()
    }

    var output: String {
        lock.lock()
        defer { lock.unlock() }
        return text.trimmingCharacters(in: .whitespacesAndNewlines)
    }
}
