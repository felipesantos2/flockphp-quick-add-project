//! The progress of a worktree's job (its creation and setup, a setup run again, a database switch)
//! in a dialog: the step it's at and its log as it grows, then Ready with Open in Browser, or the
//! error with Run Setup Again. Hide leaves the job running: the Sites list shows it, and a failure
//! is notified (notifier.rs). The macOS app's WorktreeJobView.

use std::cell::{Cell, RefCell};
use std::rc::Rc;
use std::time::Duration;

use adw::prelude::*;
use gtk::{gio, glib};

use super::versions::button;
use crate::api::{Client, Error, WorktreeStatus};
use crate::i18n::tr;
use crate::model::AppModel;

/// Starts a job and answers with the worktree's folder.
pub type Start = Box<dyn FnOnce(Client) -> Result<String, Error> + Send>;

/// How often a running job is asked about.
const POLL_INTERVAL: Duration = Duration::from_millis(500);

#[derive(Clone, PartialEq)]
enum Phase {
    Starting,
    /// What the setup does now.
    Running(String),
    /// The worktree's name and URL, once the status lists it.
    Ready {
        name: Option<String>,
        url: Option<String>,
    },
    Failed {
        path: Option<String>,
        error: String,
    },
}

struct JobView {
    dialog: adw::Dialog,
    spinner: gtk::Spinner,
    headline: gtk::Label,
    log: gtk::TextView,
    scrolled: gtk::ScrolledWindow,
    buttons: gtk::Box,
    model: Rc<AppModel>,
    /// The worktree's folder, once the job started.
    path: RefCell<Option<String>>,
    /// Counts the starts, so the polls of a job that was started again stop.
    run: Cell<u32>,
    closed: Cell<bool>,
    shown: RefCell<Option<Phase>>,
    /// Closing a worktree that is ready shows it: called with its name.
    close: Box<dyn Fn(String)>,
}

/// Opens the dialog over parent and starts the job. close gets the worktree's name when the user
/// closes the dialog once it is ready.
pub fn open(parent: &impl IsA<gtk::Widget>, model: Rc<AppModel>, title: &str, start: Start, close: impl Fn(String) + 'static) {
    let spinner = gtk::Spinner::builder().valign(gtk::Align::Start).build();
    let headline = gtk::Label::builder().wrap(true).xalign(0.0).selectable(true).hexpand(true).build();
    let top = gtk::Box::builder().spacing(8).build();
    top.append(&spinner);
    top.append(&headline);
    let log = gtk::TextView::builder()
        .editable(false)
        .cursor_visible(false)
        .monospace(true)
        .top_margin(8)
        .bottom_margin(8)
        .left_margin(8)
        .right_margin(8)
        .build();
    let scrolled = gtk::ScrolledWindow::builder().child(&log).height_request(260).css_classes(["card"]).build();
    let buttons = gtk::Box::builder().spacing(8).halign(gtk::Align::End).build();
    let column = gtk::Box::builder()
        .orientation(gtk::Orientation::Vertical)
        .spacing(12)
        .margin_top(12)
        .margin_bottom(18)
        .margin_start(18)
        .margin_end(18)
        .build();
    column.append(&top);
    column.append(&scrolled);
    column.append(&buttons);
    let view = adw::ToolbarView::new();
    view.add_top_bar(&adw::HeaderBar::new());
    view.set_content(Some(&column));
    let dialog = adw::Dialog::builder().title(title).content_width(600).child(&view).build();
    let this = Rc::new(JobView {
        dialog,
        spinner,
        headline,
        log,
        scrolled,
        buttons,
        model,
        path: RefCell::default(),
        run: Cell::new(0),
        closed: Cell::new(false),
        shown: RefCell::default(),
        close: Box::new(close),
    });
    let weak = Rc::downgrade(&this);
    this.dialog.connect_closed(move |_| {
        if let Some(this) = weak.upgrade() {
            this.closed.set(true);
            if let Some(path) = this.path.borrow().as_deref() {
                this.model.unfollow_worktree(path);
            }
        }
    });
    this.dialog.present(Some(parent));
    this.start(start);
}

/// The worktree's setup and the end of its log.
fn poll(path: &str) -> Result<(WorktreeStatus, Option<String>), Error> {
    let setup = Client.worktree_setup(path)?;
    let log = if setup.log.is_empty() { None } else { Client.service_log_tail(&setup.log, 400).ok() };
    Ok((setup, log))
}

impl JobView {
    /// Starts the job, then follows its setup until it is ready or failed, or the dialog closes.
    fn start(self: &Rc<Self>, start: Start) {
        let run = self.run.get() + 1;
        self.run.set(run);
        self.set_log("");
        self.show(Phase::Starting);
        let this = self.clone();
        glib::spawn_future_local(async move {
            let path = match gio::spawn_blocking(move || start(Client)).await.expect("worktree request") {
                Ok(path) => path,
                Err(e) => {
                    this.show(Phase::Failed { path: None, error: e.to_string() });
                    return;
                }
            };
            this.path.replace(Some(path.clone()));
            if !this.closed.get() {
                this.model.follow_worktree(&path);
            }
            this.model.refresh();
            while !this.closed.get() && this.run.get() == run {
                let p = path.clone();
                let polled = gio::spawn_blocking(move || poll(&p)).await.expect("worktree request");
                if this.closed.get() || this.run.get() != run {
                    return;
                }
                // Until the daemon knows the setup, it answers 404: asked again.
                if let Ok((setup, log)) = polled {
                    if let Some(log) = log {
                        this.set_log(&log);
                    }
                    match setup.state.as_str() {
                        "ready" => {
                            let status = gio::spawn_blocking(|| Client.status()).await.expect("status request").ok();
                            let site = status.and_then(|s| s.sites.into_iter().find(|site| site.is_worktree() && site.path == path));
                            let (name, url) = site.map_or((None, None), |site| (Some(site.name), site.url));
                            this.show(Phase::Ready { name, url });
                            this.model.refresh();
                            return;
                        }
                        "failed" => {
                            let error = if setup.error.is_empty() { tr!("worktrees.job.see_log").to_owned() } else { setup.error.clone() };
                            this.show(Phase::Failed { path: Some(path.clone()), error: tr!("worktrees.job.setup_failed", error = error) });
                            this.model.refresh();
                            return;
                        }
                        _ => this.show(Phase::Running(setup.step_text())),
                    }
                }
                glib::timeout_future(POLL_INTERVAL).await;
            }
        });
    }

    /// Shows the log, scrolled to its end, when it changed.
    fn set_log(&self, text: &str) {
        let buffer = self.log.buffer();
        if buffer.text(&buffer.start_iter(), &buffer.end_iter(), false) == text {
            return;
        }
        buffer.set_text(text);
        // Once the new text is laid out.
        let adjustment = self.scrolled.vadjustment();
        glib::idle_add_local_once(move || adjustment.set_value(adjustment.upper() - adjustment.page_size()));
    }

    fn show(self: &Rc<Self>, phase: Phase) {
        let previous = self.shown.replace(Some(phase.clone()));
        if previous.as_ref() == Some(&phase) {
            return;
        }
        let busy = matches!(phase, Phase::Starting | Phase::Running(_));
        self.spinner.set_visible(busy);
        self.spinner.set_spinning(busy);
        let (text, class) = match &phase {
            Phase::Starting => (tr!("worktrees.job.starting").to_owned(), ""),
            Phase::Running(step) => (step.clone(), ""),
            Phase::Ready { url: Some(url), .. } => (tr!("worktrees.job.ready_at", url = url), "success"),
            Phase::Ready { url: None, .. } => (tr!("worktrees.job.ready").to_owned(), "success"),
            Phase::Failed { error, .. } => (error.clone(), "warning"),
        };
        self.headline.set_label(&text);
        for class in ["success", "warning"] {
            self.headline.remove_css_class(class);
        }
        if !class.is_empty() {
            self.headline.add_css_class(class);
        }
        // A running job keeps its Hide button.
        if busy && matches!(previous, Some(Phase::Starting | Phase::Running(_))) {
            return;
        }
        while let Some(child) = self.buttons.first_child() {
            self.buttons.remove(&child);
        }
        let weak = Rc::downgrade(self);
        let close = move |name: Option<String>| {
            if let Some(this) = weak.upgrade() {
                this.dialog.close();
                if let Some(name) = name {
                    (this.close)(name);
                }
            }
        };
        match phase {
            Phase::Starting | Phase::Running(_) => {
                let hide = button(tr!("common.hide"), false, move || close(None));
                hide.set_tooltip_text(Some(tr!("worktrees.setup_goes_on_flock_tells")));
                self.buttons.append(&hide);
            }
            Phase::Ready { name, url } => {
                if let Some(url) = url {
                    let weak = Rc::downgrade(self);
                    self.buttons.append(&button(tr!("common.open_in_browser"), false, move || {
                        let dialog = weak.upgrade().map(|this| this.dialog.clone().upcast::<gtk::Widget>());
                        crate::ui::open_uri(dialog.as_ref(), &url);
                    }));
                }
                self.buttons.append(&button(tr!("common.close"), true, move || close(name.clone())));
            }
            Phase::Failed { path, .. } => {
                if let Some(path) = path {
                    let weak = Rc::downgrade(self);
                    self.buttons.append(&button(tr!("worktrees.run_again"), false, move || {
                        if let Some(this) = weak.upgrade() {
                            let path = path.clone();
                            this.start(Box::new(move |c: Client| c.setup_worktree(&path, true).map(|_| path)));
                        }
                    }));
                }
                self.buttons.append(&button(tr!("common.close"), true, move || close(None)));
            }
        }
    }
}
