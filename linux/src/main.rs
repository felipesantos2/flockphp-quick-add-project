//! Flock's Linux app: a tray icon and a libadwaita window for the local daemon, like the macOS menu
//! bar app and the Windows tray app. It talks to the daemon over its local API (api.json).

mod api;
mod autostart;
mod dumps;
mod i18n;
mod installation;
mod links;
mod logs;
mod mail;
mod model;
mod notifier;
mod panes;
mod paths;
mod prefs;
#[cfg(feature = "screenshots")]
mod screenshots;
mod setup;
mod timeline;
mod tray;
mod ui;
mod window;

use std::cell::{Cell, RefCell};
use std::path::PathBuf;
use std::rc::Rc;
use std::time::Duration;

use adw::prelude::*;
use gtk::{gio, glib};
use i18n::tr;

const APP_ID: &str = "dev.flock.Flock";

/// The package's version (linux/build-packages.sh); "dev" for a build from the source tree.
const VERSION: &str = match option_env!("FLOCK_VERSION") {
    Some(version) => version,
    None => "dev",
};

/// Styles libadwaita has no class for: the small labels next to versions and sites. A row's title
/// keeps a minimum width, so its suffixes (versions, badges, buttons; longer in Czech and Spanish)
/// can't squeeze it into a letter per line: the window's minimum width grows instead.
const STYLE: &str = "
.badge { padding: 1px 7px; border-radius: 99px; font-weight: 600; background: alpha(currentColor, 0.12); }
row > box.header > box.title { min-width: 150px; }
";

/// The app's windows: the Flock window, created at start, and the others when first opened.
#[derive(Default)]
struct Windows {
    main: RefCell<Option<Rc<window::MainWindow>>>,
    mail: RefCell<Option<Rc<mail::MailWindow>>>,
    dumps: RefCell<Option<Rc<dumps::DumpsWindow>>>,
    logs: RefCell<Option<Rc<logs::LogsWindow>>>,
}

impl Windows {
    /// Opens the Flock window at a pane, "services/<id>" at a service or "sites/<name>" at a site.
    fn pane(&self, pane: &str) -> Option<gtk::Window> {
        self.main.borrow().as_ref()?.select(pane);
        self.main()
    }

    /// Opens what a flock:// link asks about. Install links before the setup wait for it: the
    /// setup opens their pane when it's done.
    fn link(&self, model: &model::AppModel, link: links::Link) {
        match &link {
            links::Link::Secure(_) if model.needs_setup() => {
                self.main();
            }
            links::Link::Secure(_) => {
                model.set_link(Some(link));
                self.pane("sites");
            }
            links::Link::Install(install) => {
                let pane = install.pane();
                model.set_link(Some(link));
                if model.needs_setup() {
                    self.main()
                } else {
                    self.pane(&pane)
                };
            }
        }
    }

    /// Brings the Flock window up at the pane it shows; until Flock is set up, that is the setup.
    fn main(&self) -> Option<gtk::Window> {
        let window = self.main.borrow().as_ref()?.window.clone();
        window.present();
        Some(window.upcast())
    }

    fn mail(&self, app: &adw::Application, model: &Rc<model::AppModel>) -> gtk::Window {
        let window = self.mail.borrow_mut().get_or_insert_with(|| mail::MailWindow::new(app, model.clone())).clone();
        window.window.present();
        window.window.clone().upcast()
    }

    fn dumps(&self, app: &adw::Application, model: &Rc<model::AppModel>) -> gtk::Window {
        let window = self.dumps.borrow_mut().get_or_insert_with(|| dumps::DumpsWindow::new(app, model.clone())).clone();
        window.window.present();
        window.window.clone().upcast()
    }

    /// Opens the Logs window, at a log when one is given.
    fn logs(&self, app: &adw::Application, source: Option<logs::Source>) -> gtk::Window {
        let window = self.logs.borrow_mut().get_or_insert_with(|| logs::LogsWindow::new(app)).clone();
        if let Some(source) = source {
            window.select(source);
        }
        window.window.present();
        window.window.clone().upcast()
    }

    /// Opens a window by name: flock (the Flock window as it was) or setup, mail (mail/newest at the
    /// newest message), dumps, logs (logs/sites/<name>, logs/services/<name> at a log), settings
    /// (General) or settings/<pane>, e.g. settings/sites.
    fn open(&self, app: &adw::Application, model: &Rc<model::AppModel>, name: &str) -> Option<gtk::Window> {
        match name {
            "flock" | "setup" => self.main(),
            "mail" => Some(self.mail(app, model)),
            "dumps" => Some(self.dumps(app, model)),
            "logs" => Some(self.logs(app, None)),
            "mail/newest" => {
                let window = self.mail(app, model);
                if let Some(mail) = self.mail.borrow().as_ref() {
                    mail.select_newest();
                }
                Some(window)
            }
            "settings" => self.pane("general"),
            _ => {
                if let Some(site) = name.strip_prefix("logs/sites/") {
                    Some(self.logs(app, Some(logs::Source::Site(site.to_owned()))))
                } else if let Some(service) = name.strip_prefix("logs/services/") {
                    Some(self.logs(app, Some(logs::Source::Service(service.to_owned()))))
                } else {
                    name.strip_prefix("settings/").and_then(|pane| self.pane(pane))
                }
            }
        }
    }
}

/// The app's styles and icons, once the display is there.
fn load_style() {
    let display = gtk::gdk::Display::default().expect("display");
    let css = gtk::CssProvider::new();
    css.load_from_string(STYLE);
    gtk::style_context_add_provider_for_display(&display, &css, gtk::STYLE_PROVIDER_PRIORITY_APPLICATION);
    let theme = gtk::IconTheme::for_display(&display);
    if let Some(icons) = tray::development_icons() {
        theme.add_search_path(icons);
    }
    // A desktop that doesn't tell GTK its icon theme (Plasma without kde-gtk-config, Sway, i3, ...)
    // leaves GTK on a "gnome" theme that doesn't exist, with only hicolor's icons: Adwaita, which
    // GTK depends on, has every icon the app uses.
    if !theme.has_icon("help-about-symbolic") {
        gtk::Settings::for_display(&display).set_gtk_icon_theme_name(Some("Adwaita"));
    }
    gtk::Window::set_default_icon_name(APP_ID);
}

fn main() -> glib::ExitCode {
    let args: Vec<String> = std::env::args().collect();
    #[cfg(feature = "screenshots")]
    if args.iter().any(|a| a == "--screenshots") {
        return screenshots::main(&args);
    }
    let flag = |name: &str| args.iter().position(|a| a == name).and_then(|i| args.get(i + 1)).cloned();
    // --after <pid>: a restart (app.relaunch) waits for the old app to exit, which until then
    // holds the application ID: GApplication would hand this start to it. No time limit, since
    // starting early would leave no app running once the old one is gone.
    if let Some(pid) = flag("--after") {
        let process = PathBuf::from(format!("/proc/{pid}"));
        while process.exists() {
            std::thread::sleep(Duration::from_millis(50));
        }
    }
    i18n::init(&prefs::load().language);
    // --background: the start at login, which stays in the tray (when the desktop has one).
    // --open <window>: opens setup, mail, settings or settings/<pane> at start.
    // --screenshot <file.png>: renders that window (else the main one) once it shows the status,
    // saves it and quits (development and the docs' screenshots).
    let background = args.iter().any(|a| a == "--background");
    let open = flag("--open");
    let screenshot = flag("--screenshot").map(PathBuf::from);

    // A flock:// link (x-scheme-handler/flock) starts the app with it, or goes to the running one.
    let app = adw::Application::builder().application_id(APP_ID).flags(gio::ApplicationFlags::HANDLES_OPEN).build();
    let model = model::AppModel::new();
    let windows = Rc::new(Windows::default());

    app.connect_startup(|_| {
        load_style();
        autostart::refresh();
    });

    let (m, w) = (model.clone(), windows.clone());
    app.connect_activate(move |app| {
        // Started again (from the launcher) while it runs, maybe hidden in the tray.
        if w.main().is_some() {
            return;
        }
        let main = window::MainWindow::new(app, m.clone());
        m.start_polling();
        w.main.replace(Some(main.clone()));
        // At login, the window only shows if there is no tray to stay in.
        let mut shown = (!background || screenshot.is_some()).then(|| w.main()).flatten();
        if screenshot.is_none() {
            start_tray(app, &m, &w);
            notifier::start(app, &m);
        }
        if let Some(window) = open.as_deref().and_then(|name| w.open(app, &m, name)) {
            shown = Some(window);
        }
        if let (Some(path), Some(shown)) = (screenshot.clone(), shown) {
            // The paintable follows the content from its next frame on.
            let paintable = gtk::WidgetPaintable::new(shown.child().as_ref());
            // FLOCK_APP_SCREENSHOT_DELAY: seconds to wait for slow content (default 3).
            let delay = std::env::var("FLOCK_APP_SCREENSHOT_DELAY").ok().and_then(|d| d.parse().ok()).unwrap_or(3);
            glib::timeout_add_local_once(Duration::from_secs(delay), move || {
                save_screenshot(&shown, &paintable, &path);
                shown.application().expect("application").quit();
            });
        }
    });

    let (m, w) = (model.clone(), windows.clone());
    app.connect_open(move |app, files, _| {
        app.activate();
        for file in files {
            if let Some(link) = links::parse(&file.uri()) {
                w.link(&m, link);
            }
        }
    });

    // app.settings opens General, app.open('settings/sites') a pane, app.open('mail') a window.
    let quit = gio::ActionEntry::builder("quit").activate(|app: &adw::Application, _, _| app.quit()).build();
    let (m, w) = (model.clone(), windows.clone());
    let open_action = gio::ActionEntry::builder("open")
        .parameter_type(Some(glib::VariantTy::STRING))
        .activate(move |app: &adw::Application, _, name| {
            if let Some(name) = name.and_then(|n| n.str()) {
                w.open(app, &m, name);
            }
        })
        .build();
    // Restarts the app, e.g. in another language: the daemon and the sites keep running.
    let relaunch = gio::ActionEntry::builder("relaunch")
        .activate(|app: &adw::Application, _, _| match relaunch() {
            Ok(()) => app.quit(),
            Err(e) => {
                let dialog = adw::AlertDialog::new(Some(&tr!("app.could_not_relaunch", error = e)), None);
                dialog.add_response("ok", tr!("common.ok"));
                dialog.present(app.active_window().as_ref());
            }
        })
        .build();
    let mut entries = vec![quit, open_action, relaunch];
    for name in ["settings", "setup", "mail", "dumps", "logs"] {
        let (m, w) = (model.clone(), windows.clone());
        entries.push(
            gio::ActionEntry::builder(name)
                .activate(move |app: &adw::Application, _, _| {
                    w.open(app, &m, name);
                })
                .build(),
        );
    }
    app.add_action_entries(entries);
    app.set_accels_for_action("app.quit", &["<Control>q"]);
    app.set_accels_for_action("app.settings", &["<Control>comma"]);

    // GApplication would reject our flags as unknown options; it gets the links.
    let links: Vec<&String> = args.iter().take(1).chain(args.iter().skip(1).filter(|a| links::parse(a).is_some())).collect();
    app.run_with_args(&links)
}

/// Shows the tray icon. With a tray, closing the Flock window hides it, and the app keeps running;
/// without one, closing it quits.
fn start_tray(app: &adw::Application, model: &Rc<model::AppModel>, windows: &Rc<Windows>) {
    let w = windows.clone();
    let shown = Cell::new(false);
    let available = move |yes: bool| {
        let Some(main) = w.main.borrow().as_ref().map(|m| m.window.clone()) else { return };
        // Without a tray from the start, the window is the app: it shows after all (at login), and
        // closing it quits. A tray that goes away later (GNOME's, while the screen is locked) keeps
        // a hidden window hidden; the launcher shows it.
        if !yes && !shown.get() {
            main.present();
        }
        shown.set(shown.get() || yes);
        main.set_hide_on_close(shown.get());
    };
    let (app, m, w) = (app.clone(), model.clone(), windows.clone());
    tray::start(model.clone(), available, move |command| match command {
        tray::Command::Show => {
            w.main();
        }
        tray::Command::Open(name) => {
            w.open(&app, &m, name);
        }
        tray::Command::Browse(url) => ui::open_uri(None, &url),
        tray::Command::Change(change) => {
            let model = m.clone();
            m.change_settings(change, move |error| model.report(error));
        }
        tray::Command::Restart => m.restart(),
        tray::Command::Quit => app.quit(),
        tray::Command::Available(_) => {}
    });
}

/// Starts this app again once this process has exited, at General, where the Language is.
/// Through the display's launch context: the new window gets the activation token (Wayland
/// focuses it), and the app the system's language back.
fn relaunch() -> Result<(), Box<dyn std::error::Error>> {
    let exe = glib::shell_quote(paths::app_exe()?);
    let command = format!("{} --open settings --after {}", exe.to_string_lossy(), std::process::id());
    let app = gio::AppInfo::create_from_commandline(command, Some("Flock"), gio::AppInfoCreateFlags::SUPPORTS_STARTUP_NOTIFICATION)?;
    app.launch(&[], Some(&ui::launch_context()))?;
    Ok(())
}

/// Renders the window's content (a toplevel can't paint itself into a paintable) into a PNG.
fn save_screenshot(window: &gtk::Window, paintable: &gtk::WidgetPaintable, path: &PathBuf) {
    let (width, height) = (paintable.intrinsic_width(), paintable.intrinsic_height());
    let snapshot = gtk::Snapshot::new();
    paintable.snapshot(&snapshot, width as f64, height as f64);
    let Some(node) = snapshot.to_node() else {
        eprintln!("screenshot: the window drew nothing ({width}×{height})");
        return;
    };
    let Some(renderer) = window.native().and_then(|n| n.renderer()) else {
        eprintln!("screenshot: the window has no renderer");
        return;
    };
    let texture = renderer.render_texture(&node, None);
    if let Err(e) = texture.save_to_png(path) {
        eprintln!("screenshot: {e}");
    }
}
