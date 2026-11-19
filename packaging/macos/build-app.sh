#!/usr/bin/env bash
# Build Pixregie.app (universal arm64 + x86_64) and zip it. Runs on macOS.
#   packaging/macos/build-app.sh VERSION OUTDIR
set -euo pipefail
VERSION="$1"
OUT="$2"
APP="$OUT/Pixregie.app"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

for arch in arm64 amd64; do
  CGO_ENABLED=1 GOOS=darwin GOARCH=$arch \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$WORK/agent-$arch" ./cmd/pixregie-agent
done

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
lipo -create -output "$APP/Contents/MacOS/pixregie-agent" "$WORK/agent-arm64" "$WORK/agent-amd64"
sed "s/__VERSION__/${VERSION#v}/g" packaging/macos/Info.plist > "$APP/Contents/Info.plist"

go run ./tools/appicon "$WORK/Pixregie.iconset"
iconutil -c icns -o "$APP/Contents/Resources/Pixregie.icns" "$WORK/Pixregie.iconset"

# Ad-hoc signature: required to run on Apple Silicon. Not notarised, so the
# first launch needs right-click > Open.
codesign --force --deep --sign - "$APP"
ditto -c -k --keepParent "$APP" "$OUT/Pixregie-macos.zip"
