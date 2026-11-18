//! Shared state of the main window and the other windows: the daemon's status, polled every two
//! seconds, and the actions on it (restart, settings). Windows subscribe to changes.

use std::cell::{Cell, RefCell};
use std::collections::HashSet;
use std::rc::Rc;
use std::time::Duration;

use gtk::{gio, glib};

use crate::api::{Client, Error, SettingsChange, Status};
use crate::i18n::tr;
use crate::installation;
use crate::links::Link;

/// How often the daemon is asked for its status.
const POLL_INTERVAL: Duration = Duration::from_secs(2);

#[derive(Default)]
pub struct AppModel {
    status: RefCell<Option<Status>>,
    /// Why the status couldn't be read (the daemon isn't running); replaced by every poll.
    error: RefCell<Option<String>>,
    /// The last failed user action (restart, a toggle). Polls leave it alone, so it stays until
    /// it is dismissed or the next action succeeds.
    action_error: RefCell<Option<String>>,
    needs_setup: Cell<bool>,
    restarting: Cell<bool>,
    polling: Cell<bool>,
    /// A flock:// link the pane it opened asks about once its data is loaded.
    link: RefCell<Option<Link>>,
    listeners: RefCell<Vec<Rc<dyn Fn()>>>,
    /// Worktrees whose job a progress dialog follows, by folder: their failures aren't notified.
    followed_worktrees: RefCell<HashSet<String>>,
}

impl AppModel {
    pub fn new() -> Rc<Self> {
        let model = Rc::new(Self::default());
        model.needs_setup.set(installation::needs_setup());
        model
    }

    pub fn status(&self) -> Option<Status> {
        self.status.borrow().clone()
    }

    pub fn error(&self) -> Option<String> {
        self.error.borrow().clone()
    }

    pub fn action_error(&self) -> Option<String> {
        self.action_error.borrow().clone()
    }

    pub fn dismiss_error(&self) {
        self.action_error.replace(None);
        self.changed();
    }

    /// Flock isn't set up for this user yet, and the app can do it.
    pub fn needs_setup(&self) -> bool {
        self.needs_setup.get()
    }

    pub fn is_restarting(&self) -> bool {
        self.restarting.get()
    }

    pub fn link(&self) -> Option<Link> {
        self.link.borrow().clone()
    }

    pub fn set_link(&self, link: Option<Link>) {
        self.link.replace(link);
    }

    pub fn follow_worktree(&self, path: &str) {
        self.followed_worktrees.borrow_mut().insert(path.to_owned());
    }

    pub fn unfollow_worktree(&self, path: &str) {
        self.followed_worktrees.borrow_mut().remove(path);
    }

    pub fn follows_worktree(&self, path: &str) -> bool {
        self.followed_worktrees.borrow().contains(path)
    }

    /// Calls f after every change, from now on.
    pub fn subscribe(&self, f: impl Fn() + 'static) {
        self.listeners.borrow_mut().push(Rc::new(f));
    }

    fn changed(&self) {
        // A listener may subscribe another one (a window opening), so not while iterating the list.
        let listeners = self.listeners.borrow().clone();
        for f in listeners {
            f();
        }
    }

    pub fn start_polling(self: &Rc<Self>) {
        self.refresh();
        let this = self.clone();
        glib::timeout_add_local(POLL_INTERVAL, move || {
            this.refresh();
            glib::ControlFlow::Continue
        });
    }

    /// Reads the status now.
    pub fn refresh(self: &Rc<Self>) {
        if self.polling.replace(true) {
            return;
        }
        let this = self.clone();
        glib::spawn_future_local(async move {
            let result = gio::spawn_blocking(|| Client.status()).await.expect("status request");
            this.polling.set(false);
            match result {
                Ok(status) => this.apply(status),
                Err(e) => {
                    this.status.replace(None);
                    this.error.replace(Some(e.to_string()));
                }
            }
            // `flock install` or `flock uninstall` in a terminal changes it while the app runs.
            this.needs_setup.set(installation::needs_setup());
            this.changed();
        });
    }

    fn apply(&self, status: Status) {
        self.status.replace(Some(status));
        self.error.replace(None);
    }

    /// Changes settings; on failure, report gets the error (the banner of the window that asked).
    pub fn change_settings(self: &Rc<Self>, change: SettingsChange, report: impl Fn(Option<String>) + 'static) {
        self.perform(move |client| client.update_settings(&change), report);
    }

    /// Runs a request that answers with the new status, from a background thread.
    pub fn perform(
        self: &Rc<Self>,
        f: impl FnOnce(Client) -> Result<Status, Error> + Send + 'static,
        report: impl Fn(Option<String>) + 'static,
    ) {
        let this = self.clone();
        glib::spawn_future_local(async move {
            match gio::spawn_blocking(move || f(Client)).await.expect("request") {
                Ok(status) => {
                    this.apply(status);
                    report(None);
                }
                Err(e) => report(Some(e.to_string())),
            }
            this.changed();
        });
    }

    /// Runs `flock restart`, which also starts a stopped daemon.
    pub fn restart(self: &Rc<Self>) {
        self.restarting.set(true);
        self.changed();
        let this = self.clone();
        glib::spawn_future_local(async move {
            let out = gio::spawn_blocking(|| installation::run(&["restart"])).await.expect("flock restart");
            this.restarting.set(false);
            this.action_error.replace(if out.ok { None } else { Some(out.text) });
            this.changed();
            this.refresh();
        });
    }

    /// Reports a failed action in the main window's banner.
    pub fn report(&self, error: Option<String>) {
        self.action_error.replace(error);
        self.changed();
    }

    /// "Running · v1.2", "Needs attention · v1.2", "Not running" or "Not set up".
    pub fn headline(&self) -> String {
        match (self.status.borrow().as_ref(), self.needs_setup()) {
            (_, true) => tr!("menu.status.not_set_up").to_owned(),
            (Some(s), _) if s.is_degraded() => tr!("app.needs_attention_version", version = display_version(&s.version)),
            (Some(s), _) => tr!("app.running_version", version = display_version(&s.version)),
            (None, _) => tr!("sites.proxy.not_running").to_owned(),
        }
    }
}

/// "v1.2" for a release; a dev build's "dev-<hash>" as it is.
fn display_version(version: &str) -> String {
    if version.starts_with(|c: char| c.is_ascii_digit()) { format!("v{version}") } else { version.to_owned() }
}
