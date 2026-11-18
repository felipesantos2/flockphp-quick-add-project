//! Widgets and formats the windows share: times, monospaced text and HTML views.

use std::path::Path;

use adw::prelude::*;
use gtk::{gio, glib};
use webkit6::prelude::*;

use crate::i18n::{self, tr, trn};

/// "just now", "5 minutes ago", "3 hours ago", else the date, from the daemon's RFC 3339 time.
pub fn relative_time(time: &str) -> String {
    let (Some(time), Ok(now)) = (glib::DateTime::from_iso8601(time, None).ok(), glib::DateTime::now_utc()) else {
        return String::new();
    };
    let seconds = now.difference(&time).as_seconds();
    match seconds {
        ..60 => tr!("common.time.just_now").to_owned(),
        60..3600 => trn!("common.n_minutes_ago", seconds / 60),
        3600..86400 => trn!("common.n_hours_ago", seconds / 3600),
        _ => local_time(&time, tr!("common.e_b")),
    }
}

/// A daemon time in the user's time zone, e.g. "24 Oct 2026, 14:03:12".
pub fn date_time(time: &str) -> String {
    glib::DateTime::from_iso8601(time, None).map(|t| local_time(&t, tr!("common.e_b_y_h_m"))).unwrap_or_default()
}

/// "14:03:12.481" for a timeline.
pub fn clock(time: &str) -> String {
    glib::DateTime::from_iso8601(time, None).map(|t| local_time(&t, "%H:%M:%S.%f").chars().take(12).collect()).unwrap_or_default()
}

/// The English formats' month names: GLib's %b follows the system's LC_TIME, which may not be the
/// app's language. The translations' formats use the month's number.
const MONTHS: [&str; 12] = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

fn local_time(time: &glib::DateTime, format: &str) -> String {
    let Ok(local) = time.to_local() else { return String::new() };
    let format = format.replace("%b", MONTHS[(local.month() - 1) as usize]);
    local.format(&format).map(|s| s.trim().to_owned()).unwrap_or_default()
}

/// Opens a URI (a page, a mail address) in the app the desktop picks for it.
pub fn open_uri(widget: Option<&gtk::Widget>, uri: &str) {
    if i18n::overrides_system() {
        launch(uri);
        return;
    }
    let window = widget.and_then(|w| w.root()).and_downcast::<gtk::Window>();
    gtk::UriLauncher::new(uri).launch(window.as_ref(), gio::Cancellable::NONE, |_| {});
}

/// Opens a file or folder in the app the desktop picks for it.
pub fn open_file(widget: Option<&gtk::Widget>, path: impl AsRef<Path>) {
    let file = gio::File::for_path(path);
    if i18n::overrides_system() {
        launch(&file.uri());
        return;
    }
    let window = widget.and_then(|w| w.root()).and_downcast::<gtk::Window>();
    gtk::FileLauncher::new(Some(&file)).launch(window.as_ref(), gio::Cancellable::NONE, |_| {});
}

/// With the Language setting, GTK's launchers would hand it on to the app (when it isn't started
/// through the desktop portal): this starts it with the system's language instead.
fn launch(uri: &str) {
    gio::AppInfo::launch_default_for_uri_async(uri, Some(&launch_context()), gio::Cancellable::NONE, |_| {});
}

/// The display's launch context: a started app gets a startup notification or activation token,
/// and the system's LANGUAGE.
pub fn launch_context() -> gio::AppLaunchContext {
    let context = gtk::gdk::Display::default().map_or_else(gio::AppLaunchContext::new, |d| d.app_launch_context().upcast());
    i18n::with_system_language(|value| match value {
        Some(value) => context.setenv("LANGUAGE", value),
        None => context.unsetenv("LANGUAGE"),
    });
    context
}

/// Scrollable, selectable monospaced text.
pub fn monospace(text: &str) -> gtk::ScrolledWindow {
    let view =
        gtk::TextView::builder().editable(false).monospace(true).top_margin(12).bottom_margin(12).left_margin(12).right_margin(12).build();
    view.buffer().set_text(text);
    gtk::ScrolledWindow::builder().child(&view).vexpand(true).hexpand(true).build()
}

/// Renders HTML. Mail runs with JavaScript off and remote loads blocked (no tracking pixels, no
/// surprises): a Content-Security-Policy allows only inline styles and data: images. Dumps need
/// JavaScript for Symfony's collapsible output.
pub fn html_view(html: &str, javascript: bool) -> webkit6::WebView {
    let view = webkit6::WebView::builder().vexpand(true).hexpand(true).build();
    if let Some(settings) = webkit6::prelude::WebViewExt::settings(&view) {
        settings.set_enable_javascript(javascript);
        settings.set_auto_load_images(true);
        settings.set_enable_developer_extras(false);
    }
    view.load_html(&with_policy(html, javascript), None);
    // Links open in the browser, not in the view.
    view.connect_decide_policy(|view, decision, kind| {
        if kind != webkit6::PolicyDecisionType::NavigationAction {
            return false;
        }
        let Some(action) = decision.downcast_ref::<webkit6::NavigationPolicyDecision>().and_then(|d| d.navigation_action()) else {
            return false;
        };
        let uri = action.clone().request().and_then(|r| r.uri()).unwrap_or_default();
        if uri.starts_with("http://") || uri.starts_with("https://") || uri.starts_with("mailto:") {
            decision.ignore();
            open_uri(Some(view.upcast_ref()), &uri);
            return true;
        }
        false
    });
    view
}

/// The page behind a Content-Security-Policy that blocks every remote load.
pub fn with_policy(html: &str, javascript: bool) -> String {
    let policy = if javascript {
        "default-src 'none'; img-src data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; font-src data:"
    } else {
        "default-src 'none'; img-src data:; style-src 'unsafe-inline'; font-src data:"
    };
    format!("<meta http-equiv=\"Content-Security-Policy\" content=\"{policy}\">{html}")
}

/// A page saying there is nothing to show (yet).
pub fn empty(icon: &str, title: &str, description: &str) -> adw::StatusPage {
    adw::StatusPage::builder().icon_name(icon).title(title).description(glib::markup_escape_text(description)).vexpand(true).build()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn names_months_itself_or_by_number() {
        let time = glib::DateTime::from_iso8601("2026-11-15T12:00:00Z", None).unwrap();
        assert!(local_time(&time, "%e %b").ends_with("Nov"), "{}", local_time(&time, "%e %b"));
        assert!(local_time(&time, "%e. %-m.").ends_with("11."));
        assert!(local_time(&time, "%e/%-m/%Y").ends_with("11/2026"));
    }
}
