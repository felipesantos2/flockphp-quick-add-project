import Foundation
import Testing
@testable import FlockKit

/// Answers the client's requests in place of the daemon.
private final class StubProtocol: URLProtocol, @unchecked Sendable {
    struct Request: Sendable {
        var method: String
        var url: String
        var authorization: String
        var body: String
        var timeout: TimeInterval
    }

    private static let lock = NSLock()
    nonisolated(unsafe) private static var respond: @Sendable (Request) -> (Int, String) = { _ in (200, "{}") }
    nonisolated(unsafe) private static var seen: [Request] = []

    static func reset(_ respond: @escaping @Sendable (Request) -> (Int, String)) {
        lock.withLock {
            self.respond = respond
            seen = []
        }
    }

    static var requests: [Request] { lock.withLock { seen } }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var body = request.httpBody ?? Data()
        if body.isEmpty, let stream = request.httpBodyStream {
            stream.open()
            var buffer = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable {
                let n = stream.read(&buffer, maxLength: buffer.count)
                if n <= 0 { break }
                body.append(buffer, count: n)
            }
            stream.close()
        }
        let seen = Request(method: request.httpMethod ?? "GET", url: request.url?.absoluteString ?? "",
                           authorization: request.value(forHTTPHeaderField: "Authorization") ?? "",
                           body: String(decoding: body, as: UTF8.self), timeout: request.timeoutInterval)
        let (status, text) = Self.lock.withLock {
            Self.seen.append(seen)
            return Self.respond
        }(seen)

        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: nil)!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(text.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

@Suite(.serialized)
struct ClientTests {
    private let dir: URL

    init() throws {
        dir = FileManager.default.temporaryDirectory.appendingPathComponent("flock-client-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    }

    private func apiJSON(url: String = "http://127.0.0.1:53531", token: String) throws -> URL {
        let path = dir.appendingPathComponent("api.json")
        try Data(#"{"url":"\#(url)","token":"\#(token)"}"#.utf8).write(to: path)
        return path
    }

    private func client(_ path: URL) -> FlockClient {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [StubProtocol.self]
        return FlockClient(configPath: path, configuration: configuration)
    }

    @Test func sendsTheBearerTokenAndDecodesNullListsAsEmpty() async throws {
        StubProtocol.reset { _ in (200, "null") }
        let client = client(try apiJSON(token: "secret"))

        #expect(try await client.mail().isEmpty)
        #expect(try await client.services().isEmpty)
        let request = try #require(StubProtocol.requests.first)
        #expect(request.authorization == "Bearer secret")
        #expect(request.url == "http://127.0.0.1:53531/v1/mail")
    }

    @Test func rereadsAPIJSONAfterUnauthorized() async throws {
        StubProtocol.reset { request in
            request.authorization == "Bearer new"
                ? (200, #"{"services":["web"],"sites":null}"#)
                : (401, #"{"error":"missing or invalid token"}"#)
        }
        let path = try apiJSON(token: "old")
        let client = client(path)

        await #expect(throws: FlockError.server(status: 401, message: "missing or invalid token")) {
            try await client.logs()
        }
        // The daemon restarted with a new token after the client cached the old one.
        _ = try apiJSON(token: "new")
        let index = try await client.logs()
        #expect(index.services == ["web"] && index.sites.isEmpty)
        #expect(StubProtocol.requests.map(\.authorization).last == "Bearer new")
        _ = path
    }

    @Test func reportsServerErrorsWithTheDaemonsMessage() async throws {
        StubProtocol.reset { _ in (409, #"{"error":"PHP 8.5 is the default version"}"#) }
        let client = client(try apiJSON(token: "t"))

        await #expect(throws: FlockError.server(status: 409, message: "PHP 8.5 is the default version")) {
            try await client.uninstallPHP("8.5")
        }
        #expect(StubProtocol.requests.first?.method == "DELETE")
        #expect(FlockError.server(status: 409, message: "PHP 8.5 is the default version").localizedDescription == "PHP 8.5 is the default version")
        #expect(FlockError.server(status: 502, message: "").localizedDescription == "Flock answered with HTTP 502")
    }

    @Test func isNotRunningWithoutAPIJSONOrListener() async throws {
        let missing = FlockClient(configPath: dir.appendingPathComponent("nope.json"))
        await #expect(throws: FlockError.notRunning) { try await missing.status() }

        // Port 9 (discard) on loopback has no listener: the connection is refused.
        let refused = FlockClient(configPath: try apiJSON(url: "http://127.0.0.1:9", token: "t"))
        await #expect(throws: FlockError.notRunning) { try await refused.status() }
    }

    @Test func sendsOnlyGivenFieldsWhenLinkingSites() async throws {
        StubProtocol.reset { _ in (200, #"{"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z"}"#) }
        let client = client(try apiJSON(token: "t"))

        _ = try await client.linkSite(path: "/code/shop")
        let body = try #require(StubProtocol.requests.first?.body)
        let json = try JSONSerialization.jsonObject(with: Data(body.utf8)) as? [String: Any]
        #expect(json?.keys.sorted() == ["path", "secured"])
        #expect(json?["path"] as? String == "/code/shop")
        #expect(json?["secured"] as? Bool == false)
    }

    @Test func talksToTheProxyEndpoints() async throws {
        StubProtocol.reset { _ in (200, #"{"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z"}"#) }
        let client = client(try apiJSON(token: "t"))

        _ = try await client.proxy(name: "softpack", upstream: "3000")
        _ = try await client.proxy(name: "softpack", upstream: "3001", secured: true)
        _ = try await client.secureProxy("soft pack", false)
        _ = try await client.removeProxy("softpack")

        let requests = StubProtocol.requests
        #expect(requests.map(\.method) == ["POST", "POST", "PATCH", "DELETE"])
        #expect(requests.map { URL(string: $0.url)?.path ?? "" } == ["/v1/proxies", "/v1/proxies", "/v1/proxies/soft pack", "/v1/proxies/softpack"])
        // A new proxy leaves secured out, so the daemon keeps an existing one's setting.
        let first = try JSONSerialization.jsonObject(with: Data(requests[0].body.utf8)) as? [String: Any]
        #expect(first?.keys.sorted() == ["name", "upstream"] && first?["upstream"] as? String == "3000")
        let second = try JSONSerialization.jsonObject(with: Data(requests[1].body.utf8)) as? [String: Any]
        #expect(second?["secured"] as? Bool == true)
        #expect(requests[2].body == #"{"secured":false}"#)
    }

    @Test func givesSlowEndpointsLongerThanTheDefault() async throws {
        StubProtocol.reset { request in
            request.url.contains("/php/") ? (200, #"{"versions":[]}"#) : request.url.contains("/node/") ? (200, #"{"versions":[]}"#)
                : (200, #"{"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z"}"#)
        }
        let client = client(try apiJSON(token: "t"))

        _ = try await client.status()
        // The daemon may ask nodejs.org first (~30 s on a cold cache).
        _ = try await client.nodeVersions()
        // Changing PHP versions waits for the daemon's work, like the Go CLI (2 min).
        _ = try await client.installPHP("8.4")
        _ = try await client.cancelPHPInstall("8.4")
        _ = try await client.uninstallPHP("8.4")
        _ = try await client.phpVersions()

        #expect(StubProtocol.requests.map(\.timeout) == [5, 60, 120, 120, 120, 5])
    }
    @Test func talksToThePostgresVersionEndpoints() async throws {
        StubProtocol.reset { _ in (200, #"{"active":"18","port":5432,"checked_at":null,"majors":[]}"#) }
        let client = client(try apiJSON(token: "t"))

        _ = try await client.postgresVersions()
        _ = try await client.checkPostgres()
        _ = try await client.installPostgres("17")
        _ = try await client.updatePostgres("18")
        _ = try await client.activatePostgres("17")
        _ = try await client.uninstallPostgres("16")
        let versions = try await client.uninstallPostgres("15", deleteData: true)
        #expect(versions.active == "18")

        let base = "http://127.0.0.1:53531/v1/postgres"
        #expect(StubProtocol.requests.map { "\($0.method) \($0.url)" } == [
            "GET \(base)/versions",
            "POST \(base)/check",
            "POST \(base)/versions/17/install",
            "POST \(base)/versions/18/update",
            "POST \(base)/versions/17/activate",
            "DELETE \(base)/versions/16",
            "DELETE \(base)/versions/15?delete_data=1",
        ])
        #expect(StubProtocol.requests.map(\.timeout) == [5, 60, 600, 600, 120, 120, 120])
    }

    @Test func talksToTheCatalogEndpoints() async throws {
        StubProtocol.reset { request in
            request.url.hasSuffix("/log")
                ? (request.url.contains("mysql") ? (200, "==> Downloading\n") : (404, #"{"error":"no install log yet"}"#))
                : (200, #"{"checked_at":null,"services":[{"id":"mysql","job":{"action":"install","state":"running","phase":"downloading","progress":0.5,"version":"8.4.7","line":"8.4"}}]}"#)
        }
        let client = client(try apiJSON(token: "t"))

        _ = try await client.serviceCatalog()
        _ = try await client.checkServiceCatalog()
        let started = try await client.installCatalogService("mysql", line: "8.4", port: 3307)
        #expect(started["mysql"]?.job?.version == "8.4.7" && started["mysql"]?.job?.job.progress == 0.5)
        _ = try await client.installCatalogService("redis")
        _ = try await client.cancelCatalogJob("mysql")
        _ = try await client.dismissCatalogJob("mysql")
        _ = try await client.activateCatalogService("mysql", line: "9.7")
        _ = try await client.uninstallCatalogService("mysql")
        _ = try await client.uninstallCatalogService("mysql", line: "8.4")
        _ = try await client.uninstallCatalogService("mongodb", line: "7.0", deleteData: true)
        #expect(try await client.catalogLog("mysql") == "==> Downloading\n")
        #expect(try await client.catalogLog("redis") == "")

        let base = "http://127.0.0.1:53531/v1/catalog"
        #expect(StubProtocol.requests.map { "\($0.method) \($0.url)" } == [
            "GET \(base)",
            "POST \(base)/check",
            "POST \(base)/mysql/install",
            "POST \(base)/redis/install",
            "POST \(base)/mysql/cancel",
            "DELETE \(base)/mysql/job",
            "POST \(base)/mysql/activate",
            "DELETE \(base)/mysql",
            "DELETE \(base)/mysql?line=8.4",
            "DELETE \(base)/mongodb?line=7.0&delete_data=1",
            "GET \(base)/mysql/log",
            "GET \(base)/redis/log",
        ])
        let bodies = StubProtocol.requests.map(\.body)
        #expect(bodies[2].contains(#""line":"8.4""#) && bodies[2].contains(#""port":3307"#))
        #expect(bodies[3].contains(#""line":"""#) && bodies[3].contains(#""port":0"#))
        #expect(bodies[6].contains(#""line":"9.7""#))
        #expect(StubProtocol.requests.map(\.timeout) == [5, 60, 30, 30, 30, 30, 60, 60, 60, 60, 5, 5])
    }

    @Test func talksToTheAIClientAndBoostEndpoints() async throws {
        StubProtocol.reset { request in
            request.url.contains("/boost")
                ? (200, #"{"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","sites":[]}"#)
                : (200, #"{"clients":[{"id":"cursor","name":"Cursor","state":"registered"}],"command":"/flock","args":["mcp"],"snippet":"{}"}"#)
        }
        let client = client(try apiJSON(token: "t"))

        _ = try await client.mcp()
        let added = try await client.registerMCP("cursor")
        #expect(added.clients.first?.state == .registered)
        _ = try await client.unregisterMCP("cursor")
        _ = try await client.addBoostGuideline(site: "my app")

        let base = "http://127.0.0.1:53531/v1"
        #expect(StubProtocol.requests.map { "\($0.method) \($0.url)" } == [
            "GET \(base)/mcp",
            "POST \(base)/mcp/clients/cursor",
            "DELETE \(base)/mcp/clients/cursor",
            "POST \(base)/sites/my%20app/boost",
        ])
        #expect(StubProtocol.requests.map(\.timeout) == [5, 30, 30, 120])
    }

    @Test func listsAppTemplatesAndCreatesApps() async throws {
        StubProtocol.reset { request in
            request.url.contains("/templates")
                ? (200, #"{"templates":[{"name":"blank","requires_db":false},{"name":"wordpress","requires_db":true}]}"#)
                : (200, #"{"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","sites":[]}"#)
        }
        let client = client(try apiJSON(token: "t"))

        let templates = try await client.appTemplates()
        #expect(templates == [
            AppTemplate(name: "blank", requiresDb: false),
            AppTemplate(name: "wordpress", requiresDb: true),
        ])

        _ = try await client.createApp(CreateAppRequest(
            name: "my-blog",
            parentDir: "/Users/demo/Sites",
            type: "wordpress",
            db: true,
            dbService: "mysql",
            secured: true
        ))

        let base = "http://127.0.0.1:53531/v1"
        #expect(StubProtocol.requests.map { "\($0.method) \($0.url)" } == [
            "GET \(base)/apps/templates",
            "POST \(base)/apps",
        ])
        let body = try JSONSerialization.jsonObject(with: Data(StubProtocol.requests[1].body.utf8)) as? [String: Any]
        #expect(body?["name"] as? String == "my-blog")
        #expect(body?["parent_dir"] as? String == "/Users/demo/Sites")
        #expect(body?["type"] as? String == "wordpress")
        #expect(body?["db"] as? Bool == true)
        #expect(body?["db_service"] as? String == "mysql")
        #expect(body?["secured"] as? Bool == true)
        #expect(body?["php"] == nil)
        #expect(StubProtocol.requests.map(\.timeout) == [5, 180])
    }
}
