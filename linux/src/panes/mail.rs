//! Mail: Flock's SMTP server and how a Laravel app sends to it.

use std::cell::RefCell;

use adw::prelude::*;

use super::common::{PaneView, code_block, group, info_row, open_row, status_dot};
use super::{Data, Pane};
use crate::i18n::tr;

pub struct MailPane {
    view: PaneView,
    shown: RefCell<Option<Data>>,
}

impl MailPane {
    pub fn new() -> Self {
        Self { view: PaneView::new(tr!("mail.flock_catches_every_email_your_apps")), shown: RefCell::default() }
    }
}

impl Pane for MailPane {
    fn widget(&self) -> gtk::Widget {
        self.view.scrolled.clone().upcast()
    }

    fn update(&self, data: &Data) {
        if self.shown.borrow().as_ref() == Some(data) {
            return;
        }
        self.shown.replace(Some(data.clone()));
        let mail = data.services.iter().find(|s| s.id == "mail");
        let running = mail.is_some_and(|m| m.running);
        let port = data.config.as_ref().map(|c| c.ports.smtp).filter(|p| *p != 0).or(mail.map(|m| m.port)).unwrap_or_default();

        let server = group(tr!("mail.smtp_server"), "");
        server.add(&info_row(tr!("mail.host"), "127.0.0.1"));
        server.add(&info_row(tr!("mail.port"), &port.to_string()));
        let state = adw::ActionRow::builder().title(tr!("general.status")).build();
        let value = gtk::Box::builder().spacing(6).build();
        value.append(&status_dot(running));
        value.append(&gtk::Label::new(Some(if running { tr!("sites.proxy.running") } else { tr!("services.postgres.stopped") })));
        state.add_suffix(&value);
        server.add(&state);

        let env = group(tr!("mail.environment_variables_title_case"), tr!("mail.username_inbox"));
        env.add(&code_block(mail.map_or("", |m| m.env.as_str())));

        let caught = group("", "");
        let count = data.status.as_ref().map_or(0, |s| s.mail_count);
        caught.add(&open_row(tr!("mail.caught"), &count.to_string(), tr!("mail.open"), "app.mail"));

        self.view.set_groups(&[server.upcast(), env.upcast(), caught.upcast()]);
    }
}
