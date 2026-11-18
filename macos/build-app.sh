#!/usr/bin/env bash
# Builds the Flock menu bar app into build/macos/Flock.app, with the flock CLI inside it (the app
# sets Flock up from it on first start), and build/macos/Flock.dmg to install it.
# SKIP_DMG=1 builds the app only.
#
# Ad-hoc signed by default. SIGN_IDENTITY="Developer ID Application: ..." (in the keychain) signs
# the app and disk image with it and the hardened runtime. With NOTARY_KEY_PATH (an App Store
# Connect API key, .p8), NOTARY_KEY_ID and NOTARY_ISSUER_ID set too, the disk image is notarized
# and stapled.
#
# Updates (Sparkle): the app checks the appcast in the "updates" release when it is a release build
# (VERSION 1.2.3 or 1.2.3-beta.4) and SPARKLE_PUBLIC_KEY, the EdDSA key the updates are signed with,
# is set. Other builds don't update.
set -euo pipefail

cd "$(dirname "$0")"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
OUT="../build/macos"
FEED_URL="https://github.com/flock-php/flock/releases/download/updates/appcast.xml"

# Sparkle compares CFBundleVersion: M.m.p.N, N the beta's number or 1000 for the release, so
# 1.2.0-beta.3 (1.2.0.3) < 1.2.0 (1.2.0.1000) < 1.2.1-beta.1 (1.2.1.1). 0 for any other build.
if [[ "$VERSION" =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)(-beta\.([0-9]+))?$ ]]; then
	BUILD="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}.${BASH_REMATCH[3]}.${BASH_REMATCH[5]:-1000}"
else
	BUILD=0
fi
UPDATES=""
if [[ "$BUILD" != 0 && -n "${SPARKLE_PUBLIC_KEY:-}" ]]; then
	UPDATES="<key>SUFeedURL</key><string>${FEED_URL}</string>
	<key>SUPublicEDKey</key><string>${SPARKLE_PUBLIC_KEY}</string>
	<key>SUEnableAutomaticChecks</key><true/>"
fi

# Apple silicon only: Flock doesn't support Intel Macs, and an arm64-only app won't open on one.
ARCHS=(--arch arm64)
swift build -c release --product FlockBar "${ARCHS[@]}"

APP="$OUT/Flock.app"
BIN="$(swift build -c release "${ARCHS[@]}" --show-bin-path)"

# FlockScreenshots (demo data, a fake daemon) uses the app's views for the docs' screenshots; none
# of it may reach the app.
if grep -qaE 'FlockScreenshots|FakeDaemon' "$BIN/FlockBar"; then
	echo "FlockBar contains screenshot code: does it depend on FlockScreenshots?" >&2
	exit 1
fi
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources" "$APP/Contents/Frameworks"
cp "$BIN/FlockBar" "$APP/Contents/MacOS/FlockBar"
# Sparkle, where the executable looks for it in an app bundle.
ditto "$BIN/Sparkle.framework" "$APP/Contents/Frameworks/Sparkle.framework"
install_name_tool -add_rpath @executable_path/../Frameworks "$APP/Contents/MacOS/FlockBar"
# The app icon and the menu bar's logomark, rendered by design/icongen.
cp Resources/AppIcon.icns Resources/MenuBar*.png Resources/LogoMark*.png "$APP/Contents/Resources/"
cp ../LICENSE ../NOTICE ../THIRD_PARTY_NOTICES "$APP/Contents/Resources/"
# The windows' texts in each of the app's languages (lang/): <lang>.lproj next to the icon, where
# Bundle.main finds them; Info.plist lists them.
xcrun xcstringstool compile Resources/Localizable.xcstrings --output-directory "$APP/Contents/Resources"
LOCALIZATIONS=$(cd "$APP/Contents/Resources" && for dir in *.lproj; do printf '<string>%s</string>' "${dir%.lproj}"; done)

# The CLI, built like `make build`; `flock install` copies it to the data dir.
(cd .. && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "-s -w -X flock/internal/cli.Version=$VERSION" \
	-o "build/macos/Flock.app/Contents/MacOS/flock" ./cmd/flock)

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key><string>dev.flock.bar</string>
	<key>CFBundleDevelopmentRegion</key><string>en</string>
	<!-- Settings → General can pick one of them over the system's languages (AppLanguage.swift). -->
	<key>CFBundleLocalizations</key><array>${LOCALIZATIONS}</array>
	<key>CFBundleName</key><string>Flock</string>
	<key>CFBundleDisplayName</key><string>Flock</string>
	<key>CFBundleExecutable</key><string>FlockBar</string>
	<key>CFBundleIconFile</key><string>AppIcon</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>${VERSION}</string>
	<key>CFBundleVersion</key><string>${BUILD}</string>
	${UPDATES}
	<key>NSHumanReadableCopyright</key><string>Copyright © Guava s.r.o.</string>
	<key>LSMinimumSystemVersion</key><string>15.0</string>
	<key>LSUIElement</key><true/>
	<key>NSHighResolutionCapable</key><true/>
	<key>NSAppTransportSecurity</key>
	<dict><key>NSAllowsLocalNetworking</key><true/></dict>
	<!-- flock://install/<service>/<version> links from the website; the app asks before installing. -->
	<key>CFBundleURLTypes</key>
	<array>
		<dict>
			<key>CFBundleURLName</key><string>dev.flock.bar</string>
			<key>CFBundleURLSchemes</key><array><string>flock</string></array>
		</dict>
	</array>
</dict>
</plist>
PLIST

SIGN_IDENTITY="${SIGN_IDENTITY:--}"
SIGN=(codesign --force --sign "$SIGN_IDENTITY")
if [[ "$SIGN_IDENTITY" != - ]]; then
	SIGN+=(--options runtime --timestamp)
fi

# Nested code first: the app's signature covers Sparkle's and the CLI's. Sparkle's helpers are
# signed one by one, innermost first, as its documentation describes for builds without Xcode.
SPARKLE="$APP/Contents/Frameworks/Sparkle.framework"
"${SIGN[@]}" "$SPARKLE/Versions/B/XPCServices/Installer.xpc"
"${SIGN[@]}" --preserve-metadata=entitlements "$SPARKLE/Versions/B/XPCServices/Downloader.xpc"
"${SIGN[@]}" "$SPARKLE/Versions/B/Autoupdate"
"${SIGN[@]}" "$SPARKLE/Versions/B/Updater.app"
"${SIGN[@]}" "$SPARKLE"
"${SIGN[@]}" "$APP/Contents/MacOS/flock"
"${SIGN[@]}" "$APP"
codesign --verify --strict --deep "$APP"
echo "built $(cd "$OUT" && pwd)/Flock.app"

if [[ "${SKIP_DMG:-}" == 1 ]]; then
	exit 0
fi

# The disk image: the app next to a link to /Applications, to drag it there.
STAGE="$OUT/dmg"
DMG="$OUT/Flock.dmg"
rm -rf "$STAGE" "$DMG"
mkdir -p "$STAGE"
cp -R "$APP" "$STAGE/"
ln -s /Applications "$STAGE/Applications"
hdiutil create -quiet -volname "Flock" -srcfolder "$STAGE" -fs HFS+ -format UDZO -ov "$DMG"
rm -rf "$STAGE"

if [[ "$SIGN_IDENTITY" != - ]]; then
	codesign --force --sign "$SIGN_IDENTITY" --timestamp "$DMG"
fi

if [[ -n "${NOTARY_KEY_PATH:-}" ]]; then
	result=$(xcrun notarytool submit "$DMG" --key "$NOTARY_KEY_PATH" --key-id "$NOTARY_KEY_ID" \
		--issuer "$NOTARY_ISSUER_ID" --wait --output-format json)
	echo "$result"
	if [[ "$(plutil -extract status raw - <<<"$result")" != Accepted ]]; then
		xcrun notarytool log "$(plutil -extract id raw - <<<"$result")" --key "$NOTARY_KEY_PATH" \
			--key-id "$NOTARY_KEY_ID" --issuer "$NOTARY_ISSUER_ID" || true
		echo "notarization failed" >&2
		exit 1
	fi
	xcrun stapler staple "$DMG"
	spctl --assess --type open --context context:primary-signature --verbose "$DMG"
fi
echo "built $(cd "$OUT" && pwd)/Flock.dmg"
