//! The Logs window: the sites' Laravel logs as entries (filtered by level and search) and the
//! services' logs as text, newest lines in view. The macOS app's LogsWindow.

use std::cell::{Cell, RefCell};
use std::rc::Rc;
use std::time::Duration;

use adw::prelude::*;
use gtk::{gio, glib};

use crate::api::{Client, LogEntry, LogIndex};
use crate::i18n::{self, tr, tr_noop};
use crate::ui;

/// A log the window shows.
#[derive(Clone, PartialEq, Debug)]
pub enum Source {
    Site(String),
    Service(String),
}

/// The level filter's choices: the minimum level, "" for all.
const LEVELS: [(&str, &str); 4] = [
    ("", tr_noop!("logs.all_levels")),
    ("error", tr_noop!("logs.error_and_above")),
    ("warning", tr_noop!("logs.warning_and_above")),
    ("info", tr_noop!("logs.info_and_above")),
];

pub struct LogsWindow {
    pub window: adw::Window,
    list: gtk::ListBox,
    content: adw::Bin,
    banner: adw::Banner,
    search: gtk::SearchEntry,
    level: gtk::DropDown,
    auto_refresh: gtk::ToggleButton,
    clear: gtk::Button,
    title: adw::WindowTitle,
    split: adw::NavigationSplitView,
    index: RefCell<Vec<Source>>,
    selected: RefCell<Option<Source>>,
    entries: RefCell<Vec<LogEntry>>,
    text: RefCell<String>,
    /// What the content shows, so a refresh that changed nothing keeps the scroll position.
    shown: RefCell<String>,
    refreshing: Cell<bool>,
}

impl LogsWindow {
    pub fn new(app: &adw::Application) -> Rc<Self> {
        let list = gtk::ListBox::builder().css_classes(["navigation-sidebar"]).build();
        let sidebar_view = adw::ToolbarView::new();
        sidebar_view.add_top_bar(&adw::HeaderBar::new());
        sidebar_view.set_content(Some(&gtk::ScrolledWindow::builder().child(&list).vexpand(true).build()));
        let sidebar = adw::NavigationPage::new(&sidebar_view, tr!("dumps.event_kind.logs"));

        let title = adw::WindowTitle::new(tr!("dumps.event_kind.logs"), "");
        let header = adw::HeaderBar::builder().title_widget(&title).build();
        let level = gtk::DropDown::from_strings(&LEVELS.map(|(_, t)| i18n::translate(t)));
        level.set_tooltip_text(Some(tr!("logs.show_only_entries_of_this")));
        let auto_refresh = gtk::ToggleButton::builder()
            .icon_name("emblem-synchronizing-symbolic")
            .tooltip_text(tr!("logs.reload_the_log_every_2"))
            .build();
        let refresh = gtk::Button::builder().icon_name("view-refresh-symbolic").tooltip_text(tr!("mail.refresh")).build();
        let clear = gtk::Button::builder().icon_name("user-trash-symbolic").tooltip_text(tr!("logs.clear_the_sites_log")).build();
        header.pack_start(&level);
        header.pack_end(&clear);
        header.pack_end(&refresh);
        header.pack_end(&auto_refresh);
        let search = gtk::SearchEntry::builder().placeholder_text(tr!("logs.search_logs")).hexpand(true).build();
        let search_bar = gtk::SearchBar::builder()
            .child(&adw::Clamp::builder().maximum_size(520).child(&search).build())
            .search_mode_enabled(true)
            .build();
        search_bar.connect_entry(&search);
        let banner = adw::Banner::builder().button_label(tr!("sites.share.dismiss")).use_markup(false).build();
        let content = adw::Bin::builder().vexpand(true).build();
        let body = gtk::Box::new(gtk::Orientation::Vertical, 0);
        body.append(&banner);
        body.append(&content);
        let content_view = adw::ToolbarView::new();
        content_view.add_top_bar(&header);
        content_view.add_top_bar(&search_bar);
        content_view.set_content(Some(&body));
        let page = adw::NavigationPage::new(&content_view, tr!("services.log"));

        let split = adw::NavigationSplitView::builder().sidebar(&sidebar).content(&page).min_sidebar_width(220.0).build();
        let window = adw::Window::builder()
            .application(app)
            .title(tr!("dumps.event_kind.logs"))
            .default_width(1100)
            .default_height(720)
            .content(&split)
            .hide_on_close(true)
            .build();

        let this = Rc::new(Self {
            window,
            list,
            content,
            banner,
            search,
            level,
            auto_refresh,
            clear,
            title,
            split,
            index: RefCell::default(),
            selected: RefCell::default(),
            entries: RefCell::default(),
            text: RefCell::default(),
            shown: RefCell::default(),
            refreshing: Cell::new(false),
        });
        let t = this.clone();
        this.list.connect_row_activated(move |_, row| {
            let source = t.index.borrow().get(row.index() as usize).cloned();
            if let Some(source) = source {
                t.select(source);
            }
        });
        let t = this.clone();
        refresh.connect_clicked(move |_| {
            t.load_index();
            t.load();
        });
        let t = this.clone();
        this.search.connect_search_changed(move |_| t.render());
        let t = this.clone();
        this.level.connect_selected_notify(move |_| t.render());
        let t = this.clone();
        this.clear.connect_clicked(move |_| t.confirm_clear());
        this.banner.connect_button_clicked(|b| b.set_revealed(false));
        let t = this.clone();
        this.auto_refresh.connect_toggled(move |b| {
            if b.is_active() {
                t.start_auto_refresh();
            }
        });
        let weak = Rc::downgrade(&this);
        this.window.connect_visible_notify(move |w| {
            if let Some(this) = weak.upgrade().filter(|_| w.is_visible()) {
                this.load_index();
            }
        });
        this.render();
        this
    }

    /// Shows a log, e.g. a site's FrankenPHP log from Settings → Sites.
    pub fn select(self: &Rc<Self>, source: Source) {
        self.selected.replace(Some(source.clone()));
        self.entries.replace(Vec::new());
        self.text.replace(String::new());
        self.shown.replace(String::new());
        self.split.set_show_content(true);
        let (name, kind) = match &source {
            Source::Site(n) => (n.clone(), tr!("logs.laravel_log")),
            Source::Service(n) => (n.clone(), tr!("logs.service_log")),
        };
        self.title.set_title(&name);
        self.title.set_subtitle(kind);
        self.level.set_sensitive(matches!(source, Source::Site(_)));
        self.clear.set_visible(matches!(source, Source::Site(_)));
        if let Some(index) = self.index.borrow().iter().position(|s| *s == source) {
            self.list.select_row(self.list.row_at_index(index as i32).as_ref());
        }
        self.content.set_child(Some(&gtk::Spinner::builder().spinning(true).halign(gtk::Align::Center).valign(gtk::Align::Center).build()));
        self.load();
    }

    fn error(&self, error: Option<String>) {
        match error {
            Some(message) => {
                self.banner.set_title(&message);
                self.banner.set_revealed(true);
            }
            None => self.banner.set_revealed(false),
        }
    }

    fn load_index(self: &Rc<Self>) {
        let this = self.clone();
        glib::spawn_future_local(async move {
            match gio::spawn_blocking(|| Client.logs()).await.expect("logs request") {
                Ok(index) => this.set_index(index),
                Err(e) => this.error(Some(e.to_string())),
            }
        });
    }

    fn set_index(&self, index: LogIndex) {
        let sources: Vec<Source> = index
            .sites
            .unwrap_or_default()
            .into_iter()
            .map(Source::Site)
            .chain(index.services.unwrap_or_default().into_iter().map(Source::Service))
            .collect();
        if *self.index.borrow() == sources {
            return;
        }
        while let Some(row) = self.list.row_at_index(0) {
            self.list.remove(&row);
        }
        for source in &sources {
            let (name, icon) = match source {
                Source::Site(n) => (n.as_str(), "dev.flock.Flock-sites-symbolic"),
                Source::Service(n) => (n.as_str(), "emblem-system-symbolic"),
            };
            let row = gtk::Box::builder().spacing(10).margin_top(4).margin_bottom(4).build();
            row.append(&gtk::Image::from_icon_name(icon));
            row.append(&gtk::Label::builder().label(name).ellipsize(gtk::pango::EllipsizeMode::End).build());
            self.list.append(&row);
        }
        if let Some(index) = self.selected.borrow().as_ref().and_then(|selected| sources.iter().position(|s| s == selected)) {
            self.list.select_row(self.list.row_at_index(index as i32).as_ref());
        }
        self.index.replace(sources);
    }

    /// Reads the selected log again.
    fn load(self: &Rc<Self>) {
        let Some(source) = self.selected.borrow().clone() else { return };
        let this = self.clone();
        glib::spawn_future_local(async move {
            let s = source.clone();
            let result = gio::spawn_blocking(move || match s {
                Source::Site(name) => Client.site_log(&name).map(Ok),
                Source::Service(name) => Client.service_log(&name).map(Err),
            })
            .await
            .expect("log request");
            // Another log was picked meanwhile.
            if this.selected.borrow().as_ref() != Some(&source) {
                return;
            }
            match result {
                Ok(Ok(entries)) => {
                    this.entries.replace(entries);
                    this.error(None);
                }
                Ok(Err(text)) => {
                    this.text.replace(text);
                    this.error(None);
                }
                Err(e) => this.error(Some(e.to_string())),
            }
            this.render();
        });
    }

    fn start_auto_refresh(self: &Rc<Self>) {
        if self.refreshing.replace(true) {
            return;
        }
        let weak = Rc::downgrade(self);
        glib::timeout_add_local(Duration::from_secs(2), move || {
            let Some(this) = weak.upgrade() else { return glib::ControlFlow::Break };
            if !this.auto_refresh.is_active() {
                this.refreshing.set(false);
                return glib::ControlFlow::Break;
            }
            if this.window.is_visible() && this.window.is_active() {
                this.load();
            }
            glib::ControlFlow::Continue
        });
    }

    fn render(&self) {
        let query = self.search.text().trim().to_lowercase();
        let Some(source) = self.selected.borrow().clone() else {
            self.content.set_child(Some(&ui::empty("text-x-generic-symbolic", tr!("logs.pick_a_log"), "")));
            return;
        };
        match source {
            Source::Service(_) => {
                let text = self.text.borrow().clone();
                let lines = if query.is_empty() {
                    text
                } else {
                    text.lines().filter(|l| l.to_lowercase().contains(&query)).collect::<Vec<_>>().join("\n")
                };
                let key = format!("service{lines}");
                if *self.shown.borrow() == key {
                    return;
                }
                self.shown.replace(key);
                let view = ui::monospace(if lines.is_empty() { tr!("common.empty_parenthesized") } else { &lines });
                // The newest lines are at the bottom.
                let adjustment = view.vadjustment();
                glib::idle_add_local_once(move || adjustment.set_value(adjustment.upper()));
                self.content.set_child(Some(&view));
            }
            Source::Site(name) => {
                let minimum = LEVELS[self.level.selected() as usize].0;
                let all = self.entries.borrow().clone();
                self.clear.set_sensitive(!all.is_empty());
                let entries: Vec<LogEntry> = all
                    .iter()
                    .filter(|e| {
                        e.at_least(minimum)
                            && (query.is_empty() || e.message.to_lowercase().contains(&query) || e.details.to_lowercase().contains(&query))
                    })
                    .cloned()
                    .collect();
                let key = format!("site{entries:?}");
                if *self.shown.borrow() == key {
                    return;
                }
                self.shown.replace(key);
                if entries.is_empty() {
                    let page = if all.is_empty() {
                        ui::empty("emblem-ok-symbolic", tr!("logs.no_log_entries"), &tr!("logs.storage_logs_of_site_is", site = name))
                    } else {
                        ui::empty("system-search-symbolic", tr!("logs.no_results"), tr!("logs.no_entry_matches_the_search"))
                    };
                    self.content.set_child(Some(&page));
                    return;
                }
                let list = gtk::ListBox::builder()
                    .css_classes(["boxed-list"])
                    .selection_mode(gtk::SelectionMode::None)
                    .valign(gtk::Align::Start)
                    .margin_top(12)
                    .margin_bottom(12)
                    .margin_start(12)
                    .margin_end(12)
                    .build();
                for entry in &entries {
                    list.append(&entry_row(entry));
                }
                self.content.set_child(Some(&gtk::ScrolledWindow::builder().child(&list).vexpand(true).build()));
            }
        }
    }

    fn confirm_clear(self: &Rc<Self>) {
        let Some(Source::Site(name)) = self.selected.borrow().clone() else { return };
        let dialog = adw::AlertDialog::new(
            Some(&tr!("logs.clear_question", site = name)),
            Some(&tr!("logs.deletes_the_laravel_log_files", site = name)),
        );
        dialog.add_responses(&[("cancel", tr!("common.cancel")), ("clear", tr!("logs.clear"))]);
        dialog.set_response_appearance("clear", adw::ResponseAppearance::Destructive);
        let this = self.clone();
        dialog.choose(Some(&self.window), gio::Cancellable::NONE, move |response| {
            if response == "clear" {
                let (this, name) = (this.clone(), name.clone());
                glib::spawn_future_local(async move {
                    let result = gio::spawn_blocking(move || Client.clear_site_log(&name)).await.expect("logs request");
                    this.error(result.err().map(|e| e.to_string()));
                    this.load();
                });
            }
        });
    }
}

fn entry_row(entry: &LogEntry) -> gtk::Widget {
    let level = entry.level.to_lowercase();
    let class = match level.as_str() {
        "debug" => "dim-label",
        "info" | "notice" => "accent",
        "warning" => "warning",
        _ => "error",
    };
    let column = gtk::Box::builder()
        .orientation(gtk::Orientation::Vertical)
        .spacing(4)
        .margin_top(8)
        .margin_bottom(8)
        .margin_start(12)
        .margin_end(12)
        .build();
    let top = gtk::Box::builder().spacing(8).build();
    top.append(&gtk::Label::builder().label(level.to_uppercase()).css_classes(["caption", "badge", class]).build());
    top.append(&gtk::Label::builder().label(ui::date_time(&entry.time)).css_classes(["caption", "dim-label", "numeric"]).build());
    if !entry.env.is_empty() {
        top.append(&gtk::Label::builder().label(&entry.env).css_classes(["caption", "dim-label"]).build());
    }
    column.append(&top);
    column.append(
        &gtk::Label::builder()
            .label(&entry.message)
            .xalign(0.0)
            .wrap(true)
            .wrap_mode(gtk::pango::WrapMode::WordChar)
            .selectable(true)
            .lines(3)
            .ellipsize(gtk::pango::EllipsizeMode::End)
            .build(),
    );
    if !entry.details.is_empty() {
        let details =
            gtk::Label::builder().label(&entry.details).xalign(0.0).selectable(true).css_classes(["monospace", "caption"]).build();
        // Stack traces run to hundreds of lines; they scroll on their own.
        let child: gtk::Widget = if entry.details.lines().count() > 20 {
            gtk::ScrolledWindow::builder().child(&details).height_request(280).build().upcast()
        } else {
            details.upcast()
        };
        column.append(&gtk::Expander::builder().label(tr!("logs.details")).child(&child).build());
    }
    gtk::ListBoxRow::builder().child(&column).activatable(false).build().upcast()
}
