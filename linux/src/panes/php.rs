//! PHP: the versions the channel offers or that are installed (install, update, uninstall, the
//! default) and the FrankenPHP builds for them, with download progress and install logs.

use std::cell::{Cell, RefCell};
use std::collections::BTreeSet;
use std::rc::Rc;
use std::time::{Duration, Instant};

use adw::prelude::*;
use gtk::{gio, glib};

use super::common::{PaneView, ask_link, group, report};
use super::versions::{MenuItem, badge, busy_button, button, checked_text, confirm, detail, more_menu, show_log};
use super::{Data, Pane};
use crate::api::{Client, Error, FrankenPhpBuild, FrankenPhpState, PhpRelease, PhpVersions, SettingsChange};
use crate::i18n::tr;
use crate::links::{self, Link};
use crate::model::AppModel;

/// How often the versions are read again: while something downloads, and otherwise.
const ACTIVE_INTERVAL: Duration = Duration::from_secs(2);
const IDLE_INTERVAL: Duration = Duration::from_secs(30);

pub struct PhpPane(Rc<Inner>);

struct Inner {
    view: PaneView,
    model: Rc<AppModel>,
    banner: adw::Banner,
    versions: RefCell<Option<PhpVersions>>,
    frankenphp: RefCell<Option<FrankenPhpState>>,
    /// The installed versions' php.ini, from the config.
    inis: RefCell<Vec<(String, String)>>,
    /// Rows with a request waiting for the daemon: "php 8.4", "frankenphp 8.4".
    busy: RefCell<BTreeSet<String>>,
    checking: Cell<bool>,
    loading: Cell<bool>,
    loaded_at: Cell<Option<Instant>>,
    shown: RefCell<Option<String>>,
}

impl PhpPane {
    pub fn new(model: Rc<AppModel>, banner: adw::Banner) -> Self {
        Self(Rc::new(Inner {
            view: PaneView::new(tr!("php.every_installed_version_runs_its")),
            model,
            banner,
            versions: RefCell::default(),
            frankenphp: RefCell::default(),
            inis: RefCell::default(),
            busy: RefCell::default(),
            checking: Cell::new(false),
            loading: Cell::new(false),
            loaded_at: Cell::new(None),
            shown: RefCell::default(),
        }))
    }
}

impl Pane for PhpPane {
    fn widget(&self) -> gtk::Widget {
        self.0.view.scrolled.clone().upcast()
    }

    fn update(&self, data: &Data) {
        let inis = data.config.as_ref().and_then(|c| c.php.clone()).unwrap_or_default().into_iter().map(|p| (p.version, p.ini)).collect();
        self.0.inis.replace(inis);
        self.0.load(false);
        self.0.render();
    }
}

impl Inner {
    fn active(&self) -> bool {
        let php = self
            .versions
            .borrow()
            .as_ref()
            .is_some_and(|v| v.versions.iter().flatten().any(|r| r.job.as_ref().is_some_and(|j| j.running())));
        let franken = self
            .frankenphp
            .borrow()
            .as_ref()
            .is_some_and(|f| f.builds.iter().flatten().any(|b| b.job.as_ref().is_some_and(|j| j.running())));
        php || franken
    }

    /// Reads the versions again when they are due (or now, with force).
    fn load(self: &Rc<Self>, force: bool) {
        let interval = if self.active() { ACTIVE_INTERVAL } else { IDLE_INTERVAL };
        let due = self.loaded_at.get().is_none_or(|at| at.elapsed() >= interval);
        if !(force || due) || self.loading.replace(true) {
            return;
        }
        let this = self.clone();
        glib::spawn_future_local(async move {
            let (php, franken) = gio::spawn_blocking(|| (Client.php_versions(), Client.frankenphp())).await.expect("versions request");
            this.loading.set(false);
            this.loaded_at.set(Some(Instant::now()));
            match php {
                Ok(php) => {
                    this.versions.replace(Some(php));
                }
                Err(e) => report(&this.banner, Some(e.to_string())),
            }
            // 501 where FrankenPHP isn't published for this platform.
            this.frankenphp.replace(franken.ok());
            this.render();
        });
    }

    /// Asks about a pending flock://install/php link (e.g. from the page served for a site whose
    /// PHP is missing) once the versions are loaded, like Services does for services.
    fn ask_link(self: &Rc<Self>) {
        let Some(Link::Install(link)) = self.model.link().filter(|l| matches!(l, Link::Install(i) if i.service == links::PHP)) else {
            return;
        };
        let Some(resolution) = link.resolve_php(self.versions.borrow().as_ref()) else { return };
        self.model.set_link(None);
        let this = self.clone();
        ask_link(&self.view.scrolled, resolution, tr!("common.install"), move |target| {
            let minor = target.line.clone();
            this.run_php(&target.line, move |c| c.install_php(&minor));
        });
    }

    fn render(self: &Rc<Self>) {
        self.ask_link();
        let versions = self.versions.borrow().clone();
        let franken = self.frankenphp.borrow().clone();
        let key = format!("{versions:?}{franken:?}{:?}{:?}{}", self.inis.borrow(), self.busy.borrow(), self.checking.get());
        if self.shown.borrow().as_deref() == Some(key.as_str()) {
            return;
        }
        self.shown.replace(Some(key));

        let Some(versions) = versions else {
            let loading = group("", "");
            loading.add(&adw::ActionRow::builder().title(tr!("php.loading_php_versions")).build());
            self.view.set_groups(&[loading.upcast()]);
            return;
        };
        let fpm = group("PHP-FPM", &versions.check_error);
        fpm.set_header_suffix(Some(&self.check_button(versions.checked_at.as_deref())));
        for release in versions.versions.iter().flatten() {
            fpm.add(&self.release_row(release));
        }
        let mut groups = vec![fpm.upcast()];
        if let Some(franken) = franken.filter(|f| f.available) {
            let builds = group("FrankenPHP", &franken.check_error);
            for build in franken.builds.iter().flatten() {
                builds.add(&self.frankenphp_row(build));
            }
            groups.push(builds.upcast());
        }
        self.view.set_groups(&groups);
    }

    fn check_button(self: &Rc<Self>, checked_at: Option<&str>) -> gtk::Widget {
        let line = gtk::Box::builder().spacing(6).build();
        line.append(&gtk::Label::builder().label(checked_text(checked_at)).css_classes(["caption", "dim-label"]).build());
        let this = self.clone();
        let check = busy_button(tr!("versions.check_now"), tr!("versions.checking"), self.checking.get(), move || {
            this.checking.set(true);
            this.render();
            let this = this.clone();
            glib::spawn_future_local(async move {
                let result = gio::spawn_blocking(|| Client.check_php()).await.expect("check request");
                this.checking.set(false);
                this.apply_php(result);
            });
        });
        check.add_css_class("flat");
        check.add_css_class("caption");
        line.append(&check);
        line.upcast()
    }

    fn apply_php(self: &Rc<Self>, result: Result<PhpVersions, Error>) {
        match result {
            Ok(versions) => {
                self.versions.replace(Some(versions));
                report(&self.banner, None);
            }
            Err(e) => report(&self.banner, Some(e.to_string())),
        }
        self.render();
    }

    fn apply_frankenphp(self: &Rc<Self>, result: Result<FrankenPhpState, Error>) {
        match result {
            Ok(state) => {
                self.frankenphp.replace(Some(state));
                report(&self.banner, None);
            }
            Err(e) => report(&self.banner, Some(e.to_string())),
        }
        self.render();
    }

    /// Runs a PHP request for a row, its buttons disabled until the daemon answers.
    fn run_php(self: &Rc<Self>, minor: &str, f: impl FnOnce(Client) -> Result<PhpVersions, Error> + Send + 'static) {
        let key = format!("php {minor}");
        self.busy.borrow_mut().insert(key.clone());
        self.render();
        let this = self.clone();
        glib::spawn_future_local(async move {
            let result = gio::spawn_blocking(move || f(Client)).await.expect("php request");
            this.busy.borrow_mut().remove(&key);
            this.apply_php(result);
        });
    }

    fn run_frankenphp(self: &Rc<Self>, minor: &str, f: impl FnOnce(Client) -> Result<FrankenPhpState, Error> + Send + 'static) {
        let key = format!("frankenphp {minor}");
        self.busy.borrow_mut().insert(key.clone());
        self.render();
        let this = self.clone();
        glib::spawn_future_local(async move {
            let result = gio::spawn_blocking(move || f(Client)).await.expect("frankenphp request");
            this.busy.borrow_mut().remove(&key);
            this.apply_frankenphp(result);
        });
    }

    fn release_row(self: &Rc<Self>, release: &PhpRelease) -> adw::ActionRow {
        let installed = !release.installed.is_empty();
        // A running or failed job takes the place of the update button, which names its target.
        let job_shown = release.job.as_ref().is_some_and(|j| j.running() || j.failed());
        let version = if release.update && release.latest != release.installed && job_shown {
            format!("{} → {}", release.installed, release.latest)
        } else if installed {
            release.installed.clone()
        } else {
            release.latest.clone()
        };
        let row = adw::ActionRow::builder().title(format!("PHP {}", release.minor)).build();
        let icon = gtk::Image::from_icon_name("utilities-terminal-symbolic");
        if !installed {
            icon.add_css_class("dim-label");
        }
        row.add_prefix(&icon);
        row.add_suffix(&gtk::Label::builder().label(&version).css_classes(["dim-label", "numeric"]).build());
        if release.default {
            row.add_suffix(&badge(tr!("sites.default"), "accent"));
        }
        if release.prerelease {
            let pre = badge(tr!("versions.prerelease"), "warning");
            pre.set_tooltip_text(Some(&tr!("php.php_version_isnt_released_yet", version = release.latest)));
            row.add_suffix(&pre);
        }
        if installed && release.xdebug {
            row.add_suffix(&badge("Xdebug", "error"));
        }
        let idle = match release.status.as_str() {
            "installed" => tr!("versions.up_to_date"),
            "update" if release.latest == release.installed => tr!("versions.new_build"),
            "update" => tr!("versions.update_available"),
            "available" => tr!("common.not_installed"),
            _ => "",
        };
        detail(&row, release.job.as_ref(), &release.yanked, release.update, idle);

        let busy = self.busy.borrow().contains(&format!("php {}", release.minor));
        if busy {
            row.add_suffix(&gtk::Spinner::builder().spinning(true).build());
        }
        let actions = gtk::Box::builder().spacing(6).sensitive(!busy).build();
        let minor = release.minor.clone();
        let size = if release.size > 0 {
            tr!("php.downloads_php_version_size_mb", version = release.latest, size = release.size / 1_000_000)
        } else {
            String::new()
        };
        let this = self.clone();
        let log = {
            let (this, minor) = (self.clone(), minor.clone());
            move || this.show_php_log(&minor)
        };
        match release.job.as_ref() {
            Some(job) if job.running() => {
                actions.append(&button(tr!("sites.show_log"), false, log.clone()));
                let m = minor.clone();
                actions.append(&button(tr!("common.cancel"), false, move || {
                    this.run_php(&m, {
                        let m = m.clone();
                        move |c| c.cancel_php(&m)
                    })
                }));
            }
            Some(job) if job.failed() => {
                actions.append(&button(tr!("sites.show_log"), false, log.clone()));
                let (t, m) = (this.clone(), minor.clone());
                actions.append(&button(tr!("common.retry"), false, move || {
                    t.run_php(&m, {
                        let m = m.clone();
                        move |c| c.install_php(&m)
                    })
                }));
                let m = minor.clone();
                actions.append(&button(tr!("sites.share.dismiss"), false, move || {
                    this.run_php(&m, {
                        let m = m.clone();
                        move |c| c.dismiss_php(&m)
                    })
                }));
            }
            _ if installed => {
                if release.update {
                    let label = if release.latest == release.installed {
                        tr!("common.update").to_owned()
                    } else {
                        tr!("general.update_to", version = release.latest)
                    };
                    let (t, m) = (this.clone(), minor.clone());
                    let update = button(&label, true, move || {
                        t.run_php(&m, {
                            let m = m.clone();
                            move |c| c.install_php(&m)
                        })
                    });
                    update.set_tooltip_text(Some(&size));
                    actions.append(&update);
                }
                let mut items: Vec<MenuItem> = Vec::new();
                if !release.default {
                    // Next to the update button, it goes into the menu: the row's title keeps its room.
                    let (t, m) = (this.clone(), minor.clone());
                    let make_default = move || t.make_default(&m);
                    if release.update {
                        items.push((tr!("node.make_default").to_owned(), true, false, Box::new(make_default)));
                    } else {
                        actions.append(&button(tr!("node.make_default"), false, make_default));
                    }
                }
                let ini = self.inis.borrow().iter().find(|(v, _)| *v == minor).map(|(_, ini)| ini.clone()).unwrap_or_default();
                if !ini.is_empty() {
                    let r = row.clone();
                    items.push((tr!("php.open_ini").to_owned(), true, false, Box::new(move || open_file(&r, &ini))));
                }
                items.push((tr!("versions.show_last_install_log").to_owned(), true, false, Box::new(log.clone())));
                let (t, m, r) = (this.clone(), minor.clone(), row.clone());
                items.push((
                    tr!("common.uninstall_ellipsis").to_owned(),
                    !release.default,
                    true,
                    Box::new(move || {
                        let (t, m) = (t.clone(), m.clone());
                        confirm(
                            &r,
                            &tr!("php.uninstall_php_version", version = m),
                            &tr!("php.uninstall_details", version = m),
                            tr!("common.uninstall"),
                            move || {
                                let m2 = m.clone();
                                t.run_php(&m, move |c| c.uninstall_php(&m2));
                            },
                        );
                    }),
                ));
                actions.append(&more_menu(items));
            }
            _ => {
                let m = minor.clone();
                let install = button(tr!("common.install"), false, move || {
                    this.run_php(&m, {
                        let m = m.clone();
                        move |c| c.install_php(&m)
                    })
                });
                install.set_tooltip_text(Some(&size));
                actions.append(&install);
            }
        }
        row.add_suffix(&actions);
        row
    }

    fn make_default(self: &Rc<Self>, minor: &str) {
        let key = format!("php {minor}");
        self.busy.borrow_mut().insert(key.clone());
        self.render();
        let (this, banner) = (self.clone(), self.banner.clone());
        self.model.change_settings(SettingsChange { default_php: Some(minor.to_owned()), ..Default::default() }, move |e| {
            this.busy.borrow_mut().remove(&key);
            report(&banner, e);
            this.load(true);
        });
    }

    fn show_php_log(self: &Rc<Self>, minor: &str) {
        let (m, this, m2) = (minor.to_owned(), self.clone(), minor.to_owned());
        let running = move || {
            this.versions
                .borrow()
                .as_ref()
                .is_some_and(|v| v.versions.iter().flatten().any(|r| r.minor == m2 && r.job.as_ref().is_some_and(|j| j.running())))
        };
        show_log(&self.view.scrolled, &tr!("php.php_version_install_log", version = minor), move || Client.php_log(&m), running);
    }

    fn frankenphp_row(self: &Rc<Self>, build: &FrankenPhpBuild) -> adw::ActionRow {
        let installed = !build.installed.is_empty();
        let row = adw::ActionRow::builder().title(format!("PHP {}", build.php)).build();
        let icon = gtk::Image::from_icon_name("media-playback-start-symbolic");
        if !installed {
            icon.add_css_class("dim-label");
        }
        row.add_prefix(&icon);
        if build.prerelease {
            row.add_suffix(&badge(tr!("versions.prerelease"), "warning"));
        }
        let idle = match (installed, build.php_version.is_empty()) {
            (false, _) => tr!("versions.franken_not_installed").to_owned(),
            (true, true) => format!("FrankenPHP {}", build.installed),
            (true, false) => format!("FrankenPHP {} (PHP {})", build.installed, build.php_version),
        };
        detail(&row, build.job.as_ref(), &build.yanked, false, &idle);

        let busy = self.busy.borrow().contains(&format!("frankenphp {}", build.php));
        if busy {
            row.add_suffix(&gtk::Spinner::builder().spinning(true).build());
        }
        let actions = gtk::Box::builder().spacing(6).sensitive(!busy).build();
        let minor = build.php.clone();
        let run = |label: &str, suggested: bool, f: fn(&Client, &str) -> Result<FrankenPhpState, Error>| {
            let (this, m) = (self.clone(), minor.clone());
            button(label, suggested, move || {
                let m2 = m.clone();
                this.run_frankenphp(&m, move |c| f(&c, &m2));
            })
        };
        match build.job.as_ref() {
            Some(job) if job.running() => actions.append(&run(tr!("common.cancel"), false, Client::cancel_frankenphp)),
            Some(job) if job.failed() => {
                actions.append(&run(tr!("common.retry"), false, Client::install_frankenphp));
                actions.append(&run(tr!("sites.share.dismiss"), false, Client::dismiss_frankenphp));
            }
            _ => {
                // Installing ahead makes switching a site to FrankenPHP immediate (else PHP-FPM serves it meanwhile).
                if !installed && !build.latest.is_empty() {
                    actions.append(&run(tr!("common.install"), false, Client::install_frankenphp));
                }
                if build.update {
                    let label = if build.latest == build.php_version {
                        tr!("common.update").to_owned()
                    } else {
                        tr!("php.update_to_php_version", version = build.latest)
                    };
                    actions.append(&run(&label, true, Client::install_frankenphp));
                }
                if installed {
                    let (this, m, r) = (self.clone(), minor.clone(), row.clone());
                    let uninstall = button(tr!("common.uninstall_ellipsis"), false, move || {
                        let (t, m) = (this.clone(), m.clone());
                        confirm(
                            &r,
                            &tr!("php.uninstall_franken_question", version = m),
                            tr!("php.sites_that_run_on_frankenphp"),
                            tr!("common.uninstall"),
                            move || {
                                let m2 = m.clone();
                                t.run_frankenphp(&m, move |c| c.uninstall_frankenphp(&m2));
                            },
                        );
                    });
                    actions.append(&uninstall);
                }
            }
        }
        row.add_suffix(&actions);
        row
    }
}

/// Opens a file in the app the desktop picks for it (php.ini in a text editor).
fn open_file(widget: &impl IsA<gtk::Widget>, path: &str) {
    crate::ui::open_file(Some(widget.upcast_ref()), path);
}
