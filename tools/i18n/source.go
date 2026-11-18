package main

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

// The catalogs in their canonical form: texts sorted by key, fields in a fixed order, one text
// per line. A text can be added anywhere; the next run puts it in its place.
func formatCatalogs(c *Catalog) []file {
	var en strings.Builder
	en.WriteString("{")
	for i, e := range c.Entries {
		en.WriteString(separator(i) + "  " + quoteJSON(e.Key) + ": ")
		var fields []string
		if e.Plural() {
			fields = append(fields, `"one": `+quoteJSON(e.One), `"other": `+quoteJSON(e.Text))
		} else {
			fields = append(fields, `"text": `+quoteJSON(e.Text))
		}
		if e.Note != "" {
			fields = append(fields, `"note": `+quoteJSON(e.Note))
		}
		if e.NoTranslate {
			fields = append(fields, `"translate": false`)
		}
		if len(fields) == 1 && !e.Plural() {
			en.WriteString(quoteJSON(e.Text))
		} else {
			en.WriteString("{" + strings.Join(fields, ", ") + "}")
		}
	}
	en.WriteString("\n}\n")
	files := []file{{path: "lang/en/app.json", content: []byte(en.String())}}
	for _, language := range c.Languages {
		var b strings.Builder
		b.WriteString("{")
		i := 0
		for _, e := range c.Entries {
			t := e.Translations[language.Code]
			if t == nil {
				continue
			}
			b.WriteString(separator(i) + "  " + quoteJSON(e.Key) + ": " + translationJSON(t))
			i++
		}
		b.WriteString("\n}\n")
		files = append(files, file{path: "lang/" + language.Code + "/app.json", content: []byte(b.String())})
	}
	return files
}

func separator(i int) string {
	if i == 0 {
		return "\n"
	}
	return ",\n"
}

func translationJSON(t *Translation) string {
	if len(t.Platform) == 0 {
		return textJSON(t.Default)
	}
	var fields []string
	if t.Default != nil {
		fields = append(fields, `"default": `+textJSON(t.Default))
	}
	for _, platform := range platforms {
		if text, ok := t.Platform[platform]; ok {
			fields = append(fields, quoteJSON(platform)+": "+textJSON(text))
		}
	}
	return "{" + strings.Join(fields, ", ") + "}"
}

func textJSON(t *Text) string {
	if t.Forms == nil {
		return quoteJSON(t.Plain)
	}
	var fields []string
	for _, form := range formOrder {
		if value, ok := t.Forms[form]; ok {
			fields = append(fields, quoteJSON(form)+": "+quoteJSON(value))
		}
	}
	return "{" + strings.Join(fields, ", ") + "}"
}

// A JSON string, with <, > and & as they are.
func quoteJSON(s string) string {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
