#!/usr/bin/env bash
# Pixregie — Raspberry Pi installer (Raspberry Pi OS Lite, Bookworm or later).
#
#   curl -fsSLO https://github.com/dmckminard/pixregie/releases/latest/download/install-pi.sh
#   sudo bash install-pi.sh                 # install or upgrade
#   sudo bash install-pi.sh --add-agent PC  # create a token for another machine
#   sudo bash install-pi.sh --uninstall
#
# Idempotent: re-running upgrades the binary and keeps the configuration.
set -euo pipefail

REPO="dmckminard/pixregie"
VERSION="${PIXREGIE_VERSION:-latest}"
SVC_USER="pixregie-display"
BIN="/usr/local/bin/pixregie-display"
CONF_DIR="/etc/pixregie"
CONF="$CONF_DIR/display.json"
UNIT="/etc/systemd/system/pixregie-display.service"
AVAHI="/etc/avahi/services/pixregie.service"
PORT=7421

say()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mxx\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run with sudo"

as_svc() { runuser -u "$SVC_USER" -- "$@"; }

cmdline_file() {
  for f in /boot/firmware/cmdline.txt /boot/cmdline.txt; do
    [ -f "$f" ] && { echo "$f"; return; }
  done
}

uninstall() {
  say "Removing Pixregie"
  systemctl disable --now pixregie-display 2>/dev/null || true
  rm -f "$UNIT" "$AVAHI" "$BIN"
  systemctl daemon-reload
  systemctl enable getty@tty1 2>/dev/null || true
  say "Kept $CONF_DIR (tokens, password) and the HDMI settings in $(cmdline_file). Delete them by hand if you want."
  exit 0
}

add_agent() {
  local label="$1"
  [ -f "$CONF" ] || die "not installed yet: run without --add-agent first"
  say "Token for \"$label\" (shown once, copy it now):"
  as_svc "$BIN" add-agent -config "$CONF" "$label" | sed -n 2p
  systemctl restart pixregie-display
}

case "${1:-}" in
  --uninstall) uninstall ;;
  --add-agent) [ -n "${2:-}" ] || die "usage: --add-agent LABEL"; add_agent "$2"; exit 0 ;;
  "") ;;
  *) die "unknown option: $1" ;;
esac

# --- binary -----------------------------------------------------------------
case "$(uname -m)" in
  aarch64) ASSET="pixregie-display-linux-arm64" ;;
  armv6l|armv7l) ASSET="pixregie-display-linux-arm" ;;
  *) die "unsupported CPU: $(uname -m)" ;;
esac
if [ "$VERSION" = latest ]; then
  BASE="https://github.com/$REPO/releases/latest/download"
else
  BASE="https://github.com/$REPO/releases/download/$VERSION"
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
say "Downloading $ASSET ($VERSION)"
curl -fsSL -o "$TMP/$ASSET" "$BASE/$ASSET"
curl -fsSL -o "$TMP/SHA256SUMS" "$BASE/SHA256SUMS"
(cd "$TMP" && grep " $ASSET\$" SHA256SUMS | sha256sum -c --quiet -) || die "checksum mismatch, aborting"
install -m 0755 "$TMP/$ASSET" "$BIN"
say "Installed $("$BIN" version)"

# --- service user and configuration -----------------------------------------
if ! id "$SVC_USER" >/dev/null 2>&1; then
  say "Creating system user $SVC_USER"
  useradd --system --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin "$SVC_USER"
fi
usermod -a -G video,tty "$SVC_USER"
install -d -m 0750 -o "$SVC_USER" -g "$SVC_USER" "$CONF_DIR"

FIRST_RUN=0
if [ ! -f "$CONF" ]; then
  FIRST_RUN=1
  say "Debug page password (opens http://<pi>:$PORT from your Mac)"
  while :; do
    read -r -s -p "  password (10+ chars): " pw1 </dev/tty; echo
    read -r -s -p "  again: " pw2 </dev/tty; echo
    if [ "$pw1" != "$pw2" ]; then warn "passwords differ"; continue; fi
    if [ "${#pw1}" -lt 10 ]; then warn "too short"; continue; fi
    break
  done
  printf '%s\n' "$pw1" | as_svc "$BIN" set-password -config "$CONF" >/dev/null 2>&1 \
    || die "could not save the password"
  unset pw1 pw2
fi

# --- screen: native resolution, no blanking, no console cursor --------------
# The picture adapts to the screen (1280x720, 1920x1080 and integer
# multiples are pixel-perfect), so the HDMI mode is left to the monitor.
CMDLINE="$(cmdline_file)"
NEED_REBOOT=0
if [ -n "$CMDLINE" ]; then
  # v0.1.0 forced 1280x720: undo it.
  if grep -q ' video=HDMI-A-1:1280x720@60' "$CMDLINE"; then
    sed -i 's| video=HDMI-A-1:1280x720@60||' "$CMDLINE"
    NEED_REBOOT=1
  fi
  for opt in "consoleblank=0" "vt.global_cursor_default=0"; do
    if ! grep -qw -- "${opt%%=*}=[^ ]*" "$CMDLINE"; then
      cp -n "$CMDLINE" "$CMDLINE.pixregie-backup"
      sed -i "1 s|\$| $opt|" "$CMDLINE"
      NEED_REBOOT=1
    fi
  done
fi
# The login prompt on tty1 would flicker over the picture. tty2 stays
# available (Ctrl+Alt+F2) with a keyboard.
systemctl disable --now getty@tty1 >/dev/null 2>&1 || true

# --- service ------------------------------------------------------------------
cat >"$UNIT" <<EOF
[Unit]
Description=Pixregie display (Claude Code sessions board)
After=network-online.target
Wants=network-online.target

[Service]
User=$SVC_USER
Group=$SVC_USER
SupplementaryGroups=video tty
ExecStart=$BIN serve -config $CONF -fb /dev/fb0
Restart=always
RestartSec=3

# Hardening: the service only needs its config, the framebuffer and tty1.
NoNewPrivileges=yes
CapabilityBoundingSet=
AmbientCapabilities=
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
# Pairing by code adds agents to the config at run time.
ReadWritePaths=$CONF_DIR
DevicePolicy=closed
DeviceAllow=/dev/fb0 rw
DeviceAllow=/dev/tty1 w
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
ProtectClock=yes
ProtectHostname=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallArchitectures=native
SystemCallFilter=@system-service
UMask=0077

[Install]
WantedBy=multi-user.target
EOF

# --- mDNS: agents find the board by service type, not by host name ----------
if [ -d /etc/avahi/services ]; then
  cat >"$AVAHI" <<EOF
<?xml version="1.0" standalone='no'?>
<!DOCTYPE service-group SYSTEM "avahi-service.dtd">
<service-group>
  <name replace-wildcards="yes">Pixregie on %h</name>
  <service>
    <type>_pixregie._tcp</type>
    <port>$PORT</port>
  </service>
</service-group>
EOF
else
  warn "avahi not installed: agents will need the Pi's address"
fi

# --- automatic security updates ----------------------------------------------
if ! dpkg -s unattended-upgrades >/dev/null 2>&1; then
  say "Enabling automatic security updates"
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq unattended-upgrades >/dev/null
fi

systemctl daemon-reload
systemctl enable --now pixregie-display >/dev/null
systemctl restart pixregie-display

IP="$(hostname -I | awk '{print $1}')"
if [ "$FIRST_RUN" -eq 1 ]; then
  say "Token for your Mac (shown once, copy it now):"
  as_svc "$BIN" add-agent -config "$CONF" "Mac" | sed -n 2p
  systemctl restart pixregie-display
fi

echo
say "Done. Debug page: http://$IP:$PORT  (any user name, your password)"
say "Another machine: sudo bash install-pi.sh --add-agent PC"
if [ "$NEED_REBOOT" -eq 1 ]; then
  warn "Reboot once to apply the screen settings: sudo reboot"
fi
