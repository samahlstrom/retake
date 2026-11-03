#!/bin/sh
# Makes Retake.app, one universal (Apple silicon and Intel) Mac app, from the darwin archives `make snapshot` put in
# dist/, and zips it: dist/Retake.app and dist/Retake_<version>_macos.zip. UNSIGNED and untested on a real Mac
# (docs/install-plan.md): signing, notarizing and the .dmg need a Mac and an Apple Developer account.
#
#   Retake.app/Contents/Info.plist
#   Retake.app/Contents/MacOS/Retake          the retake binary: with no arguments it runs `retake start --app`
#   Retake.app/Contents/MacOS/herdr, ffmpeg, ffprobe, retake-edit   (each that both archives have)
#   Retake.app/Contents/Resources/menubar.js  the menu-bar item (osascript)
#   Retake.app/Contents/Resources/retake-bundle.json, licenses/, THIRD_PARTY_NOTICES.md, README.md
#
# Needs llvm-lipo (or lipo) to join the two architectures, and zip.
#   scripts/macos-app.sh [dist folder]
set -eu

cd "$(dirname "$0")/.."
DIST="${1:-dist}"
# The product's name and identifier aren't final; internal/desktop.AppID is the same identifier.
APP_NAME="Retake"
APP_ID="io.github.samahlstrom.retake"

die() {
  printf 'macos-app: %s\n' "$*" >&2
  exit 1
}
# Ubuntu's llvm package names it llvm-lipo-<major>; take the newest one when there's no plain llvm-lipo.
LIPO="$(command -v llvm-lipo || command -v lipo || ls /usr/bin/llvm-lipo-* /usr/lib/llvm-*/bin/llvm-lipo 2>/dev/null | sort -V | tail -n 1 || true)"
[ -n "$LIPO" ] || die "needs llvm-lipo or lipo"
command -v zip >/dev/null 2>&1 || die "needs zip"

arm="" intel=""
for a in "$DIST"/retake_*_darwin_arm64.tar.gz; do [ -f "$a" ] && arm="$a"; done
for a in "$DIST"/retake_*_darwin_amd64.tar.gz; do [ -f "$a" ] && intel="$a"; done
[ -n "$arm" ] && [ -n "$intel" ] || die "no darwin archives in $DIST (run make snapshot first)"
version="$(basename "$arm" | sed 's/^retake_//; s/_darwin_arm64\.tar\.gz$//')"

work="$(mktemp -d "$DIST/.macos-app.XXXXXX")"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/arm64" "$work/amd64"
tar -xzf "$arm" -C "$work/arm64"
tar -xzf "$intel" -C "$work/amd64"

app="$DIST/$APP_NAME.app"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"

# universal NAME DEST: one binary with both architectures, when both archives have it.
universal() {
  if [ -f "$work/arm64/$1" ] && [ -f "$work/amd64/$1" ]; then
    "$LIPO" -create -output "$2" "$work/arm64/$1" "$work/amd64/$1"
    chmod 755 "$2"
    return 0
  fi
  [ ! -f "$work/arm64/$1" ] && [ ! -f "$work/amd64/$1" ] || printf 'macos-app: %s is in one archive only: left out\n' "$1" >&2
  return 1
}
universal retake "$app/Contents/MacOS/$APP_NAME" || die "no retake binary"
for t in herdr ffmpeg ffprobe retake-edit; do
  if universal "$t" "$app/Contents/MacOS/$t"; then printf 'macos-app: %s\n' "$t" >&2; fi
done

res="$app/Contents/Resources"
cp scripts/macos/menubar.js "$res/menubar.js"
for f in README.md CHANGELOG.md THIRD_PARTY_NOTICES.md retake-bundle.json; do
  [ ! -f "$work/arm64/$f" ] || cp "$work/arm64/$f" "$res/$f"
done
[ -f "$res/retake-bundle.json" ] || printf '{\n  "bundle": 1,\n  "tools": {}\n}\n' >"$res/retake-bundle.json"
if [ -d "$work/arm64/licenses" ]; then
  cp -R "$work/arm64/licenses" "$res/licenses"
fi

# CFBundleShortVersionString wants X.Y.Z: the version without its pre-release and build parts.
short="$(printf '%s' "$version" | sed 's/[-+].*//')"
cat >"$app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key>
	<string>$APP_NAME</string>
	<key>CFBundleDisplayName</key>
	<string>$APP_NAME</string>
	<key>CFBundleIdentifier</key>
	<string>$APP_ID</string>
	<key>CFBundleExecutable</key>
	<string>$APP_NAME</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>$short</string>
	<key>CFBundleVersion</key>
	<string>$version</string>
	<key>LSMinimumSystemVersion</key>
	<string>12.0</string>
	<key>LSUIElement</key>
	<true/>
	<key>NSHighResolutionCapable</key>
	<true/>
</dict>
</plist>
EOF

zipfile="$DIST/${APP_NAME}_${version}_macos.zip"
rm -f "$zipfile"
(cd "$DIST" && zip -qry "$(basename "$zipfile")" "$APP_NAME.app")
printf 'macos-app: %s and %s (unsigned)\n' "$app" "$zipfile" >&2
