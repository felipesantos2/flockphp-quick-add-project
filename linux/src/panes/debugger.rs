//! Debugger: Xdebug detection, PhpStorm breakpoints and connecting an IDE.

use std::cell::RefCell;
use std::rc::Rc;

use adw::prelude::*;
use gtk::glib;

use super::common::{PaneView, group, info_row, report, switch_row};
use super::{Data, Pane};
use crate::api::SettingsChange;
use crate::i18n::tr;
use crate::model::AppModel;

pub struct DebuggerPane {
    view: PaneView,
    model: Rc<AppModel>,
    banner: adw::Banner,
    shown: RefCell<Option<Data>>,
}

impl DebuggerPane {
    pub fn new(model: Rc<AppModel>, banner: adw::Banner) -> Self {
        Self { view: PaneView::new(tr!("debugger.subtitle")), model, banner, shown: RefCell::default() }
    }

    fn toggle(&self, title: &str, active: bool, change: fn(bool) -> SettingsChange) -> adw::SwitchRow {
        let (model, banner) = (self.model.clone(), self.banner.clone());
        switch_row(title, "", active, move |on| {
            let banner = banner.clone();
            model.change_settings(change(on), move |e| report(&banner, e));
        })
    }
}

impl Pane for DebuggerPane {
    fn widget(&self) -> gtk::Widget {
        self.view.scrolled.clone().upcast()
    }

    fn update(&self, data: &Data) {
        let Some(status) = &data.status else { return };
        if self.shown.borrow().as_ref() == Some(data) {
            return;
        }
        self.shown.replace(Some(data.clone()));

        let detect = group("", tr!("debugger.xdebug_loads_only_for_requests"));
        detect.add(
            &self.toggle(tr!("debugger.detect_requests"), status.xdebug, |on| SettingsChange { xdebug: Some(on), ..Default::default() }),
        );
        let breakpoints = self.toggle(tr!("debugger.detect_breakpoints"), status.xdebug_breakpoints, |on| SettingsChange {
            xdebug_breakpoints: Some(on),
            ..Default::default()
        });
        breakpoints.set_sensitive(status.xdebug);
        detect.add(&breakpoints);
        let mut groups = vec![detect.upcast()];

        let debugging: Vec<_> = status.sites.iter().filter(|s| s.debugging).collect();
        if status.xdebug && status.xdebug_breakpoints && !debugging.is_empty() {
            let now = group(tr!("debugger.debugging_now"), "");
            for site in debugging {
                let row = adw::ActionRow::builder().title(glib::markup_escape_text(&site.domain)).css_classes(["warning"]).build();
                row.add_prefix(&gtk::Image::from_icon_name("applications-engineering-symbolic"));
                now.add(&row);
            }
            groups.push(now.upcast());
        }

        let ide = group(tr!("debugger.connect_ide"), "");
        ide.add(&info_row(tr!("debugger.client_port"), "9003"));
        ide.add(&info_row(tr!("debugger.mode"), "debug, develop"));
        let start = adw::ActionRow::builder()
            .title(tr!("debugger.start_session"))
            .subtitle(tr!("debugger.start_session_description"))
            .subtitle_selectable(true)
            .build();
        ide.add(&start);
        groups.push(ide.upcast());

        let availability = group(tr!("debugger.availability"), "");
        for php in data.config.as_ref().and_then(|c| c.php.clone()).unwrap_or_default() {
            let row = info_row(
                &format!("PHP {}", php.version),
                if php.xdebug_available { tr!("debugger.available") } else { tr!("debugger.not_in_build") },
            );
            availability.add(&row);
        }
        groups.push(availability.upcast());
        self.view.set_groups(&groups);
    }
}
