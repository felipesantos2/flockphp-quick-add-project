//! AI: adds Flock's MCP server to AI clients. Added once, it works in every project: the server
//! finds the project the agent works in.

use std::cell::{Cell, RefCell};
use std::rc::Rc;

use adw::prelude::*;
use gtk::{gio, glib};

use super::common::{PaneView, code_block, group, report, status_dot};
use super::versions::button;
use super::{Data, Pane};
use crate::api::{Client, Error, Mcp, McpClient};
use crate::i18n::tr;

pub struct AiPane(Rc<Inner>);

struct Inner {
    view: PaneView,
    banner: adw::Banner,
    mcp: RefCell<Option<Mcp>>,
    /// The client being added or removed.
    working: RefCell<Option<String>>,
    loaded: Cell<bool>,
}

impl AiPane {
    pub fn new(banner: adw::Banner) -> Self {
        let view = PaneView::new(tr!("ai.subtitle"));
        Self(Rc::new(Inner { view, banner, mcp: RefCell::default(), working: RefCell::default(), loaded: Cell::new(false) }))
    }
}

impl Pane for AiPane {
    fn widget(&self) -> gtk::Widget {
        self.0.view.scrolled.clone().upcast()
    }

    fn update(&self, _: &Data) {
        if !self.0.loaded.replace(true) {
            self.0.run(None, |c| c.mcp());
        }
    }
}

impl Inner {
    fn run(self: &Rc<Self>, client: Option<String>, f: impl FnOnce(Client) -> Result<Mcp, Error> + Send + 'static) {
        self.working.replace(client);
        self.render();
        let this = self.clone();
        glib::spawn_future_local(async move {
            let result = gio::spawn_blocking(move || f(Client)).await.expect("mcp request");
            this.working.replace(None);
            match result {
                Ok(mcp) => {
                    this.mcp.replace(Some(mcp));
                    report(&this.banner, None);
                }
                Err(e) => report(&this.banner, Some(e.to_string())),
            }
            this.render();
        });
    }

    fn render(self: &Rc<Self>) {
        let mcp = self.mcp.borrow().clone();
        let clients = group(tr!("ai.clients"), tr!("ai.added_once"));
        match &mcp {
            Some(mcp) => {
                for client in mcp.clients.iter().flatten() {
                    clients.add(&self.client_row(client));
                }
            }
            None => clients.add(&adw::ActionRow::builder().title(tr!("common.loading")).build()),
        }
        let other = group(tr!("ai.other_clients"), tr!("ai.add_to_settings"));
        other.add(&code_block(mcp.as_ref().map_or("", |m| m.snippet.as_str())));
        self.view.set_groups(&[clients.upcast(), other.upcast()]);
    }

    fn client_row(self: &Rc<Self>, client: &McpClient) -> adw::ActionRow {
        let caption = match client.state.as_str() {
            "registered" => tr!("ai.flock_added"),
            "available" => tr!("common.installed"),
            _ => tr!("common.not_installed"),
        };
        let mut subtitle = caption.to_owned();
        if !client.problem.is_empty() {
            subtitle.push_str(&format!("\n{}", client.problem));
        }
        let row =
            adw::ActionRow::builder().title(glib::markup_escape_text(&client.name)).subtitle(glib::markup_escape_text(&subtitle)).build();
        if !client.problem.is_empty() {
            row.add_css_class("warning");
        }
        row.add_prefix(&status_dot(client.state == "registered"));
        let working = self.working.borrow().clone();
        if working.as_deref() == Some(client.id.as_str()) {
            row.add_suffix(&gtk::Spinner::builder().spinning(true).build());
        }
        let id = client.id.clone();
        let this = self.clone();
        let action = match client.state.as_str() {
            "registered" => Some(button(tr!("common.remove"), false, move || {
                let id2 = id.clone();
                this.run(Some(id.clone()), move |c| c.unregister_mcp(&id2));
            })),
            "available" => Some(button(tr!("common.add"), false, move || {
                let id2 = id.clone();
                this.run(Some(id.clone()), move |c| c.register_mcp(&id2));
            })),
            _ => None,
        };
        if let Some(action) = action {
            action.set_sensitive(working.is_none());
            row.add_suffix(&action);
        }
        row
    }
}
