//! The daemon's local HTTP API (internal/api): its address and token come from api.json. Requests
//! block, so the UI runs them with gio::spawn_blocking.

use std::fmt;
use std::time::Duration;

use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};

use crate::paths;

#[derive(Debug, Clone, PartialEq)]
pub enum Error {
    NotRunning,
    Server { status: u16, message: String },
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        match self {
            Error::NotRunning => write!(f, "Flock is not running"),
            Error::Server { status, message } if message.is_empty() => write!(f, "Flock answered with HTTP {status}"),
            Error::Server { message, .. } => write!(f, "{message}"),
        }
    }
}

/// Contents of api.json, written by the daemon.
#[derive(Deserialize)]
struct ApiConfig {
    url: String,
    token: String,
}

/// Default timeout of a request; slow endpoints pass their own.
const DEFAULT_TIMEOUT: Duration = Duration::from_secs(5);
/// Any request but a GET waits this long at least: a change reloads the daemon, which may start
/// PHP, and the first start after an install can take several seconds while it is scanned.
const WRITE_TIMEOUT: Duration = Duration::from_secs(30);
/// Fetching the package manifest (the daemon gives up after 50 seconds).
const CHECK_TIMEOUT: Duration = Duration::from_secs(60);
/// Changing versions waits for the daemon's work, like the Go CLI (2 minutes).
const CHANGE_TIMEOUT: Duration = Duration::from_secs(120);
/// Registering with Claude Code runs its CLI.
const MCP_TIMEOUT: Duration = Duration::from_secs(30);
/// Creating an app may download and extract an archive like WordPress (the Go CLI allows 2 minutes).
const APP_CREATE_TIMEOUT: Duration = Duration::from_secs(180);
/// Downloads that answer when they are done (Node.js, PostgreSQL): 10 minutes.
const INSTALL_TIMEOUT: Duration = Duration::from_secs(600);

/// Escapes a path segment (a version, a site's name).
fn escape(segment: &str) -> String {
    segment
        .bytes()
        .map(|b| match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'.' | b'_' | b'~' => (b as char).to_string(),
            _ => format!("%{b:02X}"),
        })
        .collect()
}

#[derive(Clone, Default)]
pub struct Client;

impl Client {
    pub fn status(&self) -> Result<Status, Error> {
        self.request("GET", "/v1/status", None::<&()>, DEFAULT_TIMEOUT)
    }

    /// Changes only the given settings and returns the new status.
    pub fn update_settings(&self, change: &SettingsChange) -> Result<Status, Error> {
        self.request("POST", "/v1/settings", Some(change), DEFAULT_TIMEOUT)
    }

    pub fn config(&self) -> Result<Config, Error> {
        self.request("GET", "/v1/config", None::<&()>, DEFAULT_TIMEOUT)
    }

    pub fn services(&self) -> Result<Vec<Service>, Error> {
        self.request::<_, Option<Vec<Service>>>("GET", "/v1/services", None::<&()>, DEFAULT_TIMEOUT).map(Option::unwrap_or_default)
    }

    pub fn php_versions(&self) -> Result<PhpVersions, Error> {
        self.request("GET", "/v1/php/versions", None::<&()>, DEFAULT_TIMEOUT)
    }

    /// Makes the daemon fetch the package manifest now.
    pub fn check_php(&self) -> Result<PhpVersions, Error> {
        self.request("POST", "/v1/php/check", None::<&()>, CHECK_TIMEOUT)
    }

    /// Installs a minor's newest release, updates to it, or retries a failed install; the
    /// download runs in the daemon (see the release's job).
    pub fn install_php(&self, minor: &str) -> Result<PhpVersions, Error> {
        self.request("POST", &format!("/v1/php/versions/{}/install", escape(minor)), None::<&()>, DEFAULT_TIMEOUT)
    }

    pub fn cancel_php(&self, minor: &str) -> Result<PhpVersions, Error> {
        self.request("POST", &format!("/v1/php/versions/{}/cancel", escape(minor)), None::<&()>, DEFAULT_TIMEOUT)
    }

    /// Forgets a failed install.
    pub fn dismiss_php(&self, minor: &str) -> Result<PhpVersions, Error> {
        self.request("DELETE", &format!("/v1/php/versions/{}/job", escape(minor)), None::<&()>, DEFAULT_TIMEOUT)
    }

    pub fn uninstall_php(&self, minor: &str) -> Result<PhpVersions, Error> {
        self.request("DELETE", &format!("/v1/php/versions/{}", escape(minor)), None::<&()>, CHANGE_TIMEOUT)
    }

    pub fn php_log(&self, minor: &str) -> Result<String, Error> {
        self.text(&format!("/v1/php/versions/{}/log", escape(minor)))
    }

    pub fn frankenphp(&self) -> Result<FrankenPhpState, Error> {
        self.request("GET", "/v1/frankenphp", None::<&()>, DEFAULT_TIMEOUT)
    }

    pub fn install_frankenphp(&self, minor: &str) -> Result<FrankenPhpState, Error> {
        self.request("POST", &format!("/v1/frankenphp/{}/install", escape(minor)), None::<&()>, DEFAULT_TIMEOUT)
    }

    pub fn cancel_frankenphp(&self, minor: &str) -> Result<FrankenPhpState, Error> {
        self.request("POST", &format!("/v1/frankenphp/{}/cancel", escape(minor)), None::<&()>, DEFAULT_TIMEOUT)
    }

    pub fn dismiss_frankenphp(&self, minor: &str) -> Result<FrankenPhpState, Error> {
        self.request("DELETE", &format!("/v1/frankenphp/{}/job", escape(minor)), None::<&()>, DEFAULT_TIMEOUT)
    }

    pub fn uninstall_frankenphp(&self, minor: &str) -> Result<FrankenPhpState, Error> {
        self.request("DELETE", &format!("/v1/frankenphp/{}", escape(minor)), None::<&()>, CHANGE_TIMEOUT)
    }

    pub fn node_versions(&self) -> Result<NodeVersions, Error> {
        self.request("GET", "/v1/node/versions", None::<&()>, CHECK_TIMEOUT)
    }

    pub fn check_node(&self) -> Result<NodeVersions, Error> {
        self.request("POST", "/v1/node/check", None::<&()>, CHECK_TIMEOUT)
    }

    /// Installs a major's newest release (also how it is updated); answers when it is done.
    pub fn install_node(&self, major: &str) -> Result<NodeVersions, Error> {
        self.request("POST", &format!("/v1/node/versions/{}/install", escape(major)), None::<&()>, INSTALL_TIMEOUT)
    }

    /// Updates a major's npm to the newest version that supports it.
    pub fn update_npm(&self, major: &str) -> Result<NodeVersions, Error> {
        self.request("POST", &format!("/v1/node/versions/{}/npm", escape(major)), None::<&()>, INSTALL_TIMEOUT)
    }

    pub fn uninstall_node(&self, major: &str) -> Result<NodeVersions, Error> {
        self.request("DELETE", &format!("/v1/node/versions/{}", escape(major)), None::<&()>, CHANGE_TIMEOUT)
    }

    pub fn start_service(&self, id: &str) -> Result<Vec<Service>, Error> {
        self.request::<_, Option<Vec<Service>>>("POST", &format!("/v1/services/{}/start", escape(id)), None::<&()>, DEFAULT_TIMEOUT)
            .map(Option::unwrap_or_default)
    }

    pub fn stop_service(&self, id: &str) -> Result<Vec<Service>, Error> {
        self.request::<_, Option<Vec<Service>>>("POST", &format!("/v1/services/{}/stop", escape(id)), None::<&()>, DEFAULT_TIMEOUT)
            .map(Option::unwrap_or_default)
    }

    pub fn set_service_port(&self, id: &str, port: u16) -> Result<Vec<Service>, Error> {
        let body = serde_json::json!({ "port": port });
        self.request::<_, Option<Vec<Service>>>("PATCH", &format!("/v1/services/{}", escape(id)), Some(&body), DEFAULT_TIMEOUT)
            .map(Option::unwrap_or_default)
    }

    pub fn catalog(&self) -> Result<Catalog, Error> {
        self.request("GET", "/v1/catalog", None::<&()>, DEFAULT_TIMEOUT)
    }

    pub fn check_catalog(&self) -> Result<Catalog, Error> {
        self.request("POST", "/v1/catalog/check", None::<&()>, CHECK_TIMEOUT)
    }

    /// Installs a line of a catalog service (its newest when line is empty), or updates it.
    pub fn install_catalog(&self, id: &str, line: &str, port: u16) -> Result<Catalog, Error> {
        let body = serde_json::json!({ "line": line, "port": port });
        self.request("POST", &format!("/v1/catalog/{}/install", escape(id)), Some(&body), DEFAULT_TIMEOUT)
    }

    /// Runs another installed line instead of the active one.
    pub fn activate_catalog(&self, id: &str, line: &str) -> Result<Catalog, Error> {
        let body = serde_json::json!({ "line": line });
        self.request("POST", &format!("/v1/catalog/{}/activate", escape(id)), Some(&body), CHANGE_TIMEOUT)
    }

    pub fn cancel_catalog(&self, id: &str) -> Result<Catalog, Error> {
        self.request("POST", &format!("/v1/catalog/{}/cancel", escape(id)), None::<&()>, DEFAULT_TIMEOUT)
    }

    pub fn dismiss_catalog(&self, id: &str) -> Result<Catalog, Error> {
        self.request("DELETE", &format!("/v1/catalog/{}/job", escape(id)), None::<&()>, DEFAULT_TIMEOUT)
    }

    pub fn uninstall_catalog(&self, id: &str, line: &str, delete_data: bool) -> Result<Catalog, Error> {
        let path = format!("/v1/catalog/{}?line={}&delete_data={}", escape(id), escape(line), if delete_data { "1" } else { "0" });
        self.request("DELETE", &path, None::<&()>, CHANGE_TIMEOUT)
    }

    pub fn catalog_log(&self, id: &str) -> Result<String, Error> {
        self.text(&format!("/v1/catalog/{}/log", escape(id)))
    }

    pub fn postgres_versions(&self) -> Result<PostgresVersions, Error> {
        self.request("GET", "/v1/postgres/versions", None::<&()>, DEFAULT_TIMEOUT)
    }

    pub fn check_postgres(&self) -> Result<PostgresVersions, Error> {
        self.request("POST", "/v1/postgres/check", None::<&()>, CHECK_TIMEOUT)
    }

    /// install, update or activate a major; answers when it is done.
    pub fn postgres_operation(&self, major: &str, operation: &str) -> Result<PostgresVersions, Error> {
        let timeout = if operation == "activate" { CHANGE_TIMEOUT } else { INSTALL_TIMEOUT };
        self.request("POST", &format!("/v1/postgres/versions/{}/{}", escape(major), escape(operation)), None::<&()>, timeout)
    }

    /// Installs a major with the extensions it has a build of.
    pub fn install_postgres_with(&self, major: &str, extensions: &[String]) -> Result<PostgresVersions, Error> {
        let body = serde_json::json!({ "extensions": extensions });
        self.request("POST", &format!("/v1/postgres/versions/{}/install", escape(major)), Some(&body), INSTALL_TIMEOUT)
    }

    /// The extensions of a major; creates its folder for your own.
    pub fn postgres_extensions(&self, major: &str) -> Result<PostgresExtensions, Error> {
        self.request("GET", &format!("/v1/postgres/versions/{}/extensions", escape(major)), None::<&()>, DEFAULT_TIMEOUT)
    }

    /// install, update or uninstall an extension of a major; answers when it is done.
    pub fn postgres_extension(&self, major: &str, name: &str, operation: &str) -> Result<PostgresExtensions, Error> {
        let path = format!("/v1/postgres/versions/{}/extensions/{}", escape(major), escape(name));
        match operation {
            "uninstall" => self.request("DELETE", &path, None::<&()>, CHANGE_TIMEOUT),
            _ => self.request("POST", &format!("{path}/{}", escape(operation)), None::<&()>, INSTALL_TIMEOUT),
        }
    }

    pub fn uninstall_postgres(&self, major: &str, delete_data: bool) -> Result<PostgresVersions, Error> {
        let path = format!("/v1/postgres/versions/{}?delete_data={}", escape(major), if delete_data { "1" } else { "0" });
        self.request("DELETE", &path, None::<&()>, CHANGE_TIMEOUT)
    }

    /// Serves a folder as a site (`flock link`); the daemon names it after the folder.
    pub fn link_site(&self, path: &str) -> Result<Status, Error> {
        self.request("POST", "/v1/sites", Some(&serde_json::json!({ "path": path, "secured": false })), DEFAULT_TIMEOUT)
    }

    /// Lists available templates from apps.conf.
    pub fn app_templates(&self) -> Result<Vec<AppTemplate>, Error> {
        self.request::<_, AppTemplatesResponse>("GET", "/v1/apps/templates", None::<&()>, DEFAULT_TIMEOUT)
            .map(|r| r.templates.unwrap_or_default())
    }

    /// Creates a new project directory from an apps.conf template and links it as a site.
    pub fn create_app(&self, req: &CreateAppRequest) -> Result<Status, Error> {
        self.request("POST", "/v1/apps", Some(req), APP_CREATE_TIMEOUT)
    }

    pub fn update_site(&self, name: &str, change: &SiteChange) -> Result<Status, Error> {
        self.request("PATCH", &format!("/v1/sites/{}", escape(name)), Some(change), CHANGE_TIMEOUT)
    }

    pub fn unlink_site(&self, name: &str) -> Result<Status, Error> {
        self.request("DELETE", &format!("/v1/sites/{}", escape(name)), None::<&()>, DEFAULT_TIMEOUT)
    }

    /// Restarts a FrankenPHP site's process, e.g. so Octane's workers load changed code.
    pub fn restart_site(&self, name: &str) -> Result<Status, Error> {
        self.request("POST", &format!("/v1/sites/{}/restart", escape(name)), None::<&()>, Duration::from_secs(60))
    }

    /// Serves <name>.test from upstream (a port, host:port or URL), adding the proxy or changing its
    /// upstream. None for secured keeps the proxy's setting (HTTP for a new one).
    pub fn proxy(&self, name: &str, upstream: &str, secured: Option<bool>) -> Result<Status, Error> {
        self.request("POST", "/v1/proxies", Some(&ProxyChange::new(name, upstream, secured)), DEFAULT_TIMEOUT)
    }

    /// Serves a proxy over HTTPS (on) or plain HTTP.
    pub fn secure_proxy(&self, name: &str, on: bool) -> Result<Status, Error> {
        self.request("PATCH", &proxy_path(name), Some(&serde_json::json!({ "secured": on })), DEFAULT_TIMEOUT)
    }

    /// Stops serving a proxy; the server at its upstream keeps running.
    pub fn remove_proxy(&self, name: &str) -> Result<Status, Error> {
        self.request("DELETE", &proxy_path(name), None::<&()>, DEFAULT_TIMEOUT)
    }

    pub fn add_domain(&self, site: &str, domain: &str) -> Result<Status, Error> {
        self.request(
            "POST",
            &format!("/v1/sites/{}/domains", escape(site)),
            Some(&serde_json::json!({ "domain": domain })),
            DEFAULT_TIMEOUT,
        )
    }

    pub fn remove_domain(&self, site: &str, entry: &str) -> Result<Status, Error> {
        self.request("DELETE", &format!("/v1/sites/{}/domains/{}", escape(site), escape(entry)), None::<&()>, DEFAULT_TIMEOUT)
    }

    /// Writes a version (php, node) or the document root (root) into the project.
    pub fn pin(&self, site: &str, tool: &str, version: &str) -> Result<Status, Error> {
        self.request(
            "POST",
            &format!("/v1/sites/{}/pin", escape(site)),
            Some(&serde_json::json!({ "tool": tool, "version": version })),
            DEFAULT_TIMEOUT,
        )
    }

    pub fn unpin(&self, site: &str, tool: &str) -> Result<Status, Error> {
        self.request("DELETE", &format!("/v1/sites/{}/pin/{}", escape(site), escape(tool)), None::<&()>, DEFAULT_TIMEOUT)
    }

    /// Adds Flock's guideline to the project's Laravel Boost guidelines (runs boost:update).
    pub fn add_boost_guideline(&self, site: &str) -> Result<Status, Error> {
        self.request("POST", &format!("/v1/sites/{}/boost", escape(site)), None::<&()>, CHANGE_TIMEOUT)
    }

    pub fn site_extensions(&self, site: &str) -> Result<ExtensionReport, Error> {
        self.request("GET", &format!("/v1/sites/{}/extensions", escape(site)), None::<&()>, Duration::from_secs(30))
    }

    /// Runs `composer require ext-<name>:*` in the project.
    pub fn require_extension(&self, site: &str, name: &str) -> Result<ExtensionReport, Error> {
        self.request(
            "POST",
            &format!("/v1/sites/{}/extensions/{}/require", escape(site), escape(name)),
            None::<&()>,
            Duration::from_secs(300),
        )
    }

    pub fn start_share(&self, site: &str, allow: &[String], debug: bool) -> Result<ShareStatus, Error> {
        self.request(
            "POST",
            &format!("/v1/shares/{}", escape(site)),
            Some(&serde_json::json!({ "allow": allow, "debug": debug })),
            Duration::from_secs(30),
        )
    }

    /// Builds the share's assets again (npm run build).
    pub fn rebuild_share(&self, site: &str) -> Result<ShareStatus, Error> {
        self.request("POST", &format!("/v1/shares/{}/rebuild", escape(site)), None::<&()>, DEFAULT_TIMEOUT)
    }

    pub fn stop_share(&self, site: &str) -> Result<serde_json::Value, Error> {
        self.request("DELETE", &format!("/v1/shares/{}", escape(site)), None::<&()>, DEFAULT_TIMEOUT)
    }

    pub fn mail(&self) -> Result<Vec<MailSummary>, Error> {
        self.request::<_, Option<Vec<MailSummary>>>("GET", "/v1/mail", None::<&()>, DEFAULT_TIMEOUT).map(Option::unwrap_or_default)
    }

    pub fn mail_detail(&self, id: &str) -> Result<MailDetail, Error> {
        self.request("GET", &format!("/v1/mail/{}", escape(id)), None::<&()>, DEFAULT_TIMEOUT)
    }

    /// The message as it arrived (RFC 822).
    pub fn mail_raw(&self, id: &str) -> Result<String, Error> {
        self.text(&format!("/v1/mail/{}/raw", escape(id)))
    }

    pub fn mail_attachment(&self, id: &str, index: u32) -> Result<Vec<u8>, Error> {
        self.send("GET", &format!("/v1/mail/{}/attachments/{index}", escape(id)), None::<&()>, Duration::from_secs(30))
    }

    /// Deletes every caught message.
    pub fn clear_mail(&self) -> Result<(), Error> {
        self.send("DELETE", "/v1/mail", None::<&()>, DEFAULT_TIMEOUT).map(|_| ())
    }

    pub fn dumps(&self) -> Result<Vec<crate::timeline::Dump>, Error> {
        self.request::<_, Option<Vec<_>>>("GET", "/v1/dumps", None::<&()>, DEFAULT_TIMEOUT).map(Option::unwrap_or_default)
    }

    /// The HTML header (scripts and styles) Symfony's dumps need, once a dump was captured.
    pub fn dump_header(&self) -> Result<String, Error> {
        self.text("/v1/dumps/header")
    }

    pub fn events(&self) -> Result<Vec<crate::timeline::Event>, Error> {
        self.request::<_, Option<Vec<_>>>("GET", "/v1/events", None::<&()>, DEFAULT_TIMEOUT).map(Option::unwrap_or_default)
    }

    /// Deletes every captured dump and event.
    pub fn clear_dumps(&self) -> Result<(), Error> {
        self.send("DELETE", "/v1/dumps", None::<&()>, DEFAULT_TIMEOUT)?;
        self.send("DELETE", "/v1/events", None::<&()>, DEFAULT_TIMEOUT).map(|_| ())
    }

    /// The logs there are: the services' (Flock's log folder) and the sites' (Laravel's storage/logs).
    pub fn logs(&self) -> Result<LogIndex, Error> {
        self.request("GET", "/v1/logs", None::<&()>, DEFAULT_TIMEOUT)
    }

    /// The last lines of a service's log.
    pub fn service_log(&self, name: &str) -> Result<String, Error> {
        self.service_log_tail(name, 2000)
    }

    /// The last `lines` lines of a service's log, such as a worktree's setup.
    pub fn service_log_tail(&self, name: &str, lines: u32) -> Result<String, Error> {
        self.text(&format!("/v1/logs/services/{}?lines={lines}", escape(name)))
    }

    /// A site's Laravel log entries, newest first.
    pub fn site_log(&self, name: &str) -> Result<Vec<LogEntry>, Error> {
        self.request::<_, Option<Vec<LogEntry>>>("GET", &format!("/v1/logs/sites/{}", escape(name)), None::<&()>, DEFAULT_TIMEOUT)
            .map(Option::unwrap_or_default)
    }

    /// Deletes the Laravel log files in a site's storage/logs.
    pub fn clear_site_log(&self, name: &str) -> Result<(), Error> {
        self.send("DELETE", &format!("/v1/logs/sites/{}", escape(name)), None::<&()>, DEFAULT_TIMEOUT).map(|_| ())
    }

    pub fn mcp(&self) -> Result<Mcp, Error> {
        self.request("GET", "/v1/mcp", None::<&()>, DEFAULT_TIMEOUT)
    }

    /// Adds Flock's MCP server to an AI client (Claude Code's runs its CLI).
    pub fn register_mcp(&self, id: &str) -> Result<Mcp, Error> {
        self.request("POST", &format!("/v1/mcp/clients/{}", escape(id)), None::<&()>, MCP_TIMEOUT)
    }

    pub fn unregister_mcp(&self, id: &str) -> Result<Mcp, Error> {
        self.request("DELETE", &format!("/v1/mcp/clients/{}", escape(id)), None::<&()>, MCP_TIMEOUT)
    }

    /// The site's branches, for where a new worktree starts.
    pub fn site_branches(&self, site: &str) -> Result<Branches, Error> {
        self.request("GET", &format!("/v1/sites/{}/branches", escape(site)), None::<&()>, DEFAULT_TIMEOUT)
    }

    /// Creates a worktree of site on branch (a new branch from `from`, "" for the current commit;
    /// a branch that exists is checked out; "" for a name Flock makes up) and starts its setup.
    /// db is "", shared, copy or fresh.
    pub fn create_worktree(&self, site: &str, branch: &str, from: &str, db: &str) -> Result<WorktreeCreated, Error> {
        let body = serde_json::json!({ "site": site, "branch": branch, "from": from, "db": db });
        self.request("POST", "/v1/worktrees", Some(&body), CHANGE_TIMEOUT)
    }

    /// Sets a worktree up, or joins the setup that runs; again sets up one that is set up already.
    pub fn setup_worktree(&self, path: &str, again: bool) -> Result<WorktreeStatus, Error> {
        let body = serde_json::json!({ "path": path, "again": again });
        self.request("POST", "/v1/worktrees/setup", Some(&body), DEFAULT_TIMEOUT)
    }

    /// The setup of the worktree at path: its state, step and log.
    pub fn worktree_setup(&self, path: &str) -> Result<WorktreeStatus, Error> {
        self.request("GET", &format!("/v1/worktrees/setup?path={}", escape(path)), None::<&()>, DEFAULT_TIMEOUT)
    }

    /// Gives a worktree the site's database (shared) or one of its own (copy, fresh), as its job.
    /// The database Flock made for it before is dropped.
    pub fn switch_worktree_database(&self, path: &str, db: &str) -> Result<WorktreeStatus, Error> {
        let body = serde_json::json!({ "path": path, "db": db });
        self.request("POST", "/v1/worktrees/db", Some(&body), DEFAULT_TIMEOUT)
    }

    /// Runs the worktree's before_delete hook, removes it with git and drops the database Flock
    /// made for it. Answers with what the hook and git printed. force removes it despite changes
    /// or a failing hook.
    pub fn remove_worktree(&self, path: &str, force: bool) -> Result<String, Error> {
        let body = serde_json::json!({ "path": path, "force": force });
        self.request::<_, WorktreeRemoval>("POST", "/v1/worktrees/remove", Some(&body), INSTALL_TIMEOUT).map(|r| r.output)
    }

    /// Serves every folder in path as a site (`flock park`).
    pub fn park(&self, path: &str) -> Result<Status, Error> {
        self.request("POST", "/v1/parked", Some(&serde_json::json!({ "path": path })), DEFAULT_TIMEOUT)
    }

    pub fn unpark(&self, path: &str) -> Result<Status, Error> {
        self.request("DELETE", "/v1/parked", Some(&serde_json::json!({ "path": path })), DEFAULT_TIMEOUT)
    }

    fn request<B: Serialize, T: DeserializeOwned>(
        &self,
        method: &str,
        path: &str,
        body: Option<&B>,
        timeout: Duration,
    ) -> Result<T, Error> {
        let bytes = self.send(method, path, body, timeout)?;
        serde_json::from_slice(&bytes).map_err(|e| Error::Server { status: 200, message: format!("unexpected answer from Flock: {e}") })
    }

    /// A plain-text answer, such as an install log.
    fn text(&self, path: &str) -> Result<String, Error> {
        self.send("GET", path, None::<&()>, DEFAULT_TIMEOUT).map(|bytes| String::from_utf8_lossy(&bytes).into_owned())
    }

    /// Reads api.json for every request: a restarted daemon listens on a new port with a new token.
    fn send<B: Serialize>(&self, method: &str, path: &str, body: Option<&B>, timeout: Duration) -> Result<Vec<u8>, Error> {
        let data = std::fs::read(paths::api_json()).map_err(|_| Error::NotRunning)?;
        let config: ApiConfig = serde_json::from_slice(&data).map_err(|_| Error::NotRunning)?;
        let timeout = if method == "GET" { timeout } else { timeout.max(WRITE_TIMEOUT) };
        let agent: ureq::Agent = ureq::Agent::config_builder().http_status_as_error(false).timeout_global(Some(timeout)).build().into();
        let url = format!("{}{}", config.url.trim_end_matches('/'), path);
        let auth = format!("Bearer {}", config.token);
        let json = body.map(|b| serde_json::to_vec(b).expect("request body"));
        let response = match (method, json) {
            ("GET", _) => agent.get(&url).header("Authorization", &auth).call(),
            ("DELETE", None) => agent.delete(&url).header("Authorization", &auth).call(),
            // DELETE /v1/parked names the folder in its body.
            ("DELETE", Some(json)) => {
                agent.delete(&url).header("Authorization", &auth).force_send_body().content_type("application/json").send(&json[..])
            }
            (_, json) => {
                let json = json.unwrap_or_default();
                let builder = match method {
                    "PATCH" => agent.patch(&url),
                    "PUT" => agent.put(&url),
                    _ => agent.post(&url),
                };
                builder.header("Authorization", &auth).content_type("application/json").send(&json[..])
            }
        };
        let mut response = response.map_err(|_| Error::NotRunning)?;
        let status = response.status().as_u16();
        let bytes = response.body_mut().read_to_vec().map_err(|_| Error::NotRunning)?;
        if !(200..300).contains(&status) {
            #[derive(Deserialize)]
            struct ErrorBody {
                error: String,
            }
            let message = serde_json::from_slice::<ErrorBody>(&bytes).map(|e| e.error).unwrap_or_default();
            return Err(Error::Server { status, message });
        }
        Ok(bytes)
    }
}

fn proxy_path(name: &str) -> String {
    format!("/v1/proxies/{}", escape(name))
}

/// POST /v1/proxies: secured is left out to keep an existing proxy's setting.
#[derive(Serialize, Debug, Clone, PartialEq)]
pub struct ProxyChange {
    pub name: String,
    pub upstream: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub secured: Option<bool>,
}

impl ProxyChange {
    pub fn new(name: &str, upstream: &str, secured: Option<bool>) -> Self {
        Self { name: name.to_owned(), upstream: upstream.to_owned(), secured }
    }
}

/// POST /v1/settings: only the fields that are set change.
#[derive(Serialize, Default)]
pub struct SettingsChange {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub xdebug: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub xdebug_breakpoints: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub dumps: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub capture_off: Option<Vec<String>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub dumps_in_page: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub channel: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub default_php: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub default_node: Option<String>,
}

/// GET /v1/status (api.Status).
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct Status {
    pub version: String,
    pub pid: i64,
    pub dns: DnsStatus,
    pub http_port: u16,
    pub https_port: u16,
    pub mail: String,
    pub dumps: bool,
    pub capture_off: Option<Vec<String>>,
    pub dumps_in_page: bool,
    pub capture_unsupported: Option<Vec<String>>,
    pub mail_count: u32,
    pub dump_count: u32,
    pub event_count: u32,
    pub postgres: Option<String>,
    pub extras: Vec<ExtraStatus>,
    pub xdebug: bool,
    pub xdebug_breakpoints: bool,
    pub sites: Vec<SiteStatus>,
    /// Names served by a server of their own, e.g. a dev server on a port (`flock proxy`); an
    /// older daemon sends none.
    pub proxies: Vec<ProxyStatus>,
    pub processes: Vec<ProcStatus>,
    pub warnings: Vec<String>,
    pub channel: String,
    /// The first run's install of the default PHP and Node.js, until both are set.
    pub defaults: Option<DefaultsStatus>,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct DefaultsStatus {
    pub php: DefaultInstall,
    pub node: DefaultInstall,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct DefaultInstall {
    pub version: String,
    /// waiting, installing, failed or done.
    pub state: String,
    pub error: String,
}

impl Status {
    /// Something needs the user's attention: a warning or a process that is down.
    pub fn is_degraded(&self) -> bool {
        !self.warnings.is_empty() || self.processes.iter().any(|p| !p.up)
    }
}

/// The kinds of events Flock captures besides dumps, in the order the apps list them.
pub const EVENT_KINDS: [(&str, &str); 5] =
    [("query", "Queries"), ("log", "Logs"), ("view", "Views"), ("job", "Jobs"), ("request", "Requests")];

/// GET /v1/config (api.ConfigView): config.json plus the installed versions.
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct Config {
    pub tld: String,
    pub default_php: String,
    pub default_node: String,
    pub channel: String,
    pub root: String,
    pub ports: Ports,
    pub parked: Option<Vec<String>>,
    pub php: Option<Vec<PhpVersion>>,
    pub node: Option<Vec<String>>,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct Ports {
    pub http: u16,
    pub https: u16,
    pub smtp: u16,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct PhpVersion {
    pub version: String,
    pub full_version: String,
    pub ini: String,
    pub xdebug_available: bool,
}

/// A download and install running in the daemon, or a failed one (php.Job, frankenphp.Job, ...).
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct Job {
    pub action: String,
    pub state: String,
    pub phase: String,
    /// 0..1, or -1 when the size is unknown.
    pub progress: f64,
    pub error: String,
}

impl Job {
    pub fn running(&self) -> bool {
        self.state == "running"
    }

    pub fn failed(&self) -> bool {
        self.state == "failed"
    }

    /// "Downloading… 42%", "Downloading…" (size unknown) or "Installing…".
    pub fn text(&self) -> String {
        match self.phase.as_str() {
            "installing" => "Installing…".to_owned(),
            _ if (0.0..=1.0).contains(&self.progress) => format!("Downloading… {}%", (self.progress * 100.0).round()),
            _ => "Downloading…".to_owned(),
        }
    }
}

/// GET /v1/php/versions (api.PHPVersions).
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct PhpVersions {
    pub checked_at: Option<String>,
    pub check_error: String,
    pub versions: Option<Vec<PhpRelease>>,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct PhpRelease {
    pub minor: String,
    pub latest: String,
    pub installed: String,
    /// installed, update or available.
    pub status: String,
    pub update: bool,
    pub size: i64,
    pub prerelease: bool,
    pub yanked: String,
    pub job: Option<Job>,
    pub default: bool,
    pub xdebug: bool,
}

/// GET /v1/node/versions (api.NodeVersions).
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct NodeVersions {
    pub checked_at: Option<String>,
    pub check_error: String,
    pub versions: Option<Vec<NodeRelease>>,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct NodeRelease {
    pub major: String,
    pub latest: String,
    pub installed: String,
    pub default: bool,
    pub update: bool,
    pub npm: String,
    pub npm_latest: String,
}

/// GET /v1/frankenphp (frankenphp.State).
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct FrankenPhpState {
    pub available: bool,
    pub checked_at: Option<String>,
    pub check_error: String,
    pub builds: Option<Vec<FrankenPhpBuild>>,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct FrankenPhpBuild {
    pub php: String,
    pub installed: String,
    pub php_version: String,
    pub latest: String,
    pub update: bool,
    pub size: i64,
    pub prerelease: bool,
    pub yanked: String,
    pub job: Option<Job>,
}

/// GET /v1/catalog (catalog.Catalog): the services Flock installs on request.
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct Catalog {
    pub checked_at: Option<String>,
    pub check_error: String,
    pub services: Option<Vec<CatalogEntry>>,
}

impl Catalog {
    pub fn find(&self, id: &str) -> Option<&CatalogEntry> {
        self.services.iter().flatten().find(|e| e.id == id)
    }
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct CatalogEntry {
    pub id: String,
    pub name: String,
    pub estimate: String,
    pub installed: String,
    pub line: String,
    pub latest: String,
    pub update: bool,
    pub port: u16,
    pub lines: Option<Vec<CatalogLine>>,
    pub job: Option<CatalogJob>,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct CatalogLine {
    pub id: String,
    pub label: String,
    pub latest: String,
    pub prerelease: bool,
    pub installed: bool,
    pub version: String,
    pub active: bool,
    pub update: bool,
    pub yanked: String,
    pub has_data: bool,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct CatalogJob {
    #[serde(flatten)]
    pub job: Job,
    pub version: String,
    pub line: String,
}

/// GET /v1/postgres/versions (postgres.Versions).
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct PostgresVersions {
    pub active: String,
    pub running: bool,
    pub port: u16,
    pub checked_at: Option<String>,
    pub check_error: String,
    pub majors: Option<Vec<PostgresMajor>>,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct PostgresMajor {
    pub major: String,
    pub installed: String,
    pub latest: String,
    pub update: bool,
    pub size: i64,
    pub prerelease: bool,
    pub yanked: String,
    pub active: bool,
    pub has_data: bool,
    pub data_size: i64,
    pub eol: String,
    /// "", installing, updating, switching or uninstalling.
    pub busy: String,
    /// Names of the extensions installed for this major.
    pub extensions: Option<Vec<String>>,
}

/// GET /v1/postgres/versions/{major}/extensions (postgres.MajorExtensions): Flock's extensions for
/// one major, and the folder for your own.
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct PostgresExtensions {
    pub major: String,
    pub extensions: Option<Vec<PostgresExtension>>,
    pub custom_dir: String,
    /// What is in the custom folder, read only.
    pub custom: Option<Vec<PostgresCustomExtension>>,
}

/// An extension in a major's own extensions folder: what Flock found, and whether the server has it.
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct PostgresCustomExtension {
    pub name: String,
    pub version: String,
    pub linked: bool,
    /// Why it can't be used; empty when nothing is in the way.
    pub problem: String,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct PostgresExtension {
    pub name: String,
    pub title: String,
    pub description: String,
    /// What CREATE EXTENSION takes ("vector" for pgvector).
    pub creates: String,
    pub license: String,
    /// Loaded at server start: installing or removing it restarts PostgreSQL.
    pub preload: bool,
    pub supported: bool,
    pub installed: String,
    pub latest: String,
    pub update: bool,
    /// Newer versions exist, but no longer for this major.
    pub discontinued: bool,
    pub size: i64,
    pub prerelease: bool,
    pub yanked: String,
    /// "", installing, updating or uninstalling.
    pub busy: String,
}

/// "PostGIS, pgvector" for the extension names of a major.
pub fn extension_titles(names: &[String]) -> String {
    names
        .iter()
        .map(|n| match n.as_str() {
            "postgis" => "PostGIS",
            "pgrouting" => "pgRouting",
            "timescaledb" => "TimescaleDB",
            other => other,
        })
        .collect::<Vec<_>>()
        .join(", ")
}

/// A caught message (GET /v1/mail, mail.Message).
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct MailSummary {
    pub id: String,
    pub inbox: String,
    pub from: String,
    pub to: Option<Vec<String>>,
    pub subject: String,
    pub date: String,
}

/// GET /v1/mail/{id} (mail.Detail). cid: references in html are inlined as data URIs.
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct MailDetail {
    pub id: String,
    pub inbox: String,
    pub from: String,
    pub to: Option<Vec<String>>,
    pub cc: Option<Vec<String>>,
    pub subject: String,
    pub date: String,
    pub html: String,
    pub text: String,
    pub headers: Option<Vec<MailHeader>>,
    pub attachments: Option<Vec<MailAttachment>>,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct MailHeader {
    pub name: String,
    pub value: String,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct MailAttachment {
    pub index: u32,
    pub filename: String,
    pub content_type: String,
    pub size: u64,
}

/// GET /v1/logs.
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct LogIndex {
    pub services: Option<Vec<String>>,
    pub sites: Option<Vec<String>>,
}

/// A Laravel log entry (laravellog.Entry).
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct LogEntry {
    pub time: String,
    pub env: String,
    pub level: String,
    pub message: String,
    pub details: String,
}

/// Monolog's levels, least severe first.
const SEVERITY: [&str; 8] = ["debug", "info", "notice", "warning", "error", "critical", "alert", "emergency"];

impl LogEntry {
    /// Whether the entry is at least minimum ("" for every entry). Levels Flock doesn't know count
    /// as errors, so nothing unusual is hidden by a filter.
    pub fn at_least(&self, minimum: &str) -> bool {
        let rank = |level: &str| SEVERITY.iter().position(|l| *l == level);
        match rank(minimum) {
            None => true,
            Some(minimum) => rank(&self.level.to_lowercase()).or(rank("error")).is_some_and(|r| r >= minimum),
        }
    }
}

/// GET /v1/mcp (api.MCP): the AI clients Flock's MCP server can be added to.
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct Mcp {
    pub clients: Option<Vec<McpClient>>,
    pub snippet: String,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct McpClient {
    pub id: String,
    pub name: String,
    /// registered, available or missing.
    pub state: String,
    pub problem: String,
}

/// GET /v1/services (api.Service): every service with its live state.
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct Service {
    pub id: String,
    pub name: String,
    pub category: String,
    pub description: String,
    pub available: bool,
    pub unavailable: String,
    pub version: String,
    pub port: u16,
    pub installed: bool,
    pub enabled: bool,
    pub running: bool,
    pub stoppable: bool,
    pub env: String,
    pub log: String,
    pub url: String,
    pub problem: String,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct DnsStatus {
    pub tld: String,
    pub wildcard: bool,
    pub wildcard_problem: String,
    pub max_depth: u32,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct ExtraStatus {
    pub id: String,
    pub name: String,
    pub version: String,
    pub port: u16,
    pub enabled: bool,
    pub up: bool,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct ProcStatus {
    pub name: String,
    pub up: bool,
    pub restarts: u32,
    pub last_error: Option<String>,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct SiteStatus {
    pub name: String,
    pub domain: String,
    pub path: String,
    /// The versions the site uses. Each source is isolated (set here), project (pinned in the
    /// project) or default; pinned is what the project pins in file.
    pub php: String,
    pub php_source: String,
    pub php_pinned: String,
    pub php_file: String,
    pub node: String,
    pub node_source: String,
    pub node_pinned: String,
    pub node_file: String,
    pub node_shadowed: String,
    pub secured: bool,
    pub parked: bool,
    /// Set on a git worktree of another site: that site's name, whose settings it uses. branch is
    /// the worktree's branch ("" when HEAD is detached).
    pub parent: String,
    pub branch: String,
    /// The setup of a worktree Flock set up (None for others).
    pub worktree: Option<WorktreeStatus>,
    pub url: Option<String>,
    pub problem: Option<String>,
    pub notice: Option<String>,
    /// The document root, relative to path ("." for path itself), and where it comes from.
    pub root: String,
    pub root_source: String,
    pub root_pinned: String,
    pub root_file: String,
    /// "" when served from root, else missing, not_found or empty.
    pub root_state: Option<String>,
    pub root_suggestions: Option<Vec<String>>,
    pub debugging: bool,
    pub domains: Option<Vec<SiteDomain>>,
    /// fastcgi or frankenphp; worker is "" (classic) or octane.
    pub server: String,
    pub worker: String,
    pub octane_available: bool,
    pub frankenphp: Option<SiteFrankenPhp>,
    /// "", missing, current, outdated or custom.
    pub boost: String,
    pub share: Option<ShareStatus>,
}

impl SiteStatus {
    pub fn shared(&self) -> bool {
        self.share.as_ref().is_some_and(|s| !s.url.is_empty())
    }

    /// The project's version pin is in the project (not in a parent folder's .flockrc).
    pub fn php_pinned_here(&self) -> bool {
        !self.php_file.is_empty() && in_folder(&self.php_file, &self.path)
    }

    pub fn node_pinned_here(&self) -> bool {
        !self.node_file.is_empty() && in_folder(&self.node_file, &self.path)
    }

    pub fn root_pinned_here(&self) -> bool {
        !self.root_pinned.is_empty()
    }

    pub fn is_worktree(&self) -> bool {
        !self.parent.is_empty()
    }

    /// What a worktree is listed as: its branch, or the name it is served under ("feature-auth" of
    /// feature-auth.acme) when HEAD is detached.
    pub fn worktree_label(&self) -> String {
        if !self.branch.is_empty() {
            return self.branch.clone();
        }
        self.name.split('.').next().unwrap_or(&self.name).to_owned()
    }

    /// The database Flock made for the worktree, if it has one of its own.
    pub fn own_database(&self) -> Option<&str> {
        self.worktree.as_ref().map(|w| w.database.as_str()).filter(|d| !d.is_empty())
    }

    /// FrankenPHP runs PHP 8.2 and newer; a version that can't be read doesn't block it.
    pub fn supports_frankenphp(&self) -> bool {
        let mut parts = self.php.split('.').map(|p| p.parse::<u32>());
        match (parts.next(), parts.next()) {
            (Some(Ok(major)), Some(Ok(minor))) => (major, minor) >= (8, 2),
            _ => true,
        }
    }
}

impl Status {
    /// The sites the lists show at the top: those that aren't worktrees, and worktrees whose site
    /// isn't served (any more).
    pub fn top_sites(&self) -> Vec<&SiteStatus> {
        self.sites.iter().filter(|s| !s.is_worktree() || !self.sites.iter().any(|p| p.name == s.parent)).collect()
    }

    /// The worktrees of a site.
    pub fn worktrees_of(&self, name: &str) -> Vec<&SiteStatus> {
        self.sites.iter().filter(|s| s.parent == name).collect()
    }
}

fn in_folder(file: &str, folder: &str) -> bool {
    std::path::Path::new(file).parent() == Some(std::path::Path::new(folder))
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct SiteDomain {
    /// As stored and passed to the API: "tenant1" or "my-app-2.test".
    pub entry: String,
    pub host: String,
    pub alias: bool,
    pub url: Option<String>,
    pub problem: Option<String>,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct SiteFrankenPhp {
    /// running, installing, failed or unavailable.
    pub state: String,
    pub version: String,
    pub php_version: String,
    pub progress: f64,
    pub problem: String,
    pub log: String,
}

/// A name served by a server of its own rather than by PHP, e.g. a Node.js dev server: every
/// request, WebSocket upgrades included, passes to upstream with its Host header.
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct ProxyStatus {
    pub name: String,
    pub domain: String,
    /// Where requests go, e.g. http://127.0.0.1:3000.
    pub upstream: String,
    pub secured: bool,
    pub url: Option<String>,
    /// Why it isn't served, e.g. a site of the same name serves its domain.
    pub problem: Option<String>,
    /// Something answers at the upstream; server names it when it runs on this machine, e.g.
    /// "Node.js 22.23.4" for one of Flock's Node.js versions.
    pub running: bool,
    pub server: Option<String>,
}

impl ProxyStatus {
    /// The upstream without the http:// Flock adds: "127.0.0.1:3000".
    pub fn upstream_label(&self) -> &str {
        self.upstream.strip_prefix("http://").unwrap_or(&self.upstream)
    }

    /// What answers at the upstream: "Node.js 22.23.4", "Running" or "Not running".
    pub fn server_text(&self) -> String {
        match (&self.server, self.running) {
            (_, false) => "Not running".to_owned(),
            (Some(server), true) if !server.is_empty() => server.clone(),
            _ => "Running".to_owned(),
        }
    }
}

/// The setup of a git worktree (worktree.Status).
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct WorktreeStatus {
    /// queued, running, ready or failed.
    pub state: String,
    /// What a running setup does now, e.g. "composer install".
    pub step: String,
    pub error: String,
    /// The setup's log, for /v1/logs/services/{log}.
    pub log: String,
    /// The database mode asked for: shared, copy, fresh, or "" for the project's.
    pub db_mode: String,
    /// The worktree's own database, "" when it uses the site's.
    pub database: String,
}

impl WorktreeStatus {
    /// Queued or running: the setup, or a switch of the database.
    pub fn busy(&self) -> bool {
        self.state == "queued" || self.state == "running"
    }

    pub fn failed(&self) -> bool {
        self.state == "failed"
    }

    /// "Waiting…", "composer install…", "Ready" or "Setup failed".
    pub fn text(&self) -> String {
        match self.state.as_str() {
            "queued" => "Waiting…".to_owned(),
            "running" => self.step_text(),
            "failed" => "Setup failed".to_owned(),
            _ => "Ready".to_owned(),
        }
    }

    /// What a running setup does: "Composer install…", or "Setting up…" between steps.
    pub fn step_text(&self) -> String {
        if self.step.is_empty() { "Setting up…".to_owned() } else { format!("{}…", capitalized(&self.step)) }
    }
}

fn capitalized(text: &str) -> String {
    let mut chars = text.chars();
    chars.next().map(|c| c.to_uppercase().chain(chars).collect()).unwrap_or_default()
}

/// POST /v1/worktrees (api.WorktreeCreated).
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct WorktreeCreated {
    pub path: String,
    /// The branch it is on: the one asked for, or the name Flock made up.
    pub branch: String,
    pub setup: WorktreeStatus,
}

/// GET /v1/sites/{name}/branches (api.Branches): current is "" when HEAD is detached; default is
/// origin's HEAD, else main or master, else "".
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct Branches {
    pub current: String,
    pub default: String,
    pub local: Option<Vec<String>>,
    pub remote: Option<Vec<String>>,
}

impl Branches {
    pub fn has_local(&self, name: &str) -> bool {
        self.local.iter().flatten().any(|b| b == name)
    }

    /// Where a new branch can start, as value and label: the current commit when HEAD is
    /// detached, the current branch, the default one, the other local ones, then origin's.
    pub fn choices(&self) -> Vec<(String, String)> {
        let mut choices = Vec::new();
        if self.current.is_empty() {
            choices.push((String::new(), "The current commit".to_owned()));
        }
        let mut names: Vec<&String> = Vec::new();
        for name in [&self.current, &self.default].into_iter().chain(self.local.iter().flatten()) {
            if !name.is_empty() && self.has_local(name) && !names.contains(&name) {
                names.push(name);
            }
        }
        for name in names {
            let label = if *name == self.current {
                format!("{name} (current)")
            } else if *name == self.default {
                format!("{name} (default)")
            } else {
                name.clone()
            };
            choices.push((name.clone(), label));
        }
        choices.extend(self.remote.iter().flatten().map(|r| (r.clone(), r.clone())));
        choices
    }
}

/// POST /v1/worktrees/remove (api.WorktreeRemoval).
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
struct WorktreeRemoval {
    output: String,
}

/// A site's share (`flock share`).
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct ShareStatus {
    /// building, starting, running or failed.
    pub state: String,
    pub url: String,
    pub allow: Option<Vec<String>>,
    pub debug: bool,
    pub expires_at: Option<String>,
    pub can_rebuild: bool,
    pub rebuilding: bool,
    pub rebuild_error: String,
    pub error: String,
    pub warning: String,
    /// The URL as a QR code: a base64 PNG.
    pub qr: String,
}

/// PATCH /v1/sites/{name}: only the fields that are set change.
#[derive(Serialize, Default, Clone)]
pub struct SiteChange {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub secured: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub php: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub node: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub server: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub worker: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub root: Option<String>,
}

/// GET /v1/sites/{name}/extensions (phpext.Report).
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct ExtensionReport {
    pub php: String,
    pub laravel: bool,
    pub extensions: Option<Vec<PhpExtension>>,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct PhpExtension {
    pub name: String,
    pub required_by: Option<Vec<String>>,
    pub hint: String,
    pub available: bool,
}

/// One template entry from GET /v1/apps/templates (apps.conf).
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct AppTemplate {
    pub name: String,
    pub requires_db: bool,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
struct AppTemplatesResponse {
    templates: Option<Vec<AppTemplate>>,
}

/// POST /v1/apps.
#[derive(Serialize, Debug, Clone, PartialEq, Default)]
pub struct CreateAppRequest {
    pub name: String,
    pub parent_dir: String,
    #[serde(rename = "type")]
    pub app_type: String,
    pub db: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub db_service: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub php: Option<String>,
    pub secured: bool,
}

#[cfg(test)]
mod tests {
    use super::*;

    // A status as the daemon writes it, with fields the app doesn't read.
    #[test]
    fn decodes_a_status() {
        let json = r#"{"version":"1.4.0","pid":42,"started_at":"2026-10-23T21:00:00Z",
            "dns":{"tld":"test","addr":"127.0.0.1:53535","wildcard":true,"max_depth":4},
            "http_port":80,"https_port":443,"mail":"127.0.0.1:2525","dumps":true,"capture_off":null,
            "mail_count":3,"dump_count":0,"event_count":0,"postgres":"18.6","xdebug":false,
            "sites":[{"name":"acme","domain":"acme.test","path":"/home/a/acme","php":"8.4","secured":true,
              "url":"https://acme.test","domains":[],"server":"fastcgi","worker":"","debugging":false}],
            "processes":[{"name":"postgres","up":true,"restarts":0}],"channel":"stable"}"#;
        let status: Status = serde_json::from_str(json).unwrap();
        assert_eq!(status.version, "1.4.0");
        assert_eq!(status.postgres.as_deref(), Some("18.6"));
        assert_eq!(status.sites[0].url.as_deref(), Some("https://acme.test"));
        assert!(!status.sites[0].shared());
        assert!(!status.is_degraded());
    }

    // A site with two worktrees: one set up with a database of its own, one Flock didn't set up.
    #[test]
    fn decodes_worktrees() {
        let json = r#"{"sites":[
            {"name":"acme","domain":"acme.test","path":"/home/a/acme","secured":true},
            {"name":"feature-auth.acme","domain":"feature-auth.acme.test","path":"/home/a/acme-feature-auth",
              "parent":"acme","branch":"feature/auth","url":"https://feature-auth.acme.test",
              "worktree":{"state":"running","step":"composer install","log":"worktree-feature-auth.acme",
                "db_mode":"copy","database":"acme_feature_auth"}},
            {"name":"old.acme","domain":"old.acme.test","path":"/home/a/acme-old","parent":"acme","worktree":null}]}"#;
        let status: Status = serde_json::from_str(json).unwrap();
        assert_eq!(status.top_sites().len(), 1);
        let worktrees = status.worktrees_of("acme");
        assert_eq!(worktrees.len(), 2);
        let auth = worktrees[0];
        assert!(auth.is_worktree());
        assert_eq!(auth.worktree_label(), "feature/auth");
        assert_eq!(auth.own_database(), Some("acme_feature_auth"));
        let setup = auth.worktree.as_ref().unwrap();
        assert!(setup.busy());
        assert_eq!(setup.text(), "Composer install…");
        assert_eq!(setup.log, "worktree-feature-auth.acme");
        // Detached, and not set up by Flock.
        assert_eq!(worktrees[1].worktree_label(), "old");
        assert_eq!(worktrees[1].worktree, None);
        assert_eq!(worktrees[1].own_database(), None);
        // An older daemon's site.
        assert!(!status.sites[0].is_worktree());
        assert!(status.worktrees_of("old.acme").is_empty());
    }

    #[test]
    fn decodes_proxies() {
        let json = r#"{"proxies":[
            {"name":"softpack","domain":"softpack.test","upstream":"http://127.0.0.1:3000","secured":true,
              "url":"https://softpack.test","running":true,"server":"Node.js 22.23.4"},
            {"name":"blog","domain":"blog.test","upstream":"https://127.0.0.1:8443","secured":false,
              "problem":"blog.test is served by a site of the same name","running":false}]}"#;
        let status: Status = serde_json::from_str(json).unwrap();
        let names: Vec<&str> = status.proxies.iter().map(|p| p.name.as_str()).collect();
        assert_eq!(names, ["softpack", "blog"]);
        let softpack = &status.proxies[0];
        assert!(softpack.secured && softpack.running && softpack.problem.is_none());
        assert_eq!(softpack.url.as_deref(), Some("https://softpack.test"));
        assert_eq!(softpack.upstream_label(), "127.0.0.1:3000");
        assert_eq!(softpack.server_text(), "Node.js 22.23.4");
        let blog = &status.proxies[1];
        assert!(blog.url.is_none() && blog.problem.is_some() && !blog.running && blog.server.is_none());
        assert_eq!(blog.upstream_label(), "https://127.0.0.1:8443");
        assert_eq!(blog.server_text(), "Not running");
        let unnamed = ProxyStatus { running: true, ..Default::default() };
        assert_eq!(unnamed.server_text(), "Running");
        // An older daemon sends none.
        assert!(serde_json::from_str::<Status>(r#"{"version":"1.4.0"}"#).unwrap().proxies.is_empty());
    }

    #[test]
    fn shapes_the_proxy_requests() {
        // A new proxy leaves secured out, so the daemon keeps an existing one's setting.
        let added = serde_json::to_value(ProxyChange::new("softpack", "3000", None)).unwrap();
        assert_eq!(added, serde_json::json!({ "name": "softpack", "upstream": "3000" }));
        let secured = serde_json::to_value(ProxyChange::new("softpack", "3001", Some(true))).unwrap();
        assert_eq!(secured, serde_json::json!({ "name": "softpack", "upstream": "3001", "secured": true }));
        assert_eq!(proxy_path("soft pack"), "/v1/proxies/soft%20pack");
    }

    #[test]
    fn decodes_a_created_worktree() {
        let json = r#"{"path":"/home/a/acme-brave-otter","branch":"brave-otter","setup":{"state":"queued","log":"worktree-x.acme"}}"#;
        let created: WorktreeCreated = serde_json::from_str(json).unwrap();
        assert_eq!(created.branch, "brave-otter");
        assert_eq!(created.setup.text(), "Waiting…");
    }

    #[test]
    fn orders_the_branches_to_start_from() {
        let json = r#"{"current":"develop","default":"main","local":["develop","feature/x","main"],
            "remote":["origin/develop","origin/main"]}"#;
        let branches: Branches = serde_json::from_str(json).unwrap();
        assert!(branches.has_local("feature/x"));
        assert!(!branches.has_local("origin/main"));
        let labels: Vec<String> = branches.choices().into_iter().map(|(_, label)| label).collect();
        assert_eq!(labels, ["develop (current)", "main (default)", "feature/x", "origin/develop", "origin/main"]);
        assert_eq!(branches.choices()[1].0, "main");
        // Detached, or a daemon without branches: only the current commit.
        let detached: Branches = serde_json::from_str(r#"{"current":"","default":"","local":null,"remote":null}"#).unwrap();
        assert_eq!(detached.choices(), [(String::new(), "The current commit".to_owned())]);
        assert_eq!(Branches::default().choices().len(), 1);
    }

    #[test]
    fn filters_log_levels() {
        let entry = |level: &str| LogEntry { level: level.into(), ..Default::default() };
        assert!(entry("debug").at_least(""));
        assert!(!entry("info").at_least("warning"));
        assert!(entry("ERROR").at_least("warning"));
        assert!(entry("strange").at_least("error"));
    }

    #[test]
    fn degraded_when_a_process_is_down() {
        let down = ProcStatus { name: "postgres".into(), ..Default::default() };
        let status = Status { processes: vec![down], ..Default::default() };
        assert!(status.is_degraded());
    }

    #[test]
    fn decodes_postgres_extensions() {
        let versions: PostgresVersions = serde_json::from_str(
            r#"{"active":"18","majors":[{"major":"18","installed":"18.6","extensions":["postgis","pgvector"]},{"major":"17"}]}"#,
        )
        .unwrap();
        let majors = versions.majors.unwrap();
        assert_eq!(extension_titles(majors[0].extensions.as_deref().unwrap_or_default()), "PostGIS, pgvector");
        assert!(majors[1].extensions.is_none());

        let json = r#"{"major":"17","custom_dir":"/home/demo/.local/share/flock/services/postgres/extensions/custom/17",
            "extensions":[{"name":"timescaledb","title":"TimescaleDB","creates":"timescaledb","preload":true,"supported":true,
              "installed":"2.30.2","latest":"2.30.2","discontinued":true,"busy":"updating","size":123},
              {"name":"pgvector","supported":false}],
            "custom":[{"name":"hello","version":"1.2","linked":true},{"name":"broken","problem":"its library broken isn't in lib/"}]}"#;
        let state: PostgresExtensions = serde_json::from_str(json).unwrap();
        let extensions = state.extensions.unwrap();
        assert!(state.custom_dir.ends_with("/custom/17"));
        assert!(extensions[0].preload && extensions[0].discontinued && extensions[0].busy == "updating" && extensions[0].size == 123);
        assert!(!extensions[1].supported && extensions[1].installed.is_empty());
        let custom = state.custom.unwrap();
        assert!(custom[0].linked && custom[0].version == "1.2" && custom[0].problem.is_empty());
        assert!(!custom[1].linked && custom[1].problem.contains("lib/"));
    }

    #[test]
    fn decodes_app_templates_and_shapes_create_request() {
        let json = r#"{"templates":[{"name":"blank","requires_db":false},{"name":"wordpress","requires_db":true}]}"#;
        let decoded: AppTemplatesResponse = serde_json::from_str(json).unwrap();
        let templates = decoded.templates.unwrap();
        assert_eq!(templates.len(), 2);
        assert_eq!(templates[0].name, "blank");
        assert!(!templates[0].requires_db);
        assert_eq!(templates[1].name, "wordpress");
        assert!(templates[1].requires_db);

        let req = CreateAppRequest {
            name: "my-blog".to_owned(),
            parent_dir: "/home/demo/Sites".to_owned(),
            app_type: "wordpress".to_owned(),
            db: true,
            db_service: Some("mysql".to_owned()),
            php: None,
            secured: true,
        };
        let value = serde_json::to_value(&req).unwrap();
        assert_eq!(
            value,
            serde_json::json!({
                "name": "my-blog",
                "parent_dir": "/home/demo/Sites",
                "type": "wordpress",
                "db": true,
                "db_service": "mysql",
                "secured": true
            })
        );
    }
}
