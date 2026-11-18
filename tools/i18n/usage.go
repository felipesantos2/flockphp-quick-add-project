package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Where each app's code uses keys, from the repository root.
var (
	macSources     = []string{"macos/Sources/FlockBar", "macos/Sources/FlockKit", "macos/Sources/FlockUI"}
	linuxSources   = []string{"linux/src"}
	windowsSources = []string{"windows"}
)

var (
	// A Swift string literal that starts with a key: Text("sites.title"), "php.update_to \(version)".
	swiftKey = regexp.MustCompile(`"([a-z][a-z0-9]*(?:\.[a-z0-9][a-z0-9_]*)+)[ "]`)
	// tr!("sites.title"), trn!("dumps.count", n), tr_noop!("…").
	rustKey = regexp.MustCompile(`\b(?:tr|trn|tr_noop)!\(\s*"([^"]*)"`)
	// Strings.Sites_Title, {l:Loc Key=Sites_Title}, Plural(Strings.ResourceManager, "Dumps_Count", …).
	csName     = regexp.MustCompile(`\bStrings\.([A-Z][A-Za-z0-9_]*)`)
	xamlName   = regexp.MustCompile(`\bLoc Key=([A-Za-z0-9_]+)`)
	pluralName = regexp.MustCompile(`Plural\(Strings\.ResourceManager,\s*"([A-Za-z0-9_]+)"`)
	// {cm:Installer_OpenFlock}, CustomMessage('Installer_OpenFlock').
	innoName = regexp.MustCompile(`\{cm:([A-Za-z0-9_]+)\}|CustomMessage\('([A-Za-z0-9_]+)'\)`)
)

// resourceName is a key's name in Windows' Strings.resx and the installer's messages:
// sites.share.copy_link is Sites_Share_CopyLink.
func resourceName(key string) string {
	var parts []string
	for _, part := range strings.Split(key, ".") {
		var b strings.Builder
		for _, word := range strings.Split(part, "_") {
			if word != "" {
				b.WriteString(strings.ToUpper(word[:1]) + word[1:])
			}
		}
		parts = append(parts, b.String())
	}
	return strings.Join(parts, "_")
}

// scan marks the keys each app's code uses and returns what doesn't add up: a key the code
// uses that en/app.json lacks, or a key no app uses.
func (c *Catalog) scan(root string) ([]string, error) {
	var problems []string
	byName := map[string]*Entry{}
	for _, e := range c.Entries {
		byName[resourceName(e.Key)] = e
	}
	use := func(platform, path, key string, known bool) {
		if e := c.byKey[key]; e != nil {
			e.Used[platform] = true
		} else if known {
			problems = append(problems, fmt.Sprintf("%s uses %q, which lang/en/app.json lacks", path, key))
		}
	}
	err := walk(root, macSources, ".swift", func(path, src string) {
		for _, m := range swiftKey.FindAllStringSubmatchIndex(src, -1) {
			key := src[m[2]:m[3]]
			area, _, _ := strings.Cut(key, ".")
			// Settings keys look like text keys: @AppStorage("services.filter").
			before := strings.TrimRight(src[max(0, m[0]-20):m[0]], " ")
			setting := strings.HasSuffix(before, "AppStorage(") || strings.HasSuffix(before, "forKey:")
			use(MacOS, path, key, slices.Contains(areas, area) && !setting)
		}
	})
	if err != nil {
		return nil, err
	}
	err = walk(root, linuxSources, ".rs", func(path, src string) {
		// i18n.rs defines the macros; its docs and tests name them.
		if strings.HasSuffix(path, "/i18n.rs") {
			return
		}
		for _, m := range rustKey.FindAllStringSubmatch(src, -1) {
			use(Linux, path, m[1], true)
		}
	})
	if err != nil {
		return nil, err
	}
	windowsName := func(path, name string) {
		if e := byName[name]; e != nil {
			e.Used[Windows] = true
		} else if name != "ResourceManager" && name != "Culture" {
			problems = append(problems, fmt.Sprintf("%s uses %s, which no key of lang/en/app.json is", path, name))
		}
	}
	err = walk(root, windowsSources, "", func(path, src string) {
		switch filepath.Ext(path) {
		case ".cs", ".xaml":
			for _, re := range []*regexp.Regexp{csName, xamlName, pluralName} {
				for _, m := range re.FindAllStringSubmatch(src, -1) {
					windowsName(path, m[1])
				}
			}
		case ".iss":
			for _, m := range innoName.FindAllStringSubmatch(src, -1) {
				windowsName(path, m[1]+m[2])
			}
		}
	})
	if err != nil {
		return nil, err
	}
	for _, e := range c.Entries {
		if len(e.Used) == 0 {
			problems = append(problems, fmt.Sprintf("%q: no app uses it; delete it from every lang/*/app.json", e.Key))
		}
		if strings.HasPrefix(e.Key, "installer.") && (e.Used[MacOS] || e.Used[Linux]) {
			problems = append(problems, fmt.Sprintf("%q: installer texts are the Windows installer's", e.Key))
		}
	}
	return problems, nil
}

// walk calls f with each source file under dirs with the extension ("" for any), skipping build
// output, tests and generated files.
func walk(root string, dirs []string, ext string, f func(path, src string)) error {
	for _, dir := range dirs {
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(dir)), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return filepath.SkipDir
				}
				return err
			}
			name := d.Name()
			if d.IsDir() {
				if name == "bin" || name == "obj" || name == ".build" || strings.HasSuffix(name, ".Tests") {
					return filepath.SkipDir
				}
				return nil
			}
			if (ext != "" && filepath.Ext(name) != ext) || name == "Strings.iss" {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			f(filepath.ToSlash(rel), string(data))
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
