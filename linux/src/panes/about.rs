//! About: versions, links and uninstalling.

use std::rc::Rc;

use adw::prelude::*;

use super::{Data, Pane};
use crate::i18n::tr;
use crate::model::AppModel;
use crate::{paths, ui};

pub struct AboutPane {
    page: adw::StatusPage,
    daemon: gtk::Label,
}

impl AboutPane {
    pub fn new(model: Rc<AppModel>) -> Self {
        let daemon = gtk::Label::new(None);
        let versions = gtk::Box::builder().orientation(gtk::Orientation::Vertical).spacing(4).build();
        versions.append(&gtk::Label::new(Some(&tr!("about.app_version", version = crate::VERSION))));
        versions.append(&daemon);

        let links = gtk::Box::builder().spacing(8).halign(gtk::Align::Center).build();
        let data = gtk::Button::with_label(tr!("about.data_folder_title_case"));
        data.connect_clicked(|button| ui::open_file(Some(button.upcast_ref()), paths::root()));
        links.append(&data);
        for (label, url) in [
            (tr!("about.flock_docs"), "https://flockphp.com/docs"),
            ("Discord", "https://discord.flockphp.com"),
            (tr!("about.license"), "https://github.com/flock-php/flock/blob/main/LICENSE"),
        ] {
            links.append(&link_button(label, url));
        }

        let support = gtk::Box::builder().orientation(gtk::Orientation::Vertical).spacing(8).build();
        support.append(&gtk::Label::builder().label(tr!("about.support_description")).wrap(true).css_classes(["dim-label"]).build());
        let support_links = gtk::Box::builder().spacing(8).halign(gtk::Align::Center).build();
        for (label, url) in [
            (tr!("about.star_on_github"), "https://github.com/flock-php/flock"),
            (tr!("about.sponsor"), "https://github.com/sponsors/flock-php"),
        ] {
            support_links.append(&link_button(label, url));
        }
        support.append(&support_links);

        let column = gtk::Box::builder().orientation(gtk::Orientation::Vertical).spacing(18).build();
        column.append(&versions);
        column.append(&links);
        column.append(&support);
        let issue = link_button(tr!("about.found_a_bug_report_an"), "");
        issue.add_css_class("flat");
        column.append(&issue);
        let uninstall = gtk::Button::builder()
            .label(tr!("about.uninstall_flock_ellipsis"))
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
                .label(tr!("about.stops_flock_and_removes_it_from"))
                .wrap(true)
                .css_classes(["caption", "dim-label"])
                .build(),
        );
        column.append(&gtk::Label::builder().label(tr!("about.license_description")).css_classes(["caption", "dim-label"]).build());

        let page = adw::StatusPage::builder()
            .icon_name("network-server-symbolic")
            .title("Flock")
            .description(tr!("about.tagline"))
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
        let daemon = data.status.as_ref().map_or(tr!("about.daemon_not_running").to_owned(), |s| s.version.clone());
        self.daemon.set_label(&tr!("about.daemon_version", version = daemon));
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
    ui::open_uri(Some(widget.upcast_ref()), uri);
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
