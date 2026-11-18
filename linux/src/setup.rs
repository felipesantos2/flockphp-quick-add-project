//! What the main window shows until Flock is set up: what setting it up does, a check that no
//! other program holds Flock's ports, `flock install` (one password prompt, polkit's dialog) with
//! its output, then the first run's install of the default PHP and Node.js. The macOS and Windows
//! apps have a setup window for this; their menu bar and tray panels can't hold the steps.

use std::cell::{Cell, RefCell};
use std::rc::Rc;

use adw::prelude::*;
use gtk::{gio, glib};

use crate::api::{DefaultInstall, DefaultsStatus};
use crate::autostart;
use crate::i18n::{tr, trn};
use crate::installation::{self, Conflict};
use crate::model::AppModel;

pub struct SetupView {
    pub widget: adw::Bin,
    model: Rc<AppModel>,
    /// From "Set Up Flock" until "Done": the main window keeps showing the steps, also once the
    /// daemon is registered (which happens before the setup has finished).
    engaged: Cell<bool>,
    /// Called when the user is done with the setup, to show the main window's status.
    on_finish: RefCell<Option<Box<dyn Fn()>>>,
    /// The first run's installs as this window last saw them in the status: the status stops
    /// listing them once both are done.
    seen_defaults: RefCell<Option<DefaultsStatus>>,
    /// The "Flock is ready" page's list of next steps, rebuilt as the defaults install.
    next_steps: RefCell<Option<adw::PreferencesGroup>>,
    /// The rows in it now.
    step_rows: RefCell<Vec<adw::ActionRow>>,
    /// Open Flock at login, set once the setup succeeded.
    startup: gtk::CheckButton,
    /// Why that couldn't be set, for the next steps.
    startup_problem: RefCell<Option<String>>,
}

impl SetupView {
    pub fn new(model: Rc<AppModel>) -> Rc<Self> {
        let this = Rc::new(Self {
            widget: adw::Bin::new(),
            model,
            engaged: Cell::new(false),
            on_finish: RefCell::default(),
            seen_defaults: RefCell::default(),
            next_steps: RefCell::default(),
            step_rows: RefCell::default(),
            startup: gtk::CheckButton::builder().label(tr!("setup.open_flock_when_i_log")).active(true).build(),
            startup_problem: RefCell::default(),
        });
        this.intro();
        let weak = Rc::downgrade(&this);
        this.model.subscribe(move || {
            if let Some(this) = weak.upgrade() {
                this.update_next_steps();
            }
        });
        this
    }

    /// The setup was started and isn't done yet.
    pub fn engaged(&self) -> bool {
        self.engaged.get()
    }

    pub fn connect_finish(&self, f: impl Fn() + 'static) {
        self.on_finish.replace(Some(Box::new(f)));
    }

    /// Back to the first page, and the main window to its status.
    fn finish(self: &Rc<Self>) {
        self.engaged.set(false);
        self.intro();
        if let Some(f) = self.on_finish.borrow().as_ref() {
            f();
        }
    }

    fn page(&self, title: &str, text: &str, body: Option<&gtk::Widget>, buttons: &[gtk::Button]) {
        let column = gtk::Box::builder()
            .orientation(gtk::Orientation::Vertical)
            .spacing(18)
            .margin_top(12)
            .margin_bottom(24)
            .margin_start(32)
            .margin_end(32)
            .build();
        let icon = gtk::Image::builder()
            .icon_name("network-server-symbolic")
            .pixel_size(56)
            .halign(gtk::Align::Start)
            .css_classes(["accent"])
            .build();
        column.append(&icon);
        column.append(&gtk::Label::builder().label(title).xalign(0.0).wrap(true).css_classes(["title-1"]).build());
        if !text.is_empty() {
            column.append(&gtk::Label::builder().label(text).xalign(0.0).wrap(true).css_classes(["dim-label"]).build());
        }
        if let Some(body) = body {
            column.append(body);
        }
        let row = gtk::Box::builder().spacing(8).halign(gtk::Align::End).margin_top(6).build();
        for button in buttons {
            row.append(button);
        }
        column.append(&row);
        let clamp = adw::Clamp::builder().maximum_size(560).child(&column).build();
        let scrolled = gtk::ScrolledWindow::builder().hscrollbar_policy(gtk::PolicyType::Never).child(&clamp).build();
        self.widget.set_child(Some(&scrolled));
    }

    fn button(&self, label: &str, suggested: bool, clicked: impl Fn() + 'static) -> gtk::Button {
        let button = gtk::Button::builder().label(label).build();
        if suggested {
            button.add_css_class("suggested-action");
        }
        button.connect_clicked(move |_| clicked());
        button
    }

    /// Leaves a failed or blocked setup for the first page.
    fn back_button(self: &Rc<Self>) -> gtk::Button {
        let this = self.clone();
        self.button(tr!("setup.back"), false, move || this.finish())
    }

    fn intro(self: &Rc<Self>) {
        let list = item_list(&[
            ("network-server-symbolic", tr!("setup.background"), tr!("setup.flock_daemon_serves_your_sites_and"), ""),
            ("utilities-terminal-symbolic", tr!("setup.terminal_title"), tr!("setup.terminal_description"), ""),
            ("channel-secure-symbolic", tr!("setup.trusted_https_title"), tr!("setup.linux_asks_for_your_password_once"), ""),
        ]);
        let body = gtk::Box::builder().orientation(gtk::Orientation::Vertical).spacing(12).build();
        body.append(&list);
        // The page is rebuilt after a failed setup: the check moves along.
        if let Some(parent) = self.startup.parent().and_downcast::<gtk::Box>() {
            parent.remove(&self.startup);
        }
        body.append(&self.startup);
        body.append(&crate::panes::general::language());
        let this = self.clone();
        let set_up = self.button(tr!("setup.window_title"), true, move || this.run());
        self.page(tr!("setup.welcome"), tr!("setup.flock_runs_your_laravel_sites_php"), Some(body.upcast_ref()), &[set_up]);
        self.next_steps.replace(None);
    }

    fn run(self: &Rc<Self>) {
        self.engaged.set(true);
        let bar = gtk::ProgressBar::new();
        bar.pulse();
        self.page(tr!("setup.checking_ports"), "", Some(bar.upcast_ref()), &[]);
        let this = self.clone();
        glib::spawn_future_local(async move {
            let conflicts = gio::spawn_blocking(installation::conflicts).await.unwrap_or_default();
            if conflicts.is_empty() {
                this.install();
            } else {
                this.conflicts(&conflicts);
            }
        });
    }

    fn conflicts(self: &Rc<Self>, apps: &[(String, Option<String>, Vec<Conflict>)]) {
        let tld = self.model.status().map(|s| s.dns.tld).filter(|t| !t.is_empty()).unwrap_or_else(|| "test".to_owned());
        let items: Vec<(String, String)> = apps
            .iter()
            .map(|(app, hint, conflicts)| {
                let title = match hint {
                    Some(hint) if !hint.is_empty() => format!("{}{}", hint[..1].to_uppercase(), &hint[1..]),
                    _ => tr!("app.conflict.quit", app = app),
                };
                let ports: Vec<String> =
                    conflicts.iter().filter_map(|c| c.port.map(|p| tr!("app.conflict.port", port = p, service = c.service))).collect();
                (title, tr!("app.program_uses_ports", program = conflicts[0].holder, ports = and(ports)))
            })
            .collect();
        let list =
            item_list(&items.iter().map(|(t, d)| ("dialog-warning-symbolic", t.as_str(), d.as_str(), "warning")).collect::<Vec<_>>());
        let this = self.clone();
        let again = self.button(tr!("setup.check_again_title_case"), true, move || this.run());
        self.page(
            tr!("setup.stop_other_web_servers_first"),
            &tr!("setup.flock_needs_these_ports_and", tld = tld),
            Some(list.upcast_ref()),
            &[self.back_button(), again],
        );
    }

    fn install(self: &Rc<Self>) {
        let (log, buffer) = log_view();
        let bar = gtk::ProgressBar::new();
        let body = gtk::Box::builder().orientation(gtk::Orientation::Vertical).spacing(12).build();
        body.append(&bar);
        body.append(&log);
        self.page(tr!("setup.setting_up"), tr!("setup.linux_asks_for_your_password"), Some(body.upcast_ref()), &[]);
        let pulse = glib::timeout_add_local(std::time::Duration::from_millis(150), move || {
            bar.pulse();
            glib::ControlFlow::Continue
        });
        let this = self.clone();
        let pulse = RefCell::new(Some(pulse));
        installation::run_streaming(
            vec!["install".to_owned()],
            move |line| append(&buffer, line),
            move |output| {
                if let Some(pulse) = pulse.take() {
                    pulse.remove();
                }
                this.model.refresh();
                if output.ok {
                    let problem = autostart::set(this.startup.is_active()).err();
                    this.startup_problem.replace(problem.map(|e| tr!("setup.could_not_change_the_startup", error = e)));
                    this.done();
                } else {
                    this.failed(&output);
                }
            },
        );
    }

    fn failed(self: &Rc<Self>, output: &installation::Output) {
        let (log, buffer) = log_view();
        append(&buffer, &output.text);
        let text = if installation::declined(output) { tr!("setup.password_prompt_was_cancelled_flock") } else { tr!("setup.failed") };
        let this = self.clone();
        let again = self.button(tr!("setup.try_again"), true, move || this.run());
        self.page(tr!("setup.did_not_finish"), text, Some(log.upcast_ref()), &[self.back_button(), again]);
    }

    fn done(self: &Rc<Self>) {
        let group = adw::PreferencesGroup::builder().title(tr!("setup.next_steps")).build();
        self.next_steps.replace(Some(group.clone()));
        self.update_next_steps();
        let this = self.clone();
        let settings = self.button(tr!("setup.open_settings"), false, move || {
            this.finish();
            let _ = this.widget.activate_action("app.settings", None);
        });
        let this = self.clone();
        let done = self.button(tr!("common.done"), true, move || this.finish());
        let body = gtk::Box::builder().orientation(gtk::Orientation::Vertical).spacing(18).build();
        body.append(&group);
        body.append(
            &gtk::Label::builder()
                .label(markdown_links(tr!("setup.star")))
                .use_markup(true)
                .wrap(true)
                .xalign(0.0)
                .css_classes(["dim-label"])
                .build(),
        );
        self.page(tr!("setup.ready"), "", Some(body.upcast_ref()), &[settings, done]);
    }

    /// The next steps, with the first run's installs of the newest PHP and Node.js LTS as the
    /// status reports them: in progress, waiting for a download, failed, or done.
    fn update_next_steps(&self) {
        let Some(group) = self.next_steps.borrow().clone() else { return };
        let defaults = self.model.status().and_then(|s| s.defaults);
        if let Some(defaults) = &defaults {
            self.seen_defaults.replace(Some(defaults.clone()));
        }
        for row in self.step_rows.take() {
            group.remove(&row);
        }
        let add = |row: adw::ActionRow| {
            group.add(&row);
            self.step_rows.borrow_mut().push(row);
        };
        let shown = defaults.or_else(|| {
            // No longer listed: both are installed.
            self.seen_defaults.borrow().clone().map(|seen| DefaultsStatus {
                php: DefaultInstall { state: "done".to_owned(), ..seen.php },
                node: DefaultInstall { state: "done".to_owned(), ..seen.node },
            })
        });
        if let Some(defaults) = shown {
            let all = [("PHP", "PHP", &defaults.php), ("Node.js", "Node", &defaults.node)];
            let label = |name: &str, install: &DefaultInstall| {
                if install.version.is_empty() { name.to_owned() } else { format!("{name} {}", install.version) }
            };
            for (name, pane, install) in all.iter().filter(|(_, _, i)| i.state == "failed") {
                let error = if install.error.is_empty() { String::new() } else { format!("{}. ", install.error) };
                add(item(
                    "dialog-warning-symbolic",
                    &tr!("setup.name_couldnt_be_installed", name = label(name, install)),
                    &tr!("setup.error_try_again_in_settings", error = error, pane = pane),
                    "warning",
                ));
            }
            let pending: Vec<_> = all.iter().filter(|(_, _, i)| i.state == "waiting" || i.state == "installing").collect();
            let names = |list: &[&(&str, &str, &DefaultInstall)]| and(list.iter().map(|(n, _, i)| label(n, i)).collect());
            if !pending.is_empty() {
                let installing = pending.iter().any(|(_, _, i)| i.state == "installing");
                let row = if installing {
                    item(
                        "folder-download-symbolic",
                        &tr!("setup.installing", names = names(&pending)),
                        &trn!("setup.flock_makes_them_the_default", pending.len()),
                        "",
                    )
                } else {
                    item(
                        "folder-download-symbolic",
                        &tr!("setup.waiting_to_install_names", names = names(&pending)),
                        &trn!("setup.as_soon_as_flock_can", pending.len()),
                        "",
                    )
                };
                row.add_suffix(&gtk::Spinner::builder().spinning(true).build());
                add(row);
            }
            if all.iter().all(|(_, _, i)| i.state == "done") {
                let refs: Vec<_> = all.iter().collect();
                add(item(
                    "emblem-ok-symbolic",
                    &tr!("setup.names_are_installed", names = names(&refs)),
                    tr!("setup.theyre_the_defaults_for_your"),
                    "success",
                ));
            }
        }
        if let Some(problem) = self.startup_problem.borrow().as_deref() {
            add(item(
                "dialog-warning-symbolic",
                tr!("setup.flock_wont_open_at_login"),
                &tr!("setup.problem_settings_to_general_has", problem = problem),
                "warning",
            ));
        }
        add(item("utilities-terminal-symbolic", tr!("setup.new_terminal"), tr!("setup.new_terminal_description"), ""));
        add(item("dev.flock.Flock-sites-symbolic", tr!("setup.link_project"), tr!("setup.link_project_description"), ""));
    }
}

/// Runs `flock uninstall` in a dialog with its output; purge also deletes the sites' settings and
/// databases. The package itself stays: the package manager removes it.
pub fn uninstall(parent: &impl IsA<gtk::Widget>, model: Rc<AppModel>) {
    let purge = gtk::CheckButton::with_label(tr!("about.also_delete_data"));
    let dialog = adw::AlertDialog::new(
        Some(tr!("about.uninstall_question")),
        Some(&tr!("setup.flock_stops_and_is_removed", folder = crate::paths::root().display())),
    );
    dialog.set_extra_child(Some(&purge));
    dialog.add_responses(&[("cancel", tr!("common.cancel")), ("uninstall", tr!("common.uninstall"))]);
    dialog.set_response_appearance("uninstall", adw::ResponseAppearance::Destructive);
    let parent = parent.as_ref().clone();
    dialog.choose(Some(&parent.clone()), gio::Cancellable::NONE, move |response| {
        if response != "uninstall" {
            return;
        }
        let mut args = vec!["uninstall".to_owned()];
        if purge.is_active() {
            args.push("--purge".to_owned());
        }
        let (log, buffer) = log_view();
        let status = gtk::Label::builder().label(tr!("about.uninstalling_flock")).xalign(0.0).css_classes(["heading"]).build();
        let column = gtk::Box::builder()
            .orientation(gtk::Orientation::Vertical)
            .spacing(12)
            .margin_top(12)
            .margin_bottom(18)
            .margin_start(18)
            .margin_end(18)
            .build();
        column.append(&status);
        column.append(&log);
        let view = adw::ToolbarView::new();
        view.add_top_bar(&adw::HeaderBar::new());
        view.set_content(Some(&column));
        let progress = adw::Dialog::builder()
            .title(tr!("about.uninstall_flock"))
            .content_width(560)
            .content_height(380)
            .child(&view)
            .can_close(false)
            .build();
        progress.present(Some(&parent));
        installation::run_streaming(
            args,
            move |line| append(&buffer, line),
            move |output| {
                progress.set_can_close(true);
                status.set_label(if output.ok {
                    tr!("setup.flock_was_uninstalled_remove_the")
                } else if installation::declined(&output) {
                    tr!("about.password_prompt_was_cancelled_so")
                } else {
                    tr!("setup.uninstalling_didnt_finish_the_output")
                });
                status.set_wrap(true);
                model.refresh();
            },
        );
    });
}

/// "PHP 8.5 and Node.js 24", whole: the lists here have two items, a longer one would nest.
fn and(items: Vec<String>) -> String {
    items.into_iter().reduce(|first, second| tr!("common.list.and", first = first, second = second)).unwrap_or_default()
}

/// A list of rows with an icon, a title and a line of text.
fn item_list(items: &[(&str, &str, &str, &str)]) -> gtk::ListBox {
    let list = gtk::ListBox::builder().css_classes(["boxed-list"]).selection_mode(gtk::SelectionMode::None).build();
    for (icon, title, text, class) in items {
        list.append(&item(icon, title, text, class));
    }
    list
}

fn item(icon: &str, title: &str, text: &str, class: &str) -> adw::ActionRow {
    let row = adw::ActionRow::builder().title(glib::markup_escape_text(title)).subtitle(glib::markup_escape_text(text)).build();
    let image = gtk::Image::from_icon_name(icon);
    image.add_css_class(if class.is_empty() { "accent" } else { class });
    row.add_prefix(&image);
    row
}

/// A monospaced, scrolling log and its buffer.
fn log_view() -> (gtk::ScrolledWindow, gtk::TextBuffer) {
    let text = gtk::TextView::builder()
        .editable(false)
        .monospace(true)
        .wrap_mode(gtk::WrapMode::WordChar)
        .top_margin(8)
        .bottom_margin(8)
        .left_margin(8)
        .right_margin(8)
        .build();
    let scrolled = gtk::ScrolledWindow::builder().child(&text).height_request(220).css_classes(["card"]).build();
    (scrolled, text.buffer())
}

fn append(buffer: &gtk::TextBuffer, text: &str) {
    let mut end = buffer.end_iter();
    buffer.insert(&mut end, text);
    buffer.insert(&mut end, "\n");
}

/// Pango markup for a text with Markdown links, [label](url), as the translations write them (the
/// macOS and Windows apps share the text).
fn markdown_links(text: &str) -> String {
    let mut markup = String::new();
    let mut rest = text;
    while let Some(start) = rest.find('[') {
        let Some((label, url, after)) =
            rest[start + 1..].split_once("](").and_then(|(label, tail)| tail.split_once(')').map(|(url, after)| (label, url, after)))
        else {
            break;
        };
        markup.push_str(&glib::markup_escape_text(&rest[..start]));
        markup.push_str(&format!("<a href=\"{}\">{}</a>", glib::markup_escape_text(url), glib::markup_escape_text(label)));
        rest = after;
    }
    markup.push_str(&glib::markup_escape_text(rest));
    markup
}

#[cfg(test)]
mod tests {
    use super::markdown_links;

    #[test]
    fn makes_markdown_links_pango_ones() {
        assert_eq!(
            markdown_links("Free & open. If you like it, [a star](https://example.com/a?b=1&c=2) helps."),
            "Free &amp; open. If you like it, <a href=\"https://example.com/a?b=1&amp;c=2\">a star</a> helps."
        );
        assert_eq!(markdown_links("No [link here"), "No [link here");
    }
}
