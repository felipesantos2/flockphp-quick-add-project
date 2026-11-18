//! The Flock window: a sidebar of panes like the macOS app's Settings window has them, the selected
//! pane next to it. Until Flock is set up, the window is the setup. It reads the daemon's config
//! and services while it is open.

use std::cell::{Cell, RefCell};
use std::rc::Rc;

use adw::prelude::*;
use gtk::{gio, glib};

use crate::api::Client;
use crate::i18n::{self, tr, tr_noop};
use crate::links::Link;
use crate::model::AppModel;
use crate::panes::{self, Data, Pane};
use crate::setup::SetupView;

/// The panes in sidebar order: id, title, icon.
const PANES: [(&str, &str, &str); 10] = [
    ("general", tr_noop!("general.title"), "emblem-system-symbolic"),
    ("sites", tr_noop!("settings.pane.sites"), "dev.flock.Flock-sites-symbolic"),
    ("php", "PHP", "utilities-terminal-symbolic"),
    ("node", "Node", "package-x-generic-symbolic"),
    ("services", tr_noop!("settings.pane.services"), "network-server-symbolic"),
    ("mail", tr_noop!("services.category.mail"), "mail-unread-symbolic"),
    ("dumps", tr_noop!("menu.dumps"), "applications-science-symbolic"),
    ("debugger", tr_noop!("settings.pane.debugger"), "applications-engineering-symbolic"),
    ("ai", tr_noop!("settings.pane.ai"), "starred-symbolic"),
    ("about", tr_noop!("settings.pane.about"), "help-about-symbolic"),
];

pub struct MainWindow {
    pub window: adw::ApplicationWindow,
    model: Rc<AppModel>,
    /// The setup or the panes.
    top: gtk::Stack,
    setup: Rc<SetupView>,
    title: adw::WindowTitle,
    stack: gtk::Stack,
    content: adw::NavigationPage,
    split: adw::NavigationSplitView,
    sidebar: gtk::ListBox,
    banner: adw::Banner,
    unavailable: adw::StatusPage,
    unavailable_button: gtk::Button,
    /// The Language setting under it, which doesn't need the daemon.
    unavailable_language: adw::Bin,
    panes: Vec<(&'static str, Box<dyn Pane>)>,
    services: panes::services::ServicesPane,
    sites: panes::sites::SitesPane,
    data: RefCell<Data>,
    loading: Cell<bool>,
    /// The failed action (a restart, a toggle in the tray) the banner shows.
    action_error: RefCell<Option<String>>,
}

impl MainWindow {
    pub fn new(app: &adw::Application, model: Rc<AppModel>) -> Rc<Self> {
        let sidebar = gtk::ListBox::builder().css_classes(["navigation-sidebar"]).build();
        for (_, title, icon) in PANES {
            let row = gtk::Box::builder().spacing(12).margin_top(6).margin_bottom(6).margin_start(6).build();
            row.append(&gtk::Image::from_icon_name(icon));
            row.append(&gtk::Label::new(Some(i18n::translate(title))));
            sidebar.append(&row);
        }
        let title = adw::WindowTitle::new("Flock", "");
        let menu = gio::Menu::new();
        let windows = gio::Menu::new();
        windows.append(Some(tr!("services.category.mail")), Some("app.mail"));
        windows.append(Some(tr!("menu.dumps")), Some("app.dumps"));
        windows.append(Some(tr!("dumps.event_kind.logs")), Some("app.logs"));
        menu.append_section(None, &windows);
        let quit = gio::Menu::new();
        quit.append(Some(tr!("menu.quit_flock")), Some("app.quit"));
        menu.append_section(None, &quit);
        let menu_button = gtk::MenuButton::builder().icon_name("open-menu-symbolic").menu_model(&menu).primary(true).build();
        let sidebar_header = adw::HeaderBar::builder().title_widget(&title).build();
        sidebar_header.pack_end(&menu_button);
        let sidebar_view = adw::ToolbarView::new();
        sidebar_view.add_top_bar(&sidebar_header);
        sidebar_view.set_content(Some(&gtk::ScrolledWindow::builder().child(&sidebar).build()));
        let sidebar_page = adw::NavigationPage::new(&sidebar_view, "Flock");

        let banner = adw::Banner::builder().button_label(tr!("sites.share.dismiss")).use_markup(false).build();
        let services = panes::services::ServicesPane::new(model.clone(), banner.clone());
        let sites = panes::sites::SitesPane::new(model.clone(), banner.clone());
        let panes: Vec<(&'static str, Box<dyn Pane>)> = vec![
            ("general", Box::new(panes::general::GeneralPane::new(model.clone(), banner.clone()))),
            ("sites", Box::new(sites.clone())),
            ("php", Box::new(panes::php::PhpPane::new(model.clone(), banner.clone()))),
            ("node", Box::new(panes::node::NodePane::new(model.clone(), banner.clone()))),
            ("services", Box::new(services.clone())),
            ("mail", Box::new(panes::mail::MailPane::new())),
            ("dumps", Box::new(panes::dumps::DumpsPane::new(model.clone(), banner.clone()))),
            ("debugger", Box::new(panes::debugger::DebuggerPane::new(model.clone(), banner.clone()))),
            ("ai", Box::new(panes::ai::AiPane::new(banner.clone()))),
            ("about", Box::new(panes::about::AboutPane::new(model.clone()))),
        ];
        let stack = gtk::Stack::new();
        for (id, pane) in &panes {
            stack.add_named(&pane.widget(), Some(id));
        }
        let unavailable_button = gtk::Button::builder().halign(gtk::Align::Center).css_classes(["pill", "suggested-action"]).build();
        let unavailable_language = adw::Bin::new();
        let unavailable_body = gtk::Box::builder().orientation(gtk::Orientation::Vertical).spacing(36).build();
        unavailable_body.append(&unavailable_button);
        unavailable_body.append(&adw::Clamp::builder().maximum_size(480).child(&unavailable_language).build());
        let unavailable = adw::StatusPage::builder().icon_name("network-server-symbolic").child(&unavailable_body).build();
        stack.add_named(&unavailable, Some("unavailable"));

        let body = gtk::Box::new(gtk::Orientation::Vertical, 0);
        body.append(&banner);
        body.append(&stack);
        let content_view = adw::ToolbarView::new();
        content_view.add_top_bar(&adw::HeaderBar::new());
        content_view.set_content(Some(&body));
        let content = adw::NavigationPage::new(&content_view, tr!("general.title"));
        let split = adw::NavigationSplitView::builder().sidebar(&sidebar_page).content(&content).build();

        let setup = SetupView::new(model.clone());
        let setup_view = adw::ToolbarView::new();
        setup_view.add_top_bar(&adw::HeaderBar::builder().title_widget(&adw::WindowTitle::new("Flock", "")).build());
        setup_view.set_content(Some(&setup.widget));

        let top = gtk::Stack::new();
        top.add_named(&split, Some("panes"));
        top.add_named(&setup_view, Some("setup"));

        let window =
            adw::ApplicationWindow::builder().application(app).title("Flock").default_width(900).default_height(640).content(&top).build();
        // Narrow, the sidebar and the pane take turns.
        let breakpoint = adw::Breakpoint::new(adw::BreakpointCondition::new_length(
            adw::BreakpointConditionLengthType::MaxWidth,
            560.0,
            adw::LengthUnit::Sp,
        ));
        breakpoint.add_setter(&split, "collapsed", Some(&true.to_value()));
        window.add_breakpoint(breakpoint);

        let this = Rc::new(Self {
            window,
            model,
            top,
            setup,
            title,
            stack,
            content,
            split,
            sidebar,
            banner,
            unavailable,
            unavailable_button,
            unavailable_language,
            panes,
            services,
            sites,
            data: RefCell::new(Data::default()),
            loading: Cell::new(false),
            action_error: RefCell::new(None),
        });
        this.connect();
        this.select("general");
        this
    }

    fn connect(self: &Rc<Self>) {
        let weak = Rc::downgrade(self);
        self.sidebar.connect_row_selected(move |_, row| {
            if let (Some(this), Some(row)) = (weak.upgrade(), row) {
                this.show(PANES[row.index() as usize].0);
            }
        });
        // Collapsed, a click on the selected pane shows it again.
        let weak = Rc::downgrade(self);
        self.sidebar.connect_row_activated(move |_, _| {
            if let Some(this) = weak.upgrade() {
                this.split.set_show_content(true);
            }
        });
        let model = self.model.clone();
        self.banner.connect_button_clicked(move |banner| {
            banner.set_revealed(false);
            model.dismiss_error();
        });

        let model = self.model.clone();
        self.unavailable_button.connect_clicked(move |_| model.restart());

        let weak = Rc::downgrade(self);
        self.setup.connect_finish(move || {
            if let Some(this) = weak.upgrade() {
                // An install link that waited for the setup.
                if let Some(Link::Install(link)) = this.model.link() {
                    this.select(&link.pane());
                }
                this.reload();
            }
        });

        let weak = Rc::downgrade(self);
        self.model.subscribe(move || {
            if let Some(this) = weak.upgrade() {
                this.title.set_subtitle(&this.model.headline());
                this.show_action_error();
                if this.window.is_visible() {
                    this.reload();
                }
            }
        });
        let weak = Rc::downgrade(self);
        self.window.connect_visible_notify(move |window| {
            if let Some(this) = weak.upgrade().filter(|_| window.is_visible()) {
                this.reload();
            }
        });
    }

    /// Shows a failed action in the banner the panes report their errors in, once.
    fn show_action_error(&self) {
        let error = self.model.action_error();
        if *self.action_error.borrow() == error {
            return;
        }
        let previous = self.action_error.replace(error.clone());
        match error {
            Some(message) => {
                self.banner.set_title(&message);
                self.banner.set_revealed(true);
            }
            // The action succeeded since; a pane's error stays.
            None if previous.is_some_and(|p| self.banner.title() == glib::markup_escape_text(&p)) => self.banner.set_revealed(false),
            None => {}
        }
    }

    /// Shows a pane, e.g. from the tray's menu: "general", "services/<id>" at a service's detail,
    /// "sites/<name>" at a site's.
    pub fn select(&self, pane: &str) {
        if let Some(service) = pane.strip_prefix("services/") {
            self.select("services");
            self.services.select(service);
        } else if let Some(site) = pane.strip_prefix("sites/") {
            self.select("sites");
            self.sites.select(site);
        } else if let Some(index) = PANES.iter().position(|(id, _, _)| *id == pane) {
            self.sidebar.select_row(self.sidebar.row_at_index(index as i32).as_ref());
            self.split.set_show_content(true);
        }
    }

    fn show(&self, id: &str) {
        let title = PANES.iter().find(|(pane, _, _)| *pane == id).map_or("", |p| i18n::translate(p.1));
        self.content.set_title(title);
        self.stack.set_visible_child_name(id);
        self.render();
    }

    fn selected(&self) -> &'static str {
        self.sidebar.selected_row().map_or("general", |row| PANES[row.index() as usize].0)
    }

    /// Reads the config and services again, then shows them with the current status.
    fn reload(self: &Rc<Self>) {
        self.render();
        if self.model.status().is_none() || self.loading.replace(true) {
            return;
        }
        let this = self.clone();
        glib::spawn_future_local(async move {
            let (config, services) = gio::spawn_blocking(|| (Client.config(), Client.services())).await.expect("settings requests");
            this.loading.set(false);
            {
                let mut data = this.data.borrow_mut();
                if let Ok(config) = config {
                    data.config = Some(config);
                }
                if let Ok(services) = services {
                    data.services = services;
                }
            }
            this.render();
        });
    }

    fn render(&self) {
        // Until the setup is done, a daemon that answers is the setup's (it may still fail).
        if self.model.needs_setup() || self.setup.engaged() {
            self.top.set_visible_child_name("setup");
            return;
        }
        self.top.set_visible_child_name("panes");
        let status = self.model.status();
        let id = self.selected();
        if status.is_none() && id != "about" {
            self.unavailable.set_title(tr!("settings.flock_is_not_running"));
            self.unavailable
                .set_description(Some(&self.model.error().unwrap_or_else(|| tr!("settings.start_daemon_to_change").to_owned())));
            self.unavailable_button.set_label(tr!("menu.start_flock"));
            self.unavailable_button.set_sensitive(!self.model.is_restarting());
            // Built again when the page shows, with the setting as it is now.
            if self.stack.visible_child_name().as_deref() != Some("unavailable") {
                self.unavailable_language.set_child(Some(&panes::general::language()));
            }
            self.stack.set_visible_child_name("unavailable");
            return;
        }
        self.stack.set_visible_child_name(id);
        let data = {
            let mut data = self.data.borrow_mut();
            data.status = status;
            data.clone()
        };
        for (pane_id, pane) in &self.panes {
            if *pane_id == id {
                pane.update(&data);
            }
        }
    }
}
