package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// The apps, as a translation's per-app forms name them.
const (
	MacOS   = "macos"
	Windows = "windows"
	Linux   = "linux"
)

var platforms = []string{MacOS, Windows, Linux}

// The first part of every key: where the text shows. lang/README.md lists them.
var areas = []string{
	"about", "ai", "app", "common", "debugger", "dumps", "general", "installer", "links", "logs", "mail", "menu",
	"node", "php", "services", "settings", "setup", "sites", "updates", "versions", "worktrees",
}

// The plural categories in the order the catalogs list them.
var formOrder = []string{"zero", "one", "two", "few", "many", "other"}

// Catalog is lang/: every English text with its translations.
type Catalog struct {
	Entries   []*Entry // by key
	byKey     map[string]*Entry
	Languages []Language // the translations in lang/, by code
}

// Entry is a text of lang/en/app.json.
type Entry struct {
	Key  string
	Text string // the English; a plural's "other" form
	One  string // the English singular of a plural
	Note string // for translators
	// Not translated: every language shows the English, e.g. a product name.
	NoTranslate bool
	// Each language's translation.
	Translations map[string]*Translation
	// The apps whose code uses the key.
	Used map[string]bool
}

func (e *Entry) Plural() bool { return e.One != "" }

// Translation is a text's translation into a language: one for every app, or an app its own
// (Apple calls Settings "Ajustes" in Spanish, Windows "Configuración").
type Translation struct {
	Default  *Text
	Platform map[string]*Text
}

// For returns the translation an app uses, nil when there is none.
func (t *Translation) For(platform string) *Text {
	if t == nil {
		return nil
	}
	if text, ok := t.Platform[platform]; ok {
		return text
	}
	return t.Default
}

// Text is a translated text, or a plural's forms.
type Text struct {
	Plain string
	Forms map[string]string
}

var (
	placeholder = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)
	keyPattern  = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z0-9][a-z0-9_]*)+$`)
)

// The placeholders of a text, in order, with repeats.
func placeholders(text string) []string {
	var names []string
	for _, match := range placeholder.FindAllStringSubmatch(text, -1) {
		names = append(names, match[1])
	}
	return names
}

func nameSet(texts ...string) map[string]bool {
	set := map[string]bool{}
	for _, text := range texts {
		for _, name := range placeholders(text) {
			set[name] = true
		}
	}
	return set
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for name := range a {
		if !b[name] {
			return false
		}
	}
	return true
}

// load reads lang/: en/app.json and every other language's app.json.
func load(dir string) (*Catalog, error) {
	raw, err := readObject(filepath.Join(dir, "en", "app.json"))
	if err != nil {
		return nil, err
	}
	c := &Catalog{byKey: map[string]*Entry{}}
	var problems []string
	for key, value := range raw {
		e, err := parseEntry(key, value)
		if err != nil {
			problems = append(problems, fmt.Sprintf("en %q: %v", key, err))
			continue
		}
		c.Entries = append(c.Entries, e)
		c.byKey[key] = e
	}
	sort.Slice(c.Entries, func(i, j int) bool { return c.Entries[i].Key < c.Entries[j].Key })
	folders, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, folder := range folders {
		if !folder.IsDir() || folder.Name() == "en" {
			continue
		}
		language, ok := knownLanguage(folder.Name())
		if !ok {
			problems = append(problems, fmt.Sprintf("lang/%s: tools/i18n doesn't know this language yet; add its plural rules to tools/i18n/languages.go", folder.Name()))
			continue
		}
		c.Languages = append(c.Languages, language)
		name := language.Code + "/app.json"
		raw, err := readObject(filepath.Join(dir, language.Code, "app.json"))
		if err != nil {
			return nil, err
		}
		for key, value := range raw {
			e := c.byKey[key]
			if e == nil {
				problems = append(problems, fmt.Sprintf("%s %q: not in en/app.json (deleted or renamed? remove it here too)", name, key))
				continue
			}
			t, err := parseTranslation(value)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s %q: %v", name, key, err))
				continue
			}
			if e.Translations == nil {
				e.Translations = map[string]*Translation{}
			}
			e.Translations[language.Code] = t
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, errors.New("the catalogs have problems:\n  " + strings.Join(problems, "\n  "))
	}
	return c, nil
}

// A catalog file's object, refusing a key that's there twice (the second would win unseen).
func readObject(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("%s: not a JSON object", path)
	}
	object := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		key := token.(string)
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("%s %q: %w", path, key, err)
		}
		if _, ok := object[key]; ok {
			return nil, fmt.Errorf("%s: %q is there twice", path, key)
		}
		object[key] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, fmt.Errorf("%s: the object doesn't end with }", path)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: something follows the object", path)
	}
	return object, nil
}

func strict(data []byte, v any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(v)
}

func parseEntry(key string, data json.RawMessage) (*Entry, error) {
	e := &Entry{Key: key, Used: map[string]bool{}}
	if json.Unmarshal(data, &e.Text) == nil {
		return e, nil
	}
	var raw struct {
		Text      string `json:"text"`
		One       string `json:"one"`
		Other     string `json:"other"`
		Note      string `json:"note"`
		Translate *bool  `json:"translate"`
	}
	if err := strict(data, &raw); err != nil {
		return nil, fmt.Errorf("a text is a string or an object with text (or one and other), note and translate: %w", err)
	}
	switch {
	case raw.Text != "" && (raw.One != "" || raw.Other != ""):
		return nil, errors.New(`a text has "text", or the plural forms "one" and "other", not both`)
	case raw.Text == "" && (raw.One == "" || raw.Other == ""):
		return nil, errors.New(`a plural has both "one" and "other"`)
	case raw.Text != "":
		e.Text = raw.Text
	default:
		e.Text, e.One = raw.Other, raw.One
	}
	e.Note = raw.Note
	if raw.Translate != nil {
		if *raw.Translate {
			return nil, errors.New(`"translate" is only ever false`)
		}
		e.NoTranslate = true
	}
	return e, nil
}

func parseText(data json.RawMessage) (*Text, error) {
	var plain string
	if json.Unmarshal(data, &plain) == nil {
		return &Text{Plain: plain}, nil
	}
	var forms map[string]string
	if err := strict(data, &forms); err != nil {
		return nil, errors.New("a translation is a string or plural forms")
	}
	for form := range forms {
		if !slices.Contains(formOrder, form) {
			return nil, fmt.Errorf("unknown plural form %q", form)
		}
	}
	return &Text{Forms: forms}, nil
}

func parseTranslation(data json.RawMessage) (*Translation, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) == nil {
		perPlatform := false
		for key := range object {
			if key == "default" || slices.Contains(platforms, key) {
				perPlatform = true
			}
		}
		if perPlatform {
			t := &Translation{Platform: map[string]*Text{}}
			for key, value := range object {
				if key != "default" && !slices.Contains(platforms, key) {
					return nil, fmt.Errorf("%q is neither an app nor \"default\"", key)
				}
				text, err := parseText(value)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", key, err)
				}
				if key == "default" {
					t.Default = text
				} else {
					t.Platform[key] = text
				}
			}
			return t, nil
		}
	}
	text, err := parseText(data)
	if err != nil {
		return nil, err
	}
	return &Translation{Default: text}, nil
}

// The count of a plural text: {n}, or {0} in a text without names.
func (e *Entry) count() string {
	names := nameSet(e.Text, e.One)
	switch {
	case names["n"]:
		return "n"
	case names["0"]:
		return "0"
	}
	return ""
}

// The text an app shows in a language: its translation, else the English (nil for English).
func (e *Entry) text(language Language, platform string) *Text {
	if t := e.Translations[language.Code].For(platform); t != nil && !e.NoTranslate {
		return t
	}
	return nil
}

// The English as a Text: a plural's forms for English.
func (e *Entry) english() *Text {
	if !e.Plural() {
		return &Text{Plain: e.Text}
	}
	return &Text{Forms: map[string]string{"one": e.One, "other": e.Text}}
}

// A plural form of a language's translation, the English when it has none.
func (e *Entry) form(language Language, platform, form string) string {
	if t := e.text(language, platform); t != nil {
		if value, ok := t.Forms[form]; ok {
			return value
		}
		// Extra categories (fractions) get the last whole-number form.
		return t.Forms[language.Forms[len(language.Forms)-1]]
	}
	if form == "one" {
		return e.One
	}
	return e.Text
}

// A translation or the English, for a text without plural forms.
func (e *Entry) plain(language Language, platform string) string {
	if t := e.text(language, platform); t != nil {
		return t.Plain
	}
	return e.Text
}

func (c *Catalog) validate() []string {
	var problems []string
	for _, e := range c.Entries {
		report := func(format string, args ...any) {
			problems = append(problems, fmt.Sprintf("%q: %s", e.Key, fmt.Sprintf(format, args...)))
		}
		if !keyPattern.MatchString(e.Key) {
			report("a key is lowercase words in snake_case, separated by dots: area.what (sites.share.copy_link)")
		} else if area, _, _ := strings.Cut(e.Key, "."); !slices.Contains(areas, area) {
			report("%q isn't an area; lang/README.md lists them", area)
		}
		if strings.TrimSpace(e.Text) == "" {
			report("no English text")
		}
		for _, text := range []string{e.Text, e.One} {
			if strayBrace(text) {
				report("a brace that isn't a {placeholder} in %q", text)
			}
		}
		if strings.HasPrefix(e.Key, "installer.") && strings.ContainsAny(e.Text+e.Note, "\r\n") {
			report("installer messages and their notes are one line")
		}
		if e.Plural() {
			for name := range nameSet(e.One) {
				if !nameSet(e.Text)[name] {
					report("the singular has {%s}, which the plural lacks", name)
				}
			}
		}
		if e.NoTranslate {
			if e.Plural() {
				report(`a text with plural forms is translated`)
			}
			if len(e.Translations) > 0 {
				report("it isn't translated, but has translations")
			}
		}
		if strings.HasPrefix(e.Key, "installer.") && (e.Plural() || strings.Contains(e.Text, "%")) {
			report("installer messages have no plural forms or %% signs")
		}
		for _, language := range c.Languages {
			t := e.Translations[language.Code]
			if t == nil {
				continue
			}
			for platform := range t.Platform {
				if !e.Used[platform] {
					report("%s has a translation for %s, which doesn't use it", language.Name, platform)
				}
			}
			texts := map[string]*Text{"": t.Default}
			for platform, text := range t.Platform {
				texts[platform] = text
			}
			for platform, text := range texts {
				if text == nil {
					continue
				}
				for _, problem := range e.checkText(language, platform, text) {
					where := language.Name
					if platform != "" {
						where += " (" + platform + ")"
					}
					report("%s: %s", where, problem)
				}
			}
		}
	}
	return problems
}

// A brace outside a {placeholder}: string.Format on Windows fails on one.
func strayBrace(text string) bool {
	return strings.ContainsAny(placeholder.ReplaceAllString(text, ""), "{}")
}

// What's wrong with a translation of the entry.
func (e *Entry) checkText(language Language, platform string, text *Text) []string {
	var problems []string
	for _, value := range append(slices.Collect(maps.Values(text.Forms)), text.Plain) {
		if strayBrace(value) {
			problems = append(problems, fmt.Sprintf("a brace that isn't a {placeholder} in %q", value))
		}
		// A line of its own in Strings.iss would be read as the installer's script.
		if strings.HasPrefix(e.Key, "installer.") && strings.ContainsAny(value, "\r\n") {
			problems = append(problems, fmt.Sprintf("installer messages are one line: %q", value))
		}
	}
	if !e.Plural() {
		if text.Forms != nil {
			return []string{"plural forms for a text without them"}
		}
		if strings.TrimSpace(text.Plain) == "" {
			return []string{"empty"}
		}
		if !sameSet(nameSet(text.Plain), nameSet(e.Text)) {
			problems = append(problems, fmt.Sprintf("%q has other placeholders than the English", text.Plain))
		}
		return problems
	}
	if text.Forms == nil {
		return []string{"a plural text needs its plural forms: " + strings.Join(language.Forms, ", ")}
	}
	for _, form := range language.Forms {
		if _, ok := text.Forms[form]; !ok {
			problems = append(problems, fmt.Sprintf("no %q form (%s has %s)", form, language.Name, strings.Join(language.Forms, ", ")))
		}
	}
	for form, value := range text.Forms {
		if !slices.Contains(language.Forms, form) && !slices.Contains(language.Extra, form) {
			problems = append(problems, fmt.Sprintf("%s has no %q form", language.Name, form))
			continue
		}
		if strings.TrimSpace(value) == "" {
			problems = append(problems, fmt.Sprintf("the %q form is empty", form))
			continue
		}
		found, english := nameSet(value), nameSet(e.Text)
		withoutCount := nameSet(e.Text)
		delete(withoutCount, e.count())
		switch {
		case form != "one" && !sameSet(found, english):
			problems = append(problems, fmt.Sprintf("%q has other placeholders than the English", value))
		// A singular may leave the count out: "Show its worktree".
		case form == "one" && !sameSet(found, english) && !sameSet(found, withoutCount):
			problems = append(problems, fmt.Sprintf("%q has other placeholders than the English", value))
		// Windows fills a singular as the English singular has it.
		case form == "one" && e.Used[Windows] && (platform == "" || platform == Windows) && !sameSet(found, nameSet(e.One)):
			problems = append(problems, fmt.Sprintf("%q has other placeholders than the English singular", value))
		}
	}
	return problems
}

// Status is how much of a language is translated.
type Status struct {
	Language   Language
	Translated int
	Total      int
}

func (s Status) Share() float64 {
	if s.Total == 0 {
		return 1
	}
	return float64(s.Translated) / float64(s.Total)
}

// Ships: the apps offer the language.
func (s Status) Ships() bool { return s.Share() >= shipThreshold }

func (c *Catalog) status(language Language) Status {
	s := Status{Language: language}
	for _, e := range c.Entries {
		if e.NoTranslate {
			continue
		}
		s.Total++
		if e.Translations[language.Code] != nil {
			s.Translated++
		}
	}
	return s
}

// The languages the apps offer, English first.
func (c *Catalog) shipped() []Language {
	languages := []Language{english}
	for _, language := range c.Languages {
		if c.status(language).Ships() {
			languages = append(languages, language)
		}
	}
	return languages
}

// The keys an app uses, in order.
func (c *Catalog) usedBy(platform string) []*Entry {
	var used []*Entry
	for _, e := range c.Entries {
		if e.Used[platform] {
			used = append(used, e)
		}
	}
	return used
}
