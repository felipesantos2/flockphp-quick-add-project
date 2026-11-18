#!/usr/bin/env bash
# Checks that every key the app's code uses for a text (Text("sites.title"), String(localized: "…"),
# …) is in Resources/Localizable.xcstrings, with the arguments it passes: the compiler lists them
# (-emit-localized-strings). The catalog is generated from lang/ by tools/i18n: add a new text to
# lang/en/app.json (lang/README.md), run make i18n, then run this again.
set -euo pipefail

cd "$(dirname "$0")"
# A build of its own, kept between runs so it only recompiles what changed; a file's list is
# rewritten whenever the file is compiled.
WORK=.build/strings

swift build --product FlockBar --scratch-path "$WORK/build" \
	-Xswiftc -emit-localized-strings -Xswiftc -emit-localized-strings-path -Xswiftc "$PWD/$WORK/stringsdata" >/dev/null

python3 - "$WORK/stringsdata" Resources/Localizable.xcstrings <<'PY'
import glob, json, os, sys

used = {}
for path in glob.glob(sys.argv[1] + "/*.stringsdata"):
    data = json.load(open(path))
    # The list of a source file that has since been deleted or renamed.
    if not os.path.exists(data["source"]):
        continue
    for entries in data["tables"].values():
        for entry in entries:
            used.setdefault(entry["key"], f'{data["source"]}:{entry["location"]["startingLine"]}')

catalog = json.load(open(sys.argv[2]))["strings"]
missing = sorted(set(used) - set(catalog))
unused = sorted(set(catalog) - set(used))
for key in missing:
    print(f"missing from the catalog: {key!r} ({used[key]})")
for key in unused:
    print(f"no longer used: {key!r}")
sys.exit(1 if missing or unused else 0)
PY
echo "all $(python3 -c 'import json,sys; print(len(json.load(open(sys.argv[1]))["strings"]))' Resources/Localizable.xcstrings) texts are in the catalog"
