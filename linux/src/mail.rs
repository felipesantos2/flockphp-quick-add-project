//! The Mail window: every caught message, newest first, and the selected one as HTML, text, raw
//! message or headers, with its attachments. The macOS app's MailWindow.

use std::cell::{Cell, RefCell};
use std::rc::Rc;

use adw::prelude::*;
use gtk::{gio, glib};

use crate::api::{Client, MailDetail, MailSummary};
use crate::i18n::tr;
use crate::model::AppModel;
use crate::ui;

pub struct MailWindow {
    pub window: adw::Window,
    list: gtk::ListBox,
    detail: adw::Bin,
    clear: gtk::Button,
    banner: adw::Banner,
    split: adw::NavigationSplitView,
    messages: RefCell<Vec<MailSummary>>,
    selected: RefCell<Option<String>>,
    /// The mail count the list was last read at; the status says when it changes.
    count: Cell<Option<u32>>,
    /// The .env lines that send a project's mail to Flock.
    env: RefCell<String>,
    /// Shows the newest message once the list is read (`--open mail/newest`).
    select_newest: Cell<bool>,
    model: Rc<AppModel>,
}

impl MailWindow {
    pub fn new(app: &adw::Application, model: Rc<AppModel>) -> Rc<Self> {
        let list = gtk::ListBox::builder().css_classes(["navigation-sidebar"]).build();
        let sidebar_view = adw::ToolbarView::new();
        let sidebar_header = adw::HeaderBar::new();
        let refresh = gtk::Button::builder().icon_name("view-refresh-symbolic").tooltip_text(tr!("mail.refresh")).build();
        let clear = gtk::Button::builder().icon_name("user-trash-symbolic").tooltip_text(tr!("mail.delete_all")).build();
        sidebar_header.pack_start(&refresh);
        sidebar_header.pack_end(&clear);
        sidebar_view.add_top_bar(&sidebar_header);
        sidebar_view.set_content(Some(&gtk::ScrolledWindow::builder().child(&list).vexpand(true).build()));
        let sidebar = adw::NavigationPage::new(&sidebar_view, tr!("services.category.mail"));

        let detail = adw::Bin::new();
        let banner = adw::Banner::builder().button_label(tr!("sites.share.dismiss")).use_markup(false).build();
        let body = gtk::Box::new(gtk::Orientation::Vertical, 0);
        body.append(&banner);
        body.append(&detail);
        let content_view = adw::ToolbarView::new();
        content_view.add_top_bar(&adw::HeaderBar::builder().show_title(false).build());
        content_view.set_content(Some(&body));
        let content = adw::NavigationPage::new(&content_view, tr!("mail.message"));

        let split = adw::NavigationSplitView::builder().sidebar(&sidebar).content(&content).min_sidebar_width(300.0).build();
        let window = adw::Window::builder()
            .application(app)
            .title(tr!("services.category.mail"))
            .default_width(1100)
            .default_height(720)
            .content(&split)
            .hide_on_close(true)
            .build();

        let this = Rc::new(Self {
            window,
            list,
            detail,
            clear,
            banner,
            split,
            messages: RefCell::default(),
            selected: RefCell::default(),
            count: Cell::new(None),
            env: RefCell::default(),
            select_newest: Cell::new(false),
            model,
        });
        let t = this.clone();
        refresh.connect_clicked(move |_| t.load());
        let t = this.clone();
        this.clear.connect_clicked(move |_| t.confirm_clear());
        this.banner.connect_button_clicked(|b| b.set_revealed(false));
        let t = this.clone();
        this.list.connect_row_activated(move |_, row| {
            let id = t.messages.borrow().get(row.index() as usize).map(|m| m.id.clone());
            if let Some(id) = id {
                t.show(&id);
            }
        });
        let weak = Rc::downgrade(&this);
        this.model.subscribe(move || {
            let Some(this) = weak.upgrade().filter(|t| t.window.is_visible()) else { return };
            let count = this.model.status().map(|s| s.mail_count);
            if count != this.count.get() {
                this.load();
            }
        });
        let weak = Rc::downgrade(&this);
        this.window.connect_visible_notify(move |w| {
            if let Some(this) = weak.upgrade().filter(|_| w.is_visible()) {
                this.load();
            }
        });
        this.show_empty();
        this
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

    /// Reads the messages again (and the .env lines for the empty window).
    fn load(self: &Rc<Self>) {
        self.count.set(self.model.status().map(|s| s.mail_count));
        let this = self.clone();
        glib::spawn_future_local(async move {
            let (messages, services) = gio::spawn_blocking(|| (Client.mail(), Client.services())).await.expect("mail request");
            if let Some(env) = services.ok().and_then(|s| s.into_iter().find(|s| s.id == "mail")).map(|s| s.env) {
                this.env.replace(env);
            }
            match messages {
                Ok(messages) => {
                    this.error(None);
                    this.set_messages(messages);
                }
                Err(e) => this.error(Some(e.to_string())),
            }
        });
    }

    fn set_messages(self: &Rc<Self>, messages: Vec<MailSummary>) {
        if *self.messages.borrow() == messages {
            return;
        }
        while let Some(row) = self.list.row_at_index(0) {
            self.list.remove(&row);
        }
        for message in &messages {
            self.list.append(&message_row(message));
        }
        self.clear.set_sensitive(!messages.is_empty());
        // Cleared elsewhere (the CLI): drop the selection and its detail.
        let selected = self.selected.borrow().clone();
        if let Some(id) = &selected {
            match messages.iter().position(|m| &m.id == id) {
                Some(index) => self.list.select_row(self.list.row_at_index(index as i32).as_ref()),
                None => {
                    self.selected.replace(None);
                }
            }
        }
        self.messages.replace(messages);
        let newest = self.messages.borrow().first().map(|m| m.id.clone());
        if let Some(id) = newest.filter(|_| self.select_newest.replace(false)) {
            self.list.select_row(self.list.row_at_index(0).as_ref());
            self.show(&id);
        } else if self.selected.borrow().is_none() {
            self.show_empty();
        }
    }

    /// Shows the newest message as soon as the messages are read.
    pub fn select_newest(&self) {
        self.select_newest.set(true);
    }

    fn show_empty(&self) {
        let env = self.env.borrow().clone();
        if env.is_empty() || !self.messages.borrow().is_empty() {
            let (title, text) = if self.messages.borrow().is_empty() {
                (tr!("mail.no_mail_title_case"), "")
            } else {
                (tr!("mail.no_message_selected_title_case"), "")
            };
            self.detail.set_child(Some(&ui::empty("mail-unread-symbolic", title, text)));
            return;
        }
        let page = ui::empty("mail-unread-symbolic", tr!("mail.catch_your_apps_mail"), tr!("mail.add_these_lines_to_your"));
        let code = gtk::Label::builder().label(&env).selectable(true).xalign(0.0).css_classes(["monospace", "card"]).build();
        code.set_margin_top(6);
        page.set_child(Some(&code));
        self.detail.set_child(Some(&page));
    }

    fn show(self: &Rc<Self>, id: &str) {
        self.selected.replace(Some(id.to_owned()));
        self.split.set_show_content(true);
        self.detail.set_child(Some(&gtk::Spinner::builder().spinning(true).halign(gtk::Align::Center).valign(gtk::Align::Center).build()));
        let (this, id) = (self.clone(), id.to_owned());
        glib::spawn_future_local(async move {
            let i = id.clone();
            match gio::spawn_blocking(move || Client.mail_detail(&i)).await.expect("mail request") {
                Ok(detail) if this.selected.borrow().as_deref() == Some(id.as_str()) => this.show_detail(&detail),
                Ok(_) => {}
                Err(e) => {
                    this.detail.set_child(Some(&ui::empty("dialog-warning-symbolic", tr!("mail.could_not_load_message"), &e.to_string())))
                }
            }
        });
    }

    fn show_detail(self: &Rc<Self>, detail: &MailDetail) {
        let header = gtk::Box::builder()
            .orientation(gtk::Orientation::Vertical)
            .spacing(6)
            .margin_top(12)
            .margin_bottom(12)
            .margin_start(16)
            .margin_end(16)
            .build();
        let subject = if detail.subject.is_empty() { tr!("mail.no_subject") } else { &detail.subject };
        header.append(&gtk::Label::builder().label(subject).xalign(0.0).wrap(true).selectable(true).css_classes(["title-3"]).build());
        let fields = gtk::Grid::builder().column_spacing(10).row_spacing(2).build();
        let mut rows =
            vec![(tr!("mail.detail.from"), detail.from.clone()), (tr!("mail.detail.to"), detail.to.clone().unwrap_or_default().join(", "))];
        let cc = detail.cc.clone().unwrap_or_default();
        if !cc.is_empty() {
            rows.push((tr!("mail.detail.cc"), cc.join(", ")));
        }
        rows.push((tr!("mail.detail.date"), ui::date_time(&detail.date)));
        rows.push((tr!("mail.detail.inbox"), detail.inbox.clone()));
        for (i, (name, value)) in rows.iter().enumerate() {
            fields.attach(&gtk::Label::builder().label(*name).xalign(0.0).css_classes(["dim-label"]).build(), 0, i as i32, 1, 1);
            fields.attach(&gtk::Label::builder().label(value).xalign(0.0).selectable(true).wrap(true).build(), 1, i as i32, 1, 1);
        }
        header.append(&fields);
        let attachments = detail.attachments.clone().unwrap_or_default();
        if !attachments.is_empty() {
            let row = gtk::Box::builder().spacing(6).build();
            for attachment in attachments {
                let label = format!("{} ({})", attachment.filename, glib::format_size(attachment.size));
                let button = gtk::Button::builder()
                    .child(&adw::ButtonContent::builder().icon_name("mail-attachment-symbolic").label(&label).build())
                    .tooltip_text(tr!("mail.save_attachment"))
                    .build();
                let id = detail.id.clone();
                button.connect_clicked(move |b| save_attachment(b, &id, attachment.index, &attachment.filename));
                row.append(&button);
            }
            header.append(&gtk::ScrolledWindow::builder().child(&row).vscrollbar_policy(gtk::PolicyType::Never).build());
        }

        let stack = gtk::Stack::new();
        let switcher = gtk::StackSwitcher::builder().stack(&stack).halign(gtk::Align::Start).build();
        header.append(&switcher);
        let html: gtk::Widget = if detail.html.is_empty() {
            ui::empty("text-html-symbolic", tr!("mail.no_html_part_title_case"), "").upcast()
        } else {
            ui::html_view(&detail.html, false).upcast()
        };
        stack.add_titled(&html, Some("html"), "HTML");
        stack.add_titled(
            &ui::monospace(if detail.text.is_empty() { tr!("mail.no_text_part") } else { &detail.text }),
            Some("text"),
            tr!("mail.detail.text"),
        );
        let raw = adw::Bin::new();
        stack.add_titled(&raw, Some("raw"), tr!("mail.detail.raw"));
        let headers = gtk::ListBox::builder()
            .css_classes(["boxed-list"])
            .selection_mode(gtk::SelectionMode::None)
            .margin_top(12)
            .margin_bottom(12)
            .margin_start(12)
            .margin_end(12)
            .build();
        for header in detail.headers.iter().flatten() {
            headers.append(
                &adw::ActionRow::builder()
                    .title(glib::markup_escape_text(&header.name))
                    .subtitle(glib::markup_escape_text(&header.value))
                    .subtitle_selectable(true)
                    .build(),
            );
        }
        stack.add_titled(
            &gtk::ScrolledWindow::builder().child(&headers).vexpand(true).build(),
            Some("headers"),
            tr!("mail.detail.headers"),
        );
        if detail.html.is_empty() {
            stack.set_visible_child_name("text");
        }
        // The raw message is read once it is asked for.
        let id = detail.id.clone();
        stack.connect_visible_child_name_notify(move |stack| {
            if stack.visible_child_name().as_deref() == Some("raw") && raw.child().is_none() {
                let (raw, id) = (raw.clone(), id.clone());
                glib::spawn_future_local(async move {
                    match gio::spawn_blocking(move || Client.mail_raw(&id)).await.expect("mail request") {
                        Ok(text) => raw.set_child(Some(&ui::monospace(&text))),
                        Err(e) => {
                            raw.set_child(Some(&ui::empty("dialog-warning-symbolic", tr!("mail.could_not_load_the_raw"), &e.to_string())))
                        }
                    }
                });
            }
        });

        let column = gtk::Box::new(gtk::Orientation::Vertical, 0);
        column.append(&header);
        column.append(&gtk::Separator::new(gtk::Orientation::Horizontal));
        column.append(&stack);
        self.detail.set_child(Some(&column));
    }

    fn confirm_clear(self: &Rc<Self>) {
        let dialog = adw::AlertDialog::new(Some(tr!("mail.delete_all_question")), None);
        dialog.add_responses(&[("cancel", tr!("common.cancel")), ("delete", tr!("mail.delete_all"))]);
        dialog.set_response_appearance("delete", adw::ResponseAppearance::Destructive);
        let this = self.clone();
        dialog.choose(Some(&self.window), gio::Cancellable::NONE, move |response| {
            if response == "delete" {
                this.selected.replace(None);
                let this = this.clone();
                glib::spawn_future_local(async move {
                    let result = gio::spawn_blocking(|| Client.clear_mail()).await.expect("mail request");
                    this.error(result.err().map(|e| e.to_string()));
                    this.load();
                });
            }
        });
    }
}

fn message_row(message: &MailSummary) -> gtk::ListBoxRow {
    let column = gtk::Box::builder().orientation(gtk::Orientation::Vertical).spacing(2).margin_top(6).margin_bottom(6).build();
    let top = gtk::Box::builder().spacing(6).build();
    top.append(
        &gtk::Label::builder()
            .label(&message.from)
            .xalign(0.0)
            .hexpand(true)
            .ellipsize(gtk::pango::EllipsizeMode::End)
            .css_classes(["heading"])
            .build(),
    );
    top.append(&gtk::Label::builder().label(ui::relative_time(&message.date)).css_classes(["caption", "dim-label"]).build());
    column.append(&top);
    let subject = if message.subject.is_empty() { tr!("mail.no_subject") } else { &message.subject };
    column.append(&gtk::Label::builder().label(subject).xalign(0.0).ellipsize(gtk::pango::EllipsizeMode::End).build());
    let bottom = gtk::Box::builder().spacing(6).build();
    bottom.append(&gtk::Label::builder().label(&message.inbox).css_classes(["caption", "badge", "accent"]).build());
    bottom.append(
        &gtk::Label::builder()
            .label(message.to.clone().unwrap_or_default().join(", "))
            .xalign(0.0)
            .ellipsize(gtk::pango::EllipsizeMode::End)
            .css_classes(["caption", "dim-label"])
            .build(),
    );
    column.append(&bottom);
    gtk::ListBoxRow::builder().child(&column).build()
}

/// Asks where to save an attachment, then downloads it there.
fn save_attachment(button: &gtk::Button, id: &str, index: u32, filename: &str) {
    let window = button.root().and_downcast::<gtk::Window>();
    let id = id.to_owned();
    gtk::FileDialog::builder().initial_name(filename).build().save(window.as_ref(), gio::Cancellable::NONE, move |result| {
        let Some(path) = result.ok().and_then(|f| f.path()) else { return };
        glib::spawn_future_local(async move {
            if let Ok(Ok(data)) = gio::spawn_blocking(move || Client.mail_attachment(&id, index)).await {
                let _ = std::fs::write(path, data);
            }
        });
    });
}
