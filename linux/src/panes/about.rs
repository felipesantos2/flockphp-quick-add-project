//! About: versions, links and uninstalling.

use std::rc::Rc;

use adw::prelude::*;
use gtk::gio;

use super::{Data, Pane};
use crate::model::AppModel;
use crate::paths;

pub struct AboutPane {
    page: adw::StatusPage,
    daemon: gtk::Label,
}

impl AboutPane {
    pub fn new(model: Rc<AppModel>) -> Self {
        let daemon = gtk::Label::new(None);
        let versions = gtk::Box::builder().orientation(gtk::Orientation::Vertical).spacing(4).build();
        versions.append(&gtk::Label::new(Some(&format!("App {}", crate::VERSION))));
        versions.append(&daemon);

        let links = gtk::Box::builder().spacing(8).halign(gtk::Align::Center).build();
        let data = gtk::Button::with_label("Data Folder");
        data.connect_clicked(|button| {
            let window = button.root().and_downcast::<gtk::Window>();
            gtk::FileLauncher::new(Some(&gio::File::for_path(paths::root()))).launch(window.as_ref(), gio::Cancellable::NONE, |_| {});
        });
        links.append(&data);
        for (label, url) in [
            ("Flock Docs", "https://flockphp.com/docs"),
            ("Discord", "https://discord.flockphp.com"),
            ("License", "https://github.com/flock-php/flock/blob/main/LICENSE"),
        ] {
            links.append(&link_button(label, url));
        }

        let support = gtk::Box::builder().orientation(gtk::Orientation::Vertical).spacing(8).build();
        support.append(
            &gtk::Label::builder()
                .label("Flock is free and open source, forever. Sponsoring helps keep it maintained.")
                .wrap(true)
                .css_classes(["dim-label"])
                .build(),
        );
        let support_links = gtk::Box::builder().spacing(8).halign(gtk::Align::Center).build();
        for (label, url) in [("Star on GitHub", "https://github.com/flock-php/flock"), ("Sponsor", "https://github.com/sponsors/flock-php")]
        {
            support_links.append(&link_button(label, url));
        }
        support.append(&support_links);

        let column = gtk::Box::builder().orientation(gtk::Orientation::Vertical).spacing(18).build();
        column.append(&versions);
        column.append(&links);
        column.append(&support);
        let issue = link_button("Found a bug? Report an issue", "");
        issue.add_css_class("flat");
        column.append(&issue);
        let uninstall = gtk::Button::builder()
            .label("Uninstall Flock…")
            .halign(gtk::Align::Center)
            .css_classes(["destructive-action"])
            .margin_top(12)
            .build();
        uninstall.set_sensitive(crate::installation::setup_flock().is_some());
        let uninstall_model = model.clone();
        uninstall.connect_clicked(move |button| crate::setup::uninstall(button, uninstall_model.clone()));
        column.append(&uninstall);
        column.append(
            &gtk::Label::builder()
                .label("Stops Flock and removes it from this computer. Your sites, databases and settings are kept.")
                .wrap(true)
                .css_classes(["caption", "dim-label"])
                .build(),
        );
        column.append(
            &gtk::Label::builder()
                .label("Copyright © Guava s.r.o. Licensed under the AGPL-3.0.")
                .css_classes(["caption", "dim-label"])
                .build(),
        );

        let page = adw::StatusPage::builder()
            .icon_name("network-server-symbolic")
            .title("Flock")
            .description("A native local development environment for Laravel.")
            .child(&column)
            .build();
        issue.connect_clicked(move |button| open_uri(button, &issue_url(model.status().map(|s| s.version))));
        Self { page, daemon }
    }
}

impl Pane for AboutPane {
    fn widget(&self) -> gtk::Widget {
        self.page.clone().upcast()
    }

    fn update(&self, data: &Data) {
        let daemon = data.status.as_ref().map_or("not running".to_owned(), |s| s.version.clone());
        self.daemon.set_label(&format!("Daemon {daemon}"));
    }
}

fn link_button(label: &str, url: &str) -> gtk::Button {
    let button = gtk::Button::with_label(label);
    if !url.is_empty() {
        let url = url.to_owned();
        button.connect_clicked(move |button| open_uri(button, &url));
    }
    button
}

fn open_uri(widget: &impl IsA<gtk::Widget>, uri: &str) {
    let window = widget.root().and_downcast::<gtk::Window>();
    gtk::UriLauncher::new(uri).launch(window.as_ref(), gio::Cancellable::NONE, |_| {});
}

/// A blank GitHub issue with the versions filled in, so reports say what they run on.
fn issue_url(daemon: Option<String>) -> String {
    let os = std::fs::read_to_string("/etc/os-release")
        .ok()
        .and_then(|s| s.lines().find_map(|l| l.strip_prefix("PRETTY_NAME=").map(|v| v.trim_matches('"').to_owned())))
        .unwrap_or_else(|| "Linux".to_owned());
    let body = format!("\n\n---\nApp: {}\nDaemon: {}\nOS: {os}\n", crate::VERSION, daemon.as_deref().unwrap_or("not running"));
    let encoded: String = body
        .bytes()
        .map(|b| match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'.' | b'_' | b'~' => (b as char).to_string(),
            _ => format!("%{b:02X}"),
        })
        .collect();
    format!("https://github.com/flock-php/flock/issues/new?body={encoded}")
}
