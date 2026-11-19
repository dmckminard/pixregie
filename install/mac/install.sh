#!/usr/bin/env bash
# Pixregie — macOS installer and updater.
#
#   curl -fsSL https://github.com/dmckminard/pixregie/releases/latest/download/install-mac.sh | bash
#
# Downloads the latest Pixregie.app, checks it against SHA256SUMS signed with
# the Pixregie release key, replaces /Applications/Pixregie.app and starts
# it. Files fetched with curl carry no quarantine flag, so macOS does not
# block the unnotarised app.
set -euo pipefail

REPO="dmckminard/pixregie"
RELEASE_KEY="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPrsmtv1WZ/gg6Fg2dz99VsjIpErQ0acaMFV1hBaFQLo pixregie-release"
BASE="https://github.com/$REPO/releases/latest/download"
APP="/Applications/Pixregie.app"

say() { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
die() { printf '\033[1;31mxx\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(uname -s)" = Darwin ] || die "this installer is for macOS"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

say "Downloading Pixregie"
curl -fsSL -o "$TMP/Pixregie-macos.zip" "$BASE/Pixregie-macos.zip"
curl -fsSL -o "$TMP/SHA256SUMS" "$BASE/SHA256SUMS"
curl -fsSL -o "$TMP/SHA256SUMS.sig" "$BASE/SHA256SUMS.sig" || die "this release is not signed"
printf 'pixregie-release namespaces="pixregie-release" %s\n' "$RELEASE_KEY" > "$TMP/allowed_signers"
ssh-keygen -Y verify -f "$TMP/allowed_signers" -I pixregie-release -n pixregie-release \
  -s "$TMP/SHA256SUMS.sig" < "$TMP/SHA256SUMS" >/dev/null 2>&1 || die "release signature is NOT valid, aborting"
(cd "$TMP" && grep ' Pixregie-macos.zip$' SHA256SUMS | shasum -a 256 -c --quiet -) || die "checksum mismatch, aborting"

ditto -x -k "$TMP/Pixregie-macos.zip" "$TMP/app"
[ -d "$TMP/app/Pixregie.app" ] || die "unexpected archive content"

if pgrep -x pixregie-agent >/dev/null; then
  say "Stopping the running Pixregie"
  osascript -e 'quit app "Pixregie"' >/dev/null 2>&1 || true
  for _ in 1 2 3 4 5; do pgrep -x pixregie-agent >/dev/null || break; sleep 1; done
  pkill -x pixregie-agent 2>/dev/null || true
fi

say "Installing in $APP"
rm -rf "$APP"
mv "$TMP/app/Pixregie.app" "$APP"
xattr -dr com.apple.quarantine "$APP" 2>/dev/null || true

open "$APP"
say "Done: Pixregie $(defaults read "$APP/Contents/Info" CFBundleShortVersionString 2>/dev/null || echo '') is running in the menu bar."
