//! General: the daemon, the domain, the data folder, opening at login, the language, updates and
//! the project folders.

use std::cell::RefCell;
use std::rc::Rc;

use adw::prelude::*;
use gtk::{gio, glib};

use super::common::{PaneView, group, info_row, report, status_dot, switch_row};
use super::versions::busy_button;
use super::{Data, Pane};
use crate::api::{Client, Config, Status};
use crate::autostart;
use crate::i18n::{self, tr, tr_noop};
use crate::installation;
use crate::model::AppModel;
use crate::prefs;

pub struct GeneralPane {
    view: PaneView,
    model: Rc<AppModel>,
    banner: adw::Banner,
    shown: RefCell<Option<(Status, Option<Config>, bool)>>,
}

impl GeneralPane {
    pub fn new(model: Rc<AppModel>, banner: adw::Banner) -> Self {
        Self { view: PaneView::new(tr!("general.subtitle")), model, banner, shown: RefCell::default() }
    }

    fn daemon(&self, status: &Status) -> adw::PreferencesGroup {
        let group = group(tr!("general.daemon"), "");
        let state = adw::ActionRow::builder().title(tr!("general.status")).build();
        let degraded = status.is_degraded();
        let value = gtk::Box::builder().spacing(6).build();
        value.append(&status_dot(!degraded));
        value.append(&gtk::Label::new(Some(if degraded { tr!("menu.status.needs_attention") } else { tr!("sites.proxy.running") })));
        state.add_suffix(&value);
        group.add(&state);
        // What "Needs attention" is about.
        let down = status
            .processes
            .iter()
            .filter(|p| !p.up)
            .map(|p| format!("{}: {}", p.name, p.last_error.as_deref().unwrap_or(tr!("menu.down"))));
        for warning in status.warnings.iter().cloned().chain(down) {
            let row = adw::ActionRow::builder().title(glib::markup_escape_text(&warning)).css_classes(["warning"]).build();
            row.add_prefix(&gtk::Image::from_icon_name("dialog-warning-symbolic"));
            group.add(&row);
        }
        group.add(&info_row(tr!("general.version"), &status.version));
        group.add(&info_row(tr!("general.process"), &tr!("general.pid", pid = status.pid)));

        let model = self.model.clone();
        let restart =
            busy_button(tr!("general.restart_daemon"), tr!("general.restarting"), self.model.is_restarting(), move || model.restart());
        let row = adw::ActionRow::new();
        row.add_suffix(&restart);
        group.add(&row);
        group
    }

    fn network(&self, config: &Config, status: &Status) -> adw::PreferencesGroup {
        let footer = if config.ports.http != 80 || config.ports.https != 443 {
            tr!("general.ports", http = config.ports.http, https = config.ports.https)
        } else {
            String::new()
        };
        let group = group(tr!("general.network"), &footer);
        let row = info_row(tr!("general.domain"), &format!("*.{}", config.tld));
        row.set_subtitle(&glib::markup_escape_text(&domain_explanation(&config.tld, status)));
        group.add(&row);
        group
    }

    fn data(&self, config: &Config) -> adw::PreferencesGroup {
        let group = group(tr!("general.data"), "");
        let row = info_row(tr!("general.folder"), &config.root);
        let open = gtk::Button::builder().label(tr!("common.open")).valign(gtk::Align::Center).build();
        let root = config.root.clone();
        open.connect_clicked(move |button| crate::ui::open_file(Some(button.upcast_ref()), &root));
        row.add_suffix(&open);
        group.add(&row);
        group
    }

    fn startup(&self) -> adw::PreferencesGroup {
        let group = group(tr!("general.startup"), "");
        let banner = self.banner.clone();
        group.add(&switch_row(tr!("setup.open_flock_at_login"), tr!("general.shows_the_flock_icon_in"), autostart::enabled(), move |on| {
            if let Err(e) = autostart::set(on) {
                report(&banner, Some(tr!("general.could_not_change_startup", error = e)));
            }
        }));
        group
    }

    /// Linux apps don't update themselves: the flockphp package comes with the system's updates.
    fn updates(&self, config: &Config, status: &Status) -> adw::PreferencesGroup {
        let group = group(tr!("general.updates"), "");
        // The version in the title: as a value next to the subtitle and the button, it got squeezed.
        let version = adw::ActionRow::builder().title(glib::markup_escape_text(&format!("Flock {}", status.version))).build();
        let aur = installation::aur_package();
        match aur {
            // The desktop's updater doesn't update AUR packages.
            Some(package) => version.set_subtitle(&glib::markup_escape_text(&tr!("general.new_versions_come_from_the", package = package))),
            None => version.set_subtitle(tr!("general.new_versions_come_with_your")),
        }
        if let Some((name, updater)) = system_updater().filter(|_| aur.is_none()) {
            let open = gtk::Button::builder()
                .label(tr!("general.check_for_updates_linux"))
                .tooltip_text(tr!("general.opens_name", name = name))
                .valign(gtk::Align::Center)
                .build();
            let banner = self.banner.clone();
            open.connect_clicked(move |_| {
                if let Err(e) = i18n::system_language(std::process::Command::new(updater[0]).args(&updater[1..])).spawn() {
                    report(&banner, Some(tr!("general.could_not_open_program_error", program = updater[0], error = e)));
                }
            });
            version.add_suffix(&open);
        }
        group.add(&version);
        // `flock channel`: on Linux, Flock's own betas come from its package repository's beta
        // suite, which the system's file names, so the switch asks for a password. On Arch, they are
        // an AUR package of their own, which `flock channel` names.
        let beta = adw::SwitchRow::builder()
            .title(tr!("general.beta"))
            .subtitle(tr!("general.beta_description"))
            .active(config.channel == "beta")
            .build();
        let (model, banner, current) = (self.model.clone(), self.banner.clone(), config.channel == "beta");
        beta.connect_active_notify(move |row| {
            let on = row.is_active();
            // Also the switch going back after a declined prompt.
            if on == current {
                return;
            }
            let (model, banner, row) = (model.clone(), banner.clone(), row.clone());
            row.set_sensitive(false);
            glib::spawn_future_local(async move {
                let channel = if on { "beta" } else { "stable" };
                let out = gio::spawn_blocking(move || installation::run(&["channel", channel])).await.expect("flock channel");
                row.set_sensitive(true);
                if out.ok {
                    if let Some(hint) = out.text.lines().find(|line| line.contains("AUR package")) {
                        report(&banner, Some(hint.to_owned()));
                    }
                } else {
                    if !installation::declined(&out) {
                        report(&banner, Some(out.text));
                    }
                    row.set_active(!on);
                }
                model.refresh();
            });
        });
        beta.set_sensitive(config.channel != "experimental");
        group.add(&beta);
        if config.channel == "experimental" {
            let row =
                adw::ActionRow::builder().title(tr!("general.experimental_channel_packages_that_arent")).css_classes(["warning"]).build();
            group.add(&row);
        }
        group
    }

    fn project_folders(&self, config: &Config) -> adw::PreferencesGroup {
        let group = group(tr!("general.project_folders"), &tr!("general.project_folders_description", tld = config.tld));
        let add = gtk::Button::builder()
            .icon_name("list-add-symbolic")
            .tooltip_text(tr!("general.add_project_folders_title"))
            .css_classes(["flat"])
            .build();
        let (model, banner) = (self.model.clone(), self.banner.clone());
        add.connect_clicked(move |button| {
            let window = button.root().and_downcast::<gtk::Window>();
            let (model, banner) = (model.clone(), banner.clone());
            gtk::FileDialog::builder().title(tr!("general.add_project_folders_title")).build().select_multiple_folders(
                window.as_ref(),
                gio::Cancellable::NONE,
                move |result| {
                    let Ok(folders) = result else { return };
                    for folder in folders.iter::<gio::File>().flatten() {
                        if let Some(path) = folder.path() {
                            let path = path.to_string_lossy().into_owned();
                            let banner = banner.clone();
                            model.perform(move |client: Client| client.park(&path), move |e| report(&banner, e));
                        }
                    }
                },
            );
        });
        group.set_header_suffix(Some(&add));

        let parked = config.parked.clone().unwrap_or_default();
        if parked.is_empty() {
            group.add(&adw::ActionRow::builder().title(tr!("general.no_project_folders")).css_classes(["dim-label"]).build());
        }
        for path in parked {
            let row = adw::ActionRow::builder().title(glib::markup_escape_text(&path)).build();
            row.add_prefix(&gtk::Image::from_icon_name("folder-symbolic"));
            let remove = gtk::Button::builder()
                .icon_name("user-trash-symbolic")
                .tooltip_text(tr!("common.remove"))
                .valign(gtk::Align::Center)
                .css_classes(["flat"])
                .build();
            let (model, banner) = (self.model.clone(), self.banner.clone());
            remove.connect_clicked(move |button| confirm_unpark(button, &path, model.clone(), banner.clone()));
            row.add_suffix(&remove);
            group.add(&row);
        }
        group
    }
}

impl Pane for GeneralPane {
    fn widget(&self) -> gtk::Widget {
        self.view.scrolled.clone().upcast()
    }

    fn update(&self, data: &Data) {
        let Some(status) = data.status.clone() else { return };
        let key = (status.clone(), data.config.clone(), self.model.is_restarting());
        if self.shown.borrow().as_ref() == Some(&key) {
            return;
        }
        self.shown.replace(Some(key));
        let mut groups = vec![self.daemon(&status).upcast()];
        if let Some(config) = &data.config {
            groups.push(self.network(config, &status).upcast());
            groups.push(self.data(config).upcast());
            groups.push(self.startup().upcast());
        }
        groups.push(language().upcast());
        if let Some(config) = &data.config {
            groups.push(self.updates(config, &status).upcast());
            groups.push(self.project_folders(config).upcast());
        }
        self.view.set_groups(&groups);
    }
}

/// The desktop's program for system updates, its name and the command that opens its updates: Ubuntu's
/// Software Updater, GNOME Software or KDE Discover.
fn system_updater() -> Option<(&'static str, &'static [&'static str])> {
    const UPDATERS: [(&str, &[&str]); 3] = [
        (tr_noop!("general.software_updater"), &["update-manager"]),
        ("Software", &["gnome-software", "--mode=updates"]),
        ("Discover", &["plasma-discover", "--mode", "Update"]),
    ];
    let path = std::env::var_os("PATH")?;
    UPDATERS
        .into_iter()
        .find(|(_, u)| std::env::split_paths(&path).any(|dir| dir.join(u[0]).is_file()))
        .map(|(name, u)| (i18n::translate(name), u))
}

/// The app's language: the system's or one of i18n::LANGUAGES, from the next start. Until then, a
/// note offers the restart. Also on the setup's first page and while the daemon isn't running.
pub fn language() -> adw::PreferencesGroup {
    let group = group(tr!("general.language"), "");
    let codes: Vec<&str> = ["system"].into_iter().chain(i18n::LANGUAGES.iter().map(|(code, _)| *code)).collect();
    let names: Vec<&str> = [tr!("general.language_system")].into_iter().chain(i18n::LANGUAGES.iter().map(|(_, name)| *name)).collect();
    let saved = prefs::load().language;
    let row = adw::ComboRow::builder()
        .title(tr!("general.language"))
        .model(&gtk::StringList::new(&names))
        .selected(codes.iter().position(|code| *code == saved).unwrap_or(0) as u32)
        .build();
    let note = adw::ActionRow::builder().title(tr!("general.language_restart_note")).build();
    let restart =
        gtk::Button::builder().label(tr!("general.relaunch_flock")).action_name("app.relaunch").valign(gtk::Align::Center).build();
    note.add_suffix(&restart);
    // Only when the choice speaks another language than this run, e.g. not "System default" → its own.
    let pending = |preference: &str| i18n::resolve_next(preference) != i18n::language();
    note.set_visible(pending(&saved));
    let shown = note.clone();
    row.connect_selected_notify(move |row| {
        let code = codes[row.selected() as usize];
        let mut p = prefs::load();
        let previous = std::mem::replace(&mut p.language, code.to_owned());
        if prefs::save(&p).is_err() {
            // Not saved: back to the saved language. That notifies once more, and then matches.
            let at = codes.iter().position(|code| *code == previous).unwrap_or(0) as u32;
            if at != row.selected() {
                row.set_selected(at);
            }
            return;
        }
        shown.set_visible(pending(code));
    });
    group.add(&row);
    group.add(&note);
    group
}

/// Asks before a project folder's sites stop being served.
fn confirm_unpark(button: &gtk::Button, path: &str, model: Rc<AppModel>, banner: adw::Banner) {
    let name = std::path::Path::new(path).file_name().map_or(path.to_owned(), |n| n.to_string_lossy().into_owned());
    let dialog = adw::AlertDialog::new(
        Some(&tr!("general.remove_project_folder_question", name = name)),
        Some(&tr!("general.sites_in_path_are_no", path = path)),
    );
    dialog.add_responses(&[("cancel", tr!("common.cancel")), ("remove", tr!("common.remove"))]);
    dialog.set_response_appearance("remove", adw::ResponseAppearance::Destructive);
    let path = path.to_owned();
    dialog.choose(Some(button), gio::Cancellable::NONE, move |response| {
        if response == "remove" {
            model.perform(move |client: Client| client.unpark(&path), move |e| report(&banner, e));
        }
    });
}

/// How names under the TLD resolve: Flock's DNS server through systemd-resolved, which has no
/// fallback (Windows also writes site domains to the hosts file).
fn domain_explanation(tld: &str, status: &Status) -> String {
    if status.dns.wildcard {
        return tr!("general.every_name_any_subdomain_included_is").to_owned();
    }
    let problem =
        if status.dns.wildcard_problem.is_empty() { tr!("general.flocks_dns_server_isnt_running") } else { &status.dns.wildcard_problem };
    tr!("general.tld_doesnt_resolve_problem_linux", tld = tld, problem = problem)
}
