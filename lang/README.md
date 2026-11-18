# Translations

Flock's macOS, Windows and Linux apps share their texts. Each language has a folder here:
`en/app.json` holds every English text, and `cs/app.json`, `es/app.json`, … translate them.
Nothing else is needed to translate Flock.

`make i18n-status` shows how much of each language is translated.

## Translating

1. Fork the repository and edit `lang/<code>/app.json`. For a new language, start a new folder
   with an `app.json` that holds only `{}` (`lang/de/app.json` for German) and add its texts.
2. Run `make i18n` (or `go run ./tools/i18n`). It checks your file, puts it in order and writes the
   apps' own files from it.
3. Open a pull request. A pull request that changes only translations needs no issue first.

`make i18n-missing lang=de` lists the texts a language lacks, with their English and notes.

A language doesn't have to be complete: a text it lacks shows in English, and the apps offer a
language once 70% of it is translated. Until then it stays here for others to continue.

A new language needs its plural rules in [`tools/i18n/languages.go`](../tools/i18n/languages.go).
Most common languages are there already; if yours isn't, say so in the pull request and a
maintainer adds it.

### The format

Every text has a key and its translation:

```json
{
  "php.update_to": "Aktualizovat na {version}",
  "sites.count": {"one": "{n} web", "few": "{n} weby", "other": "{n} webů"},
  "settings.title": {"macos": "Ajustes", "default": "Configuración"}
}
```

- **Placeholders** such as `{version}` stay as they are, untranslated. Their order can change.
- **Plurals** have one form per plural category of the language: English and Spanish `one` and
  `other`, Czech `one` (1), `few` (2–4) and `other`. `make i18n` says which forms are missing. A
  singular may leave the count out (`"Show its worktree"`).
- **A different translation per app**: `macos`, `windows` or `linux`, and `default` for the others,
  where an app's own words differ (macOS calls Settings "Ajustes" in Spanish).

`en/app.json` has the same keys, with a `note` where a text needs explaining, and
`"translate": false` for texts that stay the same in every language (product names):

```json
{
  "php.update_to": {"text": "Update to {version}", "note": "{version}: a PHP version"},
  "sites.count": {"one": "{n} site", "other": "{n} sites"},
  "common.php": {"text": "PHP", "translate": false}
}
```

## Adding or changing a text in an app

A new text needs only its English: add it to `en/app.json` under a new key, use the key in the
app, and run `make i18n`. Translations are welcome but not required; until they come, the apps show
the English.

### Keys

A key is where the text shows and what it is, in lowercase snake_case words separated by dots:
`sites.share.copy_link`. The first part is one of these areas:

`about`, `ai`, `app`, `common`, `debugger`, `dumps`, `general`, `installer`, `links`, `logs`,
`mail`, `menu`, `node`, `php`, `services`, `settings`, `setup`, `sites`, `updates`, `versions`,
`worktrees`

Further parts are optional (`sites.share.…`, `services.postgres.…`). `common` is for texts used in
several places, such as `common.cancel`; `installer` is for the Windows installer's.

### The rules

- **Changing the wording keeps the key.** A typo, punctuation or a better phrasing of the same
  thing: edit the English, and the translations stay.
- **Changing the meaning needs a new key.** Delete the old key from every `app.json`. The new key
  shows up untranslated, so translators see it; an old key with new English would keep its
  translations of the old meaning. CI lists the texts whose English a pull request changes, as a
  reminder.
- **Never reuse a deleted key** for a different text.
- **Texts that are the same in English but mean different things get their own keys**, as other
  languages translate them differently: `common.view` (to view something) and
  `dumps.timeline.view` (a Laravel view).
- **A key no app uses is deleted**, from every `app.json`. `make i18n` fails for one.

### In the code

| App | A text | With a placeholder | A count |
|---|---|---|---|
| macOS | `Text("sites.title")` | `Text("php.update_to \(version)")` | `Text("sites.count \(count)")` |
| Windows | `Strings.Sites_Title` | `Localization.Format(Strings.Php_UpdateTo, version)` | `Localization.Plural(Strings.ResourceManager, "Sites_Count", count)` |
| Linux | `tr!("sites.title")` | `tr!("php.update_to", version = v)` | `trn!("sites.count", count)` |

- **macOS** passes the placeholders in the order the English has them, as Strings (a count as an
  Int). XAML on Windows uses `{l:Loc Key=Sites_Title}`.
- **Windows** passes them to `String.Format` in the order the English has them, a count first.

`make i18n` finds the keys each app uses in its code and writes the apps' files from them. Don't
edit these; CI fails when they don't match `lang/`:

- macOS: `macos/Resources/Localizable.xcstrings`, `macos/Sources/FlockUI/Languages.swift`
- Windows: `windows/Flock.Kit/Strings*.resx`, `windows/Flock.Kit/Languages.g.cs`,
  `windows/installer/Strings.iss`
- Linux: `linux/po/*.po`
