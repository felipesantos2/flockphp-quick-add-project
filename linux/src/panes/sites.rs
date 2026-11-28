//! Sites: the sites added one by one and those of the project folders, and a site's detail: its
//! address, domains and document root, its PHP and Node.js versions, what serves it, its Reverb
//! server, its share and Laravel Boost. Version and root pickers offer the project's pin (or the
//! default) first, then what isolates the site on this machine; the pin menu writes them into the
//! project.
//!
//! A Node.js site has no PHP settings: its detail shows its dev server instead, which Flock starts
//! on the site's first visit, with the server's output.
//!
//! A site's git worktrees are listed under it. A worktree's detail shows its setup and database
//! instead of settings: it uses its site's.
//!
//! Proxies (`flock proxy`) follow the sites in a group of their own; a proxy's detail shows what
//! answers at its upstream, its HTTPS and its upstream, and the command that starts its server
//! (with Start, Stop and its output) when it has one. Site and proxy names never clash (the daemon
//! refuses it), so one selection covers both.

use std::cell::{Cell, RefCell};
use std::rc::Rc;
use std::time::Duration;

use adw::prelude::*;
use base64::Engine;
use gtk::{gdk, gio, glib};

use super::common::{PaneView, ask_link, code_block, group, info_row, report, state_dot, status_dot, switch_row};
use super::versions::{MenuItem, badge, button, more_menu};
use super::worktree_job;
use super::{Data, Pane};
use crate::api::{
    AppTemplate, Branches, Client, Config, CreateAppRequest, Error, ExtensionReport, PhpExtension, ProxyChange, ProxyCommand,
    ProxyCommandChange, ProxyStatus, ShareStatus, SiteChange, SiteNodeServer, SiteReverb, SiteStatus, Status, WorktreeStatus,
};
use crate::i18n::{self, tr, tr_noop, trn};
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
    /// The site whose Reverb server is restarting.
    restarting_reverb: RefCell<Option<String>>,
    shown: RefCell<Option<String>>,
    /// The sites whose worktrees the list shows (prefs.rs).
    expanded: RefCell<Vec<String>>,
    /// The folder of the worktree being removed.
    removing: RefCell<Option<String>>,
    /// The output of a dev server, a Reverb server or a proxy's command, moved into each detail that
    /// shows one.
    output: Rc<Output>,
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
const DB_CHOICES: [&str; 3] =
    [tr_noop!("worktrees.database_shared"), tr_noop!("worktrees.database_copy"), tr_noop!("worktrees.database_fresh")];

impl SitesPane {
    pub fn new(model: Rc<AppModel>, banner: adw::Banner) -> Self {
        let list = PaneView::new(tr!("sites.subtitle"));
        let detail = PaneView::new("");
        let stack = gtk::Stack::builder().transition_type(gtk::StackTransitionType::SlideLeftRight).build();
        stack.add_named(&list.scrolled, Some("list"));
        stack.add_named(&detail.scrolled, Some("detail"));
        let output = Output::new(banner.clone());
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
            restarting_reverb: RefCell::default(),
            shown: RefCell::default(),
            expanded: RefCell::new(prefs::load().expanded_sites),
            removing: RefCell::default(),
            output,
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
        ask_link(&self.stack, resolution, tr!("sites.secure"), move |name| {
            this.change(&name, SiteChange { secured: Some(true), ..Default::default() })
        });
    }

    fn render(self: &Rc<Self>) {
        self.ask_link();
        let key = format!(
            "{:?}{:?}{:?}{:?}{:?}{:?}{}",
            self.status.borrow(),
            self.config.borrow(),
            self.selected.borrow(),
            self.restarting.borrow(),
            self.restarting_reverb.borrow(),
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
            self.output.detach();
            self.stack.set_visible_child_name("list");
            self.render_list();
        }
        // The detail shown has no output (any more).
        if self.output.group.parent().is_none() {
            self.output.stop();
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
        dialog.add_responses(&[("cancel", tr!("common.cancel")), ("ok", action)]);
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
            let empty = ui::empty("dev.flock.Flock-sites-symbolic", tr!("sites.no_sites"), tr!("sites.no_sites_description"));
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
            let group = group(tr!("sites.proxies"), "");
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
        let add = gtk::Button::builder().label(tr!("sites.add_proxy")).tooltip_text(tr!("sites.serve_a_server_of_its", tld = tld)).build();
        let this = self.clone();
        add.connect_clicked(move |_| this.add_proxy(&tld));
        add
    }

    fn add_proxy(self: &Rc<Self>, tld: &str) {
        // Stacked: side by side, they don't fit the dialog at the window's smallest.
        let name = adw::EntryRow::builder().title(tr!("sites.proxy_name")).tooltip_text(tr!("sites.e_g_my_app_to", tld = tld)).build();
        let upstream = adw::EntryRow::builder()
            .title(tr!("sites.upstream"))
            .tooltip_text(tr!("sites.n3000_host_port_or_url"))
            .activates_default(true)
            .build();
        let commands = adw::ExpanderRow::builder().title(tr!("sites.proxy.commands_optional")).expanded(false).build();
        let fields = CommandFields::new(&ProxyCommand::default());
        for row in &fields.rows {
            commands.add_row(row);
        }
        let list = gtk::ListBox::builder().css_classes(["boxed-list"]).selection_mode(gtk::SelectionMode::None).build();
        list.append(&name);
        list.append(&upstream);
        list.append(&commands);
        let dialog = adw::AlertDialog::new(Some(tr!("sites.add_proxy_title")), None);
        dialog.set_extra_child(Some(&scrolling_form(&list, &self.stack)));
        dialog.add_responses(&[("cancel", tr!("common.cancel")), ("add", tr!("common.add"))]);
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
            let change = ProxyChange::new(&name, &upstream, None).with_commands(fields.change());
            this.model.perform(
                move |c| c.add_proxy(&change),
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
        let add = gtk::Button::builder().label(tr!("sites.add_site")).build();
        let this = self.clone();
        add.connect_clicked(move |button| {
            let window = button.root().and_downcast::<gtk::Window>();
            let this = this.clone();
            gtk::FileDialog::builder().title(tr!("sites.add_site_linux")).accept_label(tr!("common.add")).build().select_folder(
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
        let button = gtk::Button::builder().label(tr!("sites.new_app")).tooltip_text(tr!("sites.new_app_tooltip")).build();
        button.set_sensitive(!self.creating_app.get());
        let this = self.clone();
        button.connect_clicked(move |_| this.new_app());
        button
    }

    fn new_app(self: &Rc<Self>) {
        if self.creating_app.get() {
            return;
        }
        self.creating_app.set(true);
        self.render();
        let this = self.clone();
        glib::spawn_future_local(async move {
            let result = gio::spawn_blocking(|| Client.app_templates()).await.expect("templates request");
            this.creating_app.set(false);
            this.render();
            let templates = match result {
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

        let name = adw::EntryRow::builder().title(tr!("sites.new_app_name")).activates_default(true).build();
        let template_row = adw::ComboRow::builder()
            .title(tr!("sites.new_app_template"))
            .model(&gtk::StringList::new(&labels))
            .selected(default_idx)
            .build();
        let folder = adw::EntryRow::builder().title(tr!("sites.new_app_folder")).activates_default(true).build();
        folder.set_text(&default_folder);
        let browse = gtk::Button::builder()
            .icon_name("folder-open-symbolic")
            .tooltip_text(tr!("sites.new_app_browse"))
            .valign(gtk::Align::Center)
            .css_classes(["flat"])
            .build();
        folder.add_suffix(&browse);

        let db_row = adw::SwitchRow::builder().title(tr!("sites.new_app_db")).build();
        let db_service_row = adw::ComboRow::builder()
            .title(tr!("sites.new_app_db_service"))
            .model(&gtk::StringList::new(&[tr!("sites.new_app_db_service_auto"), "MySQL", "MariaDB"]))
            .visible(false)
            .build();
        let secured_row = adw::SwitchRow::builder().title(tr!("sites.new_app_https")).build();

        let list = gtk::ListBox::builder().css_classes(["boxed-list"]).selection_mode(gtk::SelectionMode::None).build();
        list.append(&name);
        list.append(&template_row);
        list.append(&folder);
        list.append(&db_row);
        list.append(&db_service_row);
        list.append(&secured_row);

        let dialog = adw::AlertDialog::new(Some(tr!("sites.new_app_title")), None);
        dialog.set_extra_child(Some(&list));
        dialog.add_responses(&[("cancel", tr!("common.cancel")), ("create", tr!("sites.new_app_create"))]);
        dialog.set_response_appearance("create", adw::ResponseAppearance::Suggested);
        dialog.set_default_response(Some("create"));
        dialog.set_response_enabled("create", false);

        let sync: Rc<dyn Fn()> = Rc::new({
            let (weak, templates) = (dialog.downgrade(), templates.clone());
            let (name_w, folder_w, template_w) = (name.downgrade(), folder.downgrade(), template_row.downgrade());
            let (db_w, db_svc_w, secured_w) = (db_row.downgrade(), db_service_row.downgrade(), secured_row.downgrade());
            let tld = tld.clone();
            move || {
                let Some(dialog) = weak.upgrade() else { return };
                let Some(name) = name_w.upgrade() else { return };
                let Some(folder) = folder_w.upgrade() else { return };
                let Some(template_row) = template_w.upgrade() else { return };
                let Some(db_row) = db_w.upgrade() else { return };
                let Some(db_service_row) = db_svc_w.upgrade() else { return };
                let Some(secured_row) = secured_w.upgrade() else { return };
                let tmpl = selected_app_template(&templates, template_row.selected());
                if tmpl.requires_db && !db_row.is_active() {
                    db_row.set_active(true);
                }
                db_row.set_sensitive(!tmpl.requires_db);

                let wants_db = db_row.is_active();
                db_service_row.set_visible(wants_db);
                let subtitle = if !wants_db || has_running_db {
                    ""
                } else if tmpl.requires_db {
                    tr!("sites.new_app_hint_requires_db")
                } else {
                    tr!("sites.new_app_hint_no_db")
                };
                db_row.set_subtitle(subtitle);

                let raw_name = name.text();
                let raw_name = raw_name.trim();
                let raw_folder = folder.text();
                let raw_folder = raw_folder.trim();
                let slug = app_slug(raw_name);
                let valid = !slug.is_empty() && !raw_folder.is_empty();
                dialog.set_response_enabled("create", valid);

                let scheme = if secured_row.is_active() { "https" } else { "http" };
                let preview = if valid {
                    format!("{} → {scheme}://{slug}.{tld}", std::path::Path::new(raw_folder).join(raw_name).display())
                } else {
                    tr!("sites.new_app_preview_hint", scheme = scheme, tld = tld)
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
                let mut builder =
                    gtk::FileDialog::builder().title(tr!("sites.new_app_parent_folder")).accept_label(tr!("sites.server.choose"));
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
            if response != "create" || app_slug(&app_name).is_empty() || parent_dir.is_empty() {
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
        glib::spawn_future_local(async move {
            let (status, err) = gio::spawn_blocking(move || match Client.create_app(&req) {
                Ok(status) => (Some(status), None),
                Err(Error::OutcomeUnknown) => (Client.status().ok(), Some(Error::OutcomeUnknown.to_string())),
                Err(e) => (None, Some(e.to_string())),
            })
            .await
            .expect("create app request");
            this.creating_app.set(false);
            if let Some(status) = status {
                let created_name = status.sites.iter().find(|site| site.path == target_path).map(|site| site.name.clone());
                this.model.apply_status(status);
                if let Some(name) = created_name {
                    this.open(&name);
                } else if err.is_none() {
                    this.open(&fallback_name);
                }
            } else {
                this.render();
            }
            report(&banner, err);
        });
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
        let worktrees_text = trn!("sites.n_worktrees", worktrees.len());
        count.set_tooltip_text(Some(&worktrees_text));
        row.add_suffix(&count);
        for widget in site_suffixes(site) {
            row.add_suffix(&widget);
        }
        let open = gtk::Button::builder()
            .icon_name("go-next-symbolic")
            .tooltip_text(tr!("common.open_target", site = site.name))
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
        if let Some(server) = site.node_server.as_ref().filter(|_| site.is_node()) {
            groups.push(self.dev_server_group(site, server).upcast());
            groups.push(self.output.show(&server.log, server.live()));
        } else {
            groups.push(self.server_group(site).upcast());
            groups.push(self.reverb_group(site).upcast());
            if let Some(reverb) = &site.reverb {
                groups.push(self.output.show(&reverb_log(site, reverb), reverb.running()));
            }
            if site.boost == "missing" || site.boost == "outdated" {
                groups.push(self.boost_group(site).upcast());
            }
        }
        groups.push(self.worktrees_group(site, &status).upcast());
        self.detail.set_groups(&groups);
    }

    fn header(self: &Rc<Self>, site: &SiteStatus) -> adw::PreferencesGroup {
        let header = group("", "");
        let back = adw::ActionRow::builder().title(tr!("settings.pane.sites")).activatable(true).build();
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
            title.add_suffix(&badge(tr!("sites.project_folder_badge"), ""));
        }
        let actions = gtk::Box::builder().spacing(4).valign(gtk::Align::Center).build();
        let icon_button =
            |icon: &str, tooltip: &str| gtk::Button::builder().icon_name(icon).tooltip_text(tooltip).css_classes(["flat"]).build();
        let open = icon_button("dev.flock.Flock-sites-symbolic", tr!("sites.open_in_the_browser"));
        open.set_sensitive(site.url.is_some());
        let url = site.url.clone().unwrap_or_default();
        open.connect_clicked(move |b| launch_uri(b, &url));
        actions.append(&open);
        let folder = icon_button("folder-symbolic", tr!("sites.open_the_project_folder"));
        let path = site.path.clone();
        folder.connect_clicked(move |b| launch_file(b, &path));
        actions.append(&folder);
        if !site.is_node() {
            let extensions = icon_button("application-x-addon-symbolic", tr!("sites.php_extensions"));
            let (this, s) = (self.clone(), site.clone());
            extensions.connect_clicked(move |_| this.show_extensions(&s));
            actions.append(&extensions);
        }
        actions.append(&self.pin_menu(site));
        if !site.parked {
            let remove = icon_button("user-trash-symbolic", tr!("sites.remove_site"));
            let (this, s) = (self.clone(), site.clone());
            remove.connect_clicked(move |_| {
                let name = s.name.clone();
                this.confirm(
                    &tr!("common.remove_question", name = s.name),
                    &tr!("sites.flock_stops_serving_domain_the", domain = s.domain, path = s.path),
                    tr!("common.remove"),
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
        let group = group(tr!("sites.site"), if site.is_node() { "" } else { tr!("sites.document_root_is_the_folder") });
        let kind = adw::ComboRow::builder()
            .title(tr!("sites.type"))
            .model(&gtk::StringList::new(&["PHP", "Node.js"]))
            .selected(u32::from(site.is_node()))
            .build();
        let (this, name) = (self.clone(), site.name.clone());
        kind.connect_selected_notify(move |row| {
            let kind = if row.selected() == 1 { "node" } else { "php" };
            this.change(&name, SiteChange { kind: Some(kind.to_owned()), ..Default::default() });
        });
        group.add(&kind);

        let https = adw::SwitchRow::builder().title("HTTPS").active(site.secured).build();
        let (this, name) = (self.clone(), site.name.clone());
        https.connect_active_notify(move |row| this.change(&name, SiteChange { secured: Some(row.is_active()), ..Default::default() }));
        group.add(&https);

        for domain in site.domains.iter().flatten() {
            let title = if domain.alias { tr!("sites.alias", domain = domain.host) } else { domain.host.clone() };
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
                .tooltip_text(tr!("common.remove_target", domain = domain.host))
                .valign(gtk::Align::Center)
                .css_classes(["flat"])
                .build();
            let (this, s, entry, host) = (self.clone(), site.name.clone(), domain.entry.clone(), domain.host.clone());
            remove.connect_clicked(move |_| {
                let (s2, e2) = (s.clone(), entry.clone());
                this.confirm(
                    &tr!("common.remove_question", name = host),
                    &tr!("sites.domain_is_no_longer_served", domain = host, site = s),
                    tr!("common.remove"),
                    true,
                    move |c| c.remove_domain(&s2, &e2),
                );
            });
            row.add_suffix(&remove);
            group.add(&row);
        }
        let add = adw::ActionRow::builder()
            .title(tr!("sites.add_domain"))
            .subtitle(tr!("sites.extra_domains_subdomains_and_aliases"))
            .activatable(true)
            .build();
        add.add_prefix(&gtk::Image::from_icon_name("list-add-symbolic"));
        let (this, s, dns) = (self.clone(), site.name.clone(), status.dns.clone());
        add.connect_activated(move |_| this.add_domain(&s, &dns));
        group.add(&add);
        if !status.dns.wildcard && !status.dns.wildcard_problem.is_empty() {
            group.add(
                &adw::ActionRow::builder()
                    .title(glib::markup_escape_text(&tr!("sites.subdomains_only_here", problem = status.dns.wildcard_problem)))
                    .css_classes(["warning"])
                    .build(),
            );
        }

        if site.is_node() {
            return group;
        }
        group.add(&self.root_row(site));
        if let Some(hint) = root_hint(site) {
            let row = adw::ActionRow::builder()
                .title(glib::markup_escape_text(&hint))
                .css_classes([if site.root_state.as_deref() == Some("empty") { "dim-label" } else { "warning" }])
                .build();
            if site.root_state.as_deref().unwrap_or_default().is_empty() {
                let (this, name) = (self.clone(), site.name.clone());
                row.add_suffix(&button(tr!("sites.use_flockrc"), false, move || {
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
            trn!("sites.every_subdomain_up_to_n", dns.max_depth.max(1), site = site, tld = tld)
        } else {
            tr!("sites.wildcard_subdomains_dont_work_here", problem = dns.wildcard_problem)
        };
        let entry = gtk::Entry::builder()
            .placeholder_text(tr!("sites.tenant1_a_b_or_site", site = site, tld = tld))
            .activates_default(true)
            .build();
        let dialog = adw::AlertDialog::new(Some(&tr!("sites.add_domain_title", site = site)), Some(&help));
        dialog.set_extra_child(Some(&entry));
        dialog.add_responses(&[("cancel", tr!("common.cancel")), ("add", tr!("common.add"))]);
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
            (true, _, _) => tr!("sites.use_flockrc_root", root = root_label(&site.root_pinned)),
            (_, "default", Some("not_found")) => tr!("sites.root_automatic_none").to_owned(),
            (_, "default", _) => tr!("sites.root_automatic_found", root = root_label(&site.root)),
            _ => tr!("sites.root_automatic").to_owned(),
        };
        let mut folders = site.root_suggestions.clone().unwrap_or_default();
        if site.root_source == "isolated" && !folders.contains(&site.root) {
            folders.push(site.root.clone());
        }
        let mut labels = vec![follow];
        labels.extend(folders.iter().map(|f| root_label(f)));
        labels.push(tr!("sites.choose_folder").to_owned());
        let model = gtk::StringList::new(&labels.iter().map(String::as_str).collect::<Vec<_>>());
        let row = adw::ComboRow::builder().title(tr!("sites.document_root")).model(&model).build();
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
            .title(tr!("sites.choose_the_folder_with_the_apps", site = site.name))
            .accept_label(tr!("sites.server.choose"))
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
                Err(_) => {
                    report(&this.banner, Some(tr!("sites.choose_folder_in_project_details", folder = folder.display(), project = s.path)))
                }
            }
        });
    }

    fn share_group(self: &Rc<Self>, site: &SiteStatus) -> adw::PreferencesGroup {
        let group = group(tr!("sites.share.share"), "");
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
                group.set_description(Some(tr!("sites.share.explanation")));
                let row = adw::ActionRow::new();
                let (this, n) = (self.clone(), name.clone());
                let share = button(tr!("sites.share.share_ellipsis"), false, move || this.ask_share(&n));
                share.set_sensitive(site.url.is_some());
                row.add_suffix(&share);
                group.add(&row);
            }
            Some(share) if share.state == "running" => self.running_share(&group, &name, share, stop),
            Some(share) if share.state == "failed" => {
                let row = adw::ActionRow::builder()
                    .title(glib::markup_escape_text(if share.error.is_empty() { tr!("sites.sharing_failed") } else { &share.error }))
                    .css_classes(["warning"])
                    .build();
                let (this, n, allow, debug) = (self.clone(), name.clone(), share.allow.clone().unwrap_or_default(), share.debug);
                row.add_suffix(&button(tr!("setup.try_again"), false, move || this.start_share(&n, allow.clone(), debug)));
                row.add_suffix(&button(tr!("sites.share.dismiss"), false, stop));
                group.add(&row);
            }
            Some(share) => {
                let row = adw::ActionRow::builder()
                    .title(if share.state == "building" { tr!("sites.share.building") } else { tr!("sites.share.starting") })
                    .build();
                row.add_prefix(&gtk::Spinner::builder().spinning(true).build());
                row.add_suffix(&button(tr!("sites.share.stop"), false, stop));
                group.add(&row);
            }
        }
        group
    }

    fn running_share(self: &Rc<Self>, group: &adw::PreferencesGroup, name: &str, share: &ShareStatus, stop: impl Fn() + 'static) {
        let row = adw::ActionRow::builder().title(glib::markup_escape_text(&share.url)).title_selectable(true).build();
        let url = share.url.clone();
        row.add_suffix(&button(tr!("common.copy"), false, {
            let url = url.clone();
            let row = row.clone();
            move || row.clipboard().set_text(&url)
        }));
        let open = gtk::Button::builder()
            .icon_name("dev.flock.Flock-sites-symbolic")
            .tooltip_text(tr!("common.open"))
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
            picture.set_tooltip_text(Some(&tr!("sites.qr_code_of_url", url = share.url)));
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
            notes.push(tr!("sites.share.ends_at", time = expires.format("%H:%M").map(|s| s.to_string()).unwrap_or_default()));
        }
        let allow = share.allow.clone().unwrap_or_default();
        if !allow.is_empty() {
            notes.push(tr!("sites.share.only_for", emails = allow.join(", ")));
        }
        if share.debug {
            notes.push(tr!("sites.share.debug_on").to_owned());
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
            let rebuild =
                button(if share.rebuilding { tr!("sites.share.rebuilding") } else { tr!("sites.rebuild_assets") }, false, move || {
                    let (this, n) = (this.clone(), n.clone());
                    glib::spawn_future_local(async move {
                        let result = gio::spawn_blocking(move || Client.rebuild_share(&n)).await.expect("share request");
                        report(&this.banner, result.err().map(|e| e.to_string()));
                        this.model.refresh();
                    });
                });
            rebuild.set_sensitive(!share.rebuilding);
            rebuild.set_tooltip_text(Some(tr!("sites.runs_npm_run_build_again")));
            actions.add_suffix(&rebuild);
        }
        actions.add_suffix(&button(tr!("sites.stop_sharing"), false, stop));
        group.add(&actions);
    }

    /// Asks who may open the share (anyone with the link, or only the emails listed) and whether
    /// visitors see the app's debug mode.
    fn ask_share(self: &Rc<Self>, name: &str) {
        let allow = adw::EntryRow::builder().title(tr!("sites.share.allow_header")).build();
        allow.set_tooltip_text(Some("colleague@acme.com, *@client.com"));
        let debug =
            adw::SwitchRow::builder().title(tr!("sites.share.show_errors")).subtitle(tr!("sites.share.show_errors_explanation")).build();
        let list = gtk::ListBox::builder().css_classes(["boxed-list"]).selection_mode(gtk::SelectionMode::None).build();
        list.append(&allow);
        list.append(&debug);
        let dialog = adw::AlertDialog::new(Some(&tr!("sites.share.title", site = name)), Some(tr!("sites.share.allow_explanation")));
        dialog.set_extra_child(Some(&list));
        dialog.add_responses(&[("cancel", tr!("common.cancel")), ("share", tr!("sites.share.share"))]);
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
        let group = group(tr!("versions.versions"), tr!("sites.version_picked_here_applies_on_this_machine"));
        let php: Vec<String> = config.php.iter().flatten().map(|p| p.version.clone()).collect();
        let mut node: Vec<String> = config.node.iter().flatten().map(|v| major(v)).collect();
        node.dedup();
        if !site.is_node() {
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
        }
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
        if !site.is_node() && site.php_source == "isolated" && !site.php_pinned.is_empty() && site.php_pinned != site.php {
            let file = file_name(&site.php_file);
            let row = adw::ActionRow::builder()
                .title(glib::markup_escape_text(&tr!(
                    "sites.php_version_is_isolated_here",
                    version = site.php,
                    pinned = site.php_pinned,
                    file = file
                )))
                .css_classes(["warning"])
                .build();
            let (this, name) = (self.clone(), site.name.clone());
            row.add_suffix(&button(&tr!("sites.use_file", file = file), false, move || {
                this.change(&name, SiteChange { php: Some(String::new()), ..Default::default() })
            }));
            group.add(&row);
        }
        if site.node_source == "isolated" && !site.node_pinned.is_empty() && major(&site.node_pinned) != major(&site.node) {
            let file = file_name(&site.node_file);
            let row = adw::ActionRow::builder()
                .title(glib::markup_escape_text(&tr!(
                    "sites.node_version_is_isolated_here",
                    version = site.node,
                    pinned = site.node_pinned,
                    file = file
                )))
                .css_classes(["warning"])
                .build();
            let (this, name) = (self.clone(), site.name.clone());
            row.add_suffix(&button(&tr!("sites.use_file", file = file), false, move || {
                this.change(&name, SiteChange { node: Some(String::new()), ..Default::default() })
            }));
            group.add(&row);
        }
        if !site.node_shadowed.is_empty() {
            group.add(
                &adw::ActionRow::builder()
                    .title(glib::markup_escape_text(&tr!("sites.node_wins", pinned = site.node_pinned, other = site.node_shadowed)))
                    .css_classes(["warning"])
                    .build(),
            );
        }
        group
    }

    fn server_group(self: &Rc<Self>, site: &SiteStatus) -> adw::PreferencesGroup {
        let footer = if site.server == "frankenphp" && !site.octane_available {
            tr!("sites.frankenphp_needs_php_8_2_or")
        } else {
            tr!("sites.frankenphp_needs_php_8_2")
        };
        let group = group(tr!("sites.server"), footer);
        let franken = site.server == "frankenphp";
        let runs_on = adw::ComboRow::builder()
            .title(tr!("sites.runs_on"))
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
            .title(tr!("debugger.mode"))
            .model(&gtk::StringList::new(&[tr!("sites.classic"), "Laravel Octane"]))
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
                    let mut text = tr!("sites.frankenphp_running", version = franken.version, php = franken.php_version);
                    if octane {
                        text.push_str(tr!("sites.worker_mode"));
                    }
                    (text, "dim-label")
                }
                "installing" => {
                    let mut text = tr!("sites.downloading_frankenphp", version = site.php);
                    if (0.0..=1.0).contains(&franken.progress) {
                        text.push(' ');
                        text.push_str(&tr!("sites.percent", percent = (franken.progress * 100.0).round()));
                    }
                    (format!("{text}\n{}", tr!("sites.php_fpm_serves_the_site_until")), "dim-label")
                }
                _ => (format!("{}\n{}", franken.problem, tr!("sites.php_fpm_serves_the_site")), "warning"),
            };
            group.add(&adw::ActionRow::builder().title(glib::markup_escape_text(text.trim())).css_classes([class]).build());
        }
        let actions = adw::ActionRow::new();
        let restarting = self.restarting.borrow().as_deref() == Some(site.name.as_str());
        if restarting {
            actions.add_suffix(&gtk::Spinner::builder().spinning(true).build());
        }
        let (this, name) = (self.clone(), site.name.clone());
        let restart = button(tr!("menu.restart"), false, move || this.restart(&name));
        restart.set_sensitive(site.frankenphp.as_ref().is_some_and(|f| f.state == "running") && !restarting);
        restart.set_tooltip_text(Some(tr!("sites.restart_frankenphp_tooltip")));
        actions.add_suffix(&restart);
        let log = site
            .frankenphp
            .as_ref()
            .map(|f| f.log.clone())
            .filter(|l| !l.is_empty())
            .unwrap_or_else(|| format!("frankenphp-{}", site.name));
        let show_log = gtk::Button::builder().label(tr!("sites.show_log")).valign(gtk::Align::Center).action_name("app.open").build();
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

    /// Laravel Reverb for a PHP site: Run Reverb, then its state, the .env lines the app needs and
    /// Restart. Its output follows the group.
    fn reverb_group(self: &Rc<Self>, site: &SiteStatus) -> adw::PreferencesGroup {
        let address = site.reverb_url();
        let footer = if site.reverb.is_some() {
            tr!("sites.reverb.footnote").to_owned()
        } else if site.reverb_available {
            tr!("sites.reverb.explanation", address = address)
        } else {
            tr!("sites.reverb.install").to_owned()
        };
        let group = group(tr!("sites.reverb.title"), &footer);
        let (this, name) = (self.clone(), site.name.clone());
        let run = switch_row(tr!("sites.reverb.run"), "", site.reverb.is_some(), move |on| {
            this.change(&name, SiteChange { reverb: Some(on), ..Default::default() });
        });
        run.set_sensitive(site.reverb_available || site.reverb.is_some());
        group.add(&run);
        let Some(reverb) = &site.reverb else { return group };

        let (text, dot, class) = match reverb.state.as_str() {
            "running" => (tr!("sites.reverb.running_at", address = address), "success", ""),
            "unavailable" => (reverb.problem.clone(), "warning", "warning"),
            _ if reverb.problem.is_empty() => (tr!("sites.server.failed").to_owned(), "error", ""),
            _ => (reverb.problem.clone(), "error", ""),
        };
        let state = adw::ActionRow::builder().title(glib::markup_escape_text(&text)).build();
        if !class.is_empty() {
            state.add_css_class(class);
        }
        state.add_prefix(&gtk::Label::builder().label("●").css_classes([dot]).build());
        let restarting = self.restarting_reverb.borrow().as_deref() == Some(site.name.as_str());
        if restarting {
            state.add_suffix(&gtk::Spinner::builder().spinning(true).build());
        }
        let (this, name) = (self.clone(), site.name.clone());
        let restart = button(tr!("menu.restart"), false, move || this.restart_reverb(&name));
        restart.set_sensitive(reverb.running() && !restarting);
        restart.set_tooltip_text(Some(tr!("sites.reverb.restart_tooltip")));
        state.add_suffix(&restart);
        group.add(&state);

        if !reverb.env.is_empty() {
            let env = reverb.env.join("\n");
            let lines = code_block(&env);
            let row = adw::ActionRow::builder().title(tr!("sites.reverb.env_needs")).build();
            row.add_suffix(&button(tr!("common.copy"), false, {
                let row = row.clone();
                move || row.clipboard().set_text(&env)
            }));
            group.add(&row);
            group.add(&lines);
        }
        group
    }

    fn restart_reverb(self: &Rc<Self>, name: &str) {
        self.restarting_reverb.replace(Some(name.to_owned()));
        self.render();
        let (this, banner, n) = (self.clone(), self.banner.clone(), name.to_owned());
        self.model.perform(
            move |c| c.restart_reverb(&n),
            move |e| {
                this.restarting_reverb.replace(None);
                report(&banner, e);
                this.render();
            },
        );
    }

    /// A Node.js site's dev server: its state with Start, Stop and Restart, and the command it runs.
    fn dev_server_group(self: &Rc<Self>, site: &SiteStatus, server: &SiteNodeServer) -> adw::PreferencesGroup {
        let group = group(tr!("sites.server"), tr!("sites.flock_runs_the_projects_dev"));
        let mut subtitle = server.text();
        if let Some(note) = server.note() {
            subtitle.push('\n');
            subtitle.push_str(note);
        }
        let row = adw::ActionRow::builder().title(tr!("sites.dev_server")).subtitle(glib::markup_escape_text(&subtitle)).build();
        row.add_prefix(&state_dot(&server.state));
        let restarting = self.restarting.borrow().as_deref() == Some(site.name.as_str());
        if restarting {
            row.add_suffix(&gtk::Spinner::builder().spinning(true).build());
        }
        let name = site.name.clone();
        let buttons: Vec<gtk::Button> = match server.state.as_str() {
            "sleeping" | "failed" => {
                let this = self.clone();
                vec![button(tr!("services.start"), false, move || this.restart(&name))]
            }
            state => {
                let (this, n) = (self.clone(), name.clone());
                let mut buttons = vec![button(tr!("sites.share.stop"), false, move || {
                    let n = n.clone();
                    this.perform(move |c| c.stop_site(&n));
                })];
                if state == "running" {
                    let this = self.clone();
                    buttons.push(button(tr!("menu.restart"), false, move || this.restart(&name)));
                }
                buttons
            }
        };
        for button in buttons {
            button.set_sensitive(!restarting);
            row.add_suffix(&button);
        }
        group.add(&row);

        let command = adw::ActionRow::builder().title(tr!("sites.command")).build();
        command.add_suffix(
            &gtk::Label::builder()
                .label(&server.command)
                .selectable(true)
                .css_classes(["monospace", "dim-label"])
                .ellipsize(gtk::pango::EllipsizeMode::Middle)
                .build(),
        );
        group.add(&command);
        group
    }

    fn boost_group(self: &Rc<Self>, site: &SiteStatus) -> adw::PreferencesGroup {
        let missing = site.boost == "missing";
        let group = group("Laravel Boost", if missing { tr!("sites.boost_missing") } else { tr!("sites.boost_outdated") });
        let row = adw::ActionRow::new();
        let (this, s) = (self.clone(), site.clone());
        row.add_suffix(&button(if missing { tr!("sites.boost_add") } else { tr!("sites.boost_update") }, false, move || {
            let name = s.name.clone();
            this.confirm(
                &if missing { tr!("sites.boost_add_question", site = s.name) } else { tr!("sites.boost_update_question", site = s.name) },
                &tr!("sites.writes_ai_guidelines_flock_md", path = s.path),
                if missing { tr!("common.add") } else { tr!("common.update") },
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
        let can_pin_php = !site.is_node() && !(site.php_source == "project" && site.php_pinned_here());
        let can_pin_node = !node.is_empty() && !(site.node_source == "project" && site.node_pinned_here());
        let state = site.root_state.clone().unwrap_or_default();
        let can_pin_root = !site.is_node() && (state.is_empty() || state == "empty") && site.root_source != "project";
        let mut items: Vec<MenuItem> = Vec::new();
        let s = site.clone();
        let pin = {
            let this = self.clone();
            move |title: String, body: String, tool: &'static str, version: String| -> Box<dyn Fn()> {
                let (this, name) = (this.clone(), s.name.clone());
                Box::new(move || {
                    let (n, v) = (name.clone(), version.clone());
                    this.confirm(&title, &body, tr!("sites.pin"), false, move |c| c.pin(&n, tool, &v));
                })
            }
        };
        if can_pin_php {
            items.push((
                tr!("sites.pin_php_item", version = site.php),
                true,
                false,
                pin(
                    tr!("sites.pin_php_question", version = site.php, site = site.name),
                    tr!("sites.writes_php_version_to_flockrc", version = site.php, path = site.path),
                    "php",
                    site.php.clone(),
                ),
            ));
        }
        if can_pin_node {
            items.push((
                tr!("sites.pin_node_item", version = node),
                true,
                false,
                pin(
                    tr!("sites.pin_node_question", version = node, site = site.name),
                    tr!("sites.writes_version_to_file_in", version = node, file = node_file, path = site.path),
                    "node",
                    node.clone(),
                ),
            ));
        }
        if can_pin_root {
            let label = root_label(&site.root);
            items.push((
                tr!("sites.pin_root_item", root = label),
                true,
                false,
                pin(
                    tr!("sites.pin_root_question", root = label, site = site.name),
                    tr!("sites.writes_root_value_to_flockrc", value = site.root, path = site.path, root = label),
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
                    this.confirm(&title, &body, tr!("sites.unpin"), true, move |c| c.unpin(&n, tool));
                }),
            )
        };
        if site.php_pinned_here() {
            items.push(unpin(
                tr!("sites.unpin_php_item", file = file_name(&site.php_file)),
                tr!("sites.unpin_php_question", site = site.name),
                tr!("sites.removes_the_php_version_from", file = site.php_file),
                "php",
            ));
        }
        if site.node_pinned_here() {
            items.push(unpin(
                tr!("sites.unpin_node_item", file = file_name(&site.node_file)),
                tr!("sites.unpin_node_question", site = site.name),
                tr!("sites.removes_the_node_js_version", path = site.path),
                "node",
            ));
        }
        if site.root_pinned_here() {
            items.push(unpin(
                tr!("sites.unpin_root_item").to_owned(),
                tr!("sites.unpin_root_question", site = site.name),
                tr!("sites.removes_root_from_flockrc_in", path = site.path),
                "root",
            ));
        }
        let enabled = !items.is_empty();
        let menu = more_menu(items);
        menu.set_icon_name("view-pin-symbolic");
        menu.set_tooltip_text(Some(tr!("sites.pin_menu_tooltip")));
        menu.set_sensitive(enabled);
        menu
    }

    // MARK: worktrees

    /// A site's worktrees and New Worktree….
    fn worktrees_group(self: &Rc<Self>, site: &SiteStatus, status: &Status) -> adw::PreferencesGroup {
        let group = group(tr!("worktrees.worktrees"), &tr!("sites.each_git_worktree_of_site", site = site.name, domain = site.domain));
        let add = gtk::Button::builder().label(tr!("worktrees.new")).css_classes(["flat"]).build();
        let (this, s) = (self.clone(), site.clone());
        add.connect_clicked(move |_| this.new_worktree(&s));
        group.set_header_suffix(Some(&add));
        let worktrees = status.worktrees_of(&site.name);
        if worktrees.is_empty() {
            group.add(&adw::ActionRow::builder().title(tr!("sites.no_worktrees")).subtitle(tr!("sites.create_one_here_with_git")).build());
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
            .placeholder_text(tr!("worktrees.new.branch_placeholder"))
            .activates_default(true)
            .valign(gtk::Align::Center)
            .hexpand(true)
            .build();
        let branch_row = adw::ActionRow::builder().title(tr!("worktrees.new.branch")).build();
        branch_row.add_suffix(&branch);
        let from_model = gtk::StringList::new(&[tr!("worktrees.new.current_commit")]);
        let from = adw::ComboRow::builder().title(tr!("worktrees.new.start_from")).model(&from_model).sensitive(false).build();
        let database = adw::ComboRow::builder()
            .title(tr!("worktrees.new.database"))
            .model(&gtk::StringList::new(&[
                tr!("worktrees.project_default"),
                i18n::translate(DB_CHOICES[0]),
                i18n::translate(DB_CHOICES[1]),
                i18n::translate(DB_CHOICES[2]),
            ]))
            .build();
        let list = gtk::ListBox::builder().css_classes(["boxed-list"]).selection_mode(gtk::SelectionMode::None).build();
        list.append(&branch_row);
        list.append(&from);
        list.append(&database);
        let dialog = adw::AlertDialog::new(Some(&tr!("worktrees.new_title", site = site.name)), Some(&new_worktree_text(site, "", false)));
        dialog.set_extra_child(Some(&list));
        dialog.add_responses(&[("cancel", tr!("common.cancel")), ("create", tr!("worktrees.create"))]);
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
            let (title, site) = (tr!("worktrees.new_title", site = name), name.clone());
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
        let dialog = adw::AlertDialog::new(Some(&tr!("worktrees.could_not_remove", name = removal.label)), Some(error));
        dialog.add_responses(&[("cancel", tr!("common.cancel")), ("force", tr!("worktrees.remove_anyway"))]);
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
        // A Node.js site's worktree runs a dev server of its own.
        if let Some(server) = site.node_server.as_ref().filter(|_| site.is_node()) {
            groups.push(self.dev_server_group(site, server).upcast());
            groups.push(self.output.show(&server.log, server.live()));
        }
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
        title.add_suffix(&badge(tr!("worktrees.worktree"), ""));
        let open = gtk::Button::builder()
            .icon_name("dev.flock.Flock-sites-symbolic")
            .tooltip_text(tr!("sites.open_in_the_browser"))
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
                .tooltip_text(tr!("worktrees.remove"))
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
        let group = group(tr!("worktrees.worktree"), "");
        group.add(&info_row(
            tr!("worktrees.new.branch"),
            if site.branch.is_empty() { tr!("worktrees.detached_head") } else { site.branch.as_str() },
        ));
        let folder = info_row(tr!("general.folder"), &site.path);
        let open_folder = gtk::Button::builder()
            .icon_name("folder-symbolic")
            .tooltip_text(tr!("sites.open_the_folder"))
            .valign(gtk::Align::Center)
            .css_classes(["flat"])
            .build();
        let path = site.path.clone();
        open_folder.connect_clicked(move |b| launch_file(b, &path));
        folder.add_suffix(&open_folder);
        group.add(&folder);
        let address = info_row(tr!("sites.url"), site.url.as_deref().unwrap_or(&site.domain));
        if let Some(url) = site.url.clone() {
            address.set_activatable(true);
            address.connect_activated(move |r| launch_uri(r, &url));
        }
        group.add(&address);
        let versions = [("PHP", &site.php, &site.php_source, &site.php_file), ("Node.js", &site.node, &site.node_source, &site.node_file)];
        for (label, version, source, file) in versions.into_iter().filter(|(label, ..)| !site.is_node() || *label != "PHP") {
            let row = info_row(label, if version.is_empty() { "—" } else { version.as_str() });
            if source == "project" && !file.is_empty() {
                row.set_subtitle(&glib::markup_escape_text(&tr!("sites.branch_pins_it_in_file", file = file_name(file))));
            }
            group.add(&row);
        }
        let settings = adw::ActionRow::builder()
            .title(glib::markup_escape_text(&tr!("sites.uses_site_s_settings", site = site.parent)))
            .subtitle(tr!("sites.change_its_php_and_node"))
            .activatable(true)
            .build();
        settings.add_suffix(&gtk::Image::from_icon_name("go-next-symbolic"));
        let (this, parent) = (self.clone(), site.parent.clone());
        settings.connect_activated(move |_| this.open(&parent));
        group.add(&settings);
        group
    }

    fn database_group(self: &Rc<Self>, site: &SiteStatus, setup: &WorktreeStatus) -> adw::PreferencesGroup {
        let group = group(tr!("worktrees.new.database"), tr!("sites.database_of_its_own_gets"));
        // The project's default mode isn't in the status: a database of its own is most likely a copy.
        let current: u32 = match setup.db_mode.as_str() {
            "shared" => 0,
            "copy" => 1,
            "fresh" => 2,
            _ if !setup.database.is_empty() => 1,
            _ => 0,
        };
        let mode = adw::ComboRow::builder()
            .title(tr!("worktrees.new.database"))
            .model(&gtk::StringList::new(&DB_CHOICES.map(i18n::translate)))
            .selected(current)
            .build();
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
            (Some(name), _) => {
                (tr!("worktrees.drop_question", database = name), tr!("worktrees.its_database_database_is_dropped", database = name))
            }
            (None, "shared") => (
                tr!("worktrees.use_site_s_database", site = parent),
                tr!("sites.worktree_then_uses_site_s", worktree = label, site = parent),
            ),
            (None, "copy") => {
                let body = tr!("sites.flock_copies_site_s_database", site = parent);
                (tr!("sites.give_worktree_a_copy_of", worktree = label), body)
            }
            (None, _) => (tr!("sites.give_worktree_a_fresh_database", worktree = label), tr!("worktrees.give_fresh_details").to_owned()),
        };
        let dialog = adw::AlertDialog::new(Some(&title), Some(&body));
        dialog.add_responses(&[("cancel", tr!("common.cancel")), ("switch", tr!("worktrees.switch"))]);
        dialog.set_response_appearance(
            "switch",
            if own.is_some() { adw::ResponseAppearance::Destructive } else { adw::ResponseAppearance::Suggested },
        );
        let (this, path, db) = (self.clone(), site.path.clone(), db.to_owned());
        let job_title = tr!("worktrees.switching_database", worktree = label);
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
        let group = group(tr!("worktrees.setup"), "");
        let Some(setup) = &site.worktree else {
            let row = adw::ActionRow::builder()
                .title(tr!("sites.flock_hasnt_set_this_worktree"))
                .subtitle(tr!("sites.setting_it_up_copies_dependencies"))
                .build();
            let (this, s) = (self.clone(), site.clone());
            row.add_suffix(&button(tr!("worktrees.set_up_action"), false, move || this.run_setup(&s, false)));
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
            let show_log = gtk::Button::builder().label(tr!("sites.show_log")).valign(gtk::Align::Center).action_name("app.open").build();
            show_log.set_action_target_value(Some(&format!("logs/services/{}", setup.log).to_variant()));
            row.add_suffix(&show_log);
        }
        let (this, s) = (self.clone(), site.clone());
        let again = button(tr!("worktrees.run_again"), false, move || this.run_setup(&s, true));
        again.set_sensitive(!setup.busy());
        row.add_suffix(&again);
        group.add(&row);
        group
    }

    /// Sets the worktree up (again), in the progress dialog.
    fn run_setup(self: &Rc<Self>, site: &SiteStatus, again: bool) {
        let path = site.path.clone();
        let start: worktree_job::Start = Box::new(move |c: Client| c.setup_worktree(&path, again).map(|_| path));
        worktree_job::open(
            &self.stack,
            self.model.clone(),
            &tr!("worktrees.setting_up_title", worktree = site.worktree_label()),
            start,
            |_| {},
        );
    }

    fn ask_remove(self: &Rc<Self>, site: &SiteStatus) {
        let label = site.worktree_label();
        let mut body = match site.own_database() {
            Some(db) => {
                tr!("worktrees.remove_details_database", path = site.path, database = db)
            }
            None => tr!("worktrees.remove_details", path = site.path),
        };
        if !site.branch.is_empty() {
            body.push(' ');
            body.push_str(&tr!("worktrees.branch_stays", branch = site.branch));
        }
        let dialog = adw::AlertDialog::new(Some(&tr!("common.remove_question", name = label)), Some(&body));
        dialog.add_responses(&[("cancel", tr!("common.cancel")), ("remove", tr!("common.remove"))]);
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
        let mut groups: Vec<gtk::Widget> =
            vec![self.proxy_header(proxy).upcast(), self.proxy_group(proxy).upcast(), self.commands_group(proxy).upcast()];
        if let Some(command) = &proxy.command {
            groups.push(self.output.show(&command.log, command.live()));
        }
        self.detail.set_groups(&groups);
    }

    fn proxy_header(self: &Rc<Self>, proxy: &ProxyStatus) -> adw::PreferencesGroup {
        let header = group("", "");
        let back = adw::ActionRow::builder().title(tr!("settings.pane.sites")).activatable(true).build();
        back.add_prefix(&gtk::Image::from_icon_name("go-previous-symbolic"));
        let this = self.clone();
        back.connect_activated(move |_| this.back());
        header.add(&back);

        let title = adw::ActionRow::builder()
            .title(glib::markup_escape_text(&proxy.name))
            .subtitle(glib::markup_escape_text(proxy.url.as_deref().unwrap_or(&proxy.domain)))
            .subtitle_selectable(true)
            .build();
        title.add_suffix(&badge(tr!("sites.proxy"), ""));
        let actions = gtk::Box::builder().spacing(4).valign(gtk::Align::Center).build();
        let open = gtk::Button::builder()
            .icon_name("dev.flock.Flock-sites-symbolic")
            .tooltip_text(tr!("sites.open_in_the_browser"))
            .css_classes(["flat"])
            .build();
        open.set_sensitive(proxy.url.is_some());
        let url = proxy.url.clone().unwrap_or_default();
        open.connect_clicked(move |b| launch_uri(b, &url));
        actions.append(&open);
        let remove =
            gtk::Button::builder().icon_name("user-trash-symbolic").tooltip_text(tr!("sites.remove_proxy")).css_classes(["flat"]).build();
        let (this, p) = (self.clone(), proxy.clone());
        remove.connect_clicked(move |_| {
            let name = p.name.clone();
            this.confirm(
                &tr!("common.remove_question", name = p.name),
                &tr!("sites.remove_proxy_details", domain = p.domain, upstream = p.upstream),
                tr!("common.remove"),
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
        let footer = if proxy.command.is_some() {
            tr!("sites.every_request_passes_to_the", domain = proxy.domain)
        } else {
            tr!("sites.proxy_footnote_no_commands", domain = proxy.domain)
        };
        let group = group(tr!("sites.proxy"), &footer);
        match &proxy.command {
            Some(command) => {
                let row = self.command_state_row(&proxy.name, command);
                row.add_prefix(&proxy_dot(proxy));
                group.add(&row);
            }
            None => {
                let server = info_row(tr!("sites.server"), &proxy.server_text());
                server.add_prefix(&status_dot(proxy.running));
                if !proxy.running {
                    let note = tr!("sites.nothing_answers_yet", upstream = proxy.upstream_label());
                    server.set_subtitle(&glib::markup_escape_text(&note));
                }
                group.add(&server);
            }
        }

        let https = adw::SwitchRow::builder().title("HTTPS").active(proxy.secured).build();
        let (this, name) = (self.clone(), proxy.name.clone());
        https.connect_active_notify(move |row| {
            let (name, on) = (name.clone(), row.is_active());
            this.perform(move |c| c.secure_proxy(&name, on));
        });
        group.add(&https);

        let upstream = info_row(tr!("sites.upstream"), &proxy.upstream);
        let (this, p) = (self.clone(), proxy.clone());
        upstream.add_suffix(&button(tr!("sites.change"), false, move || this.change_upstream(&p)));
        group.add(&upstream);
        group
    }

    /// The commands that start and stop a proxy's server and the folder they run in, or Add
    /// Commands… when it has none.
    fn commands_group(self: &Rc<Self>, proxy: &ProxyStatus) -> adw::PreferencesGroup {
        let group = group(tr!("sites.proxy.commands"), "");
        let (this, p) = (self.clone(), proxy.clone());
        match &proxy.command {
            Some(command) => {
                group.add(&info_row(tr!("services.start"), &command.command));
                if !command.stop_command.is_empty() {
                    group.add(&info_row(tr!("sites.share.stop"), &command.stop_command));
                }
                group.add(&info_row(tr!("general.folder"), &command.dir));
                group.set_header_suffix(Some(&button(tr!("sites.change"), false, move || this.change_command(&p))));
            }
            None => {
                group.set_description(Some(tr!("sites.give_flock_the_commands_that")));
                let row = adw::ActionRow::new();
                row.add_suffix(&button(tr!("sites.proxy.add_commands"), false, move || this.change_command(&p)));
                group.add(&row);
            }
        }
        group
    }

    /// A proxy's server that Flock starts: its state, with Start or Stop.
    fn command_state_row(self: &Rc<Self>, name: &str, command: &ProxyCommand) -> adw::ActionRow {
        let mut subtitle = command.text();
        if let Some(note) = command.note() {
            subtitle.push('\n');
            subtitle.push_str(note);
        }
        let row = adw::ActionRow::builder().title(tr!("sites.server")).subtitle(glib::markup_escape_text(&subtitle)).build();
        let (this, name) = (self.clone(), name.to_owned());
        let action = if command.can_start() {
            button(tr!("services.start"), false, move || {
                let name = name.clone();
                this.perform(move |c| c.start_proxy(&name));
            })
        } else {
            button(tr!("sites.share.stop"), false, move || {
                let name = name.clone();
                this.perform(move |c| c.stop_proxy(&name));
            })
        };
        action.set_sensitive(command.state != "stopping");
        row.add_suffix(&action);
        row
    }

    /// Asks for the commands that start and, for a server that runs in the background, stop a
    /// proxy's server, and the folder they run in. Remove Commands (or an empty Start) removes them.
    fn change_command(self: &Rc<Self>, proxy: &ProxyStatus) {
        let current = proxy.command.clone().unwrap_or_default();
        let fields = CommandFields::new(&current);
        let list = gtk::ListBox::builder().css_classes(["boxed-list"]).selection_mode(gtk::SelectionMode::None).build();
        for row in &fields.rows {
            list.append(row);
        }
        let dialog = adw::AlertDialog::new(Some(&tr!("sites.proxy.commands_title", proxy = proxy.name)), None);
        dialog.set_extra_child(Some(&scrolling_form(&list, &self.stack)));
        dialog.add_response("cancel", tr!("common.cancel"));
        if !current.command.is_empty() {
            dialog.add_response("remove", tr!("sites.proxy.remove_commands"));
            dialog.set_response_appearance("remove", adw::ResponseAppearance::Destructive);
        }
        dialog.add_response("save", tr!("common.save"));
        dialog.set_response_appearance("save", adw::ResponseAppearance::Suggested);
        dialog.set_default_response(Some("save"));
        let (this, name) = (self.clone(), proxy.name.clone());
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            let change = match response.as_str() {
                "save" => fields.change(),
                "remove" => ProxyCommandChange::default(),
                _ => return,
            };
            let name = name.clone();
            this.perform(move |c| c.update_proxy(&name, &change));
        });
    }

    /// Asks for a proxy's new upstream; its HTTPS stays as it is.
    fn change_upstream(self: &Rc<Self>, proxy: &ProxyStatus) {
        let entry = gtk::Entry::builder()
            .text(proxy.upstream_label())
            .placeholder_text(tr!("sites.upstream_placeholder"))
            .activates_default(true)
            .build();
        let dialog = adw::AlertDialog::new(
            Some(&tr!("sites.change_upstream_title", proxy = proxy.name)),
            Some(tr!("sites.change_upstream_details")),
        );
        dialog.set_extra_child(Some(&entry));
        dialog.add_responses(&[("cancel", tr!("common.cancel")), ("change", tr!("sites.change_action"))]);
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
        let status = gtk::Label::builder().label(tr!("common.loading")).css_classes(["dim-label"]).build();
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
                .label(tr!("sites.extensions_footnote"))
                .wrap(true)
                .xalign(0.0)
                .css_classes(["caption", "dim-label"])
                .build(),
        );
        let view = adw::ToolbarView::new();
        view.add_top_bar(&adw::HeaderBar::new());
        view.set_content(Some(&gtk::ScrolledWindow::builder().child(&column).vexpand(true).build()));
        let dialog = adw::Dialog::builder()
            .title(tr!("sites.extensions_title", site = site.name))
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
                            tr!("sites.extensions_none").to_owned()
                        } else if report.laravel {
                            tr!("sites.extensions_laravel", version = report.php)
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
    let mut detail = if declared {
        tr!("sites.extension_required_by", packages = required_by.join(", "))
    } else {
        tr!("sites.extension_used", hint = ext.hint)
    };
    if !ext.available {
        detail.push_str(&tr!("sites.flocks_php_version_doesnt_include", version = php));
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
        let add = gtk::Button::builder().label(tr!("sites.add_to_composer_json")).valign(gtk::Align::Center).build();
        let (name, path) = (ext.name.clone(), path.to_owned());
        add.connect_clicked(move |button| {
            let dialog = adw::AlertDialog::new(
                Some(&tr!("sites.require_ext_name_in_composer", name = name)),
                Some(&tr!("sites.require_tooltip", name = name, path = path)),
            );
            dialog.add_responses(&[("cancel", tr!("common.cancel")), ("require", tr!("sites.require"))]);
            dialog.set_response_appearance("require", adw::ResponseAppearance::Suggested);
            let (name, require, button) = (name.clone(), require.clone(), button.clone());
            dialog.choose(Some(&button.clone()), gio::Cancellable::NONE, move |response| {
                if response == "require" {
                    button.set_sensitive(false);
                    button.set_label(tr!("sites.adding"));
                    require(name.clone());
                }
            });
        });
        row.add_suffix(&add);
    }
    row
}

/// A dialog's form, which scrolls when it's taller than the window leaves room for: the dialog's
/// heading and buttons stay visible. The window has no minimum size of its own (its content's), so
/// the room comes from its height now, between 120 and 480 pixels.
fn scrolling_form(form: &impl IsA<gtk::Widget>, parent: &impl IsA<gtk::Widget>) -> gtk::ScrolledWindow {
    // The heading, the buttons and the dialog's margins take about this much.
    const CHROME: i32 = 220;
    let window = parent.root().map_or(640, |root| root.height());
    gtk::ScrolledWindow::builder()
        .child(form)
        .hscrollbar_policy(gtk::PolicyType::Never)
        .propagate_natural_height(true)
        .max_content_height((window - CHROME).clamp(120, 480))
        .build()
}

/// The Start, Stop and Folder fields of a proxy's commands, for the dialogs that add or change
/// them. The folder is the home folder until one is chosen.
struct CommandFields {
    start: gtk::Entry,
    stop: gtk::Entry,
    folder: gtk::Entry,
    rows: [adw::ActionRow; 3],
}

impl CommandFields {
    fn new(current: &ProxyCommand) -> Self {
        // The labels in the UI's font, what's typed in monospace.
        let field = |title: &str, text: &str, placeholder: &str| {
            let entry = gtk::Entry::builder()
                .text(text)
                .placeholder_text(placeholder)
                .hexpand(true)
                .valign(gtk::Align::Center)
                .css_classes(["monospace"])
                .build();
            let row = adw::ActionRow::builder().title(title).build();
            row.add_suffix(&entry);
            (row, entry)
        };
        let (start_row, start) = field(tr!("services.start"), &current.command, "npm run dev");
        let (stop_row, stop) = field(tr!("sites.share.stop"), &current.stop_command, "docker compose stop");
        stop_row.set_subtitle(tr!("sites.proxy.stop_command_hint"));
        let dir = if current.dir.is_empty() { glib::home_dir().to_string_lossy().into_owned() } else { current.dir.clone() };
        let (folder_row, folder) = field(tr!("general.folder"), &dir, "");
        let choose = gtk::Button::builder().label(tr!("common.choose")).valign(gtk::Align::Center).css_classes(["flat"]).build();
        let entry = folder.clone();
        choose.connect_clicked(move |button| {
            let window = button.root().and_downcast::<gtk::Window>();
            let entry = entry.clone();
            gtk::FileDialog::builder()
                .title(tr!("sites.choose_folder_linux"))
                .accept_label(tr!("sites.server.choose"))
                .build()
                .select_folder(window.as_ref(), gio::Cancellable::NONE, move |result| {
                    if let Some(path) = result.ok().and_then(|f| f.path()) {
                        entry.set_text(&path.to_string_lossy());
                    }
                });
        });
        folder_row.add_suffix(&choose);
        Self { start, stop, folder, rows: [start_row, stop_row, folder_row] }
    }

    /// The commands as typed: none without a start command.
    fn change(&self) -> ProxyCommandChange {
        ProxyCommandChange::new(&self.start.text(), &self.folder.text(), &self.stop.text())
    }
}

/// The Output group of a dev server or a proxy's command: the last lines its log has, with Clear
/// and Open Log. The pane keeps one and moves it into each detail it renders, so a new status
/// doesn't reload the text or lose its scroll position. While the log is live (the server starts or
/// runs) it's read every second, as long as the pane shows it.
struct Output {
    group: adw::PreferencesGroup,
    view: gtk::TextView,
    scrolled: gtk::ScrolledWindow,
    open: gtk::Button,
    /// The log shown ("" when none is) and whether it changes.
    log: RefCell<String>,
    live: Cell<bool>,
    /// Counts the polls started, so an older one stops.
    run: Cell<u32>,
    polling: Cell<bool>,
}

impl Output {
    fn new(banner: adw::Banner) -> Rc<Self> {
        let group = group(tr!("sites.output"), "");
        let view = gtk::TextView::builder()
            .editable(false)
            .cursor_visible(false)
            .monospace(true)
            .top_margin(8)
            .bottom_margin(8)
            .left_margin(8)
            .right_margin(8)
            .build();
        let scrolled = gtk::ScrolledWindow::builder().child(&view).height_request(240).css_classes(["card"]).build();
        let buttons = gtk::Box::builder().spacing(6).valign(gtk::Align::Center).build();
        let clear = gtk::Button::builder().label(tr!("logs.clear")).css_classes(["flat"]).build();
        let open = gtk::Button::builder().label(tr!("sites.open_log")).css_classes(["flat"]).action_name("app.open").build();
        buttons.append(&clear);
        buttons.append(&open);
        group.set_header_suffix(Some(&buttons));
        group.add(&scrolled);
        let this = Rc::new(Self {
            group,
            view,
            scrolled,
            open,
            log: RefCell::default(),
            live: Cell::new(false),
            run: Cell::new(0),
            polling: Cell::new(false),
        });

        let weak = Rc::downgrade(&this);
        clear.connect_clicked(move |_| {
            let Some(this) = weak.upgrade() else { return };
            let (name, banner) = (this.log.borrow().clone(), banner.clone());
            glib::spawn_future_local(async move {
                let result = gio::spawn_blocking(move || Client.clear_service_log(&name)).await.expect("log request");
                if result.is_ok() {
                    this.view.buffer().set_text("");
                }
                report(&banner, result.err().map(|e| e.to_string()));
            });
        });
        // Shown again (the window was hidden): reading the log goes on.
        let weak = Rc::downgrade(&this);
        this.view.connect_map(move |_| {
            if let Some(this) = weak.upgrade().filter(|o| o.live.get() && !o.polling.get() && !o.log.borrow().is_empty()) {
                this.poll();
            }
        });
        this
    }

    /// The group for a detail, showing log: detached from the detail it was in, which the caller
    /// replaces. Reading the log starts again only when log or live changed.
    fn show(self: &Rc<Self>, log: &str, live: bool) -> gtk::Widget {
        self.detach();
        let changed = *self.log.borrow() != log;
        if changed {
            self.log.replace(log.to_owned());
            self.view.buffer().set_text("");
            self.open.set_action_target_value(Some(&format!("logs/services/{log}").to_variant()));
        }
        let live_changed = self.live.replace(live) != live;
        if changed || live_changed || (live && !self.polling.get()) {
            self.poll();
        }
        self.group.clone().upcast()
    }

    /// Takes the group out of the detail it's in.
    fn detach(&self) {
        if let Some(parent) = self.group.parent().and_downcast::<gtk::Box>() {
            parent.remove(&self.group);
        }
    }

    /// Stops reading the log: the detail shown has no output.
    fn stop(&self) {
        self.run.set(self.run.get() + 1);
        self.polling.set(false);
        self.log.replace(String::new());
        self.live.set(false);
    }

    /// Reads the log now and, while it's live and shown, every second.
    fn poll(self: &Rc<Self>) {
        let run = self.run.get() + 1;
        self.run.set(run);
        self.polling.set(true);
        let this = self.clone();
        glib::spawn_future_local(async move {
            loop {
                let name = this.log.borrow().clone();
                let text = gio::spawn_blocking(move || Client.service_log_tail(&name, 300)).await.expect("log request").unwrap_or_default();
                if this.run.get() != run {
                    return;
                }
                this.set_text(&text);
                if !this.live.get() {
                    this.polling.set(false);
                    return;
                }
                glib::timeout_future(Duration::from_secs(1)).await;
                if this.run.get() != run {
                    return;
                }
                // Hidden (another pane, the window closed): reading goes on once it's shown.
                if !this.view.is_mapped() {
                    this.polling.set(false);
                    return;
                }
            }
        });
    }

    /// Shows the log's text, scrolled to its end, when it changed.
    fn set_text(&self, text: &str) {
        let buffer = self.view.buffer();
        if buffer.text(&buffer.start_iter(), &buffer.end_iter(), false) == text {
            return;
        }
        buffer.set_text(text);
        // Once the new text is laid out.
        let adjustment = self.scrolled.vadjustment();
        glib::idle_add_local_once(move || adjustment.set_value(adjustment.upper() - adjustment.page_size()));
    }
}

/// A site's suffixes in the list: what it warns about, its dev server's state (a Node.js site's),
/// its share and its lock.
fn site_suffixes(site: &SiteStatus) -> Vec<gtk::Widget> {
    let mut widgets: Vec<gtk::Widget> = Vec::new();
    if let Some(problem) = problem(site) {
        let warning = gtk::Image::from_icon_name("dialog-warning-symbolic");
        warning.add_css_class("warning");
        warning.set_tooltip_text(Some(&problem));
        widgets.push(warning.upcast());
    }
    if let Some(server) = site.node_server.as_ref().filter(|_| site.is_node()) {
        let dot = state_dot(&server.state);
        dot.set_tooltip_text(Some(&server.text()));
        widgets.push(dot.upcast());
    }
    if site.shared() {
        widgets.push(badge(tr!("sites.shared"), "success").upcast());
    }
    if site.secured {
        let lock = gtk::Image::from_icon_name("channel-secure-symbolic");
        lock.add_css_class("dim-label");
        lock.set_tooltip_text(Some(tr!("sites.served_over_https")));
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
        let tag = badge(setup.database_label(), "");
        tag.set_tooltip_text(Some(&tr!("worktrees.own_database_tooltip", database = setup.database)));
        widgets.push(tag.upcast());
    }
    widgets
}

fn setup_error(setup: &WorktreeStatus) -> String {
    if setup.error.is_empty() { tr!("worktrees.setup_failed_sentence").to_owned() } else { setup.error.clone() }
}

/// What the New Worktree dialog says it does with the branch typed in.
fn new_worktree_text(site: &SiteStatus, branch: &str, exists: bool) -> String {
    if branch.is_empty() {
        return tr!("worktrees.new.explain_unnamed", site = site.name);
    }
    let address = format!("{}.{}", slug(branch), site.domain);
    if exists {
        return tr!("worktrees.new.explain_existing", branch = branch, site = site.name, address = address);
    }
    tr!("worktrees.new.explain_new", site = site.name, branch = branch, address = address)
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

/// What a site's list row warns about: the site, its document root, a domain, its FrankenPHP or its
/// Reverb server. A dev server that failed shows in its dot.
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
        .or_else(|| {
            site.reverb
                .as_ref()
                .filter(|r| !site.is_node() && (r.state == "failed" || r.state == "unavailable") && !r.problem.is_empty())
                .map(|r| r.problem.clone())
        })
}

/// The service log with a Reverb server's output.
fn reverb_log(site: &SiteStatus, reverb: &SiteReverb) -> String {
    if reverb.log.is_empty() { format!("reverb-{}", site.name) } else { reverb.log.clone() }
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
    let dot = proxy_dot(proxy);
    dot.set_tooltip_text(Some(&proxy.command.as_ref().map_or_else(|| proxy.server_text(), ProxyCommand::text)));
    widgets.push(dot.upcast());
    if proxy.secured {
        let lock = gtk::Image::from_icon_name("channel-secure-symbolic");
        lock.add_css_class("dim-label");
        lock.set_tooltip_text(Some(tr!("sites.served_over_https")));
        widgets.push(lock.upcast());
    }
    widgets
}

/// A proxy's dot: its command's state while that starts, stops or failed (amber, red), else
/// whether something answers at its upstream (green, grey).
fn proxy_dot(proxy: &ProxyStatus) -> gtk::Label {
    match proxy.command.as_ref().map(|c| c.state.as_str()) {
        Some(state @ ("starting" | "stopping" | "failed")) => state_dot(state),
        _ => status_dot(proxy.running),
    }
}

/// Why the site isn't served from its root, or a root set here over the project's.
fn root_hint(site: &SiteStatus) -> Option<String> {
    match site.root_state.as_deref().unwrap_or_default() {
        "not_found" => Some(tr!("sites.no_public_www_web_or").to_owned()),
        "missing" => Some(tr!("sites.root_missing", root = site.root)),
        "empty" => Some(tr!("sites.root_no_index", root = root_label(&site.root))),
        _ if site.root_source == "isolated" && site.root_pinned_here() => {
            Some(tr!("sites.root_shadowed", root = root_label(&site.root), pinned = root_label(&site.root_pinned)))
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
        tr!("sites.use_file_tool_version", file = file_name(file), tool = label, version = pinned)
    } else if default.is_empty() {
        tr!("sites.default").to_owned()
    } else {
        tr!("sites.default_tool_version", tool = label, version = default)
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
        "isolated" => tr!("sites.tool_version_is_set_for", tool = label, version = current),
        "project" => tr!("sites.project_pins_tool_version_in", tool = label, version = pinned, file = file_name(file)),
        _ => tr!("sites.version_default", tool = label),
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
    if root == "." { tr!("sites.project_folder_badge").to_owned() } else { root.to_owned() }
}

fn file_name(path: &str) -> String {
    std::path::Path::new(path).file_name().map_or(path.to_owned(), |n| n.to_string_lossy().into_owned())
}

/// "22.11.0" → "22"; pins and isolations are majors.
fn major(version: &str) -> String {
    version.split('.').next().unwrap_or_default().to_owned()
}

fn launch_uri(widget: &impl IsA<gtk::Widget>, uri: &str) {
    ui::open_uri(Some(widget.upcast_ref()), uri);
}

fn launch_file(widget: &impl IsA<gtk::Widget>, path: &str) {
    ui::open_file(Some(widget.upcast_ref()), path);
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
