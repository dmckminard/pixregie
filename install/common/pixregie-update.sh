#!/usr/bin/env bash
# Pixregie — automatic update of one binary (run daily by a systemd timer).
#
#   pixregie-update ASSET BINARY SERVICE
#   e.g. pixregie-update pixregie-display-linux-arm64 /usr/local/bin/pixregie-display pixregie-display
#
# Installs the latest release only if its SHA256SUMS is signed with the
# Pixregie release key and lists the asset with a matching checksum.
set -euo pipefail

REPO="dmckminard/pixregie"
RELEASE_KEY="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPrsmtv1WZ/gg6Fg2dz99VsjIpErQ0acaMFV1hBaFQLo pixregie-release"
ASSET="$1"; BIN="$2"; SERVICE="$3"

latest="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest")"
tag="${latest##*/}"
case "$tag" in v[0-9]*.[0-9]*.[0-9]*) ;; *) echo "cannot read the latest version ($latest)"; exit 1 ;; esac
current="$("$BIN" version 2>/dev/null | awk '{print $NF}')"
if [ "$current" = "$tag" ]; then
  echo "$SERVICE is up to date ($tag)"
  exit 0
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
base="https://github.com/$REPO/releases/download/$tag"
curl -fsSL -o "$TMP/SHA256SUMS" "$base/SHA256SUMS"
curl -fsSL -o "$TMP/SHA256SUMS.sig" "$base/SHA256SUMS.sig"
printf 'pixregie-release namespaces="pixregie-release" %s\n' "$RELEASE_KEY" > "$TMP/allowed_signers"
ssh-keygen -Y verify -f "$TMP/allowed_signers" -I pixregie-release -n pixregie-release \
  -s "$TMP/SHA256SUMS.sig" < "$TMP/SHA256SUMS" >/dev/null
curl -fsSL -o "$TMP/$ASSET" "$base/$ASSET"
(cd "$TMP" && grep " $ASSET\$" SHA256SUMS | sha256sum -c --quiet -)

install -m 0755 "$TMP/$ASSET" "$BIN"
systemctl restart "$SERVICE"
echo "$SERVICE updated: $current -> $tag"
