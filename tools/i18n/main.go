// Command i18n generates the apps' translation files from lang/, so the macOS, Windows and Linux
// apps share one translation of each text. Run it from the repository root after editing
// lang/ or adding a text to an app (make i18n):
//
//	go run ./tools/i18n                 write every app's files, put the catalogs in order
//	go run ./tools/i18n -check          write nothing; fail when a file is out of date (CI)
//	go run ./tools/i18n -status         how much of each language is translated
//	go run ./tools/i18n -missing de     the texts German lacks, with their English and notes
//	go run ./tools/i18n -changed old.json  the texts whose English differs from old.json's
//
// It finds the keys each app uses in its code, and writes macos/Resources/Localizable.xcstrings,
// windows/Flock.Kit/Strings*.resx and Languages.g.cs, windows/installer/Strings.iss and
// linux/po/*.po. lang/README.md describes lang/ and the rules for keys.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	check := flag.Bool("check", false, "write nothing; fail when a file is out of date")
	status := flag.Bool("status", false, "show how much of each language is translated")
	missing := flag.String("missing", "", "list the texts a language (its code) lacks")
	changed := flag.String("changed", "", "list the texts whose English differs from this older lang/en/app.json")
	flag.Parse()
	var err error
	switch {
	case *status:
		err = printStatus(".", os.Stdout)
	case *missing != "":
		err = printMissing(".", *missing, os.Stdout)
	case *changed != "":
		err = printChanged(".", *changed, os.Stdout)
	default:
		err = run(".", *check)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "i18n:", err)
		os.Exit(1)
	}
}

// open loads lang/ and finds the keys each app uses.
func open(root string) (*Catalog, error) {
	c, err := load(filepath.Join(root, "lang"))
	if err != nil {
		return nil, err
	}
	problems, err := c.scan(root)
	if err != nil {
		return nil, err
	}
	problems = append(problems, c.validate()...)
	if len(problems) > 0 {
		return nil, errors.New("the catalogs have problems:\n  " + strings.Join(problems, "\n  "))
	}
	return c, nil
}

func run(root string, check bool) error {
	c, err := open(root)
	if err != nil {
		return err
	}
	files := generate(c)
	var stale []string
	wanted := map[string]bool{}
	for _, file := range files {
		wanted[file.path] = true
		path := filepath.Join(root, filepath.FromSlash(file.path))
		old, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if bytes.Equal(old, file.content) {
			continue
		}
		if check {
			stale = append(stale, file.path)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, file.content, 0o644); err != nil {
			return err
		}
		fmt.Println("wrote", file.path)
	}
	// A language's files that it no longer has: it dropped below the threshold.
	for _, pattern := range []string{"windows/Flock.Kit/Strings.*.resx", "linux/po/*.po"} {
		matches, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		for _, match := range matches {
			rel, _ := filepath.Rel(root, match)
			rel = filepath.ToSlash(rel)
			if wanted[rel] {
				continue
			}
			if check {
				stale = append(stale, rel+" (no longer generated)")
				continue
			}
			if err := os.Remove(match); err != nil {
				return err
			}
			fmt.Println("removed", rel)
		}
	}
	if len(stale) > 0 {
		return fmt.Errorf("not up to date, run make i18n (go run ./tools/i18n):\n  %s", strings.Join(stale, "\n  "))
	}
	return nil
}

// A file the catalogs generate, by its path from the repository root.
type file struct {
	path    string
	content []byte
}

func generate(c *Catalog) []file {
	files := formatCatalogs(c)
	files = append(files, generateMacOS(c), macLanguages(c))
	files = append(files, generateWindows(c)...)
	return append(files, generateLinux(c)...)
}

func printStatus(root string, w io.Writer) error {
	c, err := open(root)
	if err != nil {
		return err
	}
	for _, language := range c.Languages {
		s := c.status(language)
		note := ""
		if !s.Ships() {
			note = fmt.Sprintf(" (the apps offer it from %.0f%%)", shipThreshold*100)
		}
		fmt.Fprintf(w, "%-8s %-22s %4.0f%%  %d of %d texts%s\n", language.Code, language.Name, s.Share()*100, s.Translated, s.Total, note)
	}
	return nil
}

func printMissing(root, code string, w io.Writer) error {
	c, err := open(root)
	if err != nil {
		return err
	}
	language, ok := knownLanguage(code)
	if !ok {
		return fmt.Errorf("lang/%s: unknown language", code)
	}
	n := 0
	for _, e := range c.Entries {
		if e.NoTranslate || e.Translations[language.Code] != nil {
			continue
		}
		n++
		fmt.Fprintf(w, "%s\n", e.Key)
		if e.Plural() {
			fmt.Fprintf(w, "    one:   %s\n    other: %s\n", e.One, e.Text)
		} else {
			fmt.Fprintf(w, "    %s\n", e.Text)
		}
		if e.Note != "" {
			fmt.Fprintf(w, "    note:  %s\n", e.Note)
		}
	}
	fmt.Fprintf(w, "%s lacks %d texts.\n", language.Name, n)
	return nil
}

// printChanged lists the texts whose English an older en/app.json has otherwise: a pull request's
// base. A changed meaning needs a new key, so translators see it; CI shows the list as a reminder.
func printChanged(root, base string, w io.Writer) error {
	raw, err := readObject(base)
	if err != nil {
		return err
	}
	c, err := load(filepath.Join(root, "lang"))
	if err != nil {
		return err
	}
	for _, e := range c.Entries {
		value, ok := raw[e.Key]
		if !ok {
			continue
		}
		old, err := parseEntry(e.Key, value)
		if err != nil {
			continue
		}
		if old.Text != e.Text || old.One != e.One {
			fmt.Fprintf(w, "%s\n    was: %s\n    now: %s\n", e.Key, old.Text, e.Text)
		}
	}
	return nil
}
