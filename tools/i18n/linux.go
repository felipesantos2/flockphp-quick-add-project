package main

import (
	"strconv"
	"strings"
)

// linux/po/<code>.po for every language the app offers, English too: msgid is the key, msgstr
// the text (the English where a language lacks it). build.rs builds each file in po/ into the
// app; src/i18n.rs reads its Language, X-Language-Name and Plural-Forms.
func generateLinux(c *Catalog) []file {
	var files []file
	for _, language := range c.shipped() {
		var b strings.Builder
		b.WriteString("# " + language.Name + " texts of the Flock Linux app (linux/src), by key.\n")
		b.WriteString("# " + generatedNote + "\n")
		b.WriteString("msgid \"\"\nmsgstr \"\"\n")
		for _, header := range []string{
			"Project-Id-Version: flock-app", "Language: " + language.Code, "X-Language-Name: " + language.Native, "MIME-Version: 1.0",
			"Content-Type: text/plain; charset=UTF-8", "Content-Transfer-Encoding: 8bit", "Plural-Forms: " + language.PluralForms,
		} {
			b.WriteString(quotePO(header+"\n") + "\n")
		}
		for _, e := range c.usedBy(Linux) {
			b.WriteString("\n")
			if e.Note != "" && language.Code == "en" {
				for line := range strings.SplitSeq(e.Note, "\n") {
					b.WriteString("#. " + line + "\n")
				}
			}
			b.WriteString("msgid " + quotePO(e.Key) + "\n")
			if !e.Plural() {
				b.WriteString("msgstr " + quotePO(e.plain(language, Linux)) + "\n")
				continue
			}
			b.WriteString("msgid_plural " + quotePO(e.Key) + "\n")
			for i, form := range language.Forms {
				b.WriteString("msgstr[" + strconv.Itoa(i) + "] " + quotePO(e.form(language, Linux, form)) + "\n")
			}
		}
		files = append(files, file{path: "linux/po/" + language.Code + ".po", content: []byte(b.String())})
	}
	return files
}

func quotePO(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`).Replace(s) + `"`
}
