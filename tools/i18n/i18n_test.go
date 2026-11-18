package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A small repository: lang/ and the apps' code that uses its keys.
func repository(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		path = filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func read(t *testing.T, root, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

const (
	sampleEN = `{
  "php.update_to": {"text": "Update to {version}", "note": "{version}: a PHP version"},
  "sites.count": {"one": "{n} site", "other": "{n} sites"},
  "settings.title": "Settings",
  "installer.not_installed": "The update was not installed: {reason}",
  "common.php": {"text": "PHP", "translate": false}
}`
	sampleCS = `{
  "php.update_to": "Aktualizovat na {version}",
  "sites.count": {"one": "{n} web", "few": "{n} weby", "other": "{n} webů"},
  "settings.title": "Nastavení",
  "installer.not_installed": "Aktualizace nebyla nainstalována: {reason}"
}`
	sampleES = `{
  "php.update_to": "Actualizar a {version}",
  "sites.count": {"one": "{n} sitio", "other": "{n} sitios"},
  "settings.title": {"macos": "Ajustes", "default": "Configuración"},
  "installer.not_installed": "La actualización no se instaló: {reason}"
}`
	sampleSwift = `struct V: View {
    var body: some View {
        Text("php.update_to \(version)")
        Text("sites.count \(count)")
        Button("settings.title") {}
        Text("common.php")
    }
    @AppStorage("services.filter") var filter = ""
}`
	sampleRust = `fn x() { tr!("php.update_to", version = v); trn!("sites.count", n); tr!("settings.title"); }`
	sampleCSharp = `var a = Strings.Php_UpdateTo; var b = Localization.Plural(Strings.ResourceManager, "Sites_Count", n); var c = Strings.Settings_Title;`
	sampleInno   = `Result := CustomMessage('Installer_NotInstalled');`
)

func sample(overrides map[string]string) map[string]string {
	files := map[string]string{
		"lang/en/app.json":                    sampleEN,
		"lang/cs/app.json":                    sampleCS,
		"lang/es/app.json":                    sampleES,
		"macos/Sources/FlockUI/View.swift":    sampleSwift,
		"linux/src/main.rs":                   sampleRust,
		"windows/Flock.Tray/Pane.cs":          sampleCSharp,
		"windows/installer/Flock.iss":         sampleInno,
		"windows/Flock.Kit.Tests/SomeTest.cs": `Strings.Not_A_Key`,
	}
	for path, content := range overrides {
		if content == "" {
			delete(files, path)
		} else {
			files[path] = content
		}
	}
	return files
}

func TestGeneratesEveryAppsFiles(t *testing.T) {
	root := repository(t, sample(nil))
	if err := run(root, false); err != nil {
		t.Fatal(err)
	}
	if err := run(root, true); err != nil {
		t.Fatalf("a second run should find everything up to date: %v", err)
	}

	xcstrings := read(t, root, "macos/Resources/Localizable.xcstrings")
	for _, want := range []string{
		`"php.update_to %@" : {`, `"value" : "Aktualizovat na %@"`, `"value" : "Update to %@"`, `"comment" : "{version}: a PHP version"`,
		`"sites.count %lld" : {`, `"value" : "%lld webů"`, `"many" : {`, `"value" : "%lld site"`,
		`"settings.title" : {`, `"value" : "Ajustes"`,
		`"common.php" : {`, `"value" : "PHP"`,
	} {
		if !strings.Contains(xcstrings, want) {
			t.Errorf("Localizable.xcstrings lacks %s:\n%s", want, xcstrings)
		}
	}
	if strings.Contains(xcstrings, "Configuración") || strings.Contains(xcstrings, "not installed") {
		t.Error("Localizable.xcstrings has another app's text")
	}
	if languages := read(t, root, "macos/Sources/FlockUI/Languages.swift"); !strings.Contains(languages, `("cs", "Čeština"),`) {
		t.Errorf("Languages.swift:\n%s", languages)
	}

	czech := read(t, root, "windows/Flock.Kit/Strings.cs.resx")
	for _, want := range []string{`<data name="Sites_Count_one"`, `<value>{0} web</value>`, `<data name="Sites_Count_few"`, `<value>Aktualizovat na {0}</value>`} {
		if !strings.Contains(czech, want) {
			t.Errorf("Strings.cs.resx lacks %s", want)
		}
	}
	english := read(t, root, "windows/Flock.Kit/Strings.resx")
	if !strings.Contains(english, "<value>Update to {0}</value>") || !strings.Contains(english, "<comment>{0}: a PHP version</comment>") || strings.Contains(english, "_few") {
		t.Errorf("Strings.resx:\n%s", english)
	}
	if es := read(t, root, "windows/Flock.Kit/Strings.es.resx"); !strings.Contains(es, "<value>Configuración</value>") {
		t.Errorf("Strings.es.resx:\n%s", es)
	}
	if cs := read(t, root, "windows/Flock.Kit/Languages.g.cs"); !strings.Contains(cs, `"cs" => new[] { "one", "few", "other" }[Index((n==1) ? 0 : (n>=2 && n<=4) ? 1 : 2)],`) {
		t.Errorf("Languages.g.cs:\n%s", cs)
	}
	iss := read(t, root, "windows/installer/Strings.iss")
	for _, want := range []string{`Name: "cs"; MessagesFile: "compiler:Languages\Czech.isl"`, "cs.Installer_NotInstalled=Aktualizace nebyla nainstalována: %1\n"} {
		if !strings.Contains(iss, want) {
			t.Errorf("Strings.iss lacks %s:\n%s", want, iss)
		}
	}

	po := read(t, root, "linux/po/cs.po")
	for _, want := range []string{
		`"Plural-Forms: nplurals=3; plural=(n==1) ? 0 : (n>=2 && n<=4) ? 1 : 2;\n"`, `"X-Language-Name: Čeština\n"`,
		"msgid \"sites.count\"\nmsgid_plural \"sites.count\"\nmsgstr[0] \"{n} web\"\nmsgstr[1] \"{n} weby\"\nmsgstr[2] \"{n} webů\"\n",
		"msgid \"settings.title\"\nmsgstr \"Nastavení\"\n",
	} {
		if !strings.Contains(po, want) {
			t.Errorf("cs.po lacks %q:\n%s", want, po)
		}
	}
	if strings.Contains(po, "common.php") || strings.Contains(po, "installer.") {
		t.Error("cs.po has another app's text")
	}
	if en := read(t, root, "linux/po/en.po"); !strings.Contains(en, "msgid \"php.update_to\"\nmsgstr \"Update to {version}\"\n") {
		t.Errorf("en.po:\n%s", en)
	}
}

func TestPartialLanguagesFallBackToEnglish(t *testing.T) {
	// German has one of four texts: below the threshold, the apps don't offer it.
	root := repository(t, sample(map[string]string{"lang/de/app.json": `{"settings.title": "Einstellungen"}`}))
	if err := run(root, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "linux", "po", "de.po")); !os.IsNotExist(err) {
		t.Error("a language below the threshold has a po file")
	}
	if strings.Contains(read(t, root, "macos/Sources/FlockUI/Languages.swift"), `"de"`) {
		t.Error("a language below the threshold is offered")
	}

	// Czech without a text: the apps show the English.
	root = repository(t, sample(map[string]string{"lang/cs/app.json": strings.Replace(sampleCS, `  "settings.title": "Nastavení",`+"\n", "", 1)}))
	if err := run(root, false); err != nil {
		t.Fatal(err)
	}
	if po := read(t, root, "linux/po/cs.po"); !strings.Contains(po, "msgid \"settings.title\"\nmsgstr \"Settings\"\n") {
		t.Errorf("cs.po lacks the English fallback:\n%s", po)
	}
	if xcstrings := read(t, root, "macos/Resources/Localizable.xcstrings"); strings.Count(xcstrings, `"value" : "Settings"`) != 2 {
		t.Errorf("Localizable.xcstrings should have the English for en and cs:\n%s", xcstrings)
	}
	if strings.Contains(read(t, root, "windows/Flock.Kit/Strings.cs.resx"), "Settings_Title") {
		t.Error("Strings.cs.resx has a text Czech lacks; .NET falls back to Strings.resx by itself")
	}
}

func TestCheckFindsStaleFiles(t *testing.T) {
	root := repository(t, sample(nil))
	if err := run(root, true); err == nil || !strings.Contains(err.Error(), "linux/po/cs.po") {
		t.Fatalf("got %v, want the files that aren't generated yet", err)
	}
	if err := run(root, false); err != nil {
		t.Fatal(err)
	}
	// A catalog out of its canonical order is stale too.
	if err := os.WriteFile(filepath.Join(root, "lang", "cs", "app.json"), []byte(strings.ReplaceAll(sampleCS, "\n  ", " ")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(root, true); err == nil || !strings.Contains(err.Error(), "lang/cs/app.json") {
		t.Fatalf("got %v, want lang/cs/app.json", err)
	}
	// So is a language's file it no longer has.
	if err := run(root, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "linux", "po", "de.po"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(root, true); err == nil || !strings.Contains(err.Error(), "linux/po/de.po (no longer generated)") {
		t.Fatalf("got %v, want linux/po/de.po", err)
	}
}

func TestReportsProblems(t *testing.T) {
	for _, test := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name:  "a key no app uses",
			files: map[string]string{"lang/en/app.json": strings.Replace(sampleEN, "{", "{\n  \"sites.unused\": \"Unused\",", 1)},
			want:  `"sites.unused": no app uses it`,
		},
		{
			name:  "a key the code uses but lang lacks",
			files: map[string]string{"linux/src/main.rs": sampleRust + `tr!("sites.missing");`},
			want:  `linux/src/main.rs uses "sites.missing", which lang/en/app.json lacks`,
		},
		{
			name:  "a Windows name that's no key",
			files: map[string]string{"windows/Flock.Tray/Pane.cs": sampleCSharp + `Strings.Sites_Missing`},
			want:  "uses Sites_Missing, which no key of lang/en/app.json is",
		},
		{
			name: "a key in no area",
			files: map[string]string{
				"lang/en/app.json":  strings.Replace(sampleEN, "{", "{\n  \"nowhere.title\": \"Title\",", 1),
				"linux/src/main.rs": sampleRust + `tr!("nowhere.title");`,
			},
			want: `"nowhere" isn't an area`,
		},
		{
			name: "a key that isn't snake_case",
			files: map[string]string{
				"lang/en/app.json":  strings.Replace(sampleEN, "{", "{\n  \"sites.CopyLink\": \"Copy\",", 1),
				"linux/src/main.rs": sampleRust + `tr!("sites.CopyLink");`,
			},
			want: "a key is lowercase words in snake_case",
		},
		{
			name:  "a lost placeholder",
			files: map[string]string{"lang/cs/app.json": strings.Replace(sampleCS, "Aktualizovat na {version}", "Aktualizovat", 1)},
			want:  `"Aktualizovat" has other placeholders than the English`,
		},
		{
			name:  "a stray brace in a translation",
			files: map[string]string{"lang/cs/app.json": strings.Replace(sampleCS, `"few": "{n} weby"`, `"few": "{n} weby {"`, 1)},
			want:  `a brace that isn't a {placeholder} in "{n} weby {"`,
		},
		{
			name:  "an installer message of two lines",
			files: map[string]string{"lang/cs/app.json": strings.Replace(sampleCS, "nainstalována: {reason}", `nainstalována: {reason}\n[Setup]`, 1)},
			want:  "installer messages are one line",
		},
		{
			name:  "a catalog that ends with the wrong bracket",
			files: map[string]string{"lang/cs/app.json": strings.TrimSuffix(sampleCS, "}") + "]"},
			want:  "the object doesn't end with }",
		},
		{
			name:  "a catalog with something after it",
			files: map[string]string{"lang/cs/app.json": sampleCS + "{}"},
			want:  "something follows the object",
		},
		{
			name:  "a missing plural form",
			files: map[string]string{"lang/cs/app.json": strings.Replace(sampleCS, `"few": "{n} weby", `, "", 1)},
			want:  `no "few" form`,
		},
		{
			name:  "a translation of a deleted key",
			files: map[string]string{"lang/cs/app.json": strings.Replace(sampleCS, "{", "{\n  \"sites.gone\": \"Pryč\",", 1)},
			want:  `cs/app.json "sites.gone": not in en/app.json`,
		},
		{
			name:  "a language tools/i18n doesn't know",
			files: map[string]string{"lang/xx/app.json": `{}`},
			want:  "lang/xx: tools/i18n doesn't know this language yet",
		},
		{
			name:  "an app's translation the app doesn't use",
			files: map[string]string{"lang/es/app.json": strings.Replace(sampleES, `"Actualizar a {version}"`, `{"default": "Actualizar a {version}", "linux": "Actualizar a {version}"}`, 1)},
			want:  "",
		},
		{
			name:  "a translated untranslatable text",
			files: map[string]string{"lang/cs/app.json": strings.Replace(sampleCS, "{", "{\n  \"common.php\": \"PHP\",", 1)},
			want:  "it isn't translated, but has translations",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := repository(t, sample(test.files))
			err := run(root, false)
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want %q", err, test.want)
			}
		})
	}
}

func TestStatusMissingAndChanged(t *testing.T) {
	root := repository(t, sample(map[string]string{"lang/de/app.json": `{"settings.title": "Einstellungen"}`}))
	var out bytes.Buffer
	if err := printStatus(root, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"cs       Czech                   100%  4 of 4 texts", "de       German                   25%  1 of 4 texts (the apps offer it from 70%)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	if err := printMissing(root, "de", &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"php.update_to\n    Update to {version}\n    note:  {version}: a PHP version\n", "sites.count\n    one:   {n} site\n    other: {n} sites\n", "German lacks 3 texts."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "common.php") || strings.Contains(out.String(), "settings.title") {
		t.Errorf("missing lists a text German has or that isn't translated:\n%s", out.String())
	}

	old := filepath.Join(root, "old.json")
	if err := os.WriteFile(old, []byte(strings.Replace(sampleEN, "Update to {version}", "Upgrade to {version}", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := printChanged(root, old, &out); err != nil {
		t.Fatal(err)
	}
	if want := "php.update_to\n    was: Upgrade to {version}\n    now: Update to {version}\n"; out.String() != want {
		t.Errorf("changed = %q, want %q", out.String(), want)
	}
}

func TestResourceNames(t *testing.T) {
	for key, want := range map[string]string{"sites.share.copy_link": "Sites_Share_CopyLink", "php.update_to": "Php_UpdateTo", "common.n0": "Common_N0"} {
		if got := resourceName(key); got != want {
			t.Errorf("resourceName(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestPluralExpressionsHaveTheirForms(t *testing.T) {
	for _, language := range append(knownLanguages, english) {
		forms := strings.TrimSpace(strings.Split(strings.TrimPrefix(language.PluralForms, "nplurals="), ";")[0])
		if forms != string(rune('0'+len(language.Forms))) {
			t.Errorf("%s: nplurals=%s for the forms %v", language.Code, forms, language.Forms)
		}
		if language.Native == "" || language.Name == "" {
			t.Errorf("%s has no name", language.Code)
		}
	}
}
