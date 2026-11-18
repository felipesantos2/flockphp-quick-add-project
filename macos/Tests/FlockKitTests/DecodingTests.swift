import Foundation
import Testing
@testable import FlockKit

private func decode<T: Decodable>(_ type: T.Type, _ json: String) throws -> T {
    try FlockJSON.decoder().decode(T.self, from: Data(json.utf8))
}

@Test func parsesGoTimesWithAndWithoutFractions() throws {
    let whole = try #require(FlockJSON.parseDate("2026-10-13T13:40:07+02:00"))
    let nanos = try #require(FlockJSON.parseDate("2026-10-13T13:40:07.740103123+02:00"))
    let utc = try #require(FlockJSON.parseDate("2026-10-13T11:40:07.5Z"))

    #expect(abs(nanos.timeIntervalSince(whole) - 0.740103123) < 0.000001)
    #expect(utc.timeIntervalSince(whole) == 0.5)
    #expect(FlockJSON.parseDate("yesterday") == nil)
}

@Test func decodesAFullStatus() throws {
    let status = try decode(Status.self, """
    {"version":"0893ae2","pid":91994,"started_at":"2026-10-13T14:10:01.123456+02:00",
     "dns":{"tld":"test","addr":"127.0.0.1:53535"},"http_port":80,"https_port":443,
     "mail":"127.0.0.1:2525","postgres":"18.6.0 on 127.0.0.1:5432",
     "extras":[{"id":"mysql","name":"MySQL","version":"8.4.6","port":3306,"enabled":true,"up":true},{"id":"redis"}],
     "xdebug":true,"xdebug_breakpoints":true,"dumps":false,"mail_count":3,"dump_count":12,
     "sites":[{"name":"watchtower","domain":"watchtower.test","path":"/code/watchtower","php":"8.5","secured":true,"parked":false,"url":"https://watchtower.test","debugging":true},
              {"name":"legacy","domain":"legacy.test","path":"/code/legacy","php":"7.4","secured":false,"parked":true,"problem":"PHP 7.4 is not installed"}],
     "processes":[{"name":"php8.5-fpm","up":true,"restarts":0},{"name":"postgres","up":false,"restarts":3,"last_error":"exit status 1: could not bind"}],
     "warnings":["postgres: port 5432 is used by postgres (pid 1)"],"channel":"beta"}
    """)

    #expect(status.httpsPort == 443)
    #expect(status.extras.map(\.id) == ["mysql", "redis"] && status.extras[0].up && status.extras[0].port == 3306)
    #expect(status.extras[1].name == "redis" && !status.extras[1].up && status.extras[1].enabled)
    #expect(status.mailCount == 3 && status.dumpCount == 12)
    #expect(status.sites[0].url == "https://watchtower.test")
    #expect(status.xdebugBreakpoints && status.sites[0].debugging && !status.sites[1].debugging)
    #expect(status.sites[1].url == nil && status.sites[1].problem != nil)
    #expect(status.processes[1].lastError == "exit status 1: could not bind")
    #expect(status.isDegraded)
    #expect(status.channel == "beta")
}

@Test func decodesProxies() throws {
    let status = try decode(Status.self, """
    {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z",
     "proxies":[{"name":"softpack","domain":"softpack.test","upstream":"http://127.0.0.1:3000","secured":true,"url":"https://softpack.test","running":true,"server":"Node.js 22.23.4"},
                {"name":"blog","domain":"blog.test","upstream":"http://127.0.0.1:3001","secured":false,"problem":"blog.test is served by a site of the same name","running":false}]}
    """)
    #expect(status.proxies.map(\.id) == ["softpack", "blog"])
    #expect(status.proxies[0].secured && status.proxies[0].url == "https://softpack.test" && status.proxies[0].problem == nil)
    #expect(status.proxies[0].running && status.proxies[0].server == "Node.js 22.23.4")
    #expect(status.proxies[1].url == nil && status.proxies[1].problem != nil && !status.proxies[1].running && status.proxies[1].server == nil)

    // An older daemon sends none.
    #expect(try decode(Status.self, #"{"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z"}"#).proxies.isEmpty)
}

@Test func decodesExtraDomainsAndTheWildcardState() throws {
    let status = try decode(Status.self, """
    {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z",
     "dns":{"tld":"test","addr":"127.0.0.1:53535","wildcard":true,"max_depth":4},
     "http_port":80,"https_port":443,"mail":"","xdebug":false,"dumps":false,"mail_count":0,"dump_count":0,"processes":[],
     "sites":[{"name":"app","domain":"app.test","path":"/code/app","php":"8.5","secured":true,"parked":false,"url":"https://app.test",
               "domains":[{"entry":"tenant1","host":"tenant1.app.test","alias":false,"url":"https://tenant1.app.test"},
                          {"entry":"promo.test","host":"promo.test","alias":true,"problem":"promo.test is already served by promo"}]},
              {"name":"blog","domain":"blog.test","path":"/code/blog","php":"8.5","secured":false,"parked":true,"domains":null}]}
    """)

    #expect(status.dns.wildcard && status.dns.wildcardProblem.isEmpty && status.dns.maxDepth == 4)
    let app = status.sites[0]
    #expect(app.domains.map(\.host) == ["tenant1.app.test", "promo.test"])
    #expect(app.domains[0].url == "https://tenant1.app.test" && !app.domains[0].alias)
    #expect(app.domains[1].alias && app.domains[1].url == nil && app.domains[1].problem != nil)
    #expect(status.sites[1].domains.isEmpty)
}

@Test func decodesTheDocumentRoot() throws {
    let status = try decode(Status.self, """
    {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","processes":[],
     "sites":[{"name":"zoo","domain":"zoo.test","path":"/code/zoo","php":"8.5","root":".","root_source":"default","root_pinned":"","root_file":"",
               "root_state":"not_found","root_suggestions":["src/www"],"notice":"No document root found"},
              {"name":"shop","domain":"shop.test","path":"/code/shop","php":"8.5","root":"web","root_source":"isolated",
               "root_pinned":"public","root_file":"/code/shop/.flockrc"},
              {"name":"old","domain":"old.test","path":"/code/old","php":"8.5"},
              {"name":"cms","domain":"cms.test","path":"/code/cms","php":"8.5","root":"src/www","root_source":"isolated","root_state":"empty"}]}
    """)

    let zoo = status.sites[0]
    #expect(zoo.rootState == .notFound && zoo.rootSuggestions == ["src/www"] && zoo.notice == "No document root found")
    #expect(zoo.root == "." && zoo.rootSource == .default && !zoo.rootPinnedHere)
    let shop = status.sites[1]
    #expect(shop.root == "web" && shop.rootSource == .isolated && shop.rootPinned == "public" && shop.rootPinnedHere && shop.rootState == .ok)
    // A daemon from before document roots: the project folder, detected.
    let old = status.sites[2]
    #expect(old.root == "." && old.rootSource == .default && old.rootState == .ok && old.rootSuggestions.isEmpty && old.notice == nil)
    #expect(status.sites[3].rootState == .empty && status.sites[3].notice == nil)
}

@Test func turnsAChosenFolderIntoADocumentRoot() throws {
    let site = try decode(Status.Site.self, #"{"name":"zoo","path":"/code/zoo"}"#)
    #expect(site.root(for: URL(fileURLWithPath: "/code/zoo/src/www")) == "src/www")
    #expect(site.root(for: URL(fileURLWithPath: "/code/zoo/src/www/")) == "src/www")
    #expect(site.root(for: URL(fileURLWithPath: "/code/zoo")) == ".")
    #expect(site.root(for: URL(fileURLWithPath: "/code/zoo-2/www")) == nil)
    #expect(site.root(for: URL(fileURLWithPath: "/code")) == nil)
}

@Test func decodesAMinimalStatusWithOmittedFields() throws {
    let status = try decode(Status.self, """
    {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","dns":{"tld":"test","addr":""},
     "http_port":80,"https_port":443,"mail":"127.0.0.1:2525","xdebug":false,"dumps":false,
     "mail_count":0,"dump_count":0,"sites":null,"processes":[]}
    """)

    #expect(status.postgres == nil && status.extras.isEmpty)
    #expect(status.sites.isEmpty && status.warnings.isEmpty)
    #expect(!status.isDegraded)
    #expect(status.channel == "stable")
    #expect(status.defaults == nil)
}

@Test func decodesTheFirstRunsDefaultInstalls() throws {
    let status = try decode(Status.self, """
    {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","processes":[],
     "defaults":{"php":{"version":"8.5","state":"installing","error":""},"node":{"state":"waiting"}}}
    """)

    let defaults = try #require(status.defaults)
    #expect(defaults.php == Status.DefaultInstall(version: "8.5", state: .installing))
    #expect(defaults.node.version.isEmpty && defaults.node.state == .waiting && defaults.node.isPending)

    let failed = try decode(Status.Defaults.self, """
    {"php":{"version":"8.5","state":"done"},"node":{"version":"24","state":"failed","error":"checksum mismatch"}}
    """)
    #expect(failed.php.state == .done && !failed.php.isPending)
    #expect(failed.node.state == .failed && failed.node.error == "checksum mismatch" && !failed.node.isPending)

    let odd = try decode(Status.Defaults.self, #"{"php":{"version":"8.5","state":"something-new"}}"#)
    #expect(odd.php.state == .unknown && odd.node == Status.DefaultInstall())
}

@Test func decodesMailListAndDetail() throws {
    let list = try decode([MailSummary].self, """
    [{"id":"1791891607740103000-000001","inbox":"watchtower","from":"App <app@watchtower.test>","to":["you@example.com"],
      "subject":"Flock mail check ✓","date":"2026-10-13T13:40:07+02:00","size":321,"path":"/x/1.eml"}]
    """)
    #expect(list.first?.subject == "Flock mail check ✓")

    let detail = try decode(MailDetail.self, """
    {"id":"1","inbox":"default","from":"a@b.test","to":["c@d.test"],"cc":null,"subject":"Hi","date":"2026-10-13T13:40:07.1Z",
     "html":"<p>Hi</p>","text":"Hi","headers":[{"name":"Subject","value":"Hi"}],
     "attachments":[{"index":0,"filename":"invoice.pdf","content_type":"application/pdf","size":2048}]}
    """)
    #expect(detail.cc.isEmpty)
    #expect(detail.attachments.first?.contentType == "application/pdf")
}

@Test func decodesDumpsWithOptionalContext() throws {
    let dumps = try decode([Dump].self, """
    [{"id":"2","time":"2026-10-13T14:00:00.999999999+02:00","site":"watchtower.test",
      "source":{"file":"/code/watchtower/app/Http/Controllers/HomeController.php","line":42,"name":"HomeController.php"},
      "request":{"method":"GET","uri":"https://watchtower.test/"},"text":"array:1 [...]","html":"<pre class=sf-dump>…</pre>"},
     {"id":"1","time":"2026-10-13T13:59:00Z","site":"","command_line":"artisan tinker","text":"1","html":"<pre>1</pre>"}]
    """)

    #expect(dumps[0].shortSource == "Controllers/HomeController.php:42")
    #expect(dumps[0].request?.method == "GET")
    #expect(dumps[1].source == nil && dumps[1].commandLine == "artisan tinker")
}

@Test func decodesLogs() throws {
    let index = try decode(LogIndex.self, #"{"services":["daemon","web"],"sites":["watchtower"]}"#)
    #expect(index.sites == ["watchtower"])

    let entries = try decode([LogEntry].self, """
    [{"time":"2026-10-13T12:00:00+00:00","env":"local","level":"ERROR","message":"Boom","details":"#0 stack"},
     {"time":"2026-10-13T11:00:00Z","env":"local","level":"info","message":"Hello"}]
    """)
    #expect(entries[0].level == "error")
    #expect(entries[1].details.isEmpty)
}

@Test func decodesTheAPIConfig() throws {
    let config = try JSONDecoder().decode(APIConfig.self, from: Data(#"{"url":"http://127.0.0.1:53531","token":"abc"}"#.utf8))
    #expect(config.url.port == 53531)
}

@Test func decodesWhereASiteVersionComesFrom() throws {
    let status = try decode(Status.self, """
    {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","dns":{"tld":"test","addr":""},
     "http_port":80,"https_port":443,"mail":"","xdebug":false,"dumps":false,"mail_count":0,"dump_count":0,"processes":[],
     "sites":[{"name":"shop","domain":"shop.test","path":"/code/shop","secured":false,"parked":false,
               "php":"8.3","php_source":"isolated","php_pinned":"8.4","php_file":"/code/shop/.flockrc",
               "node":"24","node_source":"project","node_pinned":"24","node_file":"/code/shop/.flockrc","node_shadowed":".nvmrc: 20"},
              {"name":"blog","domain":"blog.test","path":"/code/blog","php":"8.5","secured":false,"parked":true}]}
    """)

    let shop = status.sites[0]
    #expect(shop.phpSource == .isolated && shop.phpPinned == "8.4" && shop.phpPinnedHere)
    #expect(shop.nodeSource == .project && shop.nodeShadowed == ".nvmrc: 20" && shop.nodePinnedHere)
    #expect(status.sites[1].phpSource == .default && !status.sites[1].phpPinnedHere)
}

@Test func decodesASiteOnFrankenPHP() throws {
    let status = try decode(Status.self, """
    {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","dns":{"tld":"test","addr":""},
     "http_port":80,"https_port":443,"mail":"","xdebug":false,"dumps":false,"mail_count":0,"dump_count":0,"processes":[],
     "sites":[{"name":"shop","domain":"shop.test","path":"/code/shop","php":"8.4","secured":true,"parked":false,
               "server":"frankenphp","worker":"octane","octane_available":true,
               "frankenphp":{"state":"running","version":"1.13.0","php_version":"8.4.14","port":21000,"progress":0,"log":"frankenphp-shop"}},
              {"name":"blog","domain":"blog.test","path":"/code/blog","php":"8.3","secured":false,"parked":true,
               "server":"frankenphp","worker":"","octane_available":false,
               "frankenphp":{"state":"installing","port":21001,"progress":-1,"problem":"","log":"frankenphp-blog"}},
              {"name":"legacy","domain":"legacy.test","path":"/code/legacy","php":"8.1","secured":false,"parked":true}]}
    """)

    let shop = status.sites[0]
    #expect(shop.server == .frankenphp && shop.worker == .octane && shop.octaneAvailable)
    let franken = try #require(shop.frankenPHP)
    #expect(franken.state == .running && franken.version == "1.13.0" && franken.phpVersion == "8.4.14")
    #expect(franken.port == 21000 && franken.log == "frankenphp-shop" && franken.problem.isEmpty)

    let blog = status.sites[1]
    #expect(blog.worker == .classic && !blog.octaneAvailable)
    #expect(blog.frankenPHP?.state == .installing && blog.frankenPHP?.progress == nil && blog.frankenPHP?.version == "")

    // Older daemons send none of it: the site runs on PHP-FPM.
    let legacy = status.sites[2]
    #expect(legacy.server == .fastcgi && legacy.worker == .classic && legacy.frankenPHP == nil)
    #expect(!legacy.supportsFrankenPHP && shop.supportsFrankenPHP)
}

@Test func decodesASitesReverb() throws {
    let status = try decode(Status.self, """
    {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z",
     "sites":[{"name":"chat","domain":"chat.test","path":"/code/chat","php":"8.4","secured":true,"reverb_available":true,
               "reverb":{"state":"running","port":23000,"env":["REVERB_HOST=chat.test","REVERB_PORT=443","REVERB_SCHEME=https"],"log":"reverb-chat"}},
              {"name":"feed","domain":"feed.test","path":"/code/feed","php":"8.4","reverb_available":true,
               "reverb":{"state":"failed","port":23001,"problem":"reverb:start exited: missing REVERB_APP_KEY","log":"reverb-feed"}},
              {"name":"shop","domain":"shop.test","path":"/code/shop","php":"8.4","reverb_available":true},
              {"name":"legacy","domain":"legacy.test","path":"/code/legacy","php":"8.1"}]}
    """)

    let chat = status.sites[0]
    #expect(chat.reverbAvailable)
    let reverb = try #require(chat.reverb)
    #expect(reverb.state == .running && reverb.port == 23000 && reverb.log == "reverb-chat" && reverb.problem.isEmpty)
    #expect(reverb.env == ["REVERB_HOST=chat.test", "REVERB_PORT=443", "REVERB_SCHEME=https"])

    let feed = try #require(status.sites[1].reverb)
    #expect(feed.state == .failed && feed.problem.contains("REVERB_APP_KEY") && feed.env.isEmpty)

    // Installed but turned off, and a daemon from before Reverb.
    #expect(status.sites[2].reverbAvailable && status.sites[2].reverb == nil)
    #expect(!status.sites[3].reverbAvailable && status.sites[3].reverb == nil)

    let odd = try decode(Status.SiteReverb.self, #"{"state":"something-new"}"#)
    #expect(odd.state == .unknown && odd.port == 0 && odd.log.isEmpty)
}

@Test func decodesANodeSiteAndItsDevServer() throws {
    let status = try decode(Status.self, """
    {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z",
     "sites":[{"name":"web","domain":"web.test","path":"/code/web","node":"22","type":"node",
               "node_server":{"state":"running","port":22417,"command":"npm run dev -- --port 22417","log":"node-web"}},
              {"name":"next","domain":"next.test","path":"/code/next","type":"node",
               "node_server":{"state":"failed","port":22001,"command":"npm run dev -- --port 22001","problem":"node_modules is missing: run npm install in the project","log":"node-next"}},
              {"name":"shop","domain":"shop.test","path":"/code/shop","php":"8.4","type":"php"},
              {"name":"legacy","domain":"legacy.test","path":"/code/legacy","php":"8.1"}]}
    """)

    let web = status.sites[0]
    #expect(web.type == .node && web.isNode)
    let server = try #require(web.nodeServer)
    #expect(server.state == .running && server.port == 22417 && server.log == "node-web" && server.problem == nil)
    #expect(server.command == "npm run dev -- --port 22417")

    #expect(status.sites[1].nodeServer?.state == .failed && status.sites[1].nodeServer?.problem?.contains("npm install") == true)

    // A PHP site, and one from a daemon before types: PHP, without a dev server.
    #expect(status.sites[2].type == .php && status.sites[2].nodeServer == nil)
    #expect(status.sites[3].type == .php && !status.sites[3].isNode)
}

@Test func decodesAProxysCommand() throws {
    let status = try decode(Status.self, """
    {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z",
     "proxies":[{"name":"admin","domain":"admin.test","upstream":"http://127.0.0.1:8000","secured":false,"running":true,
                 "command":{"command":"python manage.py runserver 8000","dir":"/code/admin","state":"running","log":"proxy-admin"}},
                {"name":"s3","domain":"s3.test","upstream":"http://127.0.0.1:9000","secured":false,"running":false,
                 "command":{"command":"docker compose up -d","dir":"/code/s3","stop_command":"docker compose stop","state":"failed",
                            "problem":"`docker compose up -d` failed: exit status 1 — see its output","log":"proxy-s3"}},
                {"name":"plain","domain":"plain.test","upstream":"http://127.0.0.1:3000","secured":false,"running":false}]}
    """)

    let admin = try #require(status.proxies[0].command)
    #expect(admin.command == "python manage.py runserver 8000" && admin.dir == "/code/admin")
    #expect(admin.state == .running && admin.stopCommand == nil && admin.log == "proxy-admin")

    let s3 = try #require(status.proxies[1].command)
    #expect(s3.stopCommand == "docker compose stop" && s3.state == .failed && s3.problem?.contains("failed") == true)

    #expect(status.proxies[2].command == nil)
}

@Test func tellsWhichPHPVersionsFrankenPHPSupports() {
    #expect(Status.Site.frankenPHPSupports("8.2") && Status.Site.frankenPHPSupports("8.4.14") && Status.Site.frankenPHPSupports("9.0"))
    #expect(!Status.Site.frankenPHPSupports("8.1") && !Status.Site.frankenPHPSupports("7.4"))
    // The daemon has the last word on a version the app can't read.
    #expect(Status.Site.frankenPHPSupports(""))
}

@Test func decodesTheFrankenPHPBuilds() throws {
    let state = try decode(FrankenPHPState.self, """
    {"available":true,"checked_at":"2026-10-13T14:10:01Z","check_error":"",
     "builds":[{"php":"8.5","installed":"","php_version":"","latest":"8.5.11","update":false,"size":41943040,"prerelease":true,
                "job":{"action":"install","state":"running","phase":"downloading","progress":0.42,"error":"","started_at":"2026-10-13T14:11:00Z"}},
               {"php":"8.4","installed":"1.13.0","php_version":"8.4.14","latest":"8.4.26","update":true,"size":40000000,"prerelease":false,"yanked":"crashes on start","job":null},
               {"php":"8.3","installed":"","php_version":"","latest":"1.13.1","update":false,"size":39000000,
                "job":{"action":"install","state":"failed","phase":"downloading","progress":-1,"error":"connection reset","started_at":"2026-10-13T14:09:00Z"}}]}
    """)

    #expect(state.available && state.checkedAt != nil && state.checkError.isEmpty)
    #expect(state.builds.map(\.id) == ["8.5", "8.4", "8.3"])
    #expect(state.hasRunningJob)
    #expect(!state.builds[0].isInstalled && state.builds[0].job?.progress == 0.42 && state.builds[0].job?.phase == .downloading)
    #expect(state.builds[1].isInstalled && state.builds[1].update && state.builds[1].phpVersion == "8.4.14" && state.builds[1].job == nil)
    #expect(state.builds[0].prerelease && state.builds[0].yanked.isEmpty && !state.builds[1].prerelease && state.builds[1].yanked == "crashes on start")
    #expect(!state.builds[2].prerelease && state.builds[2].yanked.isEmpty)
    #expect(state.builds[2].job?.isFailed == true && state.builds[2].job?.error == "connection reset" && state.builds[2].job?.progress == nil)

    let unchecked = try decode(FrankenPHPState.self, #"{"available":false,"checked_at":null,"check_error":"","builds":null}"#)
    #expect(!unchecked.available && unchecked.checkedAt == nil && unchecked.builds.isEmpty && !unchecked.hasRunningJob)
}

@Test func decodesTheAIClients() throws {
    let state = try decode(MCPState.self, """
    {"clients":[{"id":"claude-code","name":"Claude Code","state":"registered","config":"/Users/ann/.claude.json"},
                {"id":"vscode","name":"VS Code","state":"available","config":"/x/mcp.json","problem":"Flock can't read /x/mcp.json — add Flock to it by hand"},
                {"id":"codex","name":"Codex","state":"missing"},
                {"id":"new","name":"New","state":"someday"}],
     "command":"/Users/ann/Library/Application Support/Flock/bin/flock","args":["mcp"],"snippet":"{ \\"mcpServers\\": {} }"}
    """)

    #expect(state.clients.map(\.id) == ["claude-code", "vscode", "codex", "new"])
    #expect(state.clients.map(\.state) == [.registered, .available, .missing, .missing])
    #expect(state.clients[1].problem.contains("by hand") && state.clients[2].config.isEmpty)
    #expect(state.args == ["mcp"] && state.snippet.contains("mcpServers"))
}

@Test func decodesTheBoostStateOfSites() throws {
    let status = try decode(Status.self, """
    {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","dns":{"tld":"test"},"http_port":80,"https_port":443,
     "mail":"","xdebug":false,"dumps":false,"mail_count":0,"dump_count":0,"processes":[],
     "sites":[{"name":"a","boost":"missing"},{"name":"b","boost":"outdated"},{"name":"c"},{"name":"d","boost":"current"}]}
    """)
    #expect(status.sites.map(\.boost) == [.missing, .outdated, .none, .current])
}

@Test func decodesCaptureSettingsAndDefaultsThemForAnOlderDaemon() throws {
    let status = try decode(Status.self, """
    {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","dumps":true,"processes":[],
     "capture_off":["view","job"],"dumps_in_page":true,"event_count":42,"capture_unsupported":["8.1"]}
    """)
    #expect(status.captureOff == ["view", "job"] && status.dumpsInPage && status.eventCount == 42)
    #expect(status.captureUnsupported == ["8.1"])

    let older = try decode(Status.self, """
    {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","dumps":true,"processes":[],"capture_off":null}
    """)
    #expect(older.captureOff.isEmpty && !older.dumpsInPage && older.eventCount == 0 && older.captureUnsupported.isEmpty)

    let config = try decode(FlockConfig.self, """
    {"tld":"test","dumps":true,"capture_off":["log"],"dumps_in_page":true,"ports":{"http":80,"dumps":9912,"agent":9913}}
    """)
    #expect(config.captureOff == ["log"] && config.dumpsInPage && config.ports.agent == 9913)
}

@Test func encodesOnlyTheChangedCaptureSettings() throws {
    let encoder = JSONEncoder()
    encoder.outputFormatting = .sortedKeys
    #expect(String(decoding: try encoder.encode(SettingsChange(captureOff: ["query"])), as: UTF8.self) == #"{"capture_off":["query"]}"#)
    #expect(String(decoding: try encoder.encode(SettingsChange(captureOff: [])), as: UTF8.self) == #"{"capture_off":[]}"#)
    #expect(String(decoding: try encoder.encode(SettingsChange(dumpsInPage: false)), as: UTF8.self) == #"{"dumps_in_page":false}"#)
}

@Test func decodesASitesShare() throws {
    let site = try decode(Status.Site.self, """
    {"name":"watchtower","domain":"watchtower.test","path":"/code/watchtower","php":"8.5",
     "share":{"site":"watchtower","state":"running","url":"https://quiet-river-lamp.trycloudflare.com",
              "allow":["bob@acme.com","*@client.com"],"started_at":"2026-10-19T10:00:00.5Z","expires_at":"2026-10-19T18:00:00.5Z",
              "built":true,"can_rebuild":true,"rebuilding":false,"log":"share-watchtower","qr":"iVBORw0KGgo="}}
    """)
    let share = try #require(site.share)
    #expect(site.shared && share.state == .running)
    #expect(share.url == "https://quiet-river-lamp.trycloudflare.com" && share.allow == ["bob@acme.com", "*@client.com"])
    #expect(share.canRebuild && !share.rebuilding && share.rebuildError == nil && share.built)
    #expect(share.expiresAt == FlockJSON.parseDate("2026-10-19T18:00:00.5Z"))
    #expect(share.qrPNG?.count == 8)

    let failed = try decode(Status.Site.self, #"{"name":"a","share":{"site":"a","state":"failed","error":"no tunnel","allow":null}}"#)
    #expect(failed.share?.state == .failed && failed.share?.error == "no tunnel" && failed.share?.allow == [] && !failed.shared)
    #expect(try decode(Status.Site.self, #"{"name":"a"}"#).share == nil)

    #expect(ShareStatus.parseAllow(" bob@acme.com,  *@client.com\nann@x.io ") == ["bob@acme.com", "*@client.com", "ann@x.io"])
}

@Test func decodesWorktreesUnderTheirSite() throws {
    let status = try decode(Status.self, """
    {"version":"dev","pid":1,"started_at":"2026-10-27T10:00:00+01:00",
     "sites":[{"name":"acme","domain":"acme.test","path":"/code/acme","php":"8.5"},
              {"name":"feature-auth.acme","domain":"feature-auth.acme.test","path":"/code/acme-feature-auth","php":"8.5",
               "parent":"acme","branch":"feature/auth",
               "worktree":{"state":"running","step":"composer install","log":"worktree-feature-auth.acme","db_mode":"copy","database":"acme_feature_auth"}},
              {"name":"bisect.acme","domain":"bisect.acme.test","path":"/code/bisect","php":"8.5","parent":"acme","branch":""},
              {"name":"blog","domain":"blog.test","path":"/code/blog","php":"8.4","parent":""}]}
    """)

    #expect(status.sites.topLevel.map(\.name) == ["acme", "blog"])
    let worktrees = status.sites.worktrees(of: "acme")
    #expect(worktrees.map(\.worktreeLabel) == ["feature/auth", "bisect"])
    let setup = try #require(worktrees[0].worktree)
    #expect(setup.busy && setup.step == "composer install" && setup.dbMode == .copy && setup.database == "acme_feature_auth")
    #expect(worktrees[1].worktree == nil && !status.sites[3].isWorktree)

}

@Test func ordersBranchesCurrentAndDefaultFirst() throws {
    let branches = try decode(Branches.self, """
    {"current":"develop","default":"main","local":["alpha","develop","main"],"remote":["origin/main"]}
    """)
    #expect(branches.ordered == ["develop", "main", "alpha"])
    #expect(Branches(current: "", default: "gone", local: ["a", "b"]).ordered == ["a", "b"])
}
