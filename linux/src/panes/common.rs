//! Building blocks of the panes.

use adw::prelude::*;
use gtk::{gio, glib};

use crate::i18n::tr;
use crate::links::Resolution;

/// A pane's scrolling column of groups. Replacing the groups keeps the scroll position.
pub struct PaneView {
    pub scrolled: gtk::ScrolledWindow,
    column: gtk::Box,
    /// The description above the groups, which stays.
    description: Option<gtk::Label>,
}

impl PaneView {
    /// A pane with a description of what it is about above its groups.
    pub fn new(description: &str) -> Self {
        let column = gtk::Box::builder()
            .orientation(gtk::Orientation::Vertical)
            .spacing(24)
            .margin_top(18)
            .margin_bottom(24)
            .margin_start(12)
            .margin_end(12)
            .build();
        let clamp = adw::Clamp::builder().maximum_size(680).child(&column).build();
        let scrolled = gtk::ScrolledWindow::builder().hscrollbar_policy(gtk::PolicyType::Never).vexpand(true).child(&clamp).build();
        let description = (!description.is_empty())
            .then(|| gtk::Label::builder().label(description).wrap(true).xalign(0.0).css_classes(["dim-label"]).build());
        if let Some(label) = &description {
            column.append(label);
        }
        Self { scrolled, column, description }
    }

    /// Replaces the groups below the description.
    pub fn set_groups(&self, groups: &[gtk::Widget]) {
        let adjustment = self.scrolled.vadjustment();
        let position = adjustment.value();
        let mut child = match &self.description {
            Some(label) => label.next_sibling(),
            None => self.column.first_child(),
        };
        while let Some(widget) = child {
            child = widget.next_sibling();
            self.column.remove(&widget);
        }
        for group in groups {
            self.column.append(group);
        }
        // After the new rows got their size.
        glib::idle_add_local_once(move || adjustment.set_value(position));
    }
}

/// A row with a value on its right, which can be selected and copied.
pub fn info_row(title: &str, value: &str) -> adw::ActionRow {
    let row = adw::ActionRow::builder().title(glib::markup_escape_text(title)).build();
    // Short values like "*.test" keep their width next to a long subtitle; longer ones (paths) may
    // shrink to 24 characters before they are ellipsized.
    let label = gtk::Label::builder()
        .label(value)
        .selectable(true)
        .css_classes(["dim-label"])
        .ellipsize(gtk::pango::EllipsizeMode::Middle)
        .width_chars(value.chars().count().min(24) as i32)
        .build();
    row.add_suffix(&label);
    row
}

/// An info row with a button that opens a window through an app action, e.g. "Open Mail" (app.mail).
pub fn open_row(title: &str, value: &str, button: &str, action: &str) -> adw::ActionRow {
    let row = info_row(title, value);
    row.add_suffix(&gtk::Button::builder().label(button).action_name(action).valign(gtk::Align::Center).build());
    row
}

/// A switch row that calls changed with its new state when the user flips it.
pub fn switch_row(title: &str, subtitle: &str, active: bool, changed: impl Fn(bool) + 'static) -> adw::SwitchRow {
    let row = adw::SwitchRow::builder().title(glib::markup_escape_text(title)).active(active).build();
    if !subtitle.is_empty() {
        row.set_subtitle(&glib::markup_escape_text(subtitle));
    }
    row.connect_active_notify(move |row| changed(row.is_active()));
    row
}

/// A group with a title and, below it, a footnote.
pub fn group(title: &str, footer: &str) -> adw::PreferencesGroup {
    let group = adw::PreferencesGroup::new();
    if !title.is_empty() {
        group.set_title(&glib::markup_escape_text(title));
    }
    if !footer.is_empty() {
        group.set_description(Some(&glib::markup_escape_text(footer)));
    }
    group
}

/// A ● in green, or grey when off, for a row's prefix.
pub fn status_dot(on: bool) -> gtk::Label {
    gtk::Label::builder().label("●").css_classes([if on { "success" } else { "dim-label" }]).build()
}

/// A ● for a server's state, for a row's prefix: green while it runs, amber while it starts or
/// stops, red when it failed, grey otherwise.
pub fn state_dot(state: &str) -> gtk::Label {
    let class = match state {
        "running" => "success",
        "starting" | "stopping" => "warning",
        "failed" => "error",
        _ => "dim-label",
    };
    gtk::Label::builder().label("●").css_classes([class]).build()
}

/// Shows a failed action in the Settings window's banner, or hides it.
pub fn report(banner: &adw::Banner, error: Option<String>) {
    match error {
        Some(message) => {
            banner.set_title(&message);
            banner.set_revealed(true);
        }
        None => banner.set_revealed(false),
    }
}

/// Monospaced, selectable text such as an .env snippet.
pub fn code_block(text: &str) -> gtk::Widget {
    let label = gtk::Label::builder()
        .label(text)
        .selectable(true)
        .xalign(0.0)
        .wrap(true)
        .css_classes(["monospace"])
        .margin_top(12)
        .margin_bottom(12)
        .margin_start(12)
        .margin_end(12)
        .build();
    let frame = gtk::Frame::builder().child(&label).css_classes(["view"]).build();
    frame.upcast()
}

/// Asks about a flock:// link: confirm calls act with what the link asks for, or says why there is
/// nothing to do. Not while the pane is built: the dialog needs the pane in its window.
pub fn ask_link<T: 'static>(parent: &impl IsA<gtk::Widget>, resolution: Resolution<T>, confirm: &str, act: impl FnOnce(T) + 'static) {
    let parent = parent.clone().upcast::<gtk::Widget>();
    let confirm = confirm.to_owned();
    glib::idle_add_local_once(move || {
        let dialog = adw::AlertDialog::new(Some(&resolution.title), Some(&resolution.message));
        let Some(action) = resolution.action else {
            dialog.add_response("ok", tr!("common.ok"));
            dialog.present(Some(&parent));
            return;
        };
        dialog.add_responses(&[("cancel", tr!("common.cancel")), ("confirm", &confirm)]);
        dialog.set_response_appearance("confirm", adw::ResponseAppearance::Suggested);
        dialog.set_default_response(Some("confirm"));
        dialog.choose(Some(&parent), gio::Cancellable::NONE, move |response| {
            if response == "confirm" {
                act(action);
            }
        });
    });
}
