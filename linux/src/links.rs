//! flock:// links, which the app is registered for (x-scheme-handler/flock), like the macOS and
//! Windows apps:
//!
//! - flock://install/<service>[/<line>], from the website, the docs or the page served for a site
//!   whose PHP is missing: flock://install/mysql/8.4, flock://install/php/8.5, flock://install/node/24.
//! - flock://secure/<site>, from the page served for a site that isn't secured yet.
//!
//! flock://update is the macOS and Windows apps' alone: on Linux, `flock update` runs the package
//! manager.
//!
//! Any web page can open them, so they change nothing by themselves: the pane they open says what
//! the link asks for and waits for the user to confirm.

use crate::api::{Catalog, NodeVersions, PhpVersions, PostgresVersions, Service, SiteStatus};
use crate::i18n::{self, tr, tr_noop};

pub const SCHEME: &str = "flock";
/// The service of a link that asks for a PHP minor, which the PHP pane installs.
pub const PHP: &str = "php";
/// The service of a link that asks for a Node.js major, which the Node pane installs.
pub const NODE: &str = "node";

#[derive(Clone, Debug, PartialEq)]
pub enum Link {
    Install(InstallLink),
    /// The site's name.
    Secure(String),
}

/// A service and its version line ("8.4", a PostgreSQL or Node.js major); None for the newest.
#[derive(Clone, Debug, PartialEq)]
pub struct InstallLink {
    pub service: String,
    pub line: Option<String>,
}

/// The link in a command line or message, or None when it isn't one the app handles.
pub fn parse(text: &str) -> Option<Link> {
    if text.len() > 200 {
        return None;
    }
    let (scheme, rest) = text.split_once("://")?;
    if !scheme.eq_ignore_ascii_case(SCHEME) {
        return None;
    }
    let (host, path) = rest.split_once('/').unwrap_or((rest, ""));
    let path = decode(path)?;
    let parts: Vec<&str> = path.split('/').filter(|p| !p.is_empty()).collect();
    match host.to_ascii_lowercase().as_str() {
        "install" => match parts[..] {
            [service] if safe(service, 32) => Some(install(service, None)),
            [service, line] if safe(service, 32) && safe(line, 16) => Some(install(service, Some(line))),
            _ => None,
        },
        "secure" => match parts[..] {
            [site] if safe(site, 63) => Some(Link::Secure(site.to_ascii_lowercase())),
            _ => None,
        },
        _ => None,
    }
}

fn install(service: &str, line: Option<&str>) -> Link {
    Link::Install(InstallLink { service: service.to_ascii_lowercase(), line: line.map(str::to_owned) })
}

/// Letters, digits, dots and dashes only, so nothing odd ends up in a dialog or a request.
fn safe(part: &str, max: usize) -> bool {
    !part.is_empty() && part.len() <= max && part.chars().all(|c| c.is_ascii_alphanumeric() || c == '.' || c == '-')
}

/// Undoes %-escapes; None for a broken one.
fn decode(text: &str) -> Option<String> {
    let mut bytes = Vec::with_capacity(text.len());
    let mut iter = text.bytes();
    while let Some(b) = iter.next() {
        if b == b'%' {
            let hex = [iter.next()?, iter.next()?];
            bytes.push(u8::from_str_radix(std::str::from_utf8(&hex).ok()?, 16).ok()?);
        } else {
            bytes.push(b);
        }
    }
    String::from_utf8(bytes).ok()
}

/// What an install link would install once the user confirms.
#[derive(Clone, Debug, PartialEq)]
pub struct Target {
    pub service: String,
    pub name: String,
    /// The line to pass to the daemon ("8.4", "18").
    pub line: String,
    /// How the line reads ("8.4 LTS").
    pub label: String,
    /// The release that gets installed; empty when not known yet.
    pub version: String,
}

/// What the app tells the user about a link: a question to confirm, or why there is nothing to do.
#[derive(Clone, Debug, PartialEq)]
pub struct Resolution<T> {
    pub title: String,
    pub message: String,
    /// What confirming does: install a target, secure a site. None when there is nothing to do.
    pub action: Option<T>,
}

impl<T> Resolution<T> {
    fn inform(title: String, message: impl Into<String>) -> Self {
        Self { title, message: message.into(), action: None }
    }
}

const NEWER_FLOCK: &str = tr_noop!("links.install.newer_flock");
// A PHP or Node.js version the manifest doesn't publish for this platform, or doesn't know at all.
const NO_BUILD: &str = tr_noop!("links.install.no_build");

fn install_question(target: Target, purpose: &str) -> Resolution<Target> {
    let version = if target.version.is_empty() { &target.label } else { &target.version };
    Resolution {
        title: tr!("links.install.install_title", name = target.name, version = target.label),
        message: tr!("links.install.install_message", name = target.name, version = version, purpose = purpose),
        action: Some(target),
    }
}

/// The pane that lists it.
fn already_installed(name: &str, pane: &str) -> Resolution<Target> {
    Resolution::inform(
        tr!("links.install.already_installed_title", name = name),
        tr!("links.install.already_installed_message", pane = pane),
    )
}

fn installing(name: &str, pane: &str) -> Resolution<Target> {
    Resolution::inform(tr!("links.install.installing_title", name = name), tr!("links.install.installing_message", pane = pane))
}

fn unknown_version(name: &str, line: &str, message: &'static str) -> Resolution<Target> {
    let title = if line.is_empty() {
        tr!("links.install.unknown_title", name = name)
    } else {
        tr!("links.install.flock_doesnt_offer_name_version", name = name, version = line)
    };
    Resolution::inform(title, i18n::translate(message))
}

/// Versions compare by their numbers: "8.10" is newer than "8.9".
fn numeric(version: &str) -> Vec<u64> {
    version.split('.').map(|p| p.parse().unwrap_or(0)).collect()
}

impl InstallLink {
    /// The window's pane that asks about it: PHP and Node for their versions, Services (at the
    /// service) for everything else.
    pub fn pane(&self) -> String {
        match self.service.as_str() {
            PHP | NODE => self.service.clone(),
            service => format!("services/{service}"),
        }
    }

    /// What the link asks for, measured against what this Flock offers and has installed. None
    /// while the services and versions are still loading.
    pub fn resolve(
        &self,
        services: &[Service],
        catalog: Option<&Catalog>,
        postgres: Option<&PostgresVersions>,
    ) -> Option<Resolution<Target>> {
        if services.is_empty() {
            return None;
        }
        let line = self.line.as_deref();
        let Some(service) = services.iter().find(|s| s.id == self.service) else {
            return Some(Resolution::inform(tr!("links.install.unknown_title", name = self.service), i18n::translate(NEWER_FLOCK)));
        };
        if !service.available {
            let reason = if service.unavailable.is_empty() { tr!("links.install.unavailable_message") } else { &service.unavailable };
            return Some(Resolution::inform(tr!("links.install.unavailable_title", name = service.name), reason));
        }
        if service.id == "postgres" {
            let majors = postgres?.majors.as_deref().unwrap_or_default();
            let major = match line {
                None => majors.first(),
                Some(line) => majors.iter().find(|m| m.major == line),
            };
            let Some(major) = major else {
                return Some(unknown_version(&service.name, line.unwrap_or_default(), NEWER_FLOCK));
            };
            if !major.installed.is_empty() {
                return Some(already_installed(&format!("PostgreSQL {}", major.major), tr!("settings.pane.services")));
            }
            let target = Target {
                service: service.id.clone(),
                name: service.name.clone(),
                line: major.major.clone(),
                label: major.major.clone(),
                version: major.latest.clone(),
            };
            return Some(install_question(target, tr!("links.install.purpose_service")));
        }
        let entries = catalog?.services.as_deref().unwrap_or_default();
        let Some(entry) = entries.iter().find(|e| e.id == service.id) else {
            return Some(Resolution::inform(tr!("links.install.unknown_title", name = service.name), i18n::translate(NEWER_FLOCK)));
        };
        let lines = entry.lines.as_deref().unwrap_or_default();
        let found = match line {
            None => lines.first(),
            Some(line) => lines.iter().find(|l| l.id == line),
        };
        let Some(found) = found else {
            return Some(unknown_version(&entry.name, line.unwrap_or_default(), NEWER_FLOCK));
        };
        if found.installed {
            return Some(already_installed(&format!("{} {}", entry.name, found.label), tr!("settings.pane.services")));
        }
        let target = Target {
            service: entry.id.clone(),
            name: entry.name.clone(),
            line: found.id.clone(),
            label: found.label.clone(),
            version: found.latest.clone(),
        };
        Some(install_question(target, tr!("links.install.purpose_service")))
    }

    /// What a php link asks for, measured against the PHP versions. Without a version, the newest
    /// one that isn't a pre-release. None while the versions are still loading.
    pub fn resolve_php(&self, versions: Option<&PhpVersions>) -> Option<Resolution<Target>> {
        let releases = versions?.versions.as_deref().unwrap_or_default();
        let line = self.line.as_deref();
        let release = match line {
            None => releases.iter().filter(|r| !r.prerelease && !r.latest.is_empty()).max_by_key(|r| numeric(&r.minor)),
            Some(line) => releases.iter().find(|r| r.minor == line),
        };
        let Some(release) = release.filter(|r| !r.installed.is_empty() || !r.latest.is_empty()) else {
            return Some(unknown_version("PHP", line.unwrap_or_default(), NO_BUILD));
        };
        let name = format!("PHP {}", release.minor);
        if !release.installed.is_empty() {
            return Some(already_installed(&name, "PHP"));
        }
        if release.job.as_ref().is_some_and(|j| j.running()) {
            return Some(installing(&name, "PHP"));
        }
        let target = Target {
            service: PHP.to_owned(),
            name: "PHP".to_owned(),
            line: release.minor.clone(),
            label: release.minor.clone(),
            version: release.latest.clone(),
        };
        Some(install_question(target, tr!("links.install.purpose_php")))
    }

    /// What a node link asks for, measured against the Node.js lines. Without a version, the
    /// newest line. None while the lines are still loading. installing: majors being installed
    /// already (the first run's default, one started in the Node pane).
    pub fn resolve_node(&self, versions: Option<&NodeVersions>, installing_majors: &[String]) -> Option<Resolution<Target>> {
        let releases = versions?.versions.as_deref().unwrap_or_default();
        let line = self.line.as_deref();
        let release = match line {
            None => releases.iter().filter(|r| !r.latest.is_empty()).max_by_key(|r| numeric(&r.major)),
            Some(line) => releases.iter().find(|r| r.major == line),
        };
        let Some(release) = release.filter(|r| !r.installed.is_empty() || !r.latest.is_empty()) else {
            return Some(unknown_version("Node.js", line.unwrap_or_default(), NO_BUILD));
        };
        let name = format!("Node.js {}", release.major);
        if !release.installed.is_empty() {
            return Some(already_installed(&name, "Node"));
        }
        if installing_majors.contains(&release.major) {
            return Some(installing(&name, "Node"));
        }
        let target = Target {
            service: NODE.to_owned(),
            name: "Node.js".to_owned(),
            line: release.major.clone(),
            label: release.major.clone(),
            version: release.latest.clone(),
        };
        Some(install_question(target, tr!("links.install.purpose_node")))
    }
}

/// What a secure link asks for, measured against the linked sites: the site to secure.
pub fn resolve_secure(site: &str, sites: &[SiteStatus]) -> Resolution<String> {
    match sites.iter().find(|s| s.name.eq_ignore_ascii_case(site)) {
        None => Resolution::inform(tr!("links.secure.unknown_title", site = site), tr!("links.secure.unknown_message")),
        Some(s) if s.secured => Resolution::inform(
            tr!("links.secure.already_secured_title", site = s.name),
            tr!("links.secure.already_secured_message", domain = s.domain),
        ),
        Some(s) => Resolution {
            title: tr!("links.secure.secure_title", site = s.name),
            message: tr!("links.secure.secure_message", domain = s.domain),
            action: Some(s.name.clone()),
        },
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn link(service: &str, line: Option<&str>) -> InstallLink {
        InstallLink { service: service.to_owned(), line: line.map(str::to_owned) }
    }

    #[test]
    fn parses_links() {
        assert_eq!(parse("flock://install/mysql/8.4"), Some(Link::Install(link("mysql", Some("8.4")))));
        assert_eq!(parse("flock://install/postgres/18"), Some(Link::Install(link("postgres", Some("18")))));
        assert_eq!(parse("flock://install/meilisearch"), Some(Link::Install(link("meilisearch", None))));
        assert_eq!(parse("FLOCK://INSTALL/MySQL/8.4/"), Some(Link::Install(link("mysql", Some("8.4")))));
        assert_eq!(parse("flock://install/php/8.5"), Some(Link::Install(link(PHP, Some("8.5")))));
        assert_eq!(parse("flock://install/node/24"), Some(Link::Install(link(NODE, Some("24")))));
        assert_eq!(parse("flock://secure/watchtower"), Some(Link::Secure("watchtower".to_owned())));
        assert_eq!(parse("FLOCK://SECURE/My-App/"), Some(Link::Secure("my-app".to_owned())));
    }

    #[test]
    fn rejects_anything_else() {
        for text in [
            "",
            "--background",
            "https://install/mysql/8.4",
            "flock://uninstall/mysql",
            "flock://install",
            "flock://install/mysql/8.4/extra",
            "flock://install/my%20sql",
            "flock://install/mysql/8.4%0A",
            "flock://install/mysql/8.4%",
            "flock://install/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            "https://secure/watchtower",
            "flock://unsecure/watchtower",
            "flock://secure",
            "flock://secure/watchtower/extra",
            "flock://secure/watch%20tower",
            "flock://secure/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            "flock://update",
        ] {
            assert_eq!(parse(text), None, "{text}");
        }
    }

    fn json<T: serde::de::DeserializeOwned>(text: &str) -> T {
        serde_json::from_str(text).expect("fixture")
    }

    #[test]
    fn resolves_against_what_flock_offers() {
        let services: Vec<Service> = json(
            r#"[{"id":"postgres","name":"PostgreSQL","available":true},
                {"id":"mysql","name":"MySQL","available":true},
                {"id":"redis","name":"Redis","available":true},
                {"id":"mariadb","name":"MariaDB","available":true},
                {"id":"typesense","name":"Typesense","available":false,"unavailable":"Typesense has no build for this computer."}]"#,
        );
        let catalog: Catalog = json(
            r#"{"services":[
                {"id":"mysql","name":"MySQL","installed":"","lines":[
                  {"id":"9.7","label":"9.7 LTS","latest":"9.7.1"},{"id":"8.4","label":"8.4 LTS","latest":"8.4.7"}]},
                {"id":"redis","name":"Redis","installed":"8.4.1","line":"8.4","lines":[
                  {"id":"8.10","label":"8.10"},{"id":"8.4","label":"8.4","installed":true}]}]}"#,
        );
        let postgres: PostgresVersions = json(r#"{"majors":[{"major":"18","latest":"18.7.0"},{"major":"17","installed":"17.4.0"}]}"#);
        let resolve = |service: &str, line: Option<&str>| link(service, line).resolve(&services, Some(&catalog), Some(&postgres)).unwrap();
        let target = |service: &str, name: &str, line: &str, label: &str, version: &str| Target {
            service: service.to_owned(),
            name: name.to_owned(),
            line: line.to_owned(),
            label: label.to_owned(),
            version: version.to_owned(),
        };

        assert_eq!(resolve("mysql", Some("8.4")).action, Some(target("mysql", "MySQL", "8.4", "8.4 LTS", "8.4.7")));
        assert_eq!(resolve("mysql", Some("8.4")).title, "Install MySQL 8.4 LTS?");
        assert_eq!(resolve("mysql", None).action.unwrap().line, "9.7");
        assert_eq!(resolve("postgres", None).action, Some(target("postgres", "PostgreSQL", "18", "18", "18.7.0")));
        assert_eq!(resolve("postgres", Some("17")), already_installed("PostgreSQL 17", "Services"));
        assert_eq!(resolve("redis", Some("8.4")), already_installed("Redis 8.4", "Services"));
        // Another line installs next to the installed one.
        assert_eq!(resolve("redis", Some("8.10")).action, Some(target("redis", "Redis", "8.10", "8.10", "")));
        assert_eq!(resolve("typesense", None).title, "Typesense isn't available on this computer");
        assert_eq!(resolve("mysql", Some("5.7")).title, "Flock doesn't offer MySQL 5.7");
        assert_eq!(resolve("postgres", Some("9")).title, "Flock doesn't offer PostgreSQL 9");
        assert_eq!(resolve("oracle", None).title, "Flock doesn't offer oracle");
        // In the services list, but this daemon's catalog doesn't know it.
        assert_eq!(resolve("mariadb", None).title, "Flock doesn't offer MariaDB");

        // Waits for the versions.
        assert_eq!(link("mysql", None).resolve(&[], Some(&catalog), Some(&postgres)), None);
        assert_eq!(link("mysql", None).resolve(&services, None, Some(&postgres)), None);
        assert_eq!(link("postgres", None).resolve(&services, Some(&catalog), None), None);
    }

    #[test]
    fn resolves_php_links_against_the_php_versions() {
        let versions: PhpVersions = json(
            r#"{"versions":[
                {"minor":"8.6","latest":"8.6.0RC2","status":"available","prerelease":true},
                {"minor":"8.10","latest":"8.10.1","status":"available"},
                {"minor":"8.5","latest":"8.5.12","status":"available"},
                {"minor":"8.4","latest":"8.4.26","installed":"8.4.26","status":"installed","default":true},
                {"minor":"8.3","latest":"8.3.20","status":"available","job":{"state":"running","phase":"downloading"}},
                {"minor":"7.3","installed":"7.3.33","status":"installed"}]}"#,
        );
        let resolve = |line: Option<&str>| link(PHP, line).resolve_php(Some(&versions)).unwrap();

        let question = resolve(Some("8.5"));
        assert_eq!(question.title, "Install PHP 8.5?");
        assert_eq!(question.message, "A link asked Flock to install PHP 8.5.12. Sites that use it are served as soon as it's installed.");
        // The newest that isn't a pre-release, by number; a pre-release asked for by name is offered.
        assert_eq!(resolve(None).action.unwrap().line, "8.10");
        assert_eq!(resolve(Some("8.6")).action.unwrap().line, "8.6");
        assert_eq!(resolve(Some("8.4")), already_installed("PHP 8.4", "PHP"));
        assert_eq!(resolve(Some("7.3")), already_installed("PHP 7.3", "PHP"));
        assert_eq!(resolve(Some("8.3")), installing("PHP 8.3", "PHP"));
        assert_eq!(resolve(Some("7.2")).title, "Flock doesn't offer PHP 7.2");
        assert_eq!(resolve(Some("7.2")).action, None);
        assert_eq!(link(PHP, Some("8.5")).resolve_php(None), None);
        assert_eq!(link(PHP, None).resolve_php(Some(&PhpVersions::default())).unwrap().title, "Flock doesn't offer PHP");
    }

    #[test]
    fn resolves_node_links_against_the_node_lines() {
        let versions: NodeVersions = json(
            r#"{"versions":[
                {"major":"25","latest":"25.1.0"},
                {"major":"24","latest":"24.11.1"},
                {"major":"22","latest":"22.21.0","installed":"22.21.0","default":true},
                {"major":"9","latest":"9.11.2"}]}"#,
        );
        let resolve = |line: Option<&str>, busy: &[String]| link(NODE, line).resolve_node(Some(&versions), busy).unwrap();

        assert_eq!(resolve(Some("24"), &[]).title, "Install Node.js 24?");
        assert_eq!(resolve(None, &[]).action.unwrap().line, "25");
        assert_eq!(resolve(Some("22"), &[]), already_installed("Node.js 22", "Node"));
        assert_eq!(resolve(Some("24"), &["24".to_owned()]), installing("Node.js 24", "Node"));
        assert_eq!(resolve(Some("12"), &[]).title, "Flock doesn't offer Node.js 12");
        assert_eq!(link(NODE, Some("24")).resolve_node(None, &[]), None);
    }

    #[test]
    fn resolves_secure_links_against_the_linked_sites() {
        let sites: Vec<SiteStatus> = json(
            r#"[{"name":"watchtower","domain":"watchtower.test","path":"/code/watchtower"},
                {"name":"safe","domain":"safe.test","path":"/code/safe","secured":true}]"#,
        );
        let secure = resolve_secure("watchtower", &sites);
        assert_eq!(secure.action.as_deref(), Some("watchtower"));
        assert_eq!(secure.title, "Serve watchtower over HTTPS?");
        assert!(secure.message.contains("watchtower.test"));
        assert_eq!(resolve_secure("safe", &sites).title, "safe is already secured");
        assert_eq!(resolve_secure("safe", &sites).action, None);
        assert_eq!(resolve_secure("nope", &sites).title, "Flock has no site called nope");
    }
}
