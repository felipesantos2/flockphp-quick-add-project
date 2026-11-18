package main

import (
	"fmt"
	"strconv"
	"strings"
)

// The macOS app writes a text as its key with an interpolation per placeholder, in the English
// text's order: Text("php.update_to \(version)"). Swift makes that the catalog key
// "php.update_to %@": %lld for a plural's count (an Int), %@ for everything else (a String).
type macFormat struct {
	types []string         // each argument's specifier: "@", "lld"
	args  map[string][]int // a placeholder's arguments, 1-based
}

func newMacFormat(e *Entry) *macFormat {
	f := &macFormat{args: map[string][]int{}}
	count := ""
	if e.Plural() {
		count = e.count()
	}
	for i, name := range placeholders(e.Text) {
		kind := "@"
		if name == count {
			kind = "lld"
		}
		f.types = append(f.types, kind)
		f.args[name] = append(f.args[name], i+1)
	}
	return f
}

// The entry's key in Localizable.xcstrings.
func macKey(e *Entry) string {
	key := e.Key
	for _, kind := range newMacFormat(e).types {
		key += " %" + kind
	}
	return key
}

// The text as a format string. Several arguments are numbered ("%2$@"), so a translation can
// reorder them or, in a singular, leave the count out. A name the text has twice ("{name} …
// {name}") is two arguments; a translation's first {name} is the first of them, and so on.
func (f *macFormat) render(text string) string {
	if len(f.types) == 0 {
		return text
	}
	numbered := len(f.types) > 1
	seen := map[string]int{}
	text = strings.ReplaceAll(text, "%", "%%")
	return placeholder.ReplaceAllStringFunc(text, func(match string) string {
		name := match[1 : len(match)-1]
		args := f.args[name]
		arg := args[min(seen[name], len(args)-1)]
		seen[name]++
		if numbered {
			return fmt.Sprintf("%%%d$%s", arg, f.types[arg-1])
		}
		return "%" + f.types[arg-1]
	})
}

func stringUnit(value string) map[string]any {
	return map[string]any{"stringUnit": map[string]any{"state": "translated", "value": value}}
}

// macos/Resources/Localizable.xcstrings: the texts the macOS app uses, in every language it
// offers. A text a language lacks is there in English, as macOS doesn't fall back by itself.
func generateMacOS(c *Catalog) file {
	strs := map[string]any{}
	for _, e := range c.usedBy(MacOS) {
		f := newMacFormat(e)
		localizations := map[string]any{}
		for _, language := range c.shipped() {
			if !e.Plural() {
				localizations[language.Code] = stringUnit(f.render(e.plain(language, MacOS)))
				continue
			}
			plural := map[string]any{}
			for _, form := range append(append([]string{}, language.Forms...), language.Extra...) {
				plural[form] = stringUnit(f.render(e.form(language, MacOS, form)))
			}
			localizations[language.Code] = map[string]any{"variations": map[string]any{"plural": plural}}
		}
		entry := map[string]any{"localizations": localizations}
		if e.Note != "" {
			entry["comment"] = e.Note
		}
		strs[macKey(e)] = entry
	}
	content := xcodeJSON(map[string]any{"sourceLanguage": "en", "strings": strs, "version": "1.0"})
	return file{path: "macos/Resources/Localizable.xcstrings", content: content}
}

// macos/Sources/FlockUI/Languages.swift: the languages the Language picker offers.
func macLanguages(c *Catalog) file {
	var b strings.Builder
	b.WriteString("// " + generatedNote + "\n\n")
	b.WriteString("/// The languages the app is translated to, English first: code and name in that language.\n")
	b.WriteString("let appLanguages: [(code: String, name: String)] = [\n")
	for _, language := range c.shipped() {
		fmt.Fprintf(&b, "    (%s, %s),\n", quoteJSON(language.Code), quoteJSON(language.Native))
	}
	b.WriteString("]\n")
	return file{path: "macos/Sources/FlockUI/Languages.swift", content: []byte(b.String())}
}

// JSON the way Xcode writes a string catalog: keys sorted, " : " between key and value.
func xcodeJSON(value any) []byte {
	var b strings.Builder
	writeXcodeJSON(&b, value, "")
	b.WriteString("\n")
	return []byte(b.String())
}

func writeXcodeJSON(b *strings.Builder, value any, indent string) {
	switch v := value.(type) {
	case map[string]any:
		if len(v) == 0 {
			b.WriteString("{\n\n" + indent + "}")
			return
		}
		b.WriteString("{\n")
		for i, key := range sortedKeys(v) {
			if i > 0 {
				b.WriteString(",\n")
			}
			b.WriteString(indent + "  " + quoteJSON(key) + " : ")
			writeXcodeJSON(b, v[key], indent+"  ")
		}
		b.WriteString("\n" + indent + "}")
	case string:
		b.WriteString(quoteJSON(v))
	case bool:
		b.WriteString(strconv.FormatBool(v))
	default:
		panic(fmt.Sprintf("xcodeJSON: %T", value))
	}
}
