//! The Dumps window: a terminal-like timeline, newest request on top with its dumps, queries,
//! logs, views and jobs one after another. The macOS app's DumpsWindow.

use std::cell::{Cell, RefCell};
use std::rc::Rc;

use adw::prelude::*;
use gtk::{gio, glib};
use webkit6::prelude::*;

use crate::api::{Client, EVENT_KINDS, SettingsChange};
use crate::i18n::{self, tr, trn};
use crate::model::AppModel;
use crate::prefs::{self, EDITORS};
use crate::timeline::{self, Dump, Event};
use crate::ui;

pub struct DumpsWindow {
    pub window: adw::Window,
    model: Rc<AppModel>,
    content: adw::Bin,
    web: RefCell<Option<webkit6::WebView>>,
    capture: gtk::ToggleButton,
    latest_only: gtk::ToggleButton,
    search: gtk::SearchEntry,
    clear: gtk::Button,
    banner: adw::Banner,
    note: gtk::Label,
    title: adw::WindowTitle,
    dumps: RefCell<Vec<Dump>>,
    events: RefCell<Vec<Event>>,
    header: RefCell<String>,
    shown_html: RefCell<String>,
    /// The counters the timeline was last read at; the status says when they change.
    counts: Cell<Option<(u32, u32)>>,
    syncing: Cell<bool>,
}

impl DumpsWindow {
    pub fn new(app: &adw::Application, model: Rc<AppModel>) -> Rc<Self> {
        let title = adw::WindowTitle::new(tr!("menu.dumps"), "");
        let header = adw::HeaderBar::builder().title_widget(&title).build();
        let capture = gtk::ToggleButton::builder()
            .icon_name("media-record-symbolic")
            .tooltip_text(tr!("dumps.capture_dumps_queries_logs_views"))
            .build();
        let latest_only =
            gtk::ToggleButton::builder().icon_name("document-open-recent-symbolic").tooltip_text(tr!("dumps.latest_request_only")).build();
        latest_only.set_active(true);
        let options =
            gtk::MenuButton::builder().icon_name("view-more-symbolic").tooltip_text(tr!("dumps.capture_options_and_editor")).build();
        let clear = gtk::Button::builder().icon_name("user-trash-symbolic").tooltip_text(tr!("logs.clear")).build();
        header.pack_start(&capture);
        header.pack_start(&latest_only);
        header.pack_end(&options);
        header.pack_end(&clear);
        let search = gtk::SearchEntry::builder().placeholder_text(tr!("dumps.search_dumps_queries_logs")).hexpand(true).build();
        let search_bar = gtk::SearchBar::builder()
            .child(&adw::Clamp::builder().maximum_size(520).child(&search).build())
            .search_mode_enabled(true)
            .build();
        search_bar.connect_entry(&search);

        let banner = adw::Banner::builder().button_label(tr!("sites.share.dismiss")).use_markup(false).build();
        let note = gtk::Label::builder()
            .wrap(true)
            .xalign(0.0)
            .css_classes(["caption", "dim-label"])
            .margin_start(12)
            .margin_end(12)
            .margin_top(6)
            .margin_bottom(6)
            .visible(false)
            .build();
        let content = adw::Bin::builder().vexpand(true).build();
        let body = gtk::Box::new(gtk::Orientation::Vertical, 0);
        body.append(&banner);
        body.append(&content);
        body.append(&note);
        let view = adw::ToolbarView::new();
        view.add_top_bar(&header);
        view.add_top_bar(&search_bar);
        view.set_content(Some(&body));
        let window = adw::Window::builder()
            .application(app)
            .title(tr!("menu.dumps"))
            .default_width(980)
            .default_height(720)
            .content(&view)
            .hide_on_close(true)
            .build();

        let this = Rc::new(Self {
            window,
            model,
            content,
            web: RefCell::default(),
            capture,
            latest_only,
            search,
            clear,
            banner,
            note,
            title,
            dumps: RefCell::default(),
            events: RefCell::default(),
            header: RefCell::default(),
            shown_html: RefCell::default(),
            counts: Cell::new(None),
            syncing: Cell::new(false),
        });
        options.set_popover(Some(&this.options_popover()));
        this.connect();
        this
    }

    fn connect(self: &Rc<Self>) {
        let t = self.clone();
        self.capture.connect_toggled(move |b| {
            if !t.syncing.get() {
                t.change(SettingsChange { dumps: Some(b.is_active()), ..Default::default() });
            }
        });
        let t = self.clone();
        self.latest_only.connect_toggled(move |_| t.render());
        let t = self.clone();
        self.search.connect_search_changed(move |_| t.render());
        let t = self.clone();
        self.clear.connect_clicked(move |_| t.confirm_clear());
        self.banner.connect_button_clicked(|b| b.set_revealed(false));

        let weak = Rc::downgrade(self);
        self.model.subscribe(move || {
            let Some(this) = weak.upgrade().filter(|t| t.window.is_visible()) else { return };
            this.sync();
            let counts = this.model.status().map(|s| (s.dump_count, s.event_count));
            if counts != this.counts.get() {
                this.load();
            }
        });
        let weak = Rc::downgrade(self);
        self.window.connect_visible_notify(move |w| {
            if let Some(this) = weak.upgrade().filter(|_| w.is_visible()) {
                this.sync();
                this.load();
            }
        });
    }

    /// The capture toggle and the note follow the status.
    fn sync(&self) {
        let status = self.model.status();
        self.syncing.set(true);
        self.capture.set_active(status.as_ref().is_some_and(|s| s.dumps));
        self.syncing.set(false);
        self.capture.set_sensitive(status.is_some());
        let unsupported = status.as_ref().filter(|s| s.dumps).and_then(|s| s.capture_unsupported.clone()).unwrap_or_default();
        self.note.set_visible(!unsupported.is_empty());
        if !unsupported.is_empty() {
            self.note.set_label(&trn!("dumps.php_versions_only_capture_dumps", unsupported.len(), versions = unsupported.join(", ")));
        }
    }

    fn change(self: &Rc<Self>, change: SettingsChange) {
        let banner = self.banner.clone();
        self.model.change_settings(change, move |error| {
            if let Some(message) = error {
                banner.set_title(&message);
                banner.set_revealed(true);
            }
        });
    }

    /// What is captured besides dumps, whether dump() also shows in the page, and the editor
    /// sources open in.
    fn options_popover(self: &Rc<Self>) -> gtk::Popover {
        let column = gtk::Box::builder()
            .orientation(gtk::Orientation::Vertical)
            .spacing(4)
            .margin_top(6)
            .margin_bottom(6)
            .margin_start(6)
            .margin_end(6)
            .build();
        column.append(&gtk::Label::builder().label(tr!("dumps.capture_short")).xalign(0.0).css_classes(["heading"]).build());
        let mut checks = Vec::new();
        for (kind, title) in EVENT_KINDS {
            let check = gtk::CheckButton::with_label(i18n::translate(title));
            let t = self.clone();
            check.connect_toggled(move |c| {
                if t.syncing.get() {
                    return;
                }
                let mut off = t.model.status().and_then(|s| s.capture_off).unwrap_or_default();
                off.retain(|k| k != kind);
                if !c.is_active() {
                    off.push(kind.to_owned());
                }
                t.change(SettingsChange { capture_off: Some(off), ..Default::default() });
            });
            column.append(&check);
            checks.push((kind, check));
        }
        let in_page = gtk::CheckButton::with_label(tr!("dumps.show_dump_in_the_page"));
        let t = self.clone();
        in_page.connect_toggled(move |c| {
            if !t.syncing.get() {
                t.change(SettingsChange { dumps_in_page: Some(c.is_active()), ..Default::default() });
            }
        });
        column.append(&in_page);
        column.append(&gtk::Separator::new(gtk::Orientation::Horizontal));
        column
            .append(&gtk::Label::builder().label(tr!("dumps.open_sources_in_sentence_case")).xalign(0.0).css_classes(["heading"]).build());
        let editor = gtk::DropDown::from_strings(&EDITORS.map(|(_, name)| i18n::translate(name)));
        let current = prefs::load().editor;
        editor.set_selected(EDITORS.iter().position(|(id, _)| *id == current).unwrap_or(0) as u32);
        let t = self.clone();
        editor.connect_selected_notify(move |d| {
            let mut p = prefs::load();
            p.editor = EDITORS[d.selected() as usize].0.to_owned();
            let _ = prefs::save(&p);
            t.shown_html.replace(String::new());
            t.render();
        });
        column.append(&editor);
        let popover = gtk::Popover::builder().child(&column).build();
        let t = self.clone();
        popover.connect_show(move |_| {
            let status = t.model.status();
            let off = status.as_ref().and_then(|s| s.capture_off.clone()).unwrap_or_default();
            t.syncing.set(true);
            for (kind, check) in &checks {
                check.set_active(!off.iter().any(|k| k == kind));
                check.set_sensitive(status.is_some());
            }
            in_page.set_active(status.as_ref().is_some_and(|s| s.dumps_in_page));
            t.syncing.set(false);
        });
        popover
    }

    fn load(self: &Rc<Self>) {
        self.counts.set(self.model.status().map(|s| (s.dump_count, s.event_count)));
        let this = self.clone();
        let need_header = self.header.borrow().is_empty();
        glib::spawn_future_local(async move {
            let (dumps, events, header) = gio::spawn_blocking(move || {
                let dumps = Client.dumps();
                // The header only exists once a dump was captured.
                let header = if need_header && dumps.as_ref().is_ok_and(|d| !d.is_empty()) { Client.dump_header().ok() } else { None };
                (dumps, Client.events(), header)
            })
            .await
            .expect("dumps request");
            match (dumps, events) {
                (Ok(dumps), Ok(events)) => {
                    this.dumps.replace(dumps);
                    this.events.replace(events);
                    if let Some(header) = header {
                        this.header.replace(header);
                    }
                    this.banner.set_revealed(false);
                }
                (Err(e), _) | (_, Err(e)) => {
                    this.banner.set_title(&e.to_string());
                    this.banner.set_revealed(true);
                }
            }
            this.render();
        });
    }

    fn render(&self) {
        let all = timeline::merge(&self.dumps.borrow(), &self.events.borrow());
        let latest = if self.latest_only.is_active() { timeline::latest_group(&all) } else { all.clone() };
        let visible = timeline::filter(latest, &self.search.text());
        self.clear.set_sensitive(!all.is_empty());
        self.title.set_subtitle(&if visible.is_empty() { String::new() } else { trn!("dumps.n_entries", visible.len()) });
        if visible.is_empty() {
            self.shown_html.replace(String::new());
            self.web.replace(None);
            let capturing = self.model.status().is_some_and(|s| s.dumps);
            let page = ui::empty(
                "applications-science-symbolic",
                if all.is_empty() { tr!("dumps.nothing_captured_yet") } else { tr!("dumps.nothing_matches") },
                if capturing { tr!("dumps.empty_capturing") } else { tr!("dumps.capture_is_off_turn_it_on") },
            );
            self.content.set_child(Some(&page));
            return;
        }
        let html = timeline::html(&visible, &self.header.borrow(), &prefs::load().editor);
        if *self.shown_html.borrow() == html {
            return;
        }
        let web = self.web.borrow().clone();
        match web {
            Some(web) => web.load_html(&with_policy(&html), None),
            None => {
                let web = ui::html_view(&html, true);
                self.content.set_child(Some(&web));
                self.web.replace(Some(web));
            }
        }
        self.shown_html.replace(html);
    }

    fn confirm_clear(self: &Rc<Self>) {
        let dialog = adw::AlertDialog::new(Some(tr!("dumps.clear_everything_captured")), Some(tr!("dumps.every_captured_dump_query_log")));
        dialog.add_responses(&[("cancel", tr!("common.cancel")), ("clear", tr!("logs.clear"))]);
        dialog.set_response_appearance("clear", adw::ResponseAppearance::Destructive);
        let this = self.clone();
        dialog.choose(Some(&self.window), gio::Cancellable::NONE, move |response| {
            if response == "clear" {
                let this = this.clone();
                glib::spawn_future_local(async move {
                    let _ = gio::spawn_blocking(|| Client.clear_dumps()).await;
                    this.load();
                });
            }
        });
    }
}

/// The same policy ui::html_view puts in front of a page, for reloading it.
fn with_policy(html: &str) -> String {
    ui::with_policy(html, true)
}
