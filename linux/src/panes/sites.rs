//! Sites: the sites added one by one and those of the project folders, and a site's detail: its
//! address, domains and document root, its PHP and Node.js versions, what serves it, its share and
//! Laravel Boost. Version and root pickers offer the project's pin (or the default) first, then
//! what isolates the site on this machine; the pin menu writes them into the project.
//!
//! A site's git worktrees are listed under it. A worktree's detail shows its setup and database
//! instead of settings: it uses its site's.
//!
//! Proxies (`flock proxy`) follow the sites in a group of their own; a proxy's detail shows what
//! answers at its upstream, its HTTPS and its upstream. Site and proxy names never clash (the
//! daemon refuses it), so one selection covers both.

use std::cell::{Cell, RefCell};
use std::rc::Rc;

use adw::prelude::*;
use base64::Engine;
use gtk::{gdk, gio, glib};

use super::common::{PaneView, ask_link, group, info_row, report, status_dot};
use super::versions::{MenuItem, badge, button, more_menu};
use super::worktree_job;
use super::{Data, Pane};
use crate::api::{
    AppTemplate, Branches, Client, Config, CreateAppRequest, Error, ExtensionReport, PhpExtension, ProxyStatus, ShareStatus, SiteChange,
    SiteStatus, Status, WorktreeStatus,
};
use crate::links::{self, Link};
use crate::model::AppModel;
use crate::paths;
use crate::prefs;
use crate::ui;

#[derive(Clone)]
pub struct SitesPane(Rc<Inner>);

struct Inner {
    stack: gtk::Stack,
    list: PaneView,
    detail: PaneView,
    model: Rc<AppModel>,
    banner: adw::Banner,
    status: RefCell<Option<Status>>,
    config: RefCell<Option<Config>>,
    selected: RefCell<Option<String>>,
    /// The FrankenPHP site that is restarting.
    restarting: RefCell<Option<String>>,
    shown: RefCell<Option<String>>,
    /// The sites whose worktrees the list shows (prefs.rs).
    expanded: RefCell<Vec<String>>,
    /// The folder of the worktree being removed.
    removing: RefCell<Option<String>>,
    /// Creating an application from a template.
    creating_app: Cell<bool>,
}

/// A worktree to remove: its folder, name, what it is listed as and its site.
#[derive(Clone)]
struct Removal {
    path: String,
    name: String,
    label: String,
    parent: String,
}

/// The database modes of a worktree, and how the Database rows name them.
const DB_MODES: [&str; 3] = ["shared", "copy", "fresh"];
const DB_CHOICES: [&str; 3] = ["The site's", "A copy of the site's", "A new, empty one"];

impl SitesPane {
    pub fn new(model: Rc<AppModel>, banner: adw::Banner) -> Self {
        let list = PaneView::new("Sites you added one by one, plus every folder inside your project folders.");
        let detail = PaneView::new("");
        let stack = gtk::Stack::builder().transition_type(gtk::StackTransitionType::SlideLeftRight).build();
        stack.add_named(&list.scrolled, Some("list"));
        stack.add_named(&detail.scrolled, Some("detail"));
        Self(Rc::new(Inner {
            stack,
            list,
            detail,
            model,
            banner,
            status: RefCell::default(),
            config: RefCell::default(),
            selected: RefCell::default(),
            restarting: RefCell::default(),
            shown: RefCell::default(),
            expanded: RefCell::new(prefs::load().expanded_sites),
            removing: RefCell::default(),
            creating_app: Cell::default(),
        }))
    }
}

impl SitesPane {
    /// Opens a site's detail (a flock://secure link).
    pub fn select(&self, name: &str) {
        self.0.open(name);
    }
}

impl Pane for SitesPane {
    fn widget(&self) -> gtk::Widget {
        self.0.stack.clone().upcast()
    }

    fn update(&self, data: &Data) {
        self.0.status.replace(data.status.clone());
        self.0.config.replace(data.config.clone());
        self.0.render();
    }
}

impl Inner {
    /// Asks about a flock://secure link (main.rs) once the sites are loaded: nothing changes until
    /// the user confirms.
    fn ask_link(self: &Rc<Self>) {
        let Some(Link::Secure(site)) = self.model.link() else { return };
        let Some(status) = self.status.borrow().clone() else { return };
        self.model.set_link(None);
        let resolution = links::resolve_secure(&site, &status.sites);
        if let Some(name) = &resolution.action {
            self.selected.replace(Some(name.clone()));
        }
        let this = self.clone();
        ask_link(&self.stack, resolution, "Secure", move |name| {
            this.change(&name, SiteChange { secured: Some(true), ..Default::default() })
        });
    }

    fn render(self: &Rc<Self>) {
        self.ask_link();
        let key = format!(
            "{:?}{:?}{:?}{:?}{:?}{}",
            self.status.borrow(),
            self.config.borrow(),
            self.selected.borrow(),
            self.restarting.borrow(),
            self.removing.borrow(),
            self.creating_app.get()
        );
        if self.shown.borrow().as_deref() == Some(key.as_str()) {
            return;
        }
        self.shown.replace(Some(key));
        let selected = self.selected.borrow().clone();
        if let Some(site) = selected.as_deref().and_then(|name| self.site(name)) {
            self.render_detail(&site);
        } else if let Some(proxy) = selected.as_deref().and_then(|name| self.proxy(name)) {
            self.render_proxy(&proxy);
        } else {
            self.stack.set_visible_child_name("list");
            self.render_list();
        }
    }

    fn site(&self, name: &str) -> Option<SiteStatus> {
        self.status.borrow().as_ref()?.sites.iter().find(|s| s.name == name).cloned()
    }

    fn proxy(&self, name: &str) -> Option<ProxyStatus> {
        self.status.borrow().as_ref()?.proxies.iter().find(|p| p.name == name).cloned()
    }

    fn open(self: &Rc<Self>, name: &str) {
        self.selected.replace(Some(name.to_owned()));
        self.shown.replace(None);
        self.render();
        self.detail.scrolled.vadjustment().set_value(0.0);
        self.stack.set_visible_child_name("detail");
    }

    fn back(self: &Rc<Self>) {
        self.selected.replace(None);
        self.shown.replace(None);
        self.render();
    }

    /// Runs a request that answers with the status; a failure shows in the banner.
    fn perform(self: &Rc<Self>, f: impl FnOnce(Client) -> Result<Status, Error> + Send + 'static) {
        let banner = self.banner.clone();
        self.model.perform(f, move |e| report(&banner, e));
    }

    fn change(self: &Rc<Self>, name: &str, change: SiteChange) {
        let name = name.to_owned();
        self.perform(move |c| c.update_site(&name, &change));
    }

    /// Asks before a change to the project's files or a removal.
    fn confirm(
        self: &Rc<Self>,
        title: &str,
        body: &str,
        action: &str,
        destructive: bool,
        f: impl FnOnce(Client) -> Result<Status, Error> + Send + 'static,
    ) {
        let dialog = adw::AlertDialog::new(Some(title), Some(body));
        dialog.add_responses(&[("cancel", "Cancel"), ("ok", action)]);
        dialog.set_response_appearance(
            "ok",
            if destructive { adw::ResponseAppearance::Destructive } else { adw::ResponseAppearance::Suggested },
        );
        let (this, f) = (self.clone(), RefCell::new(Some(f)));
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            if let Some(f) = f.take().filter(|_| response == "ok") {
                this.perform(f);
            }
        });
    }

    // MARK: the list

    fn render_list(self: &Rc<Self>) {
        let status = self.status.borrow().clone().unwrap_or_default();
        let sites = status.top_sites();
        // No sites or proxies: only the empty state, centred in the whole pane.
        if sites.is_empty() && status.proxies.is_empty() {
            let empty =
                ui::empty("dev.flock.Flock-sites-symbolic", "No sites", "Add a single site here, or add a project folder in General.");
            let buttons = gtk::Box::builder().spacing(12).halign(gtk::Align::Center).build();
            let add = self.add_button();
            add.add_css_class("suggested-action");
            add.add_css_class("pill");
            buttons.append(&add);
            let new_app = self.new_app_button();
            new_app.add_css_class("pill");
            buttons.append(&new_app);
            let add_proxy = self.add_proxy_button();
            add_proxy.add_css_class("pill");
            buttons.append(&add_proxy);
            if self.creating_app.get() {
                buttons.append(&gtk::Spinner::builder().spinning(true).valign(gtk::Align::Center).build());
            }
            empty.set_child(Some(&buttons));
            self.list.set_groups(&[empty.upcast()]);
            return;
        }
        let mut groups: Vec<adw::PreferencesGroup> = Vec::new();
        if !sites.is_empty() {
            let group = group("", "");
            for site in sites {
                let worktrees = status.worktrees_of(&site.name);
                if worktrees.is_empty() {
                    group.add(&self.site_row(site));
                } else {
                    group.add(&self.expander(site, &worktrees));
                }
            }
            groups.push(group);
        }
        if !status.proxies.is_empty() {
            let group = group("Proxies", "");
            for proxy in &status.proxies {
                group.add(&self.proxy_row(proxy));
            }
            groups.push(group);
        }
        // New App…, Add Proxy… and Add Site… on the first group.
        if let Some(first) = groups.first() {
            let buttons = gtk::Box::builder().spacing(6).valign(gtk::Align::Center).build();
            if self.creating_app.get() {
                buttons.append(&gtk::Spinner::builder().spinning(true).valign(gtk::Align::Center).build());
            }
            let new_app = self.new_app_button();
            new_app.add_css_class("flat");
            buttons.append(&new_app);
            let add_proxy = self.add_proxy_button();
            add_proxy.add_css_class("flat");
            buttons.append(&add_proxy);
            let add = self.add_button();
            add.add_css_class("flat");
            buttons.append(&add);
            first.set_header_suffix(Some(&buttons));
        }
        self.list.set_groups(&groups.into_iter().map(|g| g.upcast()).collect::<Vec<gtk::Widget>>());
    }

    /// Add Proxy…: asks for a name and the upstream, then adds the proxy and opens it.
    fn add_proxy_button(self: &Rc<Self>) -> gtk::Button {
        let tld = self.status.borrow().as_ref().map(|s| s.dns.tld.clone()).filter(|t| !t.is_empty()).unwrap_or_else(|| "test".into());
        let add = gtk::Button::builder()
            .label("Add Proxy…")
            .tooltip_text(format!("Serve a server of its own, e.g. a Node.js dev server on a port, at a .{tld} domain"))
            .build();
        let this = self.clone();
        add.connect_clicked(move |_| this.add_proxy(&tld));
        add
    }

    fn add_proxy(self: &Rc<Self>, tld: &str) {
        let name = adw::EntryRow::builder().title("Name, e.g. my-app").build();
        let upstream = adw::EntryRow::builder().title("Port, host:port or URL, e.g. 3000").activates_default(true).build();
        let list = gtk::ListBox::builder().css_classes(["boxed-list"]).selection_mode(gtk::SelectionMode::None).build();
        list.append(&name);
        list.append(&upstream);
        let dialog = adw::AlertDialog::new(
            Some("Add a proxy"),
            Some(&format!(
                "Flock serves <name>.{tld} from a server of its own, e.g. a Next.js, Nuxt or Vite dev server. A port means 127.0.0.1."
            )),
        );
        dialog.set_extra_child(Some(&list));
        dialog.add_responses(&[("cancel", "Cancel"), ("add", "Add")]);
        dialog.set_response_appearance("add", adw::ResponseAppearance::Suggested);
        dialog.set_default_response(Some("add"));
        let this = self.clone();
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            let (name, upstream) = (name.text().trim().to_owned(), upstream.text().trim().to_owned());
            if response != "add" || name.is_empty() || upstream.is_empty() {
                return;
            }
            // Its detail shows once the daemon added it; the new status renders it.
            let (pane, banner, n) = (this.clone(), this.banner.clone(), name.clone());
            this.model.perform(
                move |c| c.proxy(&name, &upstream, None),
                move |e| {
                    if e.is_none() {
                        pane.selected.replace(Some(n.clone()));
                        pane.shown.replace(None);
                        pane.detail.scrolled.vadjustment().set_value(0.0);
                        pane.stack.set_visible_child_name("detail");
                    }
                    report(&banner, e);
                },
            );
        });
    }

    /// A proxy's row: its name and where it passes requests, a warning when it isn't served,
    /// whether something answers there and a lock when it is secured.
    fn proxy_row(self: &Rc<Self>, proxy: &ProxyStatus) -> adw::ActionRow {
        let row = adw::ActionRow::builder()
            .title(glib::markup_escape_text(&proxy.name))
            .subtitle(glib::markup_escape_text(&format!("{} → {}", proxy.domain, proxy.upstream_label())))
            .activatable(true)
            .build();
        row.set_tooltip_text(Some(&proxy.upstream));
        for widget in proxy_suffixes(proxy) {
            row.add_suffix(&widget);
        }
        row.add_suffix(&gtk::Image::from_icon_name("go-next-symbolic"));
        let (this, name) = (self.clone(), proxy.name.clone());
        row.connect_activated(move |_| this.open(&name));
        row
    }

    /// Add Site…: picks a folder and links it.
    fn add_button(self: &Rc<Self>) -> gtk::Button {
        let add = gtk::Button::builder().label("Add Site…").build();
        let this = self.clone();
        add.connect_clicked(move |button| {
            let window = button.root().and_downcast::<gtk::Window>();
            let this = this.clone();
            gtk::FileDialog::builder().title("Add Site").accept_label("Add").build().select_folder(
                window.as_ref(),
                gio::Cancellable::NONE,
                move |result| {
                    if let Some(path) = result.ok().and_then(|f| f.path()) {
                        let path = path.to_string_lossy().into_owned();
                        this.perform(move |c| c.link_site(&path));
                    }
                },
            );
        });
        add
    }

    /// New App…: asks for a name, template, folder and database/HTTPS options, then creates the app.
    fn new_app_button(self: &Rc<Self>) -> gtk::Button {
        let button = gtk::Button::builder().label("New App…").build();
        button.set_sensitive(!self.creating_app.get());
        let this = self.clone();
        button.connect_clicked(move |_| this.new_app());
        button
    }

    fn new_app(self: &Rc<Self>) {
        if self.creating_app.get() {
            return;
        }
        let this = self.clone();
        glib::spawn_future_local(async move {
            let templates = match gio::spawn_blocking(|| Client.app_templates()).await.expect("templates request") {
                Ok(list) if !list.is_empty() => list,
                Err(Error::Server { status: 404, .. }) | Ok(_) => default_app_templates(),
                Err(e) => {
                    report(&this.banner, Some(e.to_string()));
                    return;
                }
            };
            this.show_new_app_dialog(templates);
        });
    }

    fn show_new_app_dialog(self: &Rc<Self>, templates: Vec<AppTemplate>) {
        let status = self.status.borrow().clone();
        let tld = status.as_ref().map(|s| s.dns.tld.clone()).filter(|t| !t.is_empty()).unwrap_or_else(|| "test".into());
        let has_running_db = status.as_ref().is_some_and(|s| s.extras.iter().any(|e| (e.id == "mysql" || e.id == "mariadb") && e.up));
        let default_folder = self
            .config
            .borrow()
            .as_ref()
            .and_then(|c| c.parked.as_deref())
            .and_then(|p| p.first())
            .cloned()
            .unwrap_or_else(|| paths::home().to_string_lossy().into_owned());

        let templates = Rc::new(templates);
        let labels: Vec<&str> = templates.iter().map(|t| t.name.as_str()).collect();
        let default_idx = templates.iter().position(|t| t.name.eq_ignore_ascii_case("php")).unwrap_or(0) as u32;

        let name = adw::EntryRow::builder().title("Name, e.g. my-app").activates_default(true).build();
        let template_row = adw::ComboRow::builder().title("Template").model(&gtk::StringList::new(&labels)).selected(default_idx).build();
        let folder = adw::EntryRow::builder().title("Folder").activates_default(true).build();
        folder.set_text(&default_folder);
        let browse = gtk::Button::builder()
            .icon_name("folder-open-symbolic")
            .tooltip_text("Browse…")
            .valign(gtk::Align::Center)
            .css_classes(["flat"])
            .build();
        folder.add_suffix(&browse);

        let db_row = adw::SwitchRow::builder().title("Create MySQL or MariaDB database").build();
        let db_service_row = adw::ComboRow::builder()
            .title("Database service")
            .model(&gtk::StringList::new(&["Auto (MySQL or MariaDB)", "MySQL", "MariaDB"]))
            .visible(false)
            .build();
        let secured_row = adw::SwitchRow::builder().title("Serve over HTTPS").build();

        let list = gtk::ListBox::builder().css_classes(["boxed-list"]).selection_mode(gtk::SelectionMode::None).build();
        list.append(&name);
        list.append(&template_row);
        list.append(&folder);
        list.append(&db_row);
        list.append(&db_service_row);
        list.append(&secured_row);

        let dialog = adw::AlertDialog::new(Some("New application"), None);
        dialog.set_extra_child(Some(&list));
        dialog.add_responses(&[("cancel", "Cancel"), ("create", "Create")]);
        dialog.set_response_appearance("create", adw::ResponseAppearance::Suggested);
        dialog.set_default_response(Some("create"));
        dialog.set_response_enabled("create", false);

        let sync: Rc<dyn Fn()> = Rc::new({
            let (weak, templates) = (dialog.downgrade(), templates.clone());
            let (name, folder, template_row) = (name.clone(), folder.clone(), template_row.clone());
            let (db_row, db_service_row, secured_row) = (db_row.clone(), db_service_row.clone(), secured_row.clone());
            let tld = tld.clone();
            move || {
                let Some(dialog) = weak.upgrade() else { return };
                let tmpl = selected_app_template(&templates, template_row.selected());
                if tmpl.requires_db && !db_row.is_active() {
                    db_row.set_active(true);
                }
                db_row.set_sensitive(!tmpl.requires_db);

                let wants_db = db_row.is_active();
                db_service_row.set_visible(wants_db);
                db_row.set_subtitle(if !wants_db || has_running_db {
                    ""
                } else if tmpl.requires_db {
                    "This template requires MySQL or MariaDB to be running (start one in Services)."
                } else {
                    "No running MySQL or MariaDB service detected (start one in Services)."
                });

                let raw_name = name.text();
                let raw_name = raw_name.trim();
                let raw_folder = folder.text();
                let raw_folder = raw_folder.trim();
                let valid = !raw_name.is_empty() && !raw_folder.is_empty();
                dialog.set_response_enabled("create", valid);

                let scheme = if secured_row.is_active() { "https" } else { "http" };
                let preview = if valid {
                    format!("{} → {scheme}://{}.{tld}", std::path::Path::new(raw_folder).join(raw_name).display(), app_slug(raw_name))
                } else {
                    format!("Choose a name and folder to serve at {scheme}://<name>.{tld}.")
                };
                dialog.set_body(&preview);
            }
        });

        for entry in [&name, &folder] {
            let s = sync.clone();
            entry.connect_changed(move |_| s());
        }
        {
            let s = sync.clone();
            template_row.connect_selected_notify(move |_| s());
        }
        for switch in [&db_row, &secured_row] {
            let s = sync.clone();
            switch.connect_active_notify(move |_| s());
        }
        {
            let (stack, folder_weak) = (self.stack.clone(), folder.downgrade());
            browse.connect_clicked(move |_| {
                let window = stack.root().and_downcast::<gtk::Window>();
                let mut builder = gtk::FileDialog::builder().title("Select Parent Folder").accept_label("Select");
                if let Some(current) = folder_weak.upgrade() {
                    let current = current.text();
                    let current = current.trim();
                    if !current.is_empty() && std::path::Path::new(current).is_dir() {
                        builder = builder.initial_folder(&gio::File::for_path(current));
                    }
                }
                let folder_weak = folder_weak.clone();
                builder.build().select_folder(window.as_ref(), gio::Cancellable::NONE, move |result| {
                    if let (Some(folder_entry), Some(path)) = (folder_weak.upgrade(), result.ok().and_then(|f| f.path())) {
                        folder_entry.set_text(&path.to_string_lossy());
                    }
                });
            });
        }

        sync();

        let this = self.clone();
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            let app_name = name.text().trim().to_owned();
            let parent_dir = folder.text().trim().to_owned();
            if response != "create" || app_name.is_empty() || parent_dir.is_empty() {
                return;
            }
            let tmpl = selected_app_template(&templates, template_row.selected());
            let create_db = db_row.is_active();
            let db_service = if create_db {
                match db_service_row.selected() {
                    1 => Some("mysql".to_owned()),
                    2 => Some("mariadb".to_owned()),
                    _ => None,
                }
            } else {
                None
            };
            this.create_app(CreateAppRequest {
                name: app_name,
                parent_dir,
                app_type: tmpl.name,
                db: create_db,
                db_service,
                php: None,
                secured: secured_row.is_active(),
            });
        });
    }

    fn create_app(self: &Rc<Self>, req: CreateAppRequest) {
        self.creating_app.set(true);
        self.render();
        let target_path = std::path::Path::new(&req.parent_dir).join(&req.name).to_string_lossy().into_owned();
        let fallback_name = app_slug(&req.name);
        let (this, banner) = (self.clone(), self.banner.clone());
        self.model.perform(
            move |c| c.create_app(&req),
            move |err| {
                this.creating_app.set(false);
                if err.is_none() {
                    let created_name = this
                        .model
                        .status()
                        .and_then(|s| s.sites.into_iter().find(|site| site.path == target_path).map(|site| site.name))
                        .unwrap_or_else(|| fallback_name.clone());
                    this.open(&created_name);
                } else {
                    this.render();
                }
                report(&banner, err);
            },
        );
    }

    /// A site's row, or a worktree's (by its branch), which opens its detail.
    fn site_row(self: &Rc<Self>, site: &SiteStatus) -> adw::ActionRow {
        let title = if site.is_worktree() { site.worktree_label() } else { site.name.clone() };
        let row = adw::ActionRow::builder()
            .title(glib::markup_escape_text(&title))
            .subtitle(glib::markup_escape_text(&site.domain))
            .activatable(true)
            .build();
        row.set_tooltip_text(Some(&site.path));
        for widget in worktree_suffixes(site).into_iter().chain(site_suffixes(site)) {
            row.add_suffix(&widget);
        }
        row.add_suffix(&gtk::Image::from_icon_name("go-next-symbolic"));
        let (this, name) = (self.clone(), site.name.clone());
        row.connect_activated(move |_| this.open(&name));
        row
    }

    /// A site with worktrees: its row expands to them (collapsed until the user expands it, which
    /// is remembered), and its arrow opens the site.
    fn expander(self: &Rc<Self>, site: &SiteStatus, worktrees: &[&SiteStatus]) -> adw::ExpanderRow {
        let row = adw::ExpanderRow::builder()
            .title(glib::markup_escape_text(&site.name))
            .subtitle(glib::markup_escape_text(&site.domain))
            .expanded(self.expanded.borrow().contains(&site.name))
            .build();
        row.set_tooltip_text(Some(&site.path));
        let count = badge(&worktrees.len().to_string(), "");
        let worktrees_text = if worktrees.len() == 1 { "1 worktree".to_owned() } else { format!("{} worktrees", worktrees.len()) };
        count.set_tooltip_text(Some(&worktrees_text));
        row.add_suffix(&count);
        for widget in site_suffixes(site) {
            row.add_suffix(&widget);
        }
        let open = gtk::Button::builder()
            .icon_name("go-next-symbolic")
            .tooltip_text(format!("Open {}", site.name))
            .valign(gtk::Align::Center)
            .css_classes(["flat"])
            .build();
        let (this, name) = (self.clone(), site.name.clone());
        open.connect_clicked(move |_| this.open(&name));
        row.add_suffix(&open);
        for worktree in worktrees {
            row.add_row(&self.site_row(worktree));
        }
        let (this, name) = (self.clone(), site.name.clone());
        row.connect_expanded_notify(move |row| {
            let expanded = row.is_expanded();
            {
                let mut list = this.expanded.borrow_mut();
                list.retain(|s| *s != name);
                if expanded {
                    list.push(name.clone());
                }
            }
            prefs::set_expanded(&name, expanded);
        });
        row
    }

    // MARK: a site's detail

    fn render_detail(self: &Rc<Self>, site: &SiteStatus) {
        if site.is_worktree() {
            self.render_worktree(site);
            return;
        }
        let config = self.config.borrow().clone().unwrap_or_default();
        let status = self.status.borrow().clone().unwrap_or_default();
        let mut groups: Vec<gtk::Widget> = vec![self.header(site).upcast()];
        groups.push(self.site_group(site, &status).upcast());
        groups.push(self.share_group(site).upcast());
        groups.push(self.versions_group(site, &config).upcast());
        groups.push(self.server_group(site).upcast());
        if site.boost == "missing" || site.boost == "outdated" {
            groups.push(self.boost_group(site).upcast());
        }
        groups.push(self.worktrees_group(site, &status).upcast());
        self.detail.set_groups(&groups);
    }

    fn header(self: &Rc<Self>, site: &SiteStatus) -> adw::PreferencesGroup {
        let header = group("", "");
        let back = adw::ActionRow::builder().title("Sites").activatable(true).build();
        back.add_prefix(&gtk::Image::from_icon_name("go-previous-symbolic"));
        let this = self.clone();
        back.connect_activated(move |_| this.back());
        header.add(&back);

        let title = adw::ActionRow::builder()
            .title(glib::markup_escape_text(&site.name))
            .subtitle(glib::markup_escape_text(&format!("{}\n{}", site.url.as_deref().unwrap_or(&site.domain), site.path)))
            .subtitle_selectable(true)
            .build();
        if site.parked {
            title.add_suffix(&badge("Project folder", ""));
        }
        let actions = gtk::Box::builder().spacing(4).valign(gtk::Align::Center).build();
        let icon_button =
            |icon: &str, tooltip: &str| gtk::Button::builder().icon_name(icon).tooltip_text(tooltip).css_classes(["flat"]).build();
        let open = icon_button("dev.flock.Flock-sites-symbolic", "Open in the browser");
        open.set_sensitive(site.url.is_some());
        let url = site.url.clone().unwrap_or_default();
        open.connect_clicked(move |b| launch_uri(b, &url));
        actions.append(&open);
        let folder = icon_button("folder-symbolic", "Open the project folder");
        let path = site.path.clone();
        folder.connect_clicked(move |b| launch_file(b, &path));
        actions.append(&folder);
        let extensions = icon_button("application-x-addon-symbolic", "PHP extensions");
        let (this, s) = (self.clone(), site.clone());
        extensions.connect_clicked(move |_| this.show_extensions(&s));
        actions.append(&extensions);
        actions.append(&self.pin_menu(site));
        if !site.parked {
            let remove = icon_button("user-trash-symbolic", "Remove site");
            let (this, s) = (self.clone(), site.clone());
            remove.connect_clicked(move |_| {
                let name = s.name.clone();
                this.confirm(
                    &format!("Remove {}?", s.name),
                    &format!("Flock stops serving {}. The project in {} is not touched.", s.domain, s.path),
                    "Remove",
                    true,
                    move |c| c.unlink_site(&name),
                );
            });
            actions.append(&remove);
        }
        title.add_suffix(&actions);
        header.add(&title);
        if let Some(problem) = &site.problem {
            header.add(&adw::ActionRow::builder().title(glib::markup_escape_text(problem)).css_classes(["warning"]).build());
        }
        header
    }

    fn site_group(self: &Rc<Self>, site: &SiteStatus, status: &Status) -> adw::PreferencesGroup {
        let group = group(
            "Site",
            "The document root is the folder Flock serves the site from. One picked here applies on this machine only; pin it to write it into .flockrc for everyone.",
        );
        let https = adw::SwitchRow::builder().title("HTTPS").active(site.secured).build();
        let (this, name) = (self.clone(), site.name.clone());
        https.connect_active_notify(move |row| this.change(&name, SiteChange { secured: Some(row.is_active()), ..Default::default() }));
        group.add(&https);

        for domain in site.domains.iter().flatten() {
            let title = if domain.alias { format!("{} (alias)", domain.host) } else { domain.host.clone() };
            let row = adw::ActionRow::builder().title(glib::markup_escape_text(&title)).build();
            row.add_prefix(&gtk::Image::from_icon_name("network-workgroup-symbolic"));
            if let Some(problem) = &domain.problem {
                row.set_subtitle(&glib::markup_escape_text(problem));
                row.add_css_class("warning");
            } else if let Some(url) = domain.url.clone() {
                row.set_activatable(true);
                row.connect_activated(move |r| launch_uri(r, &url));
            }
            let remove = gtk::Button::builder()
                .icon_name("list-remove-symbolic")
                .tooltip_text(format!("Remove {}", domain.host))
                .valign(gtk::Align::Center)
                .css_classes(["flat"])
                .build();
            let (this, s, entry, host) = (self.clone(), site.name.clone(), domain.entry.clone(), domain.host.clone());
            remove.connect_clicked(move |_| {
                let (s2, e2) = (s.clone(), entry.clone());
                this.confirm(&format!("Remove {host}?"), &format!("{host} is no longer served by {s}."), "Remove", true, move |c| {
                    c.remove_domain(&s2, &e2)
                });
            });
            row.add_suffix(&remove);
            group.add(&row);
        }
        let add =
            adw::ActionRow::builder().title("Add Domain…").subtitle("Extra domains: subdomains and aliases").activatable(true).build();
        add.add_prefix(&gtk::Image::from_icon_name("list-add-symbolic"));
        let (this, s, dns) = (self.clone(), site.name.clone(), status.dns.clone());
        add.connect_activated(move |_| this.add_domain(&s, &dns));
        group.add(&add);
        if !status.dns.wildcard && !status.dns.wildcard_problem.is_empty() {
            group.add(
                &adw::ActionRow::builder()
                    .title(glib::markup_escape_text(&format!(
                        "Subdomains only work when you add them here: {}.",
                        status.dns.wildcard_problem
                    )))
                    .css_classes(["warning"])
                    .build(),
            );
        }

        group.add(&self.root_row(site));
        if let Some(hint) = root_hint(site) {
            let row = adw::ActionRow::builder()
                .title(glib::markup_escape_text(&hint))
                .css_classes([if site.root_state.as_deref() == Some("empty") { "dim-label" } else { "warning" }])
                .build();
            if site.root_state.as_deref().unwrap_or_default().is_empty() {
                let (this, name) = (self.clone(), site.name.clone());
                row.add_suffix(&button("Use .flockrc", false, move || {
                    this.change(&name, SiteChange { root: Some(String::new()), ..Default::default() })
                }));
            }
            group.add(&row);
        }
        group
    }

    fn add_domain(self: &Rc<Self>, site: &str, dns: &crate::api::DnsStatus) {
        let tld = if dns.tld.is_empty() { "test" } else { &dns.tld };
        let help = if dns.wildcard || dns.tld.is_empty() {
            format!(
                "Every subdomain up to {} levels below the site already works. Add a full name like {site}-2.{tld} for an alias, or a subdomain (tenant1, a.b) so it is listed here and also resolves on Windows machines without wildcard DNS.",
                dns.max_depth.max(1)
            )
        } else {
            format!(
                "Wildcard subdomains don't work here ({}), so add each subdomain you use (tenant1, a.b). A full name adds an alias.",
                dns.wildcard_problem
            )
        };
        let entry = gtk::Entry::builder().placeholder_text(format!("tenant1, a.b or {site}-2.{tld}")).activates_default(true).build();
        let dialog = adw::AlertDialog::new(Some(&format!("Add a domain to {site}")), Some(&help));
        dialog.set_extra_child(Some(&entry));
        dialog.add_responses(&[("cancel", "Cancel"), ("add", "Add")]);
        dialog.set_response_appearance("add", adw::ResponseAppearance::Suggested);
        dialog.set_default_response(Some("add"));
        let (this, site) = (self.clone(), site.to_owned());
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            let domain = entry.text().trim().to_owned();
            if response == "add" && !domain.is_empty() {
                let site = site.clone();
                this.perform(move |c| c.add_domain(&site, &domain));
            }
        });
    }

    /// The document root: the project's or the detected one first (following the project), the
    /// folders Flock found and the one set here (this machine only), then any folder.
    fn root_row(self: &Rc<Self>, site: &SiteStatus) -> adw::ComboRow {
        let follow = match (site.root_pinned_here(), site.root_source.as_str(), site.root_state.as_deref()) {
            (true, _, _) => format!("Use .flockrc ({})", root_label(&site.root_pinned)),
            (_, "default", Some("not_found")) => "Automatic (none found)".to_owned(),
            (_, "default", _) => format!("Automatic ({})", root_label(&site.root)),
            _ => "Automatic".to_owned(),
        };
        let mut folders = site.root_suggestions.clone().unwrap_or_default();
        if site.root_source == "isolated" && !folders.contains(&site.root) {
            folders.push(site.root.clone());
        }
        let mut labels = vec![follow];
        labels.extend(folders.iter().map(|f| root_label(f)));
        labels.push("Choose Folder…".to_owned());
        let model = gtk::StringList::new(&labels.iter().map(String::as_str).collect::<Vec<_>>());
        let row = adw::ComboRow::builder().title("Document root").model(&model).build();
        let selected = if site.root_source == "isolated" { folders.iter().position(|f| *f == site.root).map_or(0, |i| i + 1) } else { 0 };
        row.set_selected(selected as u32);
        let (this, s) = (self.clone(), site.clone());
        row.connect_selected_notify(move |row| {
            let index = row.selected() as usize;
            if index == folders.len() + 1 {
                this.choose_root(&s);
            } else {
                let root = if index == 0 { String::new() } else { folders[index - 1].clone() };
                this.change(&s.name, SiteChange { root: Some(root), ..Default::default() });
            }
        });
        row
    }

    fn choose_root(self: &Rc<Self>, site: &SiteStatus) {
        let window = self.stack.root().and_downcast::<gtk::Window>();
        let dialog = gtk::FileDialog::builder()
            .title(format!("Choose the folder with the app's index.php in {}", site.name))
            .accept_label("Choose")
            .initial_folder(&gio::File::for_path(&site.path))
            .build();
        let (this, s) = (self.clone(), site.clone());
        dialog.select_folder(window.as_ref(), gio::Cancellable::NONE, move |result| {
            let Some(folder) = result.ok().and_then(|f| f.path()) else {
                this.shown.replace(None);
                this.render();
                return;
            };
            let project = std::fs::canonicalize(&s.path).unwrap_or_else(|_| s.path.clone().into());
            let chosen = std::fs::canonicalize(&folder).unwrap_or(folder.clone());
            match chosen.strip_prefix(&project) {
                Ok(rest) => {
                    let root = if rest.as_os_str().is_empty() { ".".to_owned() } else { rest.to_string_lossy().into_owned() };
                    this.change(&s.name, SiteChange { root: Some(root), ..Default::default() });
                }
                Err(_) => report(
                    &this.banner,
                    Some(format!("{} isn't in {}. The document root must be a folder in the project.", folder.display(), s.path)),
                ),
            }
        });
    }

    fn share_group(self: &Rc<Self>, site: &SiteStatus) -> adw::PreferencesGroup {
        let group = group("Share", "");
        let name = site.name.clone();
        let stop = {
            let (this, name) = (self.clone(), name.clone());
            move || {
                let (this2, name) = (this.clone(), name.clone());
                let banner = this.banner.clone();
                glib::spawn_future_local(async move {
                    let result = gio::spawn_blocking(move || Client.stop_share(&name)).await.expect("share request");
                    report(&banner, result.err().map(|e| e.to_string()));
                    this2.model.refresh();
                });
            }
        };
        match &site.share {
            None => {
                group.set_description(Some("Show the site at a temporary public address to preview work in progress, never for production. Visitors don't get debug pages, dumps or the dev tools."));
                let row = adw::ActionRow::new();
                let (this, n) = (self.clone(), name.clone());
                let share = button("Share…", false, move || this.ask_share(&n));
                share.set_sensitive(site.url.is_some());
                row.add_suffix(&share);
                group.add(&row);
            }
            Some(share) if share.state == "running" => self.running_share(&group, &name, share, stop),
            Some(share) if share.state == "failed" => {
                let row = adw::ActionRow::builder()
                    .title(glib::markup_escape_text(if share.error.is_empty() { "Sharing failed." } else { &share.error }))
                    .css_classes(["warning"])
                    .build();
                let (this, n, allow, debug) = (self.clone(), name.clone(), share.allow.clone().unwrap_or_default(), share.debug);
                row.add_suffix(&button("Try Again", false, move || this.start_share(&n, allow.clone(), debug)));
                row.add_suffix(&button("Dismiss", false, stop));
                group.add(&row);
            }
            Some(share) => {
                let row = adw::ActionRow::builder()
                    .title(if share.state == "building" { "Building assets…" } else { "Starting the tunnel…" })
                    .build();
                row.add_prefix(&gtk::Spinner::builder().spinning(true).build());
                row.add_suffix(&button("Stop", false, stop));
                group.add(&row);
            }
        }
        group
    }

    fn running_share(self: &Rc<Self>, group: &adw::PreferencesGroup, name: &str, share: &ShareStatus, stop: impl Fn() + 'static) {
        let row = adw::ActionRow::builder().title(glib::markup_escape_text(&share.url)).title_selectable(true).build();
        let url = share.url.clone();
        row.add_suffix(&button("Copy", false, {
            let url = url.clone();
            let row = row.clone();
            move || row.clipboard().set_text(&url)
        }));
        let open = gtk::Button::builder()
            .icon_name("dev.flock.Flock-sites-symbolic")
            .tooltip_text("Open")
            .valign(gtk::Align::Center)
            .css_classes(["flat"])
            .build();
        open.connect_clicked(move |b| launch_uri(b, &url));
        row.add_suffix(&open);
        group.add(&row);

        if let Some(texture) = base64::engine::general_purpose::STANDARD
            .decode(&share.qr)
            .ok()
            .and_then(|png| gdk::Texture::from_bytes(&glib::Bytes::from_owned(png)).ok())
        {
            let picture = gtk::Picture::builder()
                .paintable(&texture)
                .width_request(160)
                .height_request(160)
                .can_shrink(true)
                .halign(gtk::Align::Start)
                .build();
            picture.set_tooltip_text(Some(&format!("QR code of {}", share.url)));
            let row = adw::PreferencesRow::builder().activatable(false).child(&picture).build();
            picture.set_margin_top(12);
            picture.set_margin_bottom(12);
            picture.set_margin_start(12);
            group.add(&row);
        }
        let mut notes = Vec::new();
        if let Some(expires) =
            share.expires_at.as_deref().and_then(|t| glib::DateTime::from_iso8601(t, None).ok()).and_then(|t| t.to_local().ok())
        {
            notes.push(format!("Ends at {}", expires.format("%H:%M").map(|s| s.to_string()).unwrap_or_default()));
        }
        let allow = share.allow.clone().unwrap_or_default();
        if !allow.is_empty() {
            notes.push(format!("Only for: {}", allow.join(", ")));
        }
        if share.debug {
            notes.push("Debug mode is on: visitors see stack traces".to_owned());
        }
        if !notes.is_empty() {
            group.add(&adw::ActionRow::builder().title(glib::markup_escape_text(&notes.join("\n"))).css_classes(["dim-label"]).build());
        }
        for warning in [&share.warning, &share.rebuild_error].into_iter().filter(|w| !w.is_empty()) {
            group.add(&adw::ActionRow::builder().title(glib::markup_escape_text(warning)).css_classes(["warning"]).build());
        }
        let actions = adw::ActionRow::new();
        if share.can_rebuild {
            let (this, n) = (self.clone(), name.to_owned());
            let rebuild = button(if share.rebuilding { "Rebuilding…" } else { "Rebuild Assets" }, false, move || {
                let (this, n) = (this.clone(), n.clone());
                glib::spawn_future_local(async move {
                    let result = gio::spawn_blocking(move || Client.rebuild_share(&n)).await.expect("share request");
                    report(&this.banner, result.err().map(|e| e.to_string()));
                    this.model.refresh();
                });
            });
            rebuild.set_sensitive(!share.rebuilding);
            rebuild.set_tooltip_text(Some("Runs npm run build again, so visitors see your latest changes when they reload"));
            actions.add_suffix(&rebuild);
        }
        actions.add_suffix(&button("Stop Sharing", false, stop));
        group.add(&actions);
    }

    /// Asks who may open the share (anyone with the link, or only the emails listed) and whether
    /// visitors see the app's debug mode.
    fn ask_share(self: &Rc<Self>, name: &str) {
        let allow = adw::EntryRow::builder().title("Only these people (optional)").build();
        allow.set_tooltip_text(Some("colleague@acme.com, *@client.com"));
        let debug = adw::SwitchRow::builder()
            .title("Show error details to visitors")
            .subtitle("Turns the app's debug mode on for this share, so visitors see stack traces. Only for people you trust.")
            .build();
        let list = gtk::ListBox::builder().css_classes(["boxed-list"]).selection_mode(gtk::SelectionMode::None).build();
        list.append(&allow);
        list.append(&debug);
        let dialog = adw::AlertDialog::new(
            Some(&format!("Share {name}")),
            Some(
                "Anyone with the link can open the site unless you list email addresses — Cloudflare then emails them a code. The link stops working after 8 hours or when you stop sharing.",
            ),
        );
        dialog.set_extra_child(Some(&list));
        dialog.add_responses(&[("cancel", "Cancel"), ("share", "Share")]);
        dialog.set_response_appearance("share", adw::ResponseAppearance::Suggested);
        let (this, name) = (self.clone(), name.to_owned());
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            if response == "share" {
                let emails = allow.text().split([',', ' ', '\n']).map(str::trim).filter(|e| !e.is_empty()).map(str::to_owned).collect();
                this.start_share(&name, emails, debug.is_active());
            }
        });
    }

    fn start_share(self: &Rc<Self>, name: &str, allow: Vec<String>, debug: bool) {
        let (this, name) = (self.clone(), name.to_owned());
        glib::spawn_future_local(async move {
            let result = gio::spawn_blocking(move || Client.start_share(&name, &allow, debug)).await.expect("share request");
            report(&this.banner, result.err().map(|e| e.to_string()));
            this.model.refresh();
        });
    }

    fn versions_group(self: &Rc<Self>, site: &SiteStatus, config: &Config) -> adw::PreferencesGroup {
        let group = group(
            "Versions",
            "A version picked here applies on this machine only. To set it for everyone working on the project, pin it: PHP goes into .flockrc, Node.js into .nvmrc.",
        );
        let php: Vec<String> = config.php.iter().flatten().map(|p| p.version.clone()).collect();
        let mut node: Vec<String> = config.node.iter().flatten().map(|v| major(v)).collect();
        node.dedup();
        let (this, name) = (self.clone(), site.name.clone());
        group.add(&version_row(
            "PHP",
            &site.php,
            &site.php_source,
            &site.php_pinned,
            &site.php_file,
            &config.default_php,
            &php,
            move |v| this.change(&name, SiteChange { php: Some(v), ..Default::default() }),
        ));
        let (this, name) = (self.clone(), site.name.clone());
        group.add(&version_row(
            "Node",
            &major(&site.node),
            &site.node_source,
            &major(&site.node_pinned),
            &site.node_file,
            &config.default_node,
            &node,
            move |v| this.change(&name, SiteChange { node: Some(v), ..Default::default() }),
        ));
        if site.php_source == "isolated" && !site.php_pinned.is_empty() && site.php_pinned != site.php {
            let file = file_name(&site.php_file);
            let row = adw::ActionRow::builder()
                .title(glib::markup_escape_text(&format!(
                    "PHP {} is isolated here; the project pins {} in {file}.",
                    site.php, site.php_pinned
                )))
                .css_classes(["warning"])
                .build();
            let (this, name) = (self.clone(), site.name.clone());
            row.add_suffix(&button(&format!("Use {file}"), false, move || {
                this.change(&name, SiteChange { php: Some(String::new()), ..Default::default() })
            }));
            group.add(&row);
        }
        if site.node_source == "isolated" && !site.node_pinned.is_empty() && major(&site.node_pinned) != major(&site.node) {
            let file = file_name(&site.node_file);
            let row = adw::ActionRow::builder()
                .title(glib::markup_escape_text(&format!(
                    "Node {} is isolated here; the project pins {} in {file}.",
                    site.node, site.node_pinned
                )))
                .css_classes(["warning"])
                .build();
            let (this, name) = (self.clone(), site.name.clone());
            row.add_suffix(&button(&format!("Use {file}"), false, move || {
                this.change(&name, SiteChange { node: Some(String::new()), ..Default::default() })
            }));
            group.add(&row);
        }
        if !site.node_shadowed.is_empty() {
            group.add(
                &adw::ActionRow::builder()
                    .title(glib::markup_escape_text(&format!(".flockrc's Node {} wins over {}.", site.node_pinned, site.node_shadowed)))
                    .css_classes(["warning"])
                    .build(),
            );
        }
        group
    }

    fn server_group(self: &Rc<Self>, site: &SiteStatus) -> adw::PreferencesGroup {
        let footer = if site.server == "frankenphp" && !site.octane_available {
            "FrankenPHP needs PHP 8.2 or newer. Requests that ask for Xdebug still go to PHP-FPM. For worker mode, install Laravel Octane in the project (composer require laravel/octane)."
        } else {
            "FrankenPHP needs PHP 8.2 or newer. Requests that ask for Xdebug still go to PHP-FPM."
        };
        let group = group("Server", footer);
        let franken = site.server == "frankenphp";
        let runs_on = adw::ComboRow::builder()
            .title("Runs on")
            .model(&gtk::StringList::new(&["PHP-FPM", "FrankenPHP"]))
            .selected(u32::from(franken))
            .build();
        runs_on.set_sensitive(site.supports_frankenphp() || franken);
        let (this, name) = (self.clone(), site.name.clone());
        runs_on.connect_selected_notify(move |row| {
            let server = if row.selected() == 1 { "frankenphp" } else { "fastcgi" };
            this.change(&name, SiteChange { server: Some(server.to_owned()), ..Default::default() });
        });
        group.add(&runs_on);
        if !franken {
            return group;
        }
        let octane = site.worker == "octane";
        let mode = adw::ComboRow::builder()
            .title("Mode")
            .model(&gtk::StringList::new(&["Classic", "Laravel Octane"]))
            .selected(u32::from(octane))
            .build();
        mode.set_sensitive(site.octane_available || octane);
        let (this, name) = (self.clone(), site.name.clone());
        mode.connect_selected_notify(move |row| {
            let worker = if row.selected() == 1 { "octane" } else { "" };
            this.change(&name, SiteChange { worker: Some(worker.to_owned()), ..Default::default() });
        });
        group.add(&mode);

        if let Some(franken) = &site.frankenphp {
            let (text, class) = match franken.state.as_str() {
                "running" => {
                    let mut text = format!("Running FrankenPHP {} with PHP {}", franken.version, franken.php_version);
                    if octane {
                        text.push_str(" · worker mode");
                    }
                    (text, "dim-label")
                }
                "installing" => {
                    let mut text = format!("Downloading FrankenPHP for PHP {}…", site.php);
                    if (0.0..=1.0).contains(&franken.progress) {
                        text.push_str(&format!(" {}%", (franken.progress * 100.0).round()));
                    }
                    (format!("{text}\nPHP-FPM serves the site until it's ready."), "dim-label")
                }
                _ => (format!("{}\nPHP-FPM serves the site meanwhile.", franken.problem), "warning"),
            };
            group.add(&adw::ActionRow::builder().title(glib::markup_escape_text(text.trim())).css_classes([class]).build());
        }
        let actions = adw::ActionRow::new();
        let restarting = self.restarting.borrow().as_deref() == Some(site.name.as_str());
        if restarting {
            actions.add_suffix(&gtk::Spinner::builder().spinning(true).build());
        }
        let (this, name) = (self.clone(), site.name.clone());
        let restart = button("Restart", false, move || this.restart(&name));
        restart.set_sensitive(site.frankenphp.as_ref().is_some_and(|f| f.state == "running") && !restarting);
        restart.set_tooltip_text(Some("Restart FrankenPHP, e.g. so Octane's workers load changed code"));
        actions.add_suffix(&restart);
        let log = site
            .frankenphp
            .as_ref()
            .map(|f| f.log.clone())
            .filter(|l| !l.is_empty())
            .unwrap_or_else(|| format!("frankenphp-{}", site.name));
        let show_log = gtk::Button::builder().label("Show Log").valign(gtk::Align::Center).action_name("app.open").build();
        show_log.set_action_target_value(Some(&format!("logs/services/{log}").to_variant()));
        actions.add_suffix(&show_log);
        group.add(&actions);
        group
    }

    fn restart(self: &Rc<Self>, name: &str) {
        self.restarting.replace(Some(name.to_owned()));
        self.render();
        let (this, banner, n) = (self.clone(), self.banner.clone(), name.to_owned());
        self.model.perform(
            move |c| c.restart_site(&n),
            move |e| {
                this.restarting.replace(None);
                report(&banner, e);
                this.render();
            },
        );
    }

    fn boost_group(self: &Rc<Self>, site: &SiteStatus) -> adw::PreferencesGroup {
        let missing = site.boost == "missing";
        let group = group(
            "Laravel Boost",
            if missing {
                "The project uses Laravel Boost. Adding Flock to its guidelines tells AI agents that Flock serves the app and picks its PHP version."
            } else {
                "Flock's guideline in the project's Boost guidelines is outdated."
            },
        );
        let row = adw::ActionRow::new();
        let (this, s) = (self.clone(), site.clone());
        row.add_suffix(&button(if missing { "Add Flock to its guidelines…" } else { "Update it…" }, false, move || {
            let name = s.name.clone();
            this.confirm(
                &if missing { format!("Add Flock to {}'s Boost guidelines?", s.name) } else { format!("Update Flock's guideline in {}?", s.name) },
                &format!(
                    "Writes .ai/guidelines/flock.md in {} and runs php artisan boost:update, so AI agents know Flock serves the app and picks its PHP version. Commit it with the rest of .ai/guidelines. This changes files in your project.",
                    s.path
                ),
                if missing { "Add" } else { "Update" },
                false,
                move |c| c.add_boost_guideline(&name),
            );
        }));
        group.add(&row);
        group
    }

    /// Pins versions and the document root into the project, or removes them again.
    fn pin_menu(self: &Rc<Self>, site: &SiteStatus) -> gtk::MenuButton {
        let node = major(&site.node);
        let node_file = if site.node_pinned_here() { file_name(&site.node_file) } else { ".nvmrc".to_owned() };
        let can_pin_php = !(site.php_source == "project" && site.php_pinned_here());
        let can_pin_node = !node.is_empty() && !(site.node_source == "project" && site.node_pinned_here());
        let state = site.root_state.clone().unwrap_or_default();
        let can_pin_root = (state.is_empty() || state == "empty") && site.root_source != "project";
        let mut items: Vec<MenuItem> = Vec::new();
        let s = site.clone();
        let pin = {
            let this = self.clone();
            move |title: String, body: String, tool: &'static str, version: String| -> Box<dyn Fn()> {
                let (this, name) = (this.clone(), s.name.clone());
                Box::new(move || {
                    let (n, v) = (name.clone(), version.clone());
                    this.confirm(&title, &body, "Pin", false, move |c| c.pin(&n, tool, &v));
                })
            }
        };
        if can_pin_php {
            items.push((
                format!("Pin PHP {} to Project…", site.php),
                true,
                false,
                pin(
                    format!("Pin PHP {} to {}?", site.php, site.name),
                    format!("Writes php={} to .flockrc in {}. Commit it, and everyone using Flock on this project gets PHP {}. This changes a file in your project.", site.php, site.path, site.php),
                    "php",
                    site.php.clone(),
                ),
            ));
        }
        if can_pin_node {
            items.push((
                format!("Pin Node {node} to Project…"),
                true,
                false,
                pin(
                    format!("Pin Node {node} to {}?", site.name),
                    format!("Writes {node} to {node_file} in {}. Commit it, and Flock, nvm and other Node tools use Node {node} for this project. This changes a file in your project.", site.path),
                    "node",
                    node.clone(),
                ),
            ));
        }
        if can_pin_root {
            let label = root_label(&site.root);
            items.push((
                format!("Pin Document Root ({label}) to Project…"),
                true,
                false,
                pin(
                    format!("Pin the document root {label} to {}?", site.name),
                    format!("Writes root={} to .flockrc in {}. Commit it, and everyone using Flock on this project serves it from {label}. This changes a file in your project.", site.root, site.path),
                    "root",
                    site.root.clone(),
                ),
            ));
        }
        let unpin = |label: String, title: String, body: String, tool: &'static str| -> (String, bool, bool, Box<dyn Fn()>) {
            let (this, name) = (self.clone(), site.name.clone());
            (
                label,
                true,
                true,
                Box::new(move || {
                    let n = name.clone();
                    this.confirm(&title, &body, "Unpin", true, move |c| c.unpin(&n, tool));
                }),
            )
        };
        if site.php_pinned_here() {
            items.push(unpin(
                format!("Unpin PHP ({})…", file_name(&site.php_file)),
                format!("Unpin PHP from {}?", site.name),
                format!(
                    "Removes the PHP version from {}. The site then uses Flock's default PHP. This changes files in your project.",
                    site.php_file
                ),
                "php",
            ));
        }
        if site.node_pinned_here() {
            items.push(unpin(
                format!("Unpin Node ({})…", file_name(&site.node_file)),
                format!("Unpin Node from {}?", site.name),
                format!("Removes the Node.js version from .flockrc and deletes .nvmrc / .node-version in {}. The site then uses Flock's default Node.js. This changes files in your project.", site.path),
                "node",
            ));
        }
        if site.root_pinned_here() {
            items.push(unpin(
                "Unpin Document Root (.flockrc)…".to_owned(),
                format!("Unpin the document root from {}?", site.name),
                format!(
                    "Removes root= from .flockrc in {}. Flock then finds the document root itself. This changes files in your project.",
                    site.path
                ),
                "root",
            ));
        }
        let enabled = !items.is_empty();
        let menu = more_menu(items);
        menu.set_icon_name("view-pin-symbolic");
        menu.set_tooltip_text(Some("Pin versions and the document root to the project"));
        menu.set_sensitive(enabled);
        menu
    }

    // MARK: worktrees

    /// A site's worktrees and New Worktree….
    fn worktrees_group(self: &Rc<Self>, site: &SiteStatus, status: &Status) -> adw::PreferencesGroup {
        let group = group(
            "Worktrees",
            &format!("Each git worktree of {} is served at <branch>.{}, with its own dependencies and .env.", site.name, site.domain),
        );
        let add = gtk::Button::builder().label("New Worktree…").css_classes(["flat"]).build();
        let (this, s) = (self.clone(), site.clone());
        add.connect_clicked(move |_| this.new_worktree(&s));
        group.set_header_suffix(Some(&add));
        let worktrees = status.worktrees_of(&site.name);
        if worktrees.is_empty() {
            group.add(
                &adw::ActionRow::builder().title("No worktrees").subtitle("Create one here, with git or with your agent app.").build(),
            );
        }
        for worktree in worktrees {
            group.add(&self.site_row(worktree));
        }
        group
    }

    /// Asks for the branch, where it starts and the database of a new worktree. The branches to
    /// start from are read when it opens; a branch that exists is checked out as it is.
    fn new_worktree(self: &Rc<Self>, site: &SiteStatus) {
        let branch = gtk::Entry::builder()
            .placeholder_text("a name like brave-otter")
            .activates_default(true)
            .valign(gtk::Align::Center)
            .hexpand(true)
            .build();
        let branch_row = adw::ActionRow::builder().title("Branch").build();
        branch_row.add_suffix(&branch);
        let from_model = gtk::StringList::new(&["The current commit"]);
        let from = adw::ComboRow::builder().title("Start from").model(&from_model).sensitive(false).build();
        let database = adw::ComboRow::builder()
            .title("Database")
            .model(&gtk::StringList::new(&["Project default", DB_CHOICES[0], DB_CHOICES[1], DB_CHOICES[2]]))
            .build();
        let list = gtk::ListBox::builder().css_classes(["boxed-list"]).selection_mode(gtk::SelectionMode::None).build();
        list.append(&branch_row);
        list.append(&from);
        list.append(&database);
        let dialog = adw::AlertDialog::new(Some(&format!("New Worktree of {}", site.name)), Some(&new_worktree_text(site, "", false)));
        dialog.set_extra_child(Some(&list));
        dialog.add_responses(&[("cancel", "Cancel"), ("create", "Create")]);
        dialog.set_response_appearance("create", adw::ResponseAppearance::Suggested);
        dialog.set_default_response(Some("create"));

        // The branches, the value of each Start from choice, and whether they were read.
        let branches: Rc<RefCell<Branches>> = Rc::default();
        let values: Rc<RefCell<Vec<String>>> = Rc::new(RefCell::new(vec![String::new()]));
        let loaded = Rc::new(Cell::new(false));
        // Follows the branch typed in: one that exists starts from itself.
        let follow: Rc<dyn Fn(&str)> = Rc::new({
            let (weak, from, branches, loaded, s) = (dialog.downgrade(), from.clone(), branches.clone(), loaded.clone(), site.clone());
            move |text: &str| {
                let branch = text.trim();
                let exists = branches.borrow().has_local(branch);
                from.set_sensitive(loaded.get() && !exists);
                if let Some(dialog) = weak.upgrade() {
                    dialog.set_body(&new_worktree_text(&s, branch, exists));
                }
            }
        });
        let f = follow.clone();
        branch.connect_changed(move |entry| f(entry.text().as_str()));
        let (name, entry) = (site.name.clone(), branch.clone());
        let (from_list, model, b, v, l) = (from.clone(), from_model.clone(), branches.clone(), values.clone(), loaded.clone());
        glib::spawn_future_local(async move {
            // An older daemon has no branches to offer: the current commit only.
            let read = gio::spawn_blocking(move || Client.site_branches(&name)).await.expect("branches request").unwrap_or_default();
            let choices = read.choices();
            let labels: Vec<&str> = choices.iter().map(|(_, label)| label.as_str()).collect();
            model.splice(0, model.n_items(), &labels);
            let current = choices.iter().position(|(value, _)| *value == read.current).unwrap_or(0);
            from_list.set_selected(current as u32);
            v.replace(choices.into_iter().map(|(value, _)| value).collect());
            b.replace(read);
            l.set(true);
            follow(entry.text().as_str());
        });

        let (this, name) = (self.clone(), site.name.clone());
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            if response != "create" {
                return;
            }
            let branch = branch.text().trim().to_owned();
            let from = if branches.borrow().has_local(&branch) {
                String::new()
            } else {
                values.borrow().get(from.selected() as usize).cloned().unwrap_or_default()
            };
            let db = ["", "shared", "copy", "fresh"].get(database.selected() as usize).copied().unwrap_or_default().to_owned();
            let (title, site) = (format!("New Worktree of {name}"), name.clone());
            let start: worktree_job::Start =
                Box::new(move |c: Client| c.create_worktree(&site, &branch, &from, &db).map(|created| created.path));
            // Closed once it is ready, the dialog shows the new worktree.
            let pane = this.clone();
            worktree_job::open(&this.stack, this.model.clone(), &title, start, move |worktree| pane.open(&worktree));
        });
    }

    /// Removes a worktree and its own database. One git or its hook refuses to remove (changes, a
    /// failing hook) can be removed anyway.
    fn remove_worktree(self: &Rc<Self>, removal: Removal, force: bool) {
        self.removing.replace(Some(removal.path.clone()));
        self.render();
        let this = self.clone();
        glib::spawn_future_local(async move {
            let path = removal.path.clone();
            let result = gio::spawn_blocking(move || Client.remove_worktree(&path, force)).await.expect("worktree request");
            this.removing.replace(None);
            match result {
                Ok(_) => {
                    report(&this.banner, None);
                    // Its site's detail follows the worktree's.
                    let selected = this.selected.borrow().clone();
                    if selected.as_deref() == Some(removal.name.as_str()) {
                        this.selected.replace(Some(removal.parent.clone()));
                    }
                }
                Err(e) if !force => this.offer_force(removal, &e.to_string()),
                Err(e) => report(&this.banner, Some(e.to_string())),
            }
            this.render();
            this.model.refresh();
        });
    }

    fn offer_force(self: &Rc<Self>, removal: Removal, error: &str) {
        let dialog = adw::AlertDialog::new(Some(&format!("Couldn't remove {}", removal.label)), Some(error));
        dialog.add_responses(&[("cancel", "Cancel"), ("force", "Remove Anyway")]);
        dialog.set_response_appearance("force", adw::ResponseAppearance::Destructive);
        let this = self.clone();
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            if response == "force" {
                this.remove_worktree(removal.clone(), true);
            }
        });
    }

    // MARK: a worktree's detail

    fn render_worktree(self: &Rc<Self>, site: &SiteStatus) {
        let mut groups: Vec<gtk::Widget> = vec![self.worktree_header(site).upcast(), self.worktree_info(site).upcast()];
        if let Some(setup) = &site.worktree {
            groups.push(self.database_group(site, setup).upcast());
        }
        groups.push(self.setup_group(site).upcast());
        self.detail.set_groups(&groups);
    }

    fn worktree_header(self: &Rc<Self>, site: &SiteStatus) -> adw::PreferencesGroup {
        let header = group("", "");
        let back = adw::ActionRow::builder().title(glib::markup_escape_text(&site.parent)).activatable(true).build();
        back.add_prefix(&gtk::Image::from_icon_name("go-previous-symbolic"));
        let (this, parent) = (self.clone(), site.parent.clone());
        back.connect_activated(move |_| this.open(&parent));
        header.add(&back);

        let title = adw::ActionRow::builder()
            .title(glib::markup_escape_text(&site.worktree_label()))
            .subtitle(glib::markup_escape_text(&format!("{}\n{}", site.url.as_deref().unwrap_or(&site.domain), site.path)))
            .subtitle_selectable(true)
            .build();
        title.add_suffix(&badge("Worktree", ""));
        let open = gtk::Button::builder()
            .icon_name("dev.flock.Flock-sites-symbolic")
            .tooltip_text("Open in the browser")
            .valign(gtk::Align::Center)
            .css_classes(["flat"])
            .build();
        open.set_sensitive(site.url.is_some());
        let url = site.url.clone().unwrap_or_default();
        open.connect_clicked(move |b| launch_uri(b, &url));
        title.add_suffix(&open);
        // While the hook and git run, a spinner instead of the button.
        if self.removing.borrow().as_deref() == Some(site.path.as_str()) {
            title.add_suffix(&gtk::Spinner::builder().spinning(true).valign(gtk::Align::Center).build());
        } else {
            let remove = gtk::Button::builder()
                .icon_name("user-trash-symbolic")
                .tooltip_text("Remove worktree")
                .valign(gtk::Align::Center)
                .css_classes(["flat"])
                .build();
            let (this, s) = (self.clone(), site.clone());
            remove.connect_clicked(move |_| this.ask_remove(&s));
            title.add_suffix(&remove);
        }
        header.add(&title);
        if let Some(problem) = &site.problem {
            header.add(&adw::ActionRow::builder().title(glib::markup_escape_text(problem)).css_classes(["warning"]).build());
        }
        header
    }

    /// Branch, folder and address, and the versions it gets from its site (or the branch's pins).
    fn worktree_info(self: &Rc<Self>, site: &SiteStatus) -> adw::PreferencesGroup {
        let group = group("Worktree", "");
        group.add(&info_row("Branch", if site.branch.is_empty() { "None (detached HEAD)" } else { site.branch.as_str() }));
        let folder = info_row("Folder", &site.path);
        let open_folder = gtk::Button::builder()
            .icon_name("folder-symbolic")
            .tooltip_text("Open the folder")
            .valign(gtk::Align::Center)
            .css_classes(["flat"])
            .build();
        let path = site.path.clone();
        open_folder.connect_clicked(move |b| launch_file(b, &path));
        folder.add_suffix(&open_folder);
        group.add(&folder);
        let address = info_row("URL", site.url.as_deref().unwrap_or(&site.domain));
        if let Some(url) = site.url.clone() {
            address.set_activatable(true);
            address.connect_activated(move |r| launch_uri(r, &url));
        }
        group.add(&address);
        let versions = [("PHP", &site.php, &site.php_source, &site.php_file), ("Node.js", &site.node, &site.node_source, &site.node_file)];
        for (label, version, source, file) in versions {
            let row = info_row(label, if version.is_empty() { "—" } else { version.as_str() });
            if source == "project" && !file.is_empty() {
                row.set_subtitle(&glib::markup_escape_text(&format!("The branch pins it in {}", file_name(file))));
            }
            group.add(&row);
        }
        let settings = adw::ActionRow::builder()
            .title(glib::markup_escape_text(&format!("Uses {}'s settings", site.parent)))
            .subtitle("Change its PHP and Node.js versions, HTTPS and document root on the site.")
            .activatable(true)
            .build();
        settings.add_suffix(&gtk::Image::from_icon_name("go-next-symbolic"));
        let (this, parent) = (self.clone(), site.parent.clone());
        settings.connect_activated(move |_| this.open(&parent));
        group.add(&settings);
        group
    }

    fn database_group(self: &Rc<Self>, site: &SiteStatus, setup: &WorktreeStatus) -> adw::PreferencesGroup {
        let group = group("Database", "A database of its own gets the branch's migrations run on it.");
        // The project's default mode isn't in the status: a database of its own is most likely a copy.
        let current: u32 = match setup.db_mode.as_str() {
            "shared" => 0,
            "copy" => 1,
            "fresh" => 2,
            _ if !setup.database.is_empty() => 1,
            _ => 0,
        };
        let mode = adw::ComboRow::builder().title("Database").model(&gtk::StringList::new(&DB_CHOICES)).selected(current).build();
        if let Some(database) = site.own_database() {
            mode.set_subtitle(&glib::markup_escape_text(database));
        }
        mode.set_sensitive(!setup.busy());
        let (this, s) = (self.clone(), site.clone());
        mode.connect_selected_notify(move |row| {
            if let Some(db) = DB_MODES.get(row.selected() as usize).filter(|_| row.selected() != current) {
                this.switch_database(&s, db);
            }
        });
        group.add(&mode);
        group
    }

    /// Asks before switching a worktree's database; the one Flock made for it is dropped.
    fn switch_database(self: &Rc<Self>, site: &SiteStatus, db: &str) {
        let (label, parent) = (site.worktree_label(), site.parent.clone());
        let own = site.own_database().map(str::to_owned);
        let (title, body) = match (&own, db) {
            (Some(name), _) => (format!("Drop {name}?"), format!("Its database {name} is dropped.")),
            (None, "shared") => (format!("Use {parent}'s database?"), format!("{label} then uses {parent}'s database.")),
            (None, "copy") => {
                let body = format!("Flock copies {parent}'s database and runs the branch's migrations on it.");
                (format!("Give {label} a copy of the database?"), body)
            }
            (None, _) => (format!("Give {label} a fresh database?"), "Flock creates an empty database and runs the migrations.".to_owned()),
        };
        let dialog = adw::AlertDialog::new(Some(&title), Some(&body));
        dialog.add_responses(&[("cancel", "Cancel"), ("switch", "Switch")]);
        dialog.set_response_appearance(
            "switch",
            if own.is_some() { adw::ResponseAppearance::Destructive } else { adw::ResponseAppearance::Suggested },
        );
        let (this, path, db) = (self.clone(), site.path.clone(), db.to_owned());
        let job_title = format!("Switching {label}'s Database");
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            if response != "switch" {
                // The row shows the mode it has again.
                this.shown.replace(None);
                this.render();
                return;
            }
            let (path, db) = (path.clone(), db.clone());
            let start: worktree_job::Start = Box::new(move |c: Client| c.switch_worktree_database(&path, &db).map(|_| path));
            worktree_job::open(&this.stack, this.model.clone(), &job_title, start, |_| {});
        });
    }

    fn setup_group(self: &Rc<Self>, site: &SiteStatus) -> adw::PreferencesGroup {
        let group = group("Setup", "");
        let Some(setup) = &site.worktree else {
            let row = adw::ActionRow::builder()
                .title("Flock hasn't set this worktree up")
                .subtitle("Setting it up copies dependencies and .env, installs and runs the project's hooks.")
                .build();
            let (this, s) = (self.clone(), site.clone());
            row.add_suffix(&button("Set Up", false, move || this.run_setup(&s, false)));
            group.add(&row);
            return group;
        };
        let row = adw::ActionRow::builder().title(glib::markup_escape_text(&setup.text())).build();
        if setup.busy() {
            row.add_prefix(&gtk::Spinner::builder().spinning(true).build());
        } else if setup.failed() {
            row.set_subtitle(&glib::markup_escape_text(&setup_error(setup)));
            row.add_css_class("warning");
        }
        if !setup.log.is_empty() {
            let show_log = gtk::Button::builder().label("Show Log").valign(gtk::Align::Center).action_name("app.open").build();
            show_log.set_action_target_value(Some(&format!("logs/services/{}", setup.log).to_variant()));
            row.add_suffix(&show_log);
        }
        let (this, s) = (self.clone(), site.clone());
        let again = button("Run Setup Again", false, move || this.run_setup(&s, true));
        again.set_sensitive(!setup.busy());
        row.add_suffix(&again);
        group.add(&row);
        group
    }

    /// Sets the worktree up (again), in the progress dialog.
    fn run_setup(self: &Rc<Self>, site: &SiteStatus, again: bool) {
        let path = site.path.clone();
        let start: worktree_job::Start = Box::new(move |c: Client| c.setup_worktree(&path, again).map(|_| path));
        worktree_job::open(&self.stack, self.model.clone(), &format!("Setting Up {}", site.worktree_label()), start, |_| {});
    }

    fn ask_remove(self: &Rc<Self>, site: &SiteStatus) {
        let label = site.worktree_label();
        let mut body = match site.own_database() {
            Some(db) => format!("Flock runs its before_delete hook and deletes {} and its database {db}.", site.path),
            None => format!("Flock runs its before_delete hook and deletes {}.", site.path),
        };
        if !site.branch.is_empty() {
            body.push_str(&format!(" The branch {} stays.", site.branch));
        }
        let dialog = adw::AlertDialog::new(Some(&format!("Remove {label}?")), Some(&body));
        dialog.add_responses(&[("cancel", "Cancel"), ("remove", "Remove")]);
        dialog.set_response_appearance("remove", adw::ResponseAppearance::Destructive);
        let this = self.clone();
        let removal = Removal { path: site.path.clone(), name: site.name.clone(), label, parent: site.parent.clone() };
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            if response == "remove" {
                this.remove_worktree(removal.clone(), false);
            }
        });
    }

    // MARK: a proxy's detail

    /// A proxy: its address and actions, then what answers at its upstream, its HTTPS and upstream.
    fn render_proxy(self: &Rc<Self>, proxy: &ProxyStatus) {
        let groups: Vec<gtk::Widget> = vec![self.proxy_header(proxy).upcast(), self.proxy_group(proxy).upcast()];
        self.detail.set_groups(&groups);
    }

    fn proxy_header(self: &Rc<Self>, proxy: &ProxyStatus) -> adw::PreferencesGroup {
        let header = group("", "");
        let back = adw::ActionRow::builder().title("Sites").activatable(true).build();
        back.add_prefix(&gtk::Image::from_icon_name("go-previous-symbolic"));
        let this = self.clone();
        back.connect_activated(move |_| this.back());
        header.add(&back);

        let title = adw::ActionRow::builder()
            .title(glib::markup_escape_text(&proxy.name))
            .subtitle(glib::markup_escape_text(proxy.url.as_deref().unwrap_or(&proxy.domain)))
            .subtitle_selectable(true)
            .build();
        title.add_suffix(&badge("Proxy", ""));
        let actions = gtk::Box::builder().spacing(4).valign(gtk::Align::Center).build();
        let open = gtk::Button::builder()
            .icon_name("dev.flock.Flock-sites-symbolic")
            .tooltip_text("Open in the browser")
            .css_classes(["flat"])
            .build();
        open.set_sensitive(proxy.url.is_some());
        let url = proxy.url.clone().unwrap_or_default();
        open.connect_clicked(move |b| launch_uri(b, &url));
        actions.append(&open);
        let remove = gtk::Button::builder().icon_name("user-trash-symbolic").tooltip_text("Remove proxy").css_classes(["flat"]).build();
        let (this, p) = (self.clone(), proxy.clone());
        remove.connect_clicked(move |_| {
            let name = p.name.clone();
            this.confirm(
                &format!("Remove {}?", p.name),
                &format!("Flock stops serving {}. The server at {} keeps running.", p.domain, p.upstream),
                "Remove",
                true,
                move |c| c.remove_proxy(&name),
            );
        });
        actions.append(&remove);
        title.add_suffix(&actions);
        header.add(&title);
        if let Some(problem) = &proxy.problem {
            header.add(&adw::ActionRow::builder().title(glib::markup_escape_text(problem)).css_classes(["warning"]).build());
        }
        header
    }

    fn proxy_group(self: &Rc<Self>, proxy: &ProxyStatus) -> adw::PreferencesGroup {
        let group = group(
            "Proxy",
            &format!(
                "Every request passes to the upstream with its Host header, WebSocket upgrades (hot reload) included. Flock doesn't start the server, so start it as usual. Until it answers, {} shows a page saying so.",
                proxy.domain
            ),
        );
        let server = info_row("Server", &proxy.server_text());
        server.add_prefix(&status_dot(proxy.running));
        if !proxy.running {
            server.set_subtitle(&glib::markup_escape_text(&format!("Nothing answers at {} yet.", proxy.upstream_label())));
        }
        group.add(&server);

        let https = adw::SwitchRow::builder().title("HTTPS").active(proxy.secured).build();
        let (this, name) = (self.clone(), proxy.name.clone());
        https.connect_active_notify(move |row| {
            let (name, on) = (name.clone(), row.is_active());
            this.perform(move |c| c.secure_proxy(&name, on));
        });
        group.add(&https);

        let upstream = info_row("Upstream", &proxy.upstream);
        let (this, p) = (self.clone(), proxy.clone());
        upstream.add_suffix(&button("Change…", false, move || this.change_upstream(&p)));
        group.add(&upstream);
        group
    }

    /// Asks for a proxy's new upstream; its HTTPS stays as it is.
    fn change_upstream(self: &Rc<Self>, proxy: &ProxyStatus) {
        let entry =
            gtk::Entry::builder().text(proxy.upstream_label()).placeholder_text("Port, host:port or URL").activates_default(true).build();
        let dialog = adw::AlertDialog::new(
            Some(&format!("Change the upstream of {}", proxy.name)),
            Some("A port means 127.0.0.1; give a host and port (localhost:5173) or a URL (https://127.0.0.1:8443) for anything else."),
        );
        dialog.set_extra_child(Some(&entry));
        dialog.add_responses(&[("cancel", "Cancel"), ("change", "Change")]);
        dialog.set_response_appearance("change", adw::ResponseAppearance::Suggested);
        dialog.set_default_response(Some("change"));
        let (this, name) = (self.clone(), proxy.name.clone());
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            let upstream = entry.text().trim().to_owned();
            if response == "change" && !upstream.is_empty() {
                let name = name.clone();
                this.perform(move |c| c.proxy(&name, &upstream, None));
            }
        });
    }

    // MARK: PHP extensions

    fn show_extensions(self: &Rc<Self>, site: &SiteStatus) {
        let list = gtk::ListBox::builder().css_classes(["boxed-list"]).selection_mode(gtk::SelectionMode::None).build();
        let status = gtk::Label::builder().label("Loading…").css_classes(["dim-label"]).build();
        let column = gtk::Box::builder()
            .orientation(gtk::Orientation::Vertical)
            .spacing(12)
            .margin_top(12)
            .margin_bottom(18)
            .margin_start(18)
            .margin_end(18)
            .build();
        column.append(&status);
        column.append(&list);
        column.append(
            &gtk::Label::builder()
                .label("Requiring an extension in composer.json makes composer install refuse a server without it, instead of the app failing there.")
                .wrap(true)
                .xalign(0.0)
                .css_classes(["caption", "dim-label"])
                .build(),
        );
        let view = adw::ToolbarView::new();
        view.add_top_bar(&adw::HeaderBar::new());
        view.set_content(Some(&gtk::ScrolledWindow::builder().child(&column).vexpand(true).build()));
        let dialog = adw::Dialog::builder()
            .title(format!("PHP extensions of {}", site.name))
            .content_width(560)
            .content_height(440)
            .child(&view)
            .build();
        dialog.present(Some(&self.stack));

        let show: Show = Rc::new({
            let (list, status, path) = (list.clone(), status.clone(), site.path.clone());
            move |result: Result<ExtensionReport, Error>, again: Rc<dyn Fn(String)>| {
                while let Some(child) = list.first_child() {
                    list.remove(&child);
                }
                match result {
                    Err(e) => {
                        status.set_label(&e.to_string());
                        status.add_css_class("error");
                    }
                    Ok(report) => {
                        status.remove_css_class("error");
                        let extensions = report.extensions.clone().unwrap_or_default();
                        status.set_label(&if extensions.is_empty() {
                            "The project requires no PHP extensions.".to_owned()
                        } else if report.laravel {
                            format!("PHP {} · Laravel app", report.php)
                        } else {
                            format!("PHP {}", report.php)
                        });
                        for ext in &extensions {
                            list.append(&extension_row(ext, &report.php, &path, again.clone()));
                        }
                    }
                }
            }
        });
        load_extensions(site.name.clone(), show);
    }
}

/// Shows an extension report; the callback requires an extension, then shows the new report.
type Show = Rc<dyn Fn(Result<ExtensionReport, Error>, Rc<dyn Fn(String)>)>;

/// Loads a site's extension report and shows it.
fn load_extensions(name: String, show: Show) {
    glib::spawn_future_local(async move {
        let n = name.clone();
        let result = gio::spawn_blocking(move || Client.site_extensions(&n)).await.expect("extensions request");
        show(result, requirer(name, show.clone()));
    });
}

/// Requires an extension in the project (composer require), then shows the new report.
fn requirer(name: String, show: Show) -> Rc<dyn Fn(String)> {
    Rc::new(move |ext: String| {
        let (name, show) = (name.clone(), show.clone());
        glib::spawn_future_local(async move {
            let n = name.clone();
            let result = gio::spawn_blocking(move || Client.require_extension(&n, &ext)).await.expect("extensions request");
            show(result, requirer(name, show.clone()));
        });
    })
}

fn extension_row(ext: &PhpExtension, php: &str, path: &str, require: Rc<dyn Fn(String)>) -> adw::ActionRow {
    let required_by = ext.required_by.clone().unwrap_or_default();
    let declared = !required_by.is_empty();
    let mut detail =
        if declared { format!("Required by {}", required_by.join(", ")) } else { format!("Not required, but used: {}", ext.hint) };
    if !ext.available {
        detail.push_str(&format!(" — Flock's PHP {php} doesn't include it"));
    }
    let row = adw::ActionRow::builder().title(glib::markup_escape_text(&ext.name)).subtitle(glib::markup_escape_text(&detail)).build();
    let (icon, class) = match (ext.available, declared) {
        (false, _) => ("process-stop-symbolic", "error"),
        (true, true) => ("emblem-ok-symbolic", "success"),
        (true, false) => ("dialog-warning-symbolic", "warning"),
    };
    let icon = gtk::Image::from_icon_name(icon);
    icon.add_css_class(class);
    row.add_prefix(&icon);
    if !declared {
        let add = gtk::Button::builder().label("Add to composer.json").valign(gtk::Align::Center).build();
        let (name, path) = (ext.name.clone(), path.to_owned());
        add.connect_clicked(move |button| {
            let dialog = adw::AlertDialog::new(
                Some(&format!("Require ext-{name} in composer.json?")),
                Some(&format!("Runs composer require ext-{name}:* in {path}, which changes composer.json and composer.lock. Commit them with the project.")),
            );
            dialog.add_responses(&[("cancel", "Cancel"), ("require", "Require")]);
            dialog.set_response_appearance("require", adw::ResponseAppearance::Suggested);
            let (name, require, button) = (name.clone(), require.clone(), button.clone());
            dialog.choose(Some(&button.clone()), gio::Cancellable::NONE, move |response| {
                if response == "require" {
                    button.set_sensitive(false);
                    button.set_label("Adding…");
                    require(name.clone());
                }
            });
        });
        row.add_suffix(&add);
    }
    row
}

/// A site's suffixes in the list: what it warns about, its share and its lock.
fn site_suffixes(site: &SiteStatus) -> Vec<gtk::Widget> {
    let mut widgets: Vec<gtk::Widget> = Vec::new();
    if let Some(problem) = problem(site) {
        let warning = gtk::Image::from_icon_name("dialog-warning-symbolic");
        warning.add_css_class("warning");
        warning.set_tooltip_text(Some(&problem));
        widgets.push(warning.upcast());
    }
    if site.shared() {
        widgets.push(badge("Shared", "success").upcast());
    }
    if site.secured {
        let lock = gtk::Image::from_icon_name("channel-secure-symbolic");
        lock.add_css_class("dim-label");
        lock.set_tooltip_text(Some("Served over HTTPS"));
        widgets.push(lock.upcast());
    }
    widgets
}

/// A worktree's suffixes in a list: its setup while it runs or when it failed, and its own
/// database ("copy", "fresh").
fn worktree_suffixes(site: &SiteStatus) -> Vec<gtk::Widget> {
    let mut widgets: Vec<gtk::Widget> = Vec::new();
    let Some(setup) = &site.worktree else { return widgets };
    if setup.busy() {
        widgets.push(gtk::Spinner::builder().spinning(true).valign(gtk::Align::Center).build().upcast());
        widgets.push(gtk::Label::builder().label(setup.text()).css_classes(["dim-label", "caption"]).build().upcast());
    } else if setup.failed() {
        let warning = gtk::Image::from_icon_name("dialog-warning-symbolic");
        warning.add_css_class("warning");
        warning.set_tooltip_text(Some(&setup_error(setup)));
        widgets.push(warning.upcast());
    }
    if !setup.database.is_empty() {
        let tag = badge(if setup.db_mode.is_empty() { "own database" } else { setup.db_mode.as_str() }, "");
        tag.set_tooltip_text(Some(&format!("Its own database: {}", setup.database)));
        widgets.push(tag.upcast());
    }
    widgets
}

fn setup_error(setup: &WorktreeStatus) -> String {
    if setup.error.is_empty() { "The setup failed.".to_owned() } else { setup.error.clone() }
}

/// What the New Worktree dialog says it does with the branch typed in.
fn new_worktree_text(site: &SiteStatus, branch: &str, exists: bool) -> String {
    let address = if branch.is_empty() { "its own address".to_owned() } else { format!("{}.{}", slug(branch), site.domain) };
    if exists {
        return format!(
            "{branch} exists, so the worktree checks it out. Flock creates it next to {}, sets it up and serves it at {address}.",
            site.name
        );
    }
    let name = if branch.is_empty() { "a new branch with a name of its own".to_owned() } else { format!("the new branch {branch}") };
    format!("Flock creates the worktree next to {} on {name}, sets it up and serves it at {address}.", site.name)
}

/// The name a worktree is served under, as Flock makes it from the branch (config.WorktreeSlug):
/// "Feature/Auth" → "feature-auth".
fn slug(branch: &str) -> String {
    let mut out = String::new();
    for c in branch.to_lowercase().chars() {
        if c.is_ascii_alphanumeric() || c == '-' {
            out.push(c);
        } else if matches!(c, '/' | '_' | '.') {
            out.push('-');
        }
    }
    while out.contains("--") {
        out = out.replace("--", "-");
    }
    out.trim_matches('-').to_owned()
}

/// Built-in app templates shown while loading or against an older daemon without /v1/apps/templates.
fn default_app_templates() -> Vec<AppTemplate> {
    vec![
        AppTemplate { name: "blank".to_owned(), requires_db: false },
        AppTemplate { name: "php".to_owned(), requires_db: false },
        AppTemplate { name: "wordpress".to_owned(), requires_db: true },
    ]
}

fn selected_app_template(templates: &[AppTemplate], selected: u32) -> AppTemplate {
    templates.get(selected as usize).cloned().unwrap_or_else(|| AppTemplate { name: "php".to_owned(), requires_db: false })
}

/// Hostname label matching config.SiteName on the daemon.
fn app_slug(name: &str) -> String {
    let slug: String = name.to_lowercase().chars().map(|c| if c.is_ascii_alphanumeric() || c == '-' { c } else { '-' }).collect();
    slug.trim_matches('-').to_owned()
}

/// What a site's list row warns about: the site, its document root, a domain, or its FrankenPHP.
fn problem(site: &SiteStatus) -> Option<String> {
    if let Some(p) = site.problem.clone().or_else(|| site.notice.clone()) {
        return Some(p);
    }
    if let Some(domain) = site.domains.iter().flatten().find(|d| d.problem.is_some()) {
        return Some(format!("{}: {}", domain.host, domain.problem.clone().unwrap_or_default()));
    }
    site.frankenphp
        .as_ref()
        .filter(|f| site.server == "frankenphp" && (f.state == "failed" || f.state == "unavailable") && !f.problem.is_empty())
        .map(|f| f.problem.clone())
}

/// A proxy's suffixes in the list: why it isn't served, whether something answers at its
/// upstream, and its lock.
fn proxy_suffixes(proxy: &ProxyStatus) -> Vec<gtk::Widget> {
    let mut widgets: Vec<gtk::Widget> = Vec::new();
    if let Some(problem) = &proxy.problem {
        let warning = gtk::Image::from_icon_name("dialog-warning-symbolic");
        warning.add_css_class("warning");
        warning.set_tooltip_text(Some(problem));
        widgets.push(warning.upcast());
    }
    let dot = status_dot(proxy.running);
    dot.set_tooltip_text(Some(&proxy.server_text()));
    widgets.push(dot.upcast());
    if proxy.secured {
        let lock = gtk::Image::from_icon_name("channel-secure-symbolic");
        lock.add_css_class("dim-label");
        lock.set_tooltip_text(Some("Served over HTTPS"));
        widgets.push(lock.upcast());
    }
    widgets
}

/// Why the site isn't served from its root, or a root set here over the project's.
fn root_hint(site: &SiteStatus) -> Option<String> {
    match site.root_state.as_deref().unwrap_or_default() {
        "not_found" => Some("No public/, www/, web/ or webroot/ with an index.php was found, so Flock shows a page instead of the site. Choose the folder with the app's index.php.".to_owned()),
        "missing" => Some(format!("Choose the folder with the app's index.php, or create {}.", site.root)),
        "empty" => Some(format!("{} has no index.php or index.html, so Flock shows a page instead of the site until it does.", root_label(&site.root))),
        _ if site.root_source == "isolated" && site.root_pinned_here() => {
            Some(format!("{} is set here; the project sets {} in .flockrc.", root_label(&site.root), root_label(&site.root_pinned)))
        }
        _ => None,
    }
}

/// The project's pin ("Use .nvmrc (Node 20)") or the default first, following the project; then
/// the installed versions, which isolate the site.
#[allow(clippy::too_many_arguments)]
fn version_row(
    label: &str,
    current: &str,
    source: &str,
    pinned: &str,
    file: &str,
    default: &str,
    installed: &[String],
    change: impl Fn(String) + 'static,
) -> adw::ComboRow {
    let follow = if !pinned.is_empty() {
        format!("Use {} ({label} {pinned})", file_name(file))
    } else if default.is_empty() {
        "Default".to_owned()
    } else {
        format!("Default ({label} {default})")
    };
    let mut versions = installed.to_vec();
    if source == "isolated" && !current.is_empty() && !versions.iter().any(|v| v == current) {
        versions.push(current.to_owned());
    }
    let mut labels = vec![follow];
    labels.extend(versions.iter().map(|v| format!("{label} {v}")));
    let model = gtk::StringList::new(&labels.iter().map(String::as_str).collect::<Vec<_>>());
    let row = adw::ComboRow::builder().title(if label == "Node" { "Node.js" } else { label }).model(&model).build();
    row.set_tooltip_text(Some(&match source {
        "isolated" => format!("{label} {current} is set for this site on this machine (isolated)."),
        "project" => format!("The project pins {label} {pinned} in {}.", file_name(file)),
        _ => format!("The site follows Flock's default {label} version."),
    }));
    let selected = if source == "isolated" { versions.iter().position(|v| v == current).map_or(0, |i| i + 1) } else { 0 };
    row.set_selected(selected as u32);
    row.connect_selected_notify(move |row| {
        let index = row.selected() as usize;
        change(if index == 0 { String::new() } else { versions[index - 1].clone() });
    });
    row
}

/// "src/www", or "Project folder" for ".".
fn root_label(root: &str) -> String {
    if root == "." { "Project folder".to_owned() } else { root.to_owned() }
}

fn file_name(path: &str) -> String {
    std::path::Path::new(path).file_name().map_or(path.to_owned(), |n| n.to_string_lossy().into_owned())
}

/// "22.11.0" → "22"; pins and isolations are majors.
fn major(version: &str) -> String {
    version.split('.').next().unwrap_or_default().to_owned()
}

fn launch_uri(widget: &impl IsA<gtk::Widget>, uri: &str) {
    let window = widget.root().and_downcast::<gtk::Window>();
    gtk::UriLauncher::new(uri).launch(window.as_ref(), gio::Cancellable::NONE, |_| {});
}

fn launch_file(widget: &impl IsA<gtk::Widget>, path: &str) {
    let window = widget.root().and_downcast::<gtk::Window>();
    gtk::FileLauncher::new(Some(&gio::File::for_path(path))).launch(window.as_ref(), gio::Cancellable::NONE, |_| {});
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn makes_the_worktree_slug_like_flock() {
        assert_eq!(slug("Feature/Auth_2.x"), "feature-auth-2-x");
        assert_eq!(slug("fix//ümlaut-"), "fix-mlaut");
        assert_eq!(slug("brave-otter"), "brave-otter");
    }

    #[test]
    fn makes_the_app_slug_like_flock() {
        assert_eq!(app_slug("My Blog"), "my-blog");
        assert_eq!(app_slug("my-app_2"), "my-app-2");
        assert_eq!(app_slug("--acme--"), "acme");
    }

    #[test]
    fn default_templates_include_builtin_types() {
        let templates = default_app_templates();
        let names: Vec<&str> = templates.iter().map(|t| t.name.as_str()).collect();
        assert_eq!(names, ["blank", "php", "wordpress"]);
        assert!(!templates[0].requires_db);
        assert!(!templates[1].requires_db);
        assert!(templates[2].requires_db);
    }
}
