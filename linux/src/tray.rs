//! The tray icon, a StatusNotifierItem: KDE shows them, Ubuntu's GNOME through its AppIndicator
//! extension. Its icon follows the daemon's state like the Windows tray icon and the macOS menu
//! bar icon. Trays only show menus, so the menu carries what the Windows and macOS panels show at a
//! glance: the status, sites and proxies, Mail and Dumps counts and the Xdebug and dumps toggles. Desktops
//! without a tray (GNOME without the extension) keep the main window instead.
//!
//! ksni serves the item from its own thread; the menu's actions come back to the GTK main loop
//! through a channel.

use std::cell::RefCell;
use std::path::PathBuf;
use std::rc::Rc;

use gtk::{gio, glib};
use ksni::blocking::{Handle, TrayMethods};
use ksni::menu::{CheckmarkItem, MenuItem, StandardItem, SubMenu};

use crate::APP_ID;
use crate::api::{ProxyStatus, SettingsChange, SiteStatus, Status};
use crate::i18n::tr;
use crate::model::AppModel;

/// What the tray asks the app to do, on the main loop.
pub enum Command {
    /// A click on the icon, or Open Flock.
    Show,
    /// Opens a window by name, see Windows::open.
    Open(&'static str),
    /// Opens a site in the browser.
    Browse(String),
    /// Turns Xdebug or capturing dumps on or off.
    Change(SettingsChange),
    Restart,
    Quit,
    /// The desktop's tray went away (the extension was turned off) or came back.
    Available(bool),
}

/// What the icon and its menu show, set from the model.
#[derive(Clone, PartialEq)]
struct State {
    icon: &'static str,
    headline: String,
    /// The first warning, under the headline.
    warning: Option<String>,
    needs_setup: bool,
    running: bool,
    /// The sites, each with its worktrees; problems are in the main window.
    sites: Vec<SiteItem>,
    /// The proxies (`flock proxy`), after the sites.
    proxies: Vec<SiteItem>,
    mail_count: u32,
    dump_count: u32,
    xdebug: bool,
    dumps: bool,
}

impl State {
    fn of(model: &AppModel) -> Self {
        let status = model.status();
        let needs_setup = model.needs_setup();
        let icon = match &status {
            Some(s) if !needs_setup && s.is_degraded() => "warning",
            Some(_) if !needs_setup => "",
            _ => "stopped",
        };
        let status = status.filter(|_| !needs_setup);
        State {
            icon,
            headline: model.headline(),
            running: model.status().is_some(),
            warning: status.as_ref().and_then(|s| s.warnings.first().cloned()),
            needs_setup,
            sites: status.as_ref().map(SiteItem::all).unwrap_or_default(),
            proxies: status.as_ref().map(|s| s.proxies.iter().map(SiteItem::proxy).collect()).unwrap_or_default(),
            mail_count: status.as_ref().map_or(0, |s| s.mail_count),
            dump_count: status.as_ref().map_or(0, |s| s.dump_count),
            xdebug: status.as_ref().is_some_and(|s| s.xdebug),
            dumps: status.as_ref().is_some_and(|s| s.dumps),
        }
    }
}

/// A site or worktree in the Sites menu: what it says, and the URL it opens.
#[derive(Clone, PartialEq)]
struct SiteItem {
    label: String,
    url: Option<String>,
    worktrees: Vec<SiteItem>,
}

impl SiteItem {
    /// The sites that aren't worktrees, each with its worktrees.
    fn all(status: &Status) -> Vec<Self> {
        status
            .top_sites()
            .into_iter()
            .map(|site| SiteItem {
                label: site.domain.clone(),
                url: site.url.clone(),
                worktrees: status.worktrees_of(&site.name).into_iter().map(Self::worktree).collect(),
            })
            .collect()
    }

    /// "softpack.test · Node.js 22.23.4": what answers at the upstream, or "· Not running"; for a
    /// server Flock starts with a command, its state ("· Starting…", "· Failed: …").
    fn proxy(proxy: &ProxyStatus) -> Self {
        let state = match &proxy.command {
            Some(command) if command.state != "running" => command.text(),
            _ => proxy.server_text(),
        };
        SiteItem { label: format!("{} · {state}", proxy.domain), url: proxy.url.clone(), worktrees: Vec::new() }
    }

    /// "feature/auth", with its own database ("· copy") and its setup ("· Composer install…").
    fn worktree(site: &SiteStatus) -> Self {
        let mut label = site.worktree_label();
        if let Some(setup) = &site.worktree {
            if !setup.database.is_empty() {
                label.push_str(&format!(" · {}", setup.database_label()));
            }
            if setup.busy() || setup.failed() {
                label.push_str(&format!(" · {}", setup.text()));
            }
        }
        SiteItem { label, url: site.url.clone(), worktrees: Vec::new() }
    }
}

struct FlockTray {
    state: State,
    icon_theme_path: String,
    sender: async_channel::Sender<Command>,
}

impl FlockTray {
    fn send(&self, command: Command) {
        // Unbounded: never blocks the tray's thread. It only fails once the app has quit.
        let _ = self.sender.try_send(command);
    }
}

/// Menus treat underscores as access keys.
fn label(text: &str) -> String {
    text.replace('_', "__")
}

/// A menu item that sends a command.
fn item(text: &str, command: impl Fn() -> Command + Send + 'static) -> MenuItem<FlockTray> {
    StandardItem { label: label(text), activate: Box::new(move |tray: &mut FlockTray| tray.send(command())), ..Default::default() }.into()
}

/// A line of text in the menu (the status), which does nothing when clicked.
fn text(text: &str) -> MenuItem<FlockTray> {
    StandardItem { label: label(text), enabled: false, ..Default::default() }.into()
}

/// "Mail (3)", or "Mail" when there is nothing.
fn counted(name: &str, count: u32) -> String {
    if count == 0 { name.to_owned() } else { format!("{name} ({count})") }
}

/// A toggle; the daemon's answer updates the checkmark.
fn toggle(text: &str, checked: bool, change: fn(bool) -> SettingsChange) -> MenuItem<FlockTray> {
    CheckmarkItem {
        label: label(text),
        checked,
        activate: Box::new(move |tray: &mut FlockTray| tray.send(Command::Change(change(!checked)))),
        ..Default::default()
    }
    .into()
}

impl FlockTray {
    /// Sites, each opening in the browser. Menus have no rows to expand, so a site with worktrees
    /// is a submenu of the site and its worktrees: "acme.test (2)".
    fn sites(&self) -> MenuItem<Self> {
        let open = |site: &SiteItem| match site.url.clone() {
            Some(url) => item(&site.label, move || Command::Browse(url.clone())),
            None => text(&site.label),
        };
        let mut submenu: Vec<MenuItem<Self>> = self
            .state
            .sites
            .iter()
            .map(|site| -> MenuItem<Self> {
                if site.worktrees.is_empty() {
                    return open(site);
                }
                let mut items = vec![open(site), MenuItem::Separator];
                items.extend(site.worktrees.iter().map(open));
                SubMenu { label: label(&counted(&site.label, site.worktrees.len() as u32)), submenu: items, ..Default::default() }.into()
            })
            .collect();
        if !self.state.proxies.is_empty() {
            if !submenu.is_empty() {
                submenu.push(MenuItem::Separator);
            }
            submenu.extend(self.state.proxies.iter().map(open));
        }
        if submenu.is_empty() {
            submenu.push(text(tr!("menu.no_sites_yet")));
        }
        submenu.extend([MenuItem::Separator, item(tr!("menu.manage_sites"), || Command::Open("settings/sites"))]);
        SubMenu { label: tr!("settings.pane.sites").to_owned(), submenu, ..Default::default() }.into()
    }
}

impl ksni::Tray for FlockTray {
    fn id(&self) -> String {
        APP_ID.to_owned()
    }

    fn title(&self) -> String {
        "Flock".to_owned()
    }

    fn icon_theme_path(&self) -> String {
        self.icon_theme_path.clone()
    }

    fn icon_name(&self) -> String {
        match self.state.icon {
            "" => format!("{APP_ID}-symbolic"),
            state => format!("{APP_ID}-{state}-symbolic"),
        }
    }

    fn tool_tip(&self) -> ksni::ToolTip {
        ksni::ToolTip {
            title: format!("Flock — {}", self.state.headline),
            description: self.state.warning.clone().unwrap_or_default(),
            ..Default::default()
        }
    }

    fn activate(&mut self, _x: i32, _y: i32) {
        self.send(Command::Show);
    }

    /// The Windows tray's menu with the panel's essentials, and the status on top: GNOME shows no
    /// tooltips.
    fn menu(&self) -> Vec<MenuItem<Self>> {
        let mut items = vec![text(&self.state.headline)];
        if let Some(warning) = &self.state.warning {
            items.push(text(warning));
        }
        items.push(MenuItem::Separator);
        // Before the setup there is nothing to open yet: Mail, Dumps, Logs and Settings all need the daemon.
        if self.state.needs_setup {
            items.extend([item(tr!("menu.set_up_flock"), || Command::Open("setup")), MenuItem::Separator]);
        } else {
            items.extend([item(tr!("menu.open_flock"), || Command::Show), MenuItem::Separator]);
            if self.state.running {
                items.push(self.sites());
            }
            items.extend([
                item(&counted(tr!("services.category.mail"), self.state.mail_count), || Command::Open("mail")),
                item(&counted(tr!("menu.dumps"), self.state.dump_count), || Command::Open("dumps")),
                item(tr!("dumps.event_kind.logs"), || Command::Open("logs")),
                MenuItem::Separator,
            ]);
            if self.state.running {
                items.extend([
                    toggle("Xdebug", self.state.xdebug, |on| SettingsChange { xdebug: Some(on), ..Default::default() }),
                    toggle(tr!("dumps.capture_dumps"), self.state.dumps, |on| SettingsChange { dumps: Some(on), ..Default::default() }),
                    MenuItem::Separator,
                ]);
            }
            items.extend([
                item(tr!("menu.settings"), || Command::Open("settings")),
                item(if self.state.running { tr!("menu.restart_flock") } else { tr!("menu.start_flock") }, || Command::Restart),
            ]);
        }
        items.push(item(tr!("menu.quit_flock"), || Command::Quit));
        items
    }

    fn watcher_online(&self) {
        self.send(Command::Available(true));
    }

    fn watcher_offline(&self, _reason: ksni::OfflineReason) -> bool {
        self.send(Command::Available(false));
        // Keep the item, for when the tray comes back.
        true
    }
}

/// Where the icons are when the app runs from its source tree (target/debug/flock-app); installed,
/// they are in the icon theme, and this is None.
pub fn development_icons() -> Option<PathBuf> {
    let exe = std::env::current_exe().ok()?;
    let icons = exe.parent()?.parent()?.parent()?.join("data/icons");
    icons.is_dir().then_some(icons)
}

/// Shows the tray icon, if the desktop has a tray, and calls `available` with the answer, then
/// whenever the tray goes away or comes back. Commands from the menu go to `handle`.
pub fn start(model: Rc<AppModel>, available: impl Fn(bool) + 'static, handle: impl Fn(Command) + 'static) {
    let (sender, receiver) = async_channel::unbounded();
    let state = State::of(&model);
    let tray = FlockTray {
        state: state.clone(),
        icon_theme_path: development_icons().map(|p| p.display().to_string()).unwrap_or_default(),
        sender,
    };
    glib::spawn_future_local(async move {
        // Registering with the tray's D-Bus service takes a round trip or two. Without a tray the
        // item stays and shows once one appears: GNOME turns its extensions off while the screen
        // is locked, and an autostart may come before the desktop's tray.
        let spawned = gio::spawn_blocking(move || tray.assume_sni_available(true).spawn()).await.expect("tray");
        let tray = match spawned {
            Ok(tray) => tray,
            Err(e) => {
                eprintln!("tray: {e}");
                available(false);
                return;
            }
        };
        // A missing tray was reported while registering, so it is already in the channel.
        let offline = matches!(receiver.try_recv(), Ok(Command::Available(false)));
        available(!offline);
        follow(&model, tray, state);
        while let Ok(command) = receiver.recv().await {
            match command {
                Command::Available(yes) => available(yes),
                command => handle(command),
            }
        }
    });
}

/// Updates the icon and the menu whenever the state they show changes, from `shown` on.
fn follow(model: &Rc<AppModel>, tray: Handle<FlockTray>, shown: State) {
    let weak = Rc::downgrade(model);
    let shown = RefCell::new(shown);
    let update = move || {
        let Some(model) = weak.upgrade() else { return };
        let state = State::of(&model);
        // The model changes every poll; the tray only when what it shows does.
        if *shown.borrow() != state {
            shown.replace(state.clone());
            tray.update(|t| t.state = state);
        }
    };
    // The status may have come in while the tray registered.
    update();
    model.subscribe(update);
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn labels_proxies_after_the_sites() {
        let status: Status = serde_json::from_str(
            r#"{"sites":[{"name":"acme","domain":"acme.test","server":"fastcgi"}],
              "proxies":[{"name":"blog","domain":"blog.test","upstream":"http://127.0.0.1:3000","url":"http://blog.test","running":false},
                {"name":"minio","domain":"minio.test","upstream":"http://127.0.0.1:9000","running":true}]}"#,
        )
        .unwrap();
        let labels: Vec<String> = SiteItem::all(&status).into_iter().map(|s| s.label).collect();
        assert_eq!(labels, ["acme.test"]);
        let proxies: Vec<SiteItem> = status.proxies.iter().map(SiteItem::proxy).collect();
        assert_eq!(proxies[0].label, "blog.test · Not running");
        assert_eq!(proxies[0].url.as_deref(), Some("http://blog.test"));
        assert_eq!(proxies[1].label, "minio.test · Running");

        // A server Flock starts shows its state until it runs.
        let started: Status = serde_json::from_str(
            r#"{"proxies":[{"name":"admin","domain":"admin.test","upstream":"http://127.0.0.1:8000","running":false,
                "command":{"command":"python manage.py runserver 8000","dir":"/home/a/admin","state":"starting","log":"proxy-admin"}}]}"#,
        )
        .unwrap();
        assert_eq!(SiteItem::proxy(&started.proxies[0]).label, "admin.test · Starting…");
    }
}
