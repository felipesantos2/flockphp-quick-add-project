//! The app's texts, by key: po/<code>.po (generated from lang/ by tools/i18n, English too), which
//! build.rs builds into the binary. The language is chosen once at start, from the Language
//! setting, else the system's (LANGUAGE, LC_ALL, LC_MESSAGES, LANG); English is the fallback for
//! anything else, and for a text a language lacks. A Language setting other than the system's also
//! sets LANGUAGE, so GTK, libadwaita and WebKit translate their own texts the same way, except in a
//! C locale, where gettext ignores LANGUAGE; the programs the app starts get the system's back
//! (with_system_language).
//!
//! tr! gives a key's text, with {name} placeholders filled from name = value pairs, which lets a
//! translation put them in another order; trn! picks a plural form for a count, {n} in the text.
//! tr_noop! only marks a key, for constants translated later with translate(). lang/README.md
//! describes the keys.

use std::collections::HashMap;
use std::fmt::Display;
use std::sync::{LazyLock, OnceLock};

/// Each language's po file, (code, contents), from build.rs.
static CATALOGS: &[(&str, &str)] = include!(concat!(env!("OUT_DIR"), "/catalogs.rs"));

/// The languages the app speaks, English first: code and name in that language (never translated).
pub static LANGUAGES: LazyLock<Vec<(&'static str, &'static str)>> = LazyLock::new(|| {
    let mut languages: Vec<_> = CATALOGS.iter().map(|(code, po)| (*code, header(po, "X-Language-Name").unwrap_or(code))).collect();
    languages.sort_by_key(|(code, _)| (*code != "en", *code));
    languages
});

/// The texts of the language in use; None until init().
static CATALOG: OnceLock<Option<Catalog>> = OnceLock::new();
/// The English texts, every other language's fallback.
static ENGLISH: LazyLock<Catalog> = LazyLock::new(|| catalog("en").expect("po/en.po"));
static LANGUAGE: OnceLock<&'static str> = OnceLock::new();
/// LANGUAGE as it was before init() set it for the Language setting, None when it was unset. Not
/// set while the app follows the system.
static SYSTEM_LANGUAGE: OnceLock<Option<String>> = OnceLock::new();

struct Catalog {
    /// Each key's text, or a plural's forms in the language's order.
    messages: HashMap<String, Vec<String>>,
    /// gettext's Plural-Forms expression, which picks a count's form.
    plural: &'static str,
}

/// Chooses the language for this run: a code of LANGUAGES, or "system" (also "") for the system's.
/// Without it (the screenshots, tests) the app speaks English. Call it before GTK starts and while
/// the process has no other thread: it may set LANGUAGE.
pub fn init(preference: &str) {
    let language = resolve(preference, |name| std::env::var(name).ok());
    if LANGUAGES.iter().any(|(code, _)| *code == preference) {
        let _ = SYSTEM_LANGUAGE.set(std::env::var("LANGUAGE").ok());
        // SAFETY: before any thread is started (see above).
        unsafe { std::env::set_var("LANGUAGE", language) };
    }
    let _ = LANGUAGE.set(language);
    let _ = CATALOG.set(catalog(language));
}

/// The code of the language in use.
pub fn language() -> &'static str {
    LANGUAGE.get().copied().unwrap_or("en")
}

/// Hands the system's LANGUAGE to a program the app starts, through set (Some) or unset (None),
/// when the Language setting replaced it: a restarted app would take it for the system's.
pub fn with_system_language(set: impl FnOnce(Option<&str>)) {
    if let Some(system) = SYSTEM_LANGUAGE.get() {
        set(system.as_deref());
    }
}

/// The language a preference stands for at the next start. Reads the system's LANGUAGE, not the
/// one init() set for the Language setting.
pub fn resolve_next(preference: &str) -> &'static str {
    resolve(preference, |name| system_env(name, SYSTEM_LANGUAGE.get(), |name| std::env::var(name).ok()))
}

/// env, but LANGUAGE as it was before init() replaced it (system), when it did.
fn system_env(name: &str, system: Option<&Option<String>>, env: impl Fn(&str) -> Option<String>) -> Option<String> {
    match system {
        Some(language) if name == "LANGUAGE" => language.clone(),
        _ => env(name),
    }
}

/// The Language setting replaced the system's LANGUAGE.
pub fn overrides_system() -> bool {
    SYSTEM_LANGUAGE.get().is_some()
}

/// with_system_language for a std::process::Command.
pub fn system_language(command: &mut std::process::Command) -> &mut std::process::Command {
    with_system_language(|value| {
        match value {
            Some(value) => command.env("LANGUAGE", value),
            None => command.env_remove("LANGUAGE"),
        };
    });
    command
}

/// The language a preference stands for, with env reading the environment.
pub fn resolve(preference: &str, env: impl Fn(&str) -> Option<String>) -> &'static str {
    if let Some((code, _)) = LANGUAGES.iter().find(|(code, _)| *code == preference) {
        return code;
    }
    // gettext's order: LANGUAGE is a list, but only counts when a locale is set at all.
    let locale = ["LC_ALL", "LC_MESSAGES", "LANG"].into_iter().filter_map(&env).find(|v| !v.is_empty()).unwrap_or_default();
    let primary = |tag: &str| tag.split(['_', '-', '.', '@']).next().unwrap_or_default().to_ascii_lowercase();
    if locale.is_empty() || primary(&locale) == "c" || primary(&locale) == "posix" {
        return "en";
    }
    // A LANGUAGE list stands in for the locale's language, it doesn't come before it.
    let list = env("LANGUAGE").filter(|list| !list.is_empty()).unwrap_or(locale);
    list.split(':').find_map(|tag| LANGUAGES.iter().find(|(code, _)| *code == primary(tag)).map(|(code, _)| *code)).unwrap_or("en")
}

fn catalog(language: &str) -> Option<Catalog> {
    let (_, po) = CATALOGS.iter().find(|(code, _)| *code == language)?;
    let forms = header(po, "Plural-Forms").unwrap_or_default();
    let plural = forms.split_once("plural=").map_or("0", |(_, expression)| expression.trim().trim_end_matches(';'));
    Some(Catalog { messages: parse(po), plural })
}

/// A field of a po file's header ("Language: cs").
fn header<'a>(po: &'a str, field: &str) -> Option<&'a str> {
    po.lines().find_map(|line| line.trim().strip_prefix('"')?.strip_prefix(field)?.strip_prefix(": ")?.strip_suffix("\\n\""))
}

fn current() -> Option<&'static Catalog> {
    CATALOG.get().and_then(Option::as_ref)
}

fn english() -> &'static Catalog {
    &ENGLISH
}

/// A key's text: the language's, else the English, else the key.
pub fn translate(key: &'static str) -> &'static str {
    [current(), Some(english())]
        .into_iter()
        .flatten()
        .find_map(|catalog| catalog.messages.get(key))
        .and_then(|forms| forms.first())
        .map_or(key, String::as_str)
}

/// A plural key's form for count n: the language's, else the English.
pub fn translate_plural(n: u64, key: &'static str) -> &'static str {
    let form = |catalog: &'static Catalog| catalog.messages.get(key).and_then(|forms| forms.get(plural_form(catalog.plural, n)));
    current().and_then(form).or_else(|| form(english())).map_or(key, String::as_str)
}

/// The form for count n by a gettext plural expression ("(n != 1)"); the first for one it can't read.
fn plural_form(expression: &str, n: u64) -> usize {
    let mut parser = Expression { text: expression.as_bytes(), at: 0, n };
    parser.ternary().map_or(0, |form| usize::try_from(form).unwrap_or(0))
}

/// gettext's plural expressions: C's ?:, ||, &&, comparisons and arithmetic over the count n.
struct Expression<'a> {
    text: &'a [u8],
    at: usize,
    n: u64,
}

impl Expression<'_> {
    fn skip_space(&mut self) {
        while self.text.get(self.at).is_some_and(u8::is_ascii_whitespace) {
            self.at += 1;
        }
    }

    /// Takes token when it comes next.
    fn eat(&mut self, token: &str) -> bool {
        self.skip_space();
        let found = self.text[self.at..].starts_with(token.as_bytes());
        if found {
            self.at += token.len();
        }
        found
    }

    fn ternary(&mut self) -> Option<u64> {
        let condition = self.or()?;
        if !self.eat("?") {
            return Some(condition);
        }
        let yes = self.ternary()?;
        if !self.eat(":") {
            return None;
        }
        let no = self.ternary()?;
        Some(if condition != 0 { yes } else { no })
    }

    fn or(&mut self) -> Option<u64> {
        let mut value = self.and()?;
        while self.eat("||") {
            let right = self.and()?;
            value = u64::from(value != 0 || right != 0);
        }
        Some(value)
    }

    fn and(&mut self) -> Option<u64> {
        let mut value = self.comparison()?;
        while self.eat("&&") {
            let right = self.comparison()?;
            value = u64::from(value != 0 && right != 0);
        }
        Some(value)
    }

    fn comparison(&mut self) -> Option<u64> {
        let mut value = self.sum()?;
        loop {
            // The two-character operators first: "<=" before "<".
            let compare: fn(u64, u64) -> bool = if self.eat("==") {
                |a, b| a == b
            } else if self.eat("!=") {
                |a, b| a != b
            } else if self.eat("<=") {
                |a, b| a <= b
            } else if self.eat(">=") {
                |a, b| a >= b
            } else if self.eat("<") {
                |a, b| a < b
            } else if self.eat(">") {
                |a, b| a > b
            } else {
                return Some(value);
            };
            let right = self.sum()?;
            value = u64::from(compare(value, right));
        }
    }

    fn sum(&mut self) -> Option<u64> {
        let mut value = self.product()?;
        loop {
            if self.eat("+") {
                value = value.wrapping_add(self.product()?);
            } else if self.eat("-") {
                value = value.wrapping_sub(self.product()?);
            } else {
                return Some(value);
            }
        }
    }

    fn product(&mut self) -> Option<u64> {
        let mut value = self.unary()?;
        loop {
            if self.eat("*") {
                value = value.wrapping_mul(self.unary()?);
            } else if self.eat("/") {
                value = value.checked_div(self.unary()?)?;
            } else if self.eat("%") {
                value = value.checked_rem(self.unary()?)?;
            } else {
                return Some(value);
            }
        }
    }

    fn unary(&mut self) -> Option<u64> {
        if self.eat("!") {
            return Some(u64::from(self.unary()? == 0));
        }
        if self.eat("(") {
            let value = self.ternary()?;
            return self.eat(")").then_some(value);
        }
        if self.eat("n") {
            return Some(self.n);
        }
        self.skip_space();
        let digits = self.text[self.at..].iter().take_while(|c| c.is_ascii_digit()).count();
        let number = std::str::from_utf8(&self.text[self.at..self.at + digits]).ok()?.parse().ok()?;
        self.at += digits;
        Some(number)
    }
}

/// A count for trn!, of any integer type.
pub fn count(n: impl TryInto<u64>) -> u64 {
    n.try_into().unwrap_or(u64::MAX)
}

/// Fills a text's {name} placeholders; other braces stay as they are.
pub fn fill(text: &str, args: &[(&str, &dyn Display)]) -> String {
    let mut out = String::with_capacity(text.len());
    let mut rest = text;
    while let Some(start) = rest.find('{') {
        out.push_str(&rest[..start]);
        let after = &rest[start + 1..];
        match after.find('}').and_then(|end| args.iter().find(|(name, _)| *name == &after[..end]).map(|(_, value)| (end, value))) {
            Some((end, value)) => {
                out.push_str(&value.to_string());
                rest = &after[end + 1..];
            }
            None => {
                out.push('{');
                rest = after;
            }
        }
    }
    out.push_str(rest);
    out
}

/// A key's text; with name = value pairs, fills its {name} placeholders into a String.
macro_rules! tr {
    ($key:literal) => {
        $crate::i18n::translate($key)
    };
    ($key:literal, $($name:ident = $value:expr),+ $(,)?) => {
        $crate::i18n::fill($crate::i18n::translate($key), &[$((stringify!($name), &$value as &dyn std::fmt::Display)),+])
    };
}

/// A plural key's form for a count, with {n} and any other placeholders filled.
macro_rules! trn {
    ($key:literal, $n:expr $(, $name:ident = $value:expr)* $(,)?) => {{
        let n = $crate::i18n::count($n);
        $crate::i18n::fill(
            $crate::i18n::translate_plural(n, $key),
            &[("n", &n as &dyn std::fmt::Display) $(, (stringify!($name), &$value as &dyn std::fmt::Display))*],
        )
    }};
}

/// Marks a key where it can't be translated yet (a constant); translate() translates it where it
/// shows.
macro_rules! tr_noop {
    ($key:literal) => {
        $key
    };
}

pub(crate) use {tr, tr_noop, trn};

/// A .po catalog's entries, each msgid with its msgstr or msgstr[i] forms. Untranslated and fuzzy
/// entries and the header (msgid "") are left out, as gettext does.
fn parse(po: &str) -> HashMap<String, Vec<String>> {
    let mut messages = HashMap::new();
    let (mut id, mut forms): (Option<String>, Vec<String>) = (None, Vec::new());
    // The string a continuation line ("...") adds to: 0 the msgid, 1 msgid_plural, 2 a msgstr, 4
    // none (between entries).
    let mut field = 4;
    // The entry has the fuzzy flag (#, fuzzy), which comes before it.
    let mut fuzzy = false;
    let mut finish = |id: &mut Option<String>, forms: &mut Vec<String>, fuzzy: &mut bool| {
        let translated = !forms.is_empty() && forms.iter().all(|f| !f.is_empty()) && !*fuzzy;
        if let Some(id) = id.take().filter(|id| !id.is_empty() && translated) {
            messages.insert(id, std::mem::take(forms));
        }
        forms.clear();
        *fuzzy = false;
    };
    for line in po.lines().map(str::trim) {
        if line.is_empty() {
            continue;
        }
        // A comment or a msgid after a msgstr starts the next entry.
        let next = line.starts_with('#') || line.starts_with("msgid ");
        if next && field == 2 {
            finish(&mut id, &mut forms, &mut fuzzy);
            field = 4;
        }
        if let Some(flags) = line.strip_prefix("#,") {
            fuzzy |= flags.split(',').any(|f| f.trim() == "fuzzy");
        } else if line.starts_with('#') {
            continue;
        } else if let Some(rest) = line.strip_prefix("msgid ") {
            id = Some(unquote(rest));
            field = 0;
        } else if line.starts_with("msgid_plural ") {
            field = 1;
        } else if let Some(rest) = line.strip_prefix("msgstr") {
            let value = rest.split_once(' ').map(|(_, v)| v).unwrap_or_default();
            forms.push(unquote(value));
            field = 2;
        } else if line.starts_with('"') {
            match field {
                0 => id.get_or_insert_default().push_str(&unquote(line)),
                2 => forms.last_mut().expect("msgstr").push_str(&unquote(line)),
                _ => {}
            }
        }
    }
    finish(&mut id, &mut forms, &mut fuzzy);
    messages
}

/// A .po string's text, without its quotes and escapes.
fn unquote(quoted: &str) -> String {
    let inner = quoted.trim().strip_prefix('"').and_then(|s| s.strip_suffix('"')).unwrap_or_default();
    let mut out = String::with_capacity(inner.len());
    let mut chars = inner.chars();
    while let Some(c) = chars.next() {
        if c != '\\' {
            out.push(c);
            continue;
        }
        match chars.next() {
            Some('n') => out.push('\n'),
            Some('t') => out.push('\t'),
            Some(other) => out.push(other),
            None => {}
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use std::path::Path;

    use super::*;

    /// The keys the sources mark with tr!, tr_noop! and trn!, the plural ones (trn!) true.
    fn marked() -> Vec<(String, bool)> {
        fn walk(dir: &Path, files: &mut Vec<std::path::PathBuf>) {
            for entry in std::fs::read_dir(dir).expect("src").flatten() {
                let path = entry.path();
                if path.is_dir() {
                    walk(&path, files);
                } else if path.extension().is_some_and(|e| e == "rs") && !path.ends_with("i18n.rs") {
                    files.push(path);
                }
            }
        }
        let mut files = Vec::new();
        walk(&Path::new(env!("CARGO_MANIFEST_DIR")).join("src"), &mut files);
        let mut found = Vec::new();
        for file in files {
            let source = std::fs::read_to_string(&file).expect("source");
            let marks = ["tr!(", "tr_noop!(", "trn!("].map(|mark| source.match_indices(mark));
            for (i, _) in marks.into_iter().flatten() {
                // Not the end of another name, e.g. "str!(".
                if source[..i].ends_with(|c: char| c.is_alphanumeric() || c == '_') {
                    continue;
                }
                let rest = &source[i..];
                let rest = &rest[rest.find('(').expect("(") + 1..];
                let (key, _) = literal(rest).unwrap_or_else(|| panic!("{}: tr! without a literal", file.display()));
                found.push((key, source[i..].starts_with("trn!(")));
            }
        }
        found
    }

    /// The Rust string literal at the start of text (after whitespace), unescaped, and the rest.
    fn literal(text: &str) -> Option<(String, &str)> {
        let text = text.trim_start().strip_prefix('"')?;
        let mut out = String::new();
        let mut chars = text.char_indices();
        while let Some((i, c)) = chars.next() {
            match c {
                '"' => return Some((out, &text[i + 1..])),
                '\\' => match chars.next()?.1 {
                    'n' => out.push('\n'),
                    't' => out.push('\t'),
                    other => out.push(other),
                },
                c => out.push(c),
            }
        }
        None
    }

    /// The number of plural forms of a po file's Plural-Forms.
    fn plural_forms(po: &str) -> usize {
        let forms = header(po, "Plural-Forms").expect("Plural-Forms");
        let count = forms.split_once("nplurals=").and_then(|(_, rest)| rest.split(';').next()).expect("nplurals");
        count.trim().parse().expect("a number of forms")
    }

    #[test]
    fn every_key_has_its_english_text() {
        let marked = marked();
        assert!(marked.len() > 100, "found only {} keys", marked.len());
        let english = parse(include_str!("../po/en.po"));
        for (key, plural) in &marked {
            let forms = english.get(key).unwrap_or_else(|| panic!("po/en.po lacks {key:?}: run make i18n"));
            assert_eq!(forms.len(), if *plural { 2 } else { 1 }, "{key:?} is used with trn! only when it has plural forms");
        }
    }

    #[test]
    fn every_language_has_every_key() {
        let english = parse(include_str!("../po/en.po"));
        for (code, po) in CATALOGS {
            let messages = parse(po);
            let forms = plural_forms(po);
            for (key, english_forms) in &english {
                let found = messages.get(key).unwrap_or_else(|| panic!("po/{code}.po lacks {key:?}"));
                let wanted = if english_forms.len() > 1 { forms } else { 1 };
                assert_eq!(found.len(), wanted, "po/{code}.po: the forms of {key:?}");
            }
            let catalog = catalog(code).expect("catalog");
            assert!((0..1000).all(|n| plural_form(catalog.plural, n) < forms), "po/{code}.po: a form beyond nplurals");
        }
    }

    #[test]
    fn lists_the_languages() {
        assert_eq!(LANGUAGES[0], ("en", "English"));
        assert!(LANGUAGES.contains(&("cs", "Čeština")));
    }

    #[test]
    fn parses_po_entries() {
        let po = "msgid \"\"\nmsgstr \"\"\n\"Language: cs\\n\"\n\n#. a note\nmsgid \"sites.open\"\nmsgstr \"\"\n\"Otevřít „{name}“\"\n\n\
                  msgid \"sites.count\"\nmsgid_plural \"sites.count\"\nmsgstr[0] \"{n} web\"\nmsgstr[1] \"{n} weby\"\nmsgstr[2] \"{n} webů\"\n\n\
                  #, fuzzy, c-format\nmsgid \"sites.fuzzy\"\nmsgstr \"Nejisté\"\n\n\
                  msgid \"sites.untranslated\"\nmsgstr \"\"\n";
        let messages = parse(po);
        assert_eq!(messages.len(), 2);
        assert_eq!(messages["sites.open"], ["Otevřít „{name}“"]);
        assert_eq!(messages["sites.count"], ["{n} web", "{n} weby", "{n} webů"]);
        assert_eq!(header(po, "Language"), Some("cs"));
    }

    #[test]
    fn plural_rules() {
        let forms = |expression: &str, counts: &[u64]| counts.iter().map(|&n| plural_form(expression, n)).collect::<Vec<_>>();
        let czech = "(n==1) ? 0 : (n>=2 && n<=4) ? 1 : 2";
        assert_eq!(forms(czech, &[0, 1, 2, 4, 5, 11, 22, 101]), [2, 0, 1, 1, 2, 2, 2, 2]);
        assert_eq!(forms("(n != 1)", &[0, 1, 2, 21]), [1, 0, 1, 1]);
        assert_eq!(forms("(n > 1)", &[0, 1, 2]), [0, 0, 1]);
        let russian = "(n%10==1 && n%100!=11) ? 0 : (n%10>=2 && n%10<=4 && (n%100<10 || n%100>=20)) ? 1 : 2";
        assert_eq!(forms(russian, &[1, 2, 5, 11, 12, 21, 22, 25, 111, 112]), [0, 1, 2, 2, 2, 0, 1, 2, 2, 2]);
        assert_eq!(forms("0", &[1, 5]), [0, 0]);
        // One it can't read gives the first form.
        assert_eq!(forms("(n ==", &[1]), [0]);
    }

    #[test]
    fn english_without_a_language() {
        assert_eq!(translate("app.not_running"), "Flock is not running");
        assert_eq!(translate("app.no_such_key"), "app.no_such_key");
        assert_eq!(trn!("app.n_events", 1), "1 event");
        assert_eq!(trn!("app.n_events", 3usize), "3 events");
        assert_eq!(tr!("common.list.and", first = "a", second = String::from("b")), "a and b");
    }

    #[test]
    fn fills_placeholders() {
        assert_eq!(fill("{b} before {a}", &[("a", &1), ("b", &"two")]), "two before 1");
        assert_eq!(fill("{unknown} and {", &[("a", &1)]), "{unknown} and {");
    }

    #[test]
    fn resolves_the_language() {
        let env = |vars: &'static [(&'static str, &'static str)]| {
            move |name: &str| vars.iter().find(|(n, _)| *n == name).map(|(_, v)| v.to_string())
        };
        assert_eq!(resolve("cs", env(&[("LANG", "es_ES.UTF-8")])), "cs");
        assert_eq!(resolve("system", env(&[("LANG", "cs_CZ.UTF-8")])), "cs");
        assert_eq!(resolve("", env(&[("LANG", "es_MX.UTF-8")])), "es");
        assert_eq!(resolve("system", env(&[("LANG", "de_DE.UTF-8")])), "en");
        assert_eq!(resolve("system", env(&[("LANGUAGE", "de:es:cs"), ("LANG", "de_DE.UTF-8")])), "es");
        assert_eq!(resolve("system", env(&[("LANGUAGE", "de"), ("LANG", "cs_CZ.UTF-8")])), "en");
        assert_eq!(resolve("system", env(&[("LANGUAGE", ""), ("LANG", "cs_CZ.UTF-8")])), "cs");
        assert_eq!(resolve("system", env(&[("LC_ALL", "cs_CZ.UTF-8"), ("LANG", "es_ES.UTF-8")])), "cs");
        assert_eq!(resolve("system", env(&[("LC_MESSAGES", "es_ES.UTF-8"), ("LANG", "cs_CZ.UTF-8")])), "es");
        // A C locale ignores LANGUAGE, as gettext does.
        assert_eq!(resolve("system", env(&[("LANGUAGE", "cs"), ("LANG", "C.UTF-8")])), "en");
        assert_eq!(resolve("system", env(&[])), "en");
    }

    #[test]
    fn resolves_the_system_language_under_an_override() {
        // init() set LANGUAGE=cs for the Language setting.
        let env = |name: &str| match name {
            "LANGUAGE" => Some("cs".to_owned()),
            "LANG" => Some("en_US.UTF-8".to_owned()),
            _ => None,
        };
        assert_eq!(resolve("system", |name| system_env(name, Some(&None), env)), "en");
        assert_eq!(resolve("system", |name| system_env(name, Some(&Some("es".to_owned())), env)), "es");
        // Following the system, LANGUAGE is the system's own.
        assert_eq!(resolve("system", |name| system_env(name, None, env)), "cs");
    }

    #[test]
    fn finds_marked_literals() {
        assert_eq!(literal(" \"sites.count\", n) rest"), Some(("sites.count".to_owned(), ", n) rest")));
    }
}
