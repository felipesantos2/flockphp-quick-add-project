import Foundation
import Testing
@testable import FlockKit

@Test func parsesInstallLinks() throws {
    let link = { (string: String) in URL(string: string).flatMap(InstallLink.init(url:)) }

    #expect(link("flock://install/mysql/8.4") == InstallLink(service: "mysql", line: "8.4"))
    #expect(link("flock://install/postgres/18") == InstallLink(service: "postgres", line: "18"))
    #expect(link("flock://install/meilisearch") == InstallLink(service: "meilisearch"))
    #expect(link("FLOCK://INSTALL/MySQL/8.4/") == InstallLink(service: "mysql", line: "8.4"))

    #expect(link("https://install/mysql/8.4") == nil)
    #expect(link("flock://uninstall/mysql") == nil)
    #expect(link("flock://install") == nil)
    #expect(link("flock://install/mysql/8.4/extra") == nil)
    #expect(link("flock://install/my%20sql") == nil)
    #expect(link("flock://install/mysql/8.4%0A") == nil)
    #expect(link("flock://install/\(String(repeating: "a", count: 33))") == nil)
}

private func fixtures() throws -> ([Service], ServiceCatalog, PostgresVersions) {
    let decoder = FlockJSON.decoder()
    let services = try decoder.decode([Service].self, from: Data("""
    [{"id":"postgres","name":"PostgreSQL","available":true},
     {"id":"mysql","name":"MySQL","available":true},
     {"id":"redis","name":"Redis","available":true},
     {"id":"mariadb","name":"MariaDB","available":true},
     {"id":"typesense","name":"Typesense","available":false,"unavailable":"Typesense has no Windows build."}]
    """.utf8))
    let catalog = try decoder.decode(ServiceCatalog.self, from: Data("""
    {"services":[
      {"id":"mysql","name":"MySQL","installed":"","lines":[
        {"id":"9.7","label":"9.7 LTS","latest":"9.7.1"},{"id":"8.4","label":"8.4 LTS","latest":"8.4.7"}]},
      {"id":"redis","name":"Redis","installed":"8.4.1","line":"8.4","lines":[
        {"id":"8.10","label":"8.10"},{"id":"8.4","label":"8.4","installed":true}]}
    ]}
    """.utf8))
    let postgres = try decoder.decode(PostgresVersions.self, from: Data("""
    {"majors":[{"major":"18","latest":"18.7.0"},{"major":"17","installed":"17.4.0"}]}
    """.utf8))
    return (services, catalog, postgres)
}

@Test func resolvesInstallLinksAgainstWhatFlockOffers() throws {
    let (services, catalog, postgres) = try fixtures()
    let resolve = { (link: InstallLink) in link.resolve(services: services, catalog: catalog, postgres: postgres) }

    let mysql = try #require(resolve(InstallLink(service: "mysql", line: "8.4"))?.confirmation)
    #expect(mysql == InstallLink.Install(service: "mysql", name: "MySQL", line: "8.4", label: "8.4 LTS", version: "8.4.7"))
    #expect(resolve(InstallLink(service: "mysql"))?.confirmation?.line == "9.7")
    #expect(resolve(InstallLink(service: "postgres"))?.confirmation == InstallLink.Install(service: "postgres", name: "PostgreSQL", line: "18", label: "18", version: "18.7.0"))

    #expect(resolve(InstallLink(service: "postgres", line: "17")) == .installed(name: "PostgreSQL 17"))
    #expect(resolve(InstallLink(service: "redis", line: "8.4")) == .installed(name: "Redis 8.4"))
    // Another line installs next to the installed one.
    #expect(resolve(InstallLink(service: "redis", line: "8.10"))?.confirmation?.line == "8.10")
    #expect(resolve(InstallLink(service: "typesense")) == .unavailable(name: "Typesense", reason: "Typesense has no Windows build."))
    #expect(resolve(InstallLink(service: "mysql", line: "5.7")) == .unknownVersion(name: "MySQL", line: "5.7"))
    #expect(resolve(InstallLink(service: "postgres", line: "9")) == .unknownVersion(name: "PostgreSQL", line: "9"))
    #expect(resolve(InstallLink(service: "oracle")) == .unknown(name: "oracle"))
    // In the services list, but this daemon's catalog doesn't know it.
    #expect(resolve(InstallLink(service: "mariadb")) == .unknown(name: "MariaDB"))
}

@Test func waitsForTheVersionsBeforeResolving() throws {
    let (services, catalog, postgres) = try fixtures()
    #expect(InstallLink(service: "mysql").resolve(services: [], catalog: catalog, postgres: postgres) == nil)
    #expect(InstallLink(service: "mysql").resolve(services: services, catalog: nil, postgres: postgres) == nil)
    #expect(InstallLink(service: "postgres").resolve(services: services, catalog: catalog, postgres: nil) == nil)
    #expect(InstallLink(service: "php").resolve(php: nil) == nil)
    #expect(InstallLink(service: "node").resolve(node: nil) == nil)
}

@Test func resolvesPHPInstallLinks() throws {
    let catalog = try FlockJSON.decoder().decode(PHPCatalog.self, from: Data("""
    {"versions":[
      {"minor":"8.4","latest":"8.4.27","installed":"8.4.27","status":"installed"},
      {"minor":"8.10","latest":"8.10.1","status":"available",
       "job":{"action":"install","state":"running","phase":"downloading"}},
      {"minor":"8.9","latest":"8.9.3","status":"available"},
      {"minor":"8.11","latest":"8.11.0RC1","status":"available","prerelease":true},
      {"minor":"7.4","latest":"","installed":"7.4.33","status":"installed"},
      {"minor":"7.2","latest":"","status":"available"}
    ]}
    """.utf8))
    let resolve = { (line: String?) in InstallLink(service: "php", line: line).resolve(php: catalog) }

    let install = try #require(resolve("8.9")?.confirmation)
    #expect(install == InstallLink.Install(service: "php", name: "PHP", line: "8.9", label: "8.9", version: "8.9.3"))
    #expect(resolve("8.9")?.title == "links.install.install_title PHP 8.9")
    #expect(resolve("8.9")?.message == "links.install.link_asked_flock_to_install_sites PHP 8.9.3")
    #expect(resolve("8.4") == .installed(name: "PHP 8.4", pane: "PHP"))
    #expect(resolve("7.4") == .installed(name: "PHP 7.4", pane: "PHP"))
    #expect(resolve("8.10") == .installing(name: "PHP 8.10"))
    #expect(resolve("8.11")?.confirmation?.version == "8.11.0RC1")
    #expect(resolve("7.2") == .noBuild(name: "PHP", line: "7.2"))
    #expect(resolve("7.3") == .noBuild(name: "PHP", line: "7.3"))
    #expect(resolve("7.3")?.title == "links.install.flock_doesnt_offer_name_version PHP 7.3")
    #expect(resolve("7.3")?.message == "links.install.no_build")
    #expect(resolve("8.4")?.message == "links.install.already_installed_message PHP")
    // Without a version: the newest that isn't a pre-release ("8.10" is newer than "8.9").
    #expect(resolve(nil) == .installing(name: "PHP 8.10"))

    let empty = try FlockJSON.decoder().decode(PHPCatalog.self, from: Data(#"{"versions":[]}"#.utf8))
    #expect(InstallLink(service: "php").resolve(php: empty) == .noBuild(name: "PHP", line: ""))
}

@Test func resolvesNodeInstallLinks() throws {
    let catalog = try FlockJSON.decoder().decode(NodeCatalog.self, from: Data("""
    {"versions":[
      {"major":"22","latest":"22.21.0","installed":"22.20.0","default":true},
      {"major":"24","latest":"24.11.0"},
      {"major":"9","latest":"9.11.2"}
    ]}
    """.utf8))
    let resolve = { (line: String?, installing: Set<String>) in
        InstallLink(service: "node", line: line).resolve(node: catalog, installing: installing)
    }

    #expect(resolve("24", [])?.confirmation == InstallLink.Install(service: "node", name: "Node.js", line: "24", label: "24", version: "24.11.0"))
    #expect(resolve(nil, [])?.confirmation?.line == "24")
    #expect(resolve("22", []) == .installed(name: "Node.js 22", pane: "Node"))
    #expect(resolve("22", [])?.message == "links.install.already_installed_message Node")
    #expect(resolve("24", ["24"]) == .installing(name: "Node.js 24"))
    #expect(resolve("24", ["24"])?.title == "links.install.installing_title Node.js 24")
    #expect(resolve("12", []) == .noBuild(name: "Node.js", line: "12"))
}
