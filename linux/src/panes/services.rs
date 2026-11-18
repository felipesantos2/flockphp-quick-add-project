//! Services: databases, caches, search and the tools around the sites, grouped by category, and
//! a service's detail. PostgreSQL and the catalog services (MySQL, Valkey, Redis, Meilisearch, ...)
//! are installed, updated and switched between versions from their detail.

use std::cell::{Cell, RefCell};
use std::rc::Rc;
use std::time::{Duration, Instant};

use adw::prelude::*;
use gtk::{gio, glib};

use super::common::{PaneView, ask_link, code_block, group, info_row, report, status_dot};
use super::versions::{MenuItem, badge, button, checked_text, confirm, more_menu, show_log};
use super::{Data, Pane};
use crate::api::{
    Catalog, CatalogEntry, CatalogLine, Client, Error, PostgresCustomExtension, PostgresExtension, PostgresExtensions, PostgresMajor,
    PostgresVersions, Service, extension_titles,
};
use crate::i18n::tr;
use crate::links::{self, Link};
use crate::model::AppModel;
use crate::paths;

const CATEGORY_ORDER: [&str; 7] = ["Core", "Database", "Cache & Queue", "Search", "Storage", "Mail", "Debugging"];

/// A category's heading: the daemon's categories are English ids, these are the ones it has now.
fn category_title(category: &str) -> &str {
    match category {
        "Core" => tr!("services.category.core"),
        "Database" => tr!("services.category.database"),
        "Cache & Queue" => tr!("services.category.cache_queue"),
        "Search" => tr!("services.category.search"),
        "Storage" => tr!("services.category.storage"),
        "Mail" => tr!("services.category.mail"),
        "Debugging" => tr!("services.category.debugging"),
        other => other,
    }
}
const ACTIVE_INTERVAL: Duration = Duration::from_secs(2);
const IDLE_INTERVAL: Duration = Duration::from_secs(30);

#[derive(Clone)]
pub struct ServicesPane(Rc<Inner>);

struct Inner {
    model: Rc<AppModel>,
    stack: gtk::Stack,
    list: PaneView,
    detail: PaneView,
    banner: adw::Banner,
    services: RefCell<Vec<Service>>,
    tld: RefCell<String>,
    catalog: RefCell<Option<Catalog>>,
    postgres: RefCell<Option<PostgresVersions>>,
    /// The service whose detail is open.
    selected: RefCell<Option<String>>,
    /// Only installed services (and the built-in ones), or every service Flock offers.
    installed_only: Cell<bool>,
    /// Services with a request waiting for the daemon (PostgreSQL's take minutes).
    pending: RefCell<Vec<String>>,
    checking: Cell<bool>,
    loading: Cell<bool>,
    loaded_at: Cell<Option<Instant>>,
    shown: RefCell<Option<String>>,
}

impl ServicesPane {
    pub fn new(model: Rc<AppModel>, banner: adw::Banner) -> Self {
        let list = PaneView::new(tr!("services.subtitle"));
        let detail = PaneView::new("");
        let stack = gtk::Stack::builder().transition_type(gtk::StackTransitionType::SlideLeftRight).build();
        stack.add_named(&list.scrolled, Some("list"));
        stack.add_named(&detail.scrolled, Some("detail"));
        Self(Rc::new(Inner {
            model,
            stack,
            list,
            detail,
            banner,
            services: RefCell::default(),
            tld: RefCell::default(),
            catalog: RefCell::default(),
            postgres: RefCell::default(),
            selected: RefCell::default(),
            installed_only: Cell::new(false),
            pending: RefCell::default(),
            checking: Cell::new(false),
            loading: Cell::new(false),
            loaded_at: Cell::new(None),
            shown: RefCell::default(),
        }))
    }

    /// Opens a service's detail (the main window's "Install…", a flock://install link).
    pub fn select(&self, id: &str) {
        self.0.open(id);
    }
}

impl Pane for ServicesPane {
    fn widget(&self) -> gtk::Widget {
        self.0.stack.clone().upcast()
    }

    fn update(&self, data: &Data) {
        self.0.services.replace(data.services.clone());
        self.0.tld.replace(data.config.as_ref().map(|c| c.tld.clone()).filter(|t| !t.is_empty()).unwrap_or_else(|| "test".to_owned()));
        self.0.load(false);
        self.0.render();
    }
}

impl Inner {
    fn busy(&self) -> bool {
        !self.pending.borrow().is_empty()
            || self
                .catalog
                .borrow()
                .as_ref()
                .is_some_and(|c| c.services.iter().flatten().any(|e| e.job.as_ref().is_some_and(|j| j.job.running())))
            || self.postgres.borrow().as_ref().is_some_and(|p| p.majors.iter().flatten().any(|m| !m.busy.is_empty()))
    }

    /// Reads the catalog and PostgreSQL's versions when they are due.
    fn load(self: &Rc<Self>, force: bool) {
        let interval = if self.busy() { ACTIVE_INTERVAL } else { IDLE_INTERVAL };
        let due = self.loaded_at.get().is_none_or(|at| at.elapsed() >= interval);
        if !(force || due) || self.loading.replace(true) {
            return;
        }
        let this = self.clone();
        glib::spawn_future_local(async move {
            let (catalog, postgres) =
                gio::spawn_blocking(|| (Client.catalog(), Client.postgres_versions())).await.expect("services request");
            this.loading.set(false);
            this.loaded_at.set(Some(Instant::now()));
            if let Ok(catalog) = catalog {
                this.catalog.replace(Some(catalog));
            }
            if let Ok(postgres) = postgres {
                this.postgres.replace(Some(postgres));
            }
            this.render();
        });
    }

    fn open(self: &Rc<Self>, id: &str) {
        self.selected.replace(Some(id.to_owned()));
        self.shown.replace(None);
        self.render();
        self.stack.set_visible_child_name("detail");
    }

    fn back(self: &Rc<Self>) {
        self.selected.replace(None);
        self.stack.set_visible_child_name("list");
        self.shown.replace(None);
        self.render();
    }

    /// Asks about a pending flock://install link (main.rs) once the services and versions are
    /// loaded: nothing installs until the user confirms. The PHP and Node panes ask about links to
    /// their versions.
    fn ask_link(self: &Rc<Self>) {
        let Some(Link::Install(link)) =
            self.model.link().filter(|l| matches!(l, Link::Install(i) if i.service != links::PHP && i.service != links::NODE))
        else {
            return;
        };
        let resolution = link.resolve(&self.services.borrow(), self.catalog.borrow().as_ref(), self.postgres.borrow().as_ref());
        let Some(resolution) = resolution else { return };
        self.model.set_link(None);
        let this = self.clone();
        ask_link(&self.stack, resolution, tr!("common.install"), move |target| {
            let (id, line) = (target.service.clone(), target.line.clone());
            if id == "postgres" {
                this.run_postgres(move |c| c.postgres_operation(&line, "install"));
                return;
            }
            let port = this.catalog.borrow().as_ref().and_then(|c| c.services.iter().flatten().find(|e| e.id == id).map(|e| e.port));
            let i2 = id.clone();
            this.run_catalog(&id, move |c| c.install_catalog(&i2, &line, port.unwrap_or_default()));
        });
    }

    fn render(self: &Rc<Self>) {
        self.ask_link();
        let key = format!(
            "{:?}{:?}{:?}{:?}{}{:?}{}",
            self.services.borrow(),
            self.catalog.borrow(),
            self.postgres.borrow(),
            self.selected.borrow(),
            self.installed_only.get(),
            self.pending.borrow(),
            self.checking.get()
        );
        if self.shown.borrow().as_deref() == Some(key.as_str()) {
            return;
        }
        self.shown.replace(Some(key));
        // Not borrowed while rendering: the detail goes back to the list when its service is gone.
        let selected = self.selected.borrow().clone();
        match selected {
            Some(id) => self.render_detail(&id),
            None => self.render_list(),
        }
    }

    // MARK: the list

    fn render_list(self: &Rc<Self>) {
        let services = self.services.borrow().clone();
        let visible: Vec<&Service> = services
            .iter()
            .filter(|s| !self.installed_only.get() || !on_request(s) || s.installed || self.activity(s) != Activity::None)
            .collect();

        let filter = gtk::Box::builder().css_classes(["linked"]).build();
        let installed = gtk::ToggleButton::builder().label(tr!("common.installed")).active(self.installed_only.get()).build();
        let all = gtk::ToggleButton::builder().label(tr!("services.all")).group(&installed).active(!self.installed_only.get()).build();
        filter.append(&installed);
        filter.append(&all);
        let this = self.clone();
        installed.connect_toggled(move |b| {
            this.installed_only.set(b.is_active());
            this.render();
        });

        let mut categories: Vec<String> =
            CATEGORY_ORDER.iter().map(|c| c.to_string()).filter(|c| visible.iter().any(|s| &s.category == c)).collect();
        let mut others: Vec<String> = visible.iter().map(|s| s.category.clone()).filter(|c| !categories.contains(c)).collect();
        others.sort();
        others.dedup();
        categories.extend(others);

        let mut groups: Vec<gtk::Widget> = Vec::new();
        for (i, category) in categories.iter().enumerate() {
            let group = group(category_title(category), "");
            if i == 0 {
                group.set_header_suffix(Some(&filter));
            }
            for service in visible.iter().filter(|s| &s.category == category) {
                group.add(&self.service_row(service));
            }
            groups.push(group.upcast());
        }
        let hidden = services.len() - visible.len();
        if self.installed_only.get() && hidden > 0 {
            let more = group("", "");
            let row = adw::ActionRow::builder()
                .title(tr!("services.more_services_count", count = hidden))
                .subtitle(tr!("services.every_service_flock_offers_mysql"))
                .activatable(true)
                .build();
            row.add_suffix(&gtk::Image::from_icon_name("go-next-symbolic"));
            let toggle = all.clone();
            row.connect_activated(move |_| toggle.set_active(true));
            more.add(&row);
            groups.push(more.upcast());
        }
        self.list.set_groups(&groups);
    }

    fn activity(&self, service: &Service) -> Activity {
        if self.pending.borrow().contains(&service.id) {
            return Activity::Working;
        }
        if service.id == "postgres" {
            let postgres = self.postgres.borrow();
            let majors = postgres.as_ref().map(|p| p.majors.clone().unwrap_or_default()).unwrap_or_default();
            if majors.iter().any(|m| !m.busy.is_empty()) {
                return Activity::Working;
            }
            return if majors.iter().any(|m| m.update && !m.installed.is_empty()) { Activity::Update } else { Activity::None };
        }
        let catalog = self.catalog.borrow();
        let Some(entry) = catalog.as_ref().and_then(|c| c.find(&service.id)) else { return Activity::None };
        match &entry.job {
            Some(job) if job.job.running() => Activity::Working,
            Some(job) if job.job.failed() => Activity::Failed,
            _ if entry.lines.iter().flatten().any(|l| l.update) => Activity::Update,
            _ => Activity::None,
        }
    }

    fn service_row(self: &Rc<Self>, service: &Service) -> adw::ActionRow {
        let mut detail = Vec::new();
        if !service.installed {
            if installable(service) {
                detail.push(if service.available {
                    tr!("common.not_installed").to_owned()
                } else {
                    tr!("services.not_available_on_linux").to_owned()
                });
            }
        } else {
            if !service.version.is_empty() {
                detail.push(service.version.clone());
            }
            if service.port > 0 {
                detail.push(tr!("services.port_number", port = service.port));
            }
            if !service.running {
                detail.push(tr!("services.postgres.stopped").to_owned());
            }
        }
        let mut subtitle = detail.join(" · ");
        if !service.problem.is_empty() {
            subtitle.push_str(&format!("\n{}", service.problem));
        }
        let row = adw::ActionRow::builder()
            .title(glib::markup_escape_text(&service.name))
            .subtitle(glib::markup_escape_text(&subtitle))
            .activatable(true)
            .build();
        if !service.problem.is_empty() {
            row.add_css_class("warning");
        }
        if !service.available {
            row.add_css_class("dim-label");
            row.set_tooltip_text(Some(&service.unavailable));
        }
        row.add_prefix(&status_dot(service.running));
        let activity = self.activity(service);
        match activity {
            Activity::Update => row.add_suffix(&badge(tr!("services.update_noun"), "accent")),
            Activity::Failed => row.add_suffix(&badge(tr!("services.install_failed"), "error")),
            Activity::Working => row.add_suffix(&gtk::Spinner::builder().spinning(true).build()),
            Activity::None => {}
        }
        if service.installed && service.stoppable && activity != Activity::Working {
            let (this, s) = (self.clone(), service.clone());
            let toggle =
                button(if service.running { tr!("sites.share.stop") } else { tr!("services.start") }, false, move || this.start_stop(&s));
            row.add_suffix(&toggle);
        }
        row.add_suffix(&gtk::Image::from_icon_name("go-next-symbolic"));
        let (this, id) = (self.clone(), service.id.clone());
        row.connect_activated(move |_| this.open(&id));
        row
    }

    fn start_stop(self: &Rc<Self>, service: &Service) {
        let id = service.id.clone();
        if !service.running {
            let id2 = id.clone();
            self.run_services(&id, move |c| c.start_service(&id2));
            return;
        }
        let this = self.clone();
        confirm(
            &self.list.scrolled,
            &tr!("services.stop_question", service = service.name),
            &tr!("services.your_sites_cant_use_service", service = service.name),
            tr!("sites.share.stop"),
            move || {
                let id2 = id.clone();
                this.run_services(&id, move |c| c.stop_service(&id2));
            },
        );
    }

    fn run_services(self: &Rc<Self>, id: &str, f: impl FnOnce(Client) -> Result<Vec<Service>, Error> + Send + 'static) {
        self.pending.borrow_mut().push(id.to_owned());
        self.render();
        let (this, id) = (self.clone(), id.to_owned());
        glib::spawn_future_local(async move {
            let result = gio::spawn_blocking(move || f(Client)).await.expect("services request");
            this.pending.borrow_mut().retain(|p| *p != id);
            match result {
                Ok(services) => {
                    this.services.replace(services);
                    report(&this.banner, None);
                }
                Err(e) => report(&this.banner, Some(e.to_string())),
            }
            this.render();
        });
    }

    fn run_catalog(self: &Rc<Self>, id: &str, f: impl FnOnce(Client) -> Result<Catalog, Error> + Send + 'static) {
        self.pending.borrow_mut().push(id.to_owned());
        self.render();
        let (this, id) = (self.clone(), id.to_owned());
        glib::spawn_future_local(async move {
            let result = gio::spawn_blocking(move || f(Client)).await.expect("catalog request");
            this.pending.borrow_mut().retain(|p| *p != id);
            match result {
                Ok(catalog) => {
                    this.catalog.replace(Some(catalog));
                    report(&this.banner, None);
                }
                Err(e) => report(&this.banner, Some(e.to_string())),
            }
            this.render();
            this.refresh_services();
        });
    }

    fn run_postgres(self: &Rc<Self>, f: impl FnOnce(Client) -> Result<PostgresVersions, Error> + Send + 'static) {
        self.pending.borrow_mut().push("postgres".to_owned());
        self.render();
        let this = self.clone();
        glib::spawn_future_local(async move {
            let result = gio::spawn_blocking(move || f(Client)).await.expect("postgres request");
            this.pending.borrow_mut().retain(|p| p != "postgres");
            match result {
                Ok(versions) => {
                    this.postgres.replace(Some(versions));
                    report(&this.banner, None);
                }
                Err(e) => report(&this.banner, Some(e.to_string())),
            }
            this.render();
            this.refresh_services();
        });
    }

    /// The service list after an install changed it (the Settings window reads it every two seconds too).
    fn refresh_services(self: &Rc<Self>) {
        let this = self.clone();
        glib::spawn_future_local(async move {
            if let Ok(services) = gio::spawn_blocking(|| Client.services()).await.expect("services request") {
                this.services.replace(services);
                this.render();
            }
        });
    }

    // MARK: a service's detail

    fn render_detail(self: &Rc<Self>, id: &str) {
        let Some(service) = self.services.borrow().iter().find(|s| s.id == id).cloned() else {
            // Before the services are loaded (a link opened the pane), the detail waits for them.
            if !self.services.borrow().is_empty() {
                self.back();
            }
            return;
        };
        let header = group("", "");
        let back = adw::ActionRow::builder().title(tr!("settings.pane.services")).activatable(true).build();
        back.add_prefix(&gtk::Image::from_icon_name("go-previous-symbolic"));
        let this = self.clone();
        back.connect_activated(move |_| this.back());
        header.add(&back);
        let title = adw::ActionRow::builder()
            .title(glib::markup_escape_text(&service.name))
            .subtitle(glib::markup_escape_text(&self.status_text(&service)))
            .css_classes(["property"])
            .build();
        title.add_prefix(&status_dot(service.running));
        header.add(&title);
        let mut groups: Vec<gtk::Widget> = vec![header.upcast()];

        if !service.available {
            let unavailable = group(
                tr!("services.not_available_on_linux"),
                if service.unavailable.is_empty() { tr!("services.flock_cant_run_this_service") } else { &service.unavailable },
            );
            groups.push(unavailable.upcast());
        } else if service.id == "postgres" {
            groups.extend(self.postgres_detail(&service));
        } else if on_request(&service) {
            groups.extend(self.catalog_detail(&service));
        } else {
            groups.extend(self.service_info(&service));
        }
        self.detail.set_groups(&groups);
    }

    fn status_text(&self, service: &Service) -> String {
        if !service.installed {
            return if service.available {
                tr!("common.not_installed").to_owned()
            } else {
                tr!("services.catalog.not_available").to_owned()
            };
        }
        let mut parts = vec![
            (if service.running {
                tr!("sites.proxy.running")
            } else if service.enabled {
                tr!("sites.proxy.not_running")
            } else {
                tr!("services.postgres.stopped")
            })
            .to_owned(),
        ];
        if service.port > 0 {
            parts.push(tr!("services.port_number", port = service.port));
        }
        parts.join(" · ")
    }

    /// What every installed service shows: its problem, what it does (core services), port, log,
    /// environment and connection.
    fn service_info(self: &Rc<Self>, service: &Service) -> Vec<gtk::Widget> {
        let tld = self.tld.borrow().clone();
        let dns_broken = !service.running || !service.problem.is_empty();
        let explanation = match service.id.as_str() {
            "web" if service.running => Some(tr!("services.web_running", tld = tld)),
            "web" => Some(tr!("services.web_down").to_owned()),
            "dns" if !dns_broken => Some(tr!("services.systemd_resolved_sends_every_name", tld = tld)),
            "dns" => {
                let problem = if service.problem.is_empty() { tr!("services.dns_server_isnt_running") } else { &service.problem };
                Some(tr!("general.tld_doesnt_resolve_problem_linux", tld = tld, problem = problem))
            }
            _ => None,
        };
        let info = group("", "");
        if !service.problem.is_empty() && !(service.id == "dns" && dns_broken) {
            info.add(&adw::ActionRow::builder().title(glib::markup_escape_text(&service.problem)).css_classes(["warning"]).build());
        }
        if let Some(text) = explanation {
            info.add(&adw::ActionRow::builder().title(glib::markup_escape_text(&text)).build());
        }
        let configurable = service.installed && service.port > 0 && service.id != "web" && service.id != "dns";
        if configurable {
            let row = info_row(tr!("mail.port"), &service.port.to_string());
            let (this, s) = (self.clone(), service.clone());
            row.add_suffix(&button(tr!("sites.change"), false, move || this.change_port(&s)));
            info.add(&row);
        } else if service.installed && service.port > 0 {
            info.add(&info_row(tr!("mail.port"), &tr!("services.port_set_by_flock_install", port = service.port)));
        }
        if !service.log.is_empty() {
            let row = adw::ActionRow::builder().title(tr!("services.log")).build();
            let file = paths::root().join("log").join(format!("{}.log", service.log));
            row.add_suffix(&button(tr!("common.open"), false, move || {
                crate::ui::open_file(None, &file);
            }));
            info.add(&row);
        }
        let mut groups = vec![info.upcast()];
        if !service.env.is_empty() {
            let env = group(tr!("mail.environment_variables_title_case"), "");
            env.add(&code_block(&service.env));
            groups.push(env.upcast());
        }
        if !service.url.is_empty() {
            let url = group(tr!("services.connection"), "");
            url.add(&code_block(&service.url));
            groups.push(url.upcast());
        }
        groups
    }

    fn change_port(self: &Rc<Self>, service: &Service) {
        let entry =
            gtk::Entry::builder().text(service.port.to_string()).input_purpose(gtk::InputPurpose::Digits).activates_default(true).build();
        let dialog = adw::AlertDialog::new(
            Some(&tr!("services.service_port", service = service.name)),
            Some(tr!("services.port_between_1024_and_65535")),
        );
        dialog.set_extra_child(Some(&entry));
        dialog.add_responses(&[("cancel", tr!("common.cancel")), ("save", tr!("services.change_port"))]);
        dialog.set_response_appearance("save", adw::ResponseAppearance::Suggested);
        dialog.set_default_response(Some("save"));
        let (this, id) = (self.clone(), service.id.clone());
        dialog.choose(Some(&self.detail.scrolled), gio::Cancellable::NONE, move |response| {
            let Ok(port) = entry.text().parse::<u16>() else { return };
            if response == "save" && port >= 1024 {
                let id2 = id.clone();
                this.run_services(&id, move |c| c.set_service_port(&id2, port));
            }
        });
    }

    fn check_line(self: &Rc<Self>, checked_at: Option<&str>, check: fn(&Rc<Inner>)) -> gtk::Widget {
        let line = gtk::Box::builder().spacing(6).build();
        line.append(&gtk::Label::builder().label(checked_text(checked_at)).css_classes(["caption", "dim-label"]).build());
        if self.checking.get() {
            line.append(&gtk::Spinner::builder().spinning(true).build());
        } else {
            let this = self.clone();
            let b = button(tr!("versions.check_now"), false, move || check(&this));
            b.add_css_class("flat");
            line.append(&b);
        }
        line.upcast()
    }

    // MARK: catalog services

    fn catalog_detail(self: &Rc<Self>, service: &Service) -> Vec<gtk::Widget> {
        let catalog = self.catalog.borrow().clone();
        let Some(entry) = catalog.as_ref().and_then(|c| c.find(&service.id)).cloned() else {
            let loading = group("", if catalog.is_some() { tr!("services.this_flock_daemon_doesnt_know") } else { "" });
            loading.add(
                &adw::ActionRow::builder()
                    .title(if catalog.is_some() { tr!("services.not_offered") } else { tr!("common.loading") })
                    .build(),
            );
            return vec![loading.upcast()];
        };
        let catalog = catalog.expect("catalog");
        let lines = entry.lines.clone().unwrap_or_default();
        let locked = self.pending.borrow().contains(&entry.id) || entry.job.as_ref().is_some_and(|j| j.job.running());
        let footer = if lines.iter().filter(|l| l.installed).count() > 1 {
            tr!("services.side_by_side")
        } else if lines.iter().any(|l| l.installed) {
            tr!("services.updates_keep_data")
        } else {
            entry.estimate.as_str()
        };
        let versions = group(tr!("versions.versions"), footer);
        versions.set_header_suffix(Some(&self.check_line(catalog.checked_at.as_deref(), |this| {
            this.checking.set(true);
            this.render();
            let this = this.clone();
            glib::spawn_future_local(async move {
                let result = gio::spawn_blocking(|| Client.check_catalog()).await.expect("check request");
                this.checking.set(false);
                if let Ok(catalog) = result {
                    this.catalog.replace(Some(catalog));
                }
                this.render();
            });
        })));
        if !catalog.check_error.is_empty() {
            versions.add(
                &adw::ActionRow::builder()
                    .title(glib::markup_escape_text(&tr!("services.postgres.couldnt_check_for_new_releases", error = catalog.check_error)))
                    .css_classes(["warning"])
                    .build(),
            );
        }
        if let Some(job) = &entry.job {
            versions.add(&self.catalog_job_row(&entry, job));
        }
        for line in &lines {
            versions.add(&self.catalog_line_row(&entry, line, locked));
        }
        let mut groups = vec![versions.upcast()];
        if lines.iter().any(|l| l.installed) {
            groups.extend(self.service_info(service));
        }
        groups
    }

    fn catalog_job_row(self: &Rc<Self>, entry: &CatalogEntry, job: &crate::api::CatalogJob) -> adw::ActionRow {
        let target = if job.version.is_empty() { entry.name.clone() } else { format!("{} {}", entry.name, job.version) };
        let row = adw::ActionRow::new();
        let id = entry.id.clone();
        let log = {
            let (this, id) = (self.clone(), id.clone());
            move || this.show_catalog_log(&id)
        };
        if job.job.running() {
            row.set_title(&glib::markup_escape_text(&match job.job.phase.as_str() {
                "installing" => tr!("services.installing_name", name = target),
                _ => tr!("services.catalog.downloading_name", name = target),
            }));
            if (0.0..=1.0).contains(&job.job.progress) && job.job.phase != "installing" {
                row.add_suffix(
                    &gtk::ProgressBar::builder().fraction(job.job.progress).valign(gtk::Align::Center).width_request(100).build(),
                );
            } else {
                row.add_suffix(&gtk::Spinner::builder().spinning(true).build());
            }
            row.add_suffix(&button(tr!("sites.show_log"), false, log));
            let (this, i) = (self.clone(), id.clone());
            row.add_suffix(&button(tr!("common.cancel"), false, move || {
                let i2 = i.clone();
                this.run_catalog(&i, move |c| c.cancel_catalog(&i2));
            }));
        } else if job.job.failed() {
            row.set_title(&glib::markup_escape_text(&tr!("services.installing_failed", name = entry.name)));
            row.set_subtitle(&glib::markup_escape_text(&job.job.error));
            row.add_css_class("error");
            row.add_suffix(&button(tr!("sites.show_log"), false, log));
            let (this, i, line, port) = (self.clone(), id.clone(), job.line.clone(), entry.port);
            row.add_suffix(&button(tr!("common.retry"), false, move || {
                let (i2, l2) = (i.clone(), line.clone());
                this.run_catalog(&i, move |c| c.install_catalog(&i2, &l2, port));
            }));
            let (this, i) = (self.clone(), id.clone());
            row.add_suffix(&button(tr!("sites.share.dismiss"), false, move || {
                let i2 = i.clone();
                this.run_catalog(&i, move |c| c.dismiss_catalog(&i2));
            }));
        }
        row
    }

    fn catalog_line_row(self: &Rc<Self>, entry: &CatalogEntry, line: &CatalogLine, locked: bool) -> adw::ActionRow {
        let name = &entry.name;
        let row = adw::ActionRow::builder().title(glib::markup_escape_text(&format!("{name} {}", line.label))).build();
        let icon = gtk::Image::from_icon_name(if line.active { "emblem-ok-symbolic" } else { "radio-symbolic" });
        if line.active {
            icon.add_css_class("success");
        } else if !line.installed {
            icon.add_css_class("dim-label");
        }
        row.add_prefix(&icon);
        if line.active {
            row.add_suffix(&badge(tr!("services.active"), "accent"));
        }
        if line.prerelease {
            row.add_suffix(&badge(tr!("versions.prerelease"), "warning"));
        }
        let detail = match (line.installed, line.update) {
            (true, true) => tr!("services.old_to_new_available", old = line.version, new = line.latest),
            (true, false) => line.version.clone(),
            (false, _) if line.has_data => tr!("services.not_installed_its_data_is", version = line.latest),
            (false, _) => tr!("services.not_installed_version", version = line.latest),
        };
        let mut subtitle = detail;
        if !line.yanked.is_empty() {
            subtitle.push('\n');
            subtitle.push_str(&tr!("services.withdrawn", reason = line.yanked));
            row.add_css_class("warning");
        }
        row.set_subtitle(&glib::markup_escape_text(&subtitle));

        let actions = gtk::Box::builder().spacing(6).sensitive(!locked).build();
        let (id, line_id, port) = (entry.id.clone(), line.id.clone(), entry.port);
        let install = {
            let (this, id, line_id) = (self.clone(), id.clone(), line_id.clone());
            move || {
                let (i2, l2) = (id.clone(), line_id.clone());
                this.run_catalog(&id, move |c| c.install_catalog(&i2, &l2, port));
            }
        };
        if !line.installed {
            actions.append(&button(tr!("common.install"), false, install));
        } else {
            if line.update && !line.latest.is_empty() {
                actions.append(&button(&tr!("general.update_to", version = line.latest), true, install.clone()));
            }
            if !line.active {
                let (this, e, l) = (self.clone(), entry.clone(), line.clone());
                actions.append(&button(tr!("services.switch_ellipsis"), false, move || this.switch_catalog(&e, &l)));
            }
            let mut items: Vec<MenuItem> = Vec::new();
            if !line.update {
                items.push((tr!("services.reinstall", version = line.version), true, false, Box::new(install)));
            }
            let (this, i) = (self.clone(), id.clone());
            items.push((tr!("services.show_last_log").to_owned(), true, false, Box::new(move || this.show_catalog_log(&i))));
            let (this, e, l) = (self.clone(), entry.clone(), line.clone());
            items.push((tr!("common.uninstall_ellipsis").to_owned(), true, true, Box::new(move || this.uninstall_catalog(&e, &l))));
            actions.append(&more_menu(items));
        }
        row.add_suffix(&actions);
        row
    }

    fn switch_catalog(self: &Rc<Self>, entry: &CatalogEntry, target: &CatalogLine) {
        let name = &entry.name;
        let own = if target.has_data {
            tr!("services.catalog.name_version_uses_its_own", name = name, version = target.label)
        } else {
            tr!("services.catalog.name_version_starts_with_empty", name = name, version = target.label)
        };
        let message = if entry.line.is_empty() {
            own
        } else {
            tr!("services.name_version_stops_its_data", name = name, version = entry.line, then = own)
        };
        let dialog = adw::AlertDialog::new(
            Some(&tr!("services.catalog.switch_to_name_version", name = name, version = target.label)),
            Some(&message),
        );
        dialog.add_responses(&[("cancel", tr!("common.cancel")), ("switch", tr!("worktrees.switch"))]);
        dialog.set_response_appearance("switch", adw::ResponseAppearance::Suggested);
        let (this, id, line) = (self.clone(), entry.id.clone(), target.id.clone());
        dialog.choose(Some(&self.detail.scrolled), gio::Cancellable::NONE, move |response| {
            if response == "switch" {
                let (i2, l2) = (id.clone(), line.clone());
                this.run_catalog(&id, move |c| c.activate_catalog(&i2, &l2));
            }
        });
    }

    fn uninstall_catalog(self: &Rc<Self>, entry: &CatalogEntry, line: &CatalogLine) {
        let name = &entry.name;
        let mut message = String::new();
        if line.active {
            match entry.lines.iter().flatten().find(|l| l.installed && l.id != line.id) {
                Some(next) => {
                    message = tr!("services.catalog.name_version_stops_and_name", name = name, version = line.label, next = next.label)
                }
                None => message = tr!("services.catalog.name_version_stops_your_sites", name = name, version = line.label),
            }
        }
        if !message.is_empty() {
            message.push(' ');
        }
        message.push_str(if line.has_data { tr!("services.keep_its_data_to_pick") } else { tr!("services.no_data") });
        let dialog =
            adw::AlertDialog::new(Some(&tr!("services.catalog.uninstall_name_version", name = name, version = line.label)), Some(&message));
        dialog.add_response("cancel", tr!("common.cancel"));
        if line.has_data {
            dialog.add_response("keep", tr!("services.catalog.uninstall_keep_data"));
            dialog.add_response("delete", tr!("services.catalog.uninstall_and_delete_data"));
            dialog.set_response_appearance("delete", adw::ResponseAppearance::Destructive);
        } else {
            dialog.add_response("keep", tr!("common.uninstall"));
            dialog.set_response_appearance("keep", adw::ResponseAppearance::Destructive);
        }
        let (this, id, line_id) = (self.clone(), entry.id.clone(), line.id.clone());
        dialog.choose(Some(&self.detail.scrolled), gio::Cancellable::NONE, move |response| {
            if response == "keep" || response == "delete" {
                let (i2, l2, delete) = (id.clone(), line_id.clone(), response == "delete");
                this.run_catalog(&id, move |c| c.uninstall_catalog(&i2, &l2, delete));
            }
        });
    }

    fn show_catalog_log(self: &Rc<Self>, id: &str) {
        let name = self.services.borrow().iter().find(|s| s.id == id).map_or(id.to_owned(), |s| s.name.clone());
        let (i, this, i2) = (id.to_owned(), self.clone(), id.to_owned());
        let running =
            move || this.catalog.borrow().as_ref().and_then(|c| c.find(&i2).and_then(|e| e.job.clone())).is_some_and(|j| j.job.running());
        show_log(&self.detail.scrolled, &tr!("services.install_log", name = name), move || Client.catalog_log(&i), running);
    }

    // MARK: PostgreSQL

    fn postgres_detail(self: &Rc<Self>, service: &Service) -> Vec<gtk::Widget> {
        let Some(versions) = self.postgres.borrow().clone() else {
            let loading = group("", "");
            loading.add(&adw::ActionRow::builder().title(tr!("common.loading")).build());
            return vec![loading.upcast()];
        };
        let majors = versions.majors.clone().unwrap_or_default();
        let working = self.pending.borrow().contains(&"postgres".to_owned()) || majors.iter().any(|m| !m.busy.is_empty());
        let group_ = group(tr!("versions.versions"), tr!("services.postgres.side_by_side"));
        group_.set_header_suffix(Some(&self.check_line(versions.checked_at.as_deref(), |this| {
            this.checking.set(true);
            this.render();
            let this = this.clone();
            glib::spawn_future_local(async move {
                let result = gio::spawn_blocking(|| Client.check_postgres()).await.expect("check request");
                this.checking.set(false);
                if let Ok(versions) = result {
                    this.postgres.replace(Some(versions));
                }
                this.render();
            });
        })));
        if !versions.check_error.is_empty() {
            group_.add(
                &adw::ActionRow::builder()
                    .title(glib::markup_escape_text(&tr!("services.postgres.couldnt_check_for_new_releases", error = versions.check_error)))
                    .css_classes(["warning"])
                    .build(),
            );
        }
        for major in &majors {
            group_.add(&self.postgres_row(&versions, major, working));
        }
        let mut groups = vec![group_.upcast()];
        if service.installed {
            groups.extend(self.service_info(service));
        }
        groups
    }

    fn postgres_row(self: &Rc<Self>, versions: &PostgresVersions, major: &PostgresMajor, working: bool) -> adw::ActionRow {
        let installed = !major.installed.is_empty();
        let row = adw::ActionRow::builder().title(format!("PostgreSQL {}", major.major)).build();
        let icon = gtk::Image::from_icon_name(if major.active { "emblem-ok-symbolic" } else { "radio-symbolic" });
        if major.active {
            icon.add_css_class("success");
        } else if !installed {
            icon.add_css_class("dim-label");
        }
        row.add_prefix(&icon);
        if major.active {
            row.add_suffix(&badge(tr!("services.active"), "accent"));
        }
        if major.prerelease {
            row.add_suffix(&badge(tr!("versions.prerelease"), "warning"));
        }
        let subtitle = match major.busy.as_str() {
            "installing" => tr!("services.postgres.installing_postgresql_version", version = major.major),
            "updating" => tr!("services.postgres.updating_postgresql_version", version = major.major),
            "switching" => tr!("services.postgres.switching_to_postgresql_version", version = major.major),
            "uninstalling" => tr!("services.postgres.uninstalling_postgresql_version", version = major.major),
            _ if installed && major.update => tr!("services.old_to_new_available", old = major.installed, new = major.latest),
            _ if installed && !major.extensions.as_deref().unwrap_or_default().is_empty() => {
                format!("{} · {}", major.installed, extension_titles(major.extensions.as_deref().unwrap_or_default()))
            }
            _ if installed => major.installed.clone(),
            _ if major.has_data => tr!("services.not_installed_size_of_databases", size = bytes(major.data_size)),
            _ => tr!("common.not_installed").to_owned(),
        };
        row.set_subtitle(&glib::markup_escape_text(&subtitle));
        if !major.busy.is_empty() {
            row.add_suffix(&gtk::Spinner::builder().spinning(true).build());
        }

        let actions = gtk::Box::builder().spacing(6).sensitive(!working).build();
        let m = major.major.clone();
        let operation = |label: &str, suggested: bool, op: &'static str| {
            let (this, m) = (self.clone(), m.clone());
            button(label, suggested, move || {
                let m2 = m.clone();
                this.run_postgres(move |c| c.postgres_operation(&m2, op));
            })
        };
        if !installed {
            let (this, v, target) = (self.clone(), versions.clone(), major.major.clone());
            actions.append(&button(tr!("common.install"), false, move || this.install_postgres(&v, &target)));
        } else {
            if major.update {
                // "Update": the subtitle shows the version it goes to ("17.10 → 17.11 available").
                actions.append(&operation(tr!("common.update"), true, "update"));
            }
            if !major.active {
                let (this, v, target) = (self.clone(), versions.clone(), major.clone());
                actions.append(&button(tr!("services.switch_ellipsis"), false, move || this.switch_postgres(&v, &target)));
            }
            // An icon, like ⋯: with Update and Switch, a third labelled button doesn't fit.
            let extensions = gtk::Button::builder()
                .icon_name("application-x-addon-symbolic")
                .tooltip_text(tr!("services.postgres.extensions_of_postgresql_version_postgis", version = major.major))
                .valign(gtk::Align::Center)
                .css_classes(["flat"])
                .build();
            let (this, target) = (self.clone(), major.major.clone());
            extensions.connect_clicked(move |_| this.postgres_extensions(&target));
            actions.append(&extensions);
            let (this, v, target) = (self.clone(), versions.clone(), major.clone());
            actions.append(&more_menu(vec![(
                tr!("common.uninstall_ellipsis").to_owned(),
                true,
                true,
                Box::new(move || this.uninstall_postgres(&v, &target)),
            )]));
        }
        row.add_suffix(&actions);
        row
    }

    /// Installs a major; when the active one has extensions, asks whether the new one gets them too.
    fn install_postgres(self: &Rc<Self>, versions: &PostgresVersions, major: &str) {
        let active = versions.majors.iter().flatten().find(|m| m.active && !m.installed.is_empty());
        let extensions = active.and_then(|m| m.extensions.clone()).unwrap_or_default();
        let m = major.to_owned();
        if extensions.is_empty() {
            self.run_postgres(move |c| c.postgres_operation(&m, "install"));
            return;
        }
        let dialog = adw::AlertDialog::new(
            Some(&tr!("services.postgres.also_install_the_extensions_of", version = versions.active)),
            Some(&tr!(
                "services.postgres.extensions_for_postgresql_version_where",
                extensions = extension_titles(&extensions),
                version = major
            )),
        );
        dialog.add_responses(&[
            ("cancel", tr!("common.cancel")),
            ("only", tr!("services.postgres.postgresql_only")),
            ("with", tr!("services.postgres.install_with_extensions")),
        ]);
        dialog.set_response_appearance("with", adw::ResponseAppearance::Suggested);
        dialog.set_default_response(Some("with"));
        let this = self.clone();
        dialog.choose(Some(&self.detail.scrolled), gio::Cancellable::NONE, move |response| match response.as_str() {
            "with" => {
                let (m, extensions) = (m.clone(), extensions.clone());
                this.run_postgres(move |c| c.install_postgres_with(&m, &extensions));
            }
            "only" => {
                let m = m.clone();
                this.run_postgres(move |c| c.postgres_operation(&m, "install"));
            }
            _ => {}
        });
    }

    /// The extensions of one major: Flock's own (each a download of its own) and the folder for yours.
    fn postgres_extensions(self: &Rc<Self>, major: &str) {
        let status = gtk::Label::builder()
            .label(tr!("services.postgres.after_installing_one_run_create"))
            .wrap(true)
            .xalign(0.0)
            .css_classes(["dim-label"])
            .build();
        let list = adw::PreferencesGroup::new();
        let folder = group(tr!("services.your_own_extensions"), "");
        let column = gtk::Box::builder()
            .orientation(gtk::Orientation::Vertical)
            .spacing(18)
            .margin_top(12)
            .margin_bottom(18)
            .margin_start(18)
            .margin_end(18)
            .build();
        column.append(&status);
        column.append(&list);
        column.append(&folder);
        let view = adw::ToolbarView::new();
        view.add_top_bar(&adw::HeaderBar::new());
        view.set_content(Some(&gtk::ScrolledWindow::builder().child(&column).vexpand(true).build()));
        let dialog = adw::Dialog::builder()
            .title(tr!("services.postgres.postgresql_version_extensions", version = major))
            .content_width(560)
            .content_height(520)
            .child(&view)
            .build();
        dialog.present(Some(&self.stack));

        let state = Rc::new(ExtensionsDialog {
            pane: self.clone(),
            major: major.to_owned(),
            list,
            folder,
            status,
            rows: RefCell::new(vec![]),
            polling: Cell::new(false),
        });
        state.load();
        // Then the newest manifest (the daemon fetches it only hourly), so a package published
        // meanwhile doesn't show as "not available".
        glib::spawn_future_local(async move {
            let _ = gio::spawn_blocking(|| Client.check_postgres()).await;
            state.load();
        });
    }

    fn switch_postgres(self: &Rc<Self>, versions: &PostgresVersions, target: &PostgresMajor) {
        let own = if target.has_data {
            tr!("services.postgres.postgresql_version_uses_its_own", version = target.major)
        } else {
            tr!("services.postgres.postgresql_version_starts_with_a", version = target.major)
        };
        let message = if versions.active.is_empty() {
            own
        } else {
            tr!("services.postgresql_version_stops_its_databases", version = versions.active, then = own)
        };
        let dialog =
            adw::AlertDialog::new(Some(&tr!("services.postgres.switch_to_postgresql_version", version = target.major)), Some(&message));
        dialog.add_responses(&[("cancel", tr!("common.cancel")), ("switch", tr!("worktrees.switch"))]);
        dialog.set_response_appearance("switch", adw::ResponseAppearance::Suggested);
        let (this, m) = (self.clone(), target.major.clone());
        dialog.choose(Some(&self.detail.scrolled), gio::Cancellable::NONE, move |response| {
            if response == "switch" {
                let m2 = m.clone();
                this.run_postgres(move |c| c.postgres_operation(&m2, "activate"));
            }
        });
    }

    fn uninstall_postgres(self: &Rc<Self>, versions: &PostgresVersions, major: &PostgresMajor) {
        let next = versions.majors.iter().flatten().find(|m| !m.installed.is_empty() && m.major != major.major);
        let message = match (major.active, next) {
            (false, _) => tr!("services.postgres.removes_the_postgresql_version_server", version = major.installed),
            (true, Some(next)) => tr!("services.postgres.postgresql_version_stops_and_flock", version = major.major, next = next.major),
            (true, None) => {
                tr!("services.postgres.postgresql_version_stops_and_your", version = major.major)
            }
        };
        let dialog =
            adw::AlertDialog::new(Some(&tr!("services.postgres.uninstall_postgresql_version", version = major.major)), Some(&message));
        dialog.add_response("cancel", tr!("common.cancel"));
        dialog.add_response("keep", tr!("services.uninstall_keep_databases"));
        if major.has_data {
            dialog.add_response("delete", &tr!("services.uninstall_and_delete_databases_size", size = bytes(major.data_size)));
            dialog.set_response_appearance("delete", adw::ResponseAppearance::Destructive);
        }
        let (this, m) = (self.clone(), major.major.clone());
        dialog.choose(Some(&self.detail.scrolled), gio::Cancellable::NONE, move |response| {
            if response == "keep" || response == "delete" {
                let (m2, delete) = (m.clone(), response == "delete");
                this.run_postgres(move |c| c.uninstall_postgres(&m2, delete));
            }
        });
    }
}

/// One of your own extensions: read only, with what to run or what is in the way.
fn custom_row(ext: &PostgresCustomExtension) -> adw::ActionRow {
    let title = if ext.version.is_empty() { ext.name.clone() } else { format!("{} {}", ext.name, ext.version) };
    let detail = if ext.linked { format!("CREATE EXTENSION {};", ext.name) } else { ext.problem.clone() };
    let row = adw::ActionRow::builder()
        .title(glib::markup_escape_text(&title))
        .subtitle(glib::markup_escape_text(&detail))
        .subtitle_selectable(ext.linked)
        .build();
    row.add_suffix(&badge(tr!("services.postgres.yours"), ""));
    if !ext.linked {
        row.add_prefix(&gtk::Image::builder().icon_name("dialog-warning-symbolic").css_classes(["warning"]).build());
    }
    row
}

/// The open extensions dialog of a major: reloads itself after every change.
struct ExtensionsDialog {
    pane: Rc<Inner>,
    major: String,
    list: adw::PreferencesGroup,
    folder: adw::PreferencesGroup,
    status: gtk::Label,
    rows: RefCell<Vec<gtk::Widget>>,
    /// A reload is scheduled while something runs or waits: one at a time.
    polling: Cell<bool>,
}

impl ExtensionsDialog {
    fn load(self: &Rc<Self>) {
        let (this, major) = (self.clone(), self.major.clone());
        glib::spawn_future_local(async move {
            let result = gio::spawn_blocking(move || Client.postgres_extensions(&major)).await.expect("extensions request");
            this.show(result);
        });
    }

    fn show(self: &Rc<Self>, result: Result<PostgresExtensions, Error>) {
        for row in self.rows.borrow_mut().drain(..) {
            if let Some(group) = row.parent().and_then(|p| p.ancestor(adw::PreferencesGroup::static_type())) {
                group.downcast::<adw::PreferencesGroup>().expect("group").remove(&row);
            }
        }
        let state = match result {
            Ok(state) => state,
            Err(e) => {
                self.status.set_label(&e.to_string());
                self.status.add_css_class("error");
                return;
            }
        };
        self.status.remove_css_class("error");
        self.status.set_label(tr!("services.postgres.after_installing_one_run_create"));
        let extensions = state.extensions.clone().unwrap_or_default();
        let busy = extensions.iter().any(|e| !e.busy.is_empty());
        let mut rows = vec![];
        for ext in &extensions {
            let row = self.row(ext);
            self.list.add(&row);
            rows.push(row.upcast());
        }
        for ext in state.custom.clone().unwrap_or_default() {
            let row = custom_row(&ext);
            self.folder.add(&row);
            rows.push(row.upcast());
        }
        let open = adw::ActionRow::builder()
            .title(tr!("services.extensions_folder"))
            .subtitle(glib::markup_escape_text(&tr!("services.put_a_library_in_lib", version = self.major)))
            .build();
        let dir = state.custom_dir.clone();
        open.add_suffix(&button(tr!("common.open"), false, move || {
            crate::ui::open_file(None, &dir);
        }));
        self.folder.add(&open);
        rows.push(open.upcast());
        self.rows.replace(rows);
        if !self.polling.replace(true) {
            // Every two seconds while anything runs or waits (also started elsewhere: the CLI, the
            // other window), otherwise every few seconds for what is put into the extensions folder.
            // Until the dialog is gone: then the rows have no window any more.
            let this = self.clone();
            let interval = if busy { ACTIVE_INTERVAL } else { Duration::from_secs(4) };
            glib::timeout_add_local_once(interval, move || {
                this.polling.set(false);
                if this.list.root().is_some() {
                    this.load();
                }
            });
        }
    }

    /// Every row stays usable while another one works: the daemon queues the operations, so several
    /// extensions can be installed in a row (the queued ones are "waiting").
    fn row(self: &Rc<Self>, ext: &PostgresExtension) -> adw::ActionRow {
        let mut caption = vec![ext.description.clone()];
        if !ext.supported {
            caption = vec![tr!("services.postgres.not_available_for_postgresql_version", version = self.major)];
        } else if ext.discontinued {
            caption.push(tr!("services.postgres.no_longer_updated_for_postgresql", version = self.major));
        }
        if ext.supported && !ext.installed.is_empty() {
            caption.push(format!("CREATE EXTENSION {};", ext.creates));
        } else if ext.supported && ext.preload {
            caption.push(tr!("services.postgres.postgresql_version_restarts_to_load", version = self.major));
        }
        let title = match (ext.installed.is_empty(), ext.update) {
            (true, _) => ext.title.clone(),
            (false, true) => format!("{} {} → {}", ext.title, ext.installed, ext.latest),
            (false, false) => format!("{} {}", ext.title, ext.installed),
        };
        let row = adw::ActionRow::builder()
            .title(glib::markup_escape_text(&title))
            .subtitle(glib::markup_escape_text(&caption.join(" · ")))
            .sensitive(ext.supported || !ext.installed.is_empty())
            .build();
        if !ext.license.is_empty() {
            row.set_tooltip_text(Some(&tr!("services.postgres.license_license", license = ext.license)));
        }
        if ext.prerelease {
            row.add_suffix(&badge(tr!("versions.prerelease"), "warning"));
        }
        if !ext.busy.is_empty() {
            let busy = match ext.busy.as_str() {
                "installing" => tr!("common.installing").to_owned(),
                "updating" => tr!("common.updating").to_owned(),
                "uninstalling" => tr!("common.uninstalling").to_owned(),
                "waiting" => tr!("services.postgres.waiting").to_owned(),
                other => {
                    let mut busy = other.to_owned();
                    busy[..1].make_ascii_uppercase();
                    format!("{busy}…")
                }
            };
            row.add_suffix(&gtk::Label::builder().label(busy).css_classes(["dim-label"]).build());
            row.add_suffix(&gtk::Spinner::builder().spinning(true).build());
            return row;
        }
        let actions = gtk::Box::builder().spacing(6).build();
        let operation = |label: &str, suggested: bool, op: &'static str| {
            let (this, name) = (self.clone(), ext.name.clone());
            button(label, suggested, move || this.run(&name, op))
        };
        if ext.installed.is_empty() {
            actions.append(&operation(tr!("common.install"), false, "install"));
        } else {
            if ext.update {
                actions.append(&operation(tr!("common.update"), true, "update"));
            }
            let (this, name) = (self.clone(), ext.name.clone());
            let items: Vec<MenuItem> = vec![(
                tr!("services.postgres.uninstall_name", name = ext.title),
                true,
                true,
                Box::new(move || this.run(&name, "uninstall")),
            )];
            actions.append(&more_menu(items));
        }
        row.add_suffix(&actions);
        row
    }

    fn run(self: &Rc<Self>, name: &str, operation: &'static str) {
        let (this, major, name) = (self.clone(), self.major.clone(), name.to_owned());
        // Shows it running, or waiting behind another one, while the request is out.
        glib::timeout_add_local_once(Duration::from_millis(300), {
            let this = self.clone();
            move || this.load()
        });
        glib::spawn_future_local(async move {
            let result = gio::spawn_blocking(move || Client.postgres_extension(&major, &name, operation)).await.expect("extension request");
            this.show(result);
            // A major's row names its extensions.
            let pane = this.pane.clone();
            if let Ok(versions) = gio::spawn_blocking(|| Client.postgres_versions()).await.expect("versions request") {
                pane.postgres.replace(Some(versions));
                pane.render();
            }
        });
    }
}

#[derive(PartialEq, Clone, Copy)]
enum Activity {
    None,
    Update,
    Working,
    Failed,
}

/// A catalog service (MySQL, Valkey, ...): installed on request, its detail lists its versions.
fn on_request(service: &Service) -> bool {
    !["web", "dns", "hosts", "mail", "dumps", "postgres"].contains(&service.id.as_str())
}

/// PostgreSQL and the catalog services are installed from their detail.
fn installable(service: &Service) -> bool {
    service.id == "postgres" || on_request(service)
}

/// "1.2 GB", "340 MB".
fn bytes(n: i64) -> String {
    glib::format_size(n.max(0) as u64).to_string()
}
