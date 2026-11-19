#!/usr/bin/env bash
# Pixregie — Raspberry Pi installer (Raspberry Pi OS Lite, Bookworm or later).
#
#   curl -fsSLO https://github.com/dmckminard/pixregie/releases/latest/download/install-pi.sh
#   sudo bash install-pi.sh                            install or upgrade
#
#   sudo bash install-pi.sh --agents                   paired machines and servers
#   sudo bash install-pi.sh --remove-agent NAME        revoke a machine
#   sudo bash install-pi.sh --rename-agent OLD NEW
#   sudo bash install-pi.sh --add-agent NAME           token for a machine that cannot pair by code
#   sudo bash install-pi.sh --add-server NAME user@host[:port]   watch a server through SSH
#   sudo bash install-pi.sh --remove-server NAME
#   sudo bash install-pi.sh --harden-ssh               SSH by key only (asked during install)
#   sudo bash install-pi.sh --uninstall
#
# Idempotent: re-running upgrades and keeps the configuration. Every download
# is checked against SHA256SUMS signed with the Pixregie release key.
set -euo pipefail

REPO="dmckminard/pixregie"
RELEASE_KEY="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPrsmtv1WZ/gg6Fg2dz99VsjIpErQ0acaMFV1hBaFQLo pixregie-release"
VERSION="${PIXREGIE_VERSION:-latest}"
SVC_USER="pixregie-display"
TUN_USER="pixregie-tunnel"
BIN="/usr/local/bin/pixregie-display"
UPDATER="/usr/local/sbin/pixregie-update"
CONF_DIR="/etc/pixregie"
CONF="$CONF_DIR/display.json"
TUN_DIR="$CONF_DIR/tunnel"
UNIT="/etc/systemd/system/pixregie-display.service"
AVAHI="/etc/avahi/services/pixregie.service"
PORT=7421

say()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mxx\033[0m %s\n' "$*" >&2; exit 1; }
ask()  { local a; read -r -p "$1 " a </dev/tty; printf '%s' "$a"; }

[ "$(id -u)" -eq 0 ] || die "run with sudo"

as_svc() { runuser -u "$SVC_USER" -- "$@"; }
need_installed() { [ -x "$BIN" ] && [ -f "$CONF" ] || die "not installed yet: run sudo bash install-pi.sh first"; }

cmdline_file() {
  for f in /boot/firmware/cmdline.txt /boot/cmdline.txt; do
    [ -f "$f" ] && { echo "$f"; return; }
  done
}

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

# fetch ASSET...: downloads into $TMP, all checked against the signed list.
fetch() {
  [ -f "$TMP/SHA256SUMS" ] || {
    curl -fsSL -o "$TMP/SHA256SUMS" "$BASE/SHA256SUMS"
    curl -fsSL -o "$TMP/SHA256SUMS.sig" "$BASE/SHA256SUMS.sig" || die "this release is not signed"
    printf 'pixregie-release namespaces="pixregie-release" %s\n' "$RELEASE_KEY" > "$TMP/allowed_signers"
    ssh-keygen -Y verify -f "$TMP/allowed_signers" -I pixregie-release -n pixregie-release \
      -s "$TMP/SHA256SUMS.sig" < "$TMP/SHA256SUMS" >/dev/null 2>&1 \
      || die "release signature is NOT valid, aborting"
  }
  local a
  for a in "$@"; do
    curl -fsSL -o "$TMP/$a" "$BASE/$a"
    (cd "$TMP" && grep " $a\$" SHA256SUMS | sha256sum -c --quiet -) || die "checksum mismatch for $a, aborting"
  done
}

# --- machines and servers ----------------------------------------------------

uninstall() {
  say "Removing Pixregie"
  systemctl disable --now pixregie-display 2>/dev/null || true
  for u in /etc/systemd/system/pixregie-tunnel@*.service; do
    [ -e "$u" ] && systemctl disable --now "$(basename "$u")" 2>/dev/null || true
  done
  rm -f "$UNIT" "$AVAHI" "$BIN" \
        /etc/systemd/system/pixregie-tunnel@.service /etc/udev/rules.d/70-pixregie-fb.rules
  systemctl daemon-reload
  systemctl enable getty@tty1 2>/dev/null || true
  say "Kept $CONF_DIR (paired machines, password, tunnel key). Delete it by hand to forget them."
  exit 0
}

add_agent() {
  need_installed
  say "Token for \"$1\" (shown once, copy it now):"
  as_svc "$BIN" add-agent -config "$CONF" "$1" | sed -n 2p
}

tunnel_unit() {
  cat >/etc/systemd/system/pixregie-tunnel@.service <<EOF
[Unit]
Description=Pixregie SSH tunnel to server %I
After=network-online.target
Wants=network-online.target

[Service]
User=$TUN_USER
EnvironmentFile=$TUN_DIR/%i.env
# Local forward only: the server cannot reach the Pi through it, and the
# server-side key options only allow 127.0.0.1:7423 (the read-only agent).
ExecStart=/usr/bin/ssh -N -T -p \${PORT} -i $TUN_DIR/id_ed25519 \\
  -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes \\
  -o UserKnownHostsFile=$TUN_DIR/known_hosts -o ExitOnForwardFailure=yes \\
  -o ServerAliveInterval=30 -o ServerAliveCountMax=3 \\
  -L 127.0.0.1:\${LOCAL_PORT}:127.0.0.1:7423 $TUN_USER@\${HOST}
Restart=always
RestartSec=15

NoNewPrivileges=yes
CapabilityBoundingSet=
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
ReadOnlyPaths=$TUN_DIR
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes

[Install]
WantedBy=multi-user.target
EOF
}

add_server() {
  need_installed
  local label="$1" target="$2" host port lport name token fp
  host="${target%%:*}"; port=22
  [ "$host" != "$target" ] && port="${target##*:}"
  host="${host#*@}" # the tunnel always logs in as $TUN_USER
  [[ "$label" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$ ]] || die "NAME: letters, digits, . _ - (no spaces)"
  [[ "$port" =~ ^[0-9]+$ ]] || die "bad port in $target"

  if ! id "$TUN_USER" >/dev/null 2>&1; then
    useradd --system --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin "$TUN_USER"
  fi
  install -d -m 0750 -o root -g "$TUN_USER" "$TUN_DIR"
  if [ ! -f "$TUN_DIR/id_ed25519" ]; then
    ssh-keygen -q -t ed25519 -N "" -C "pixregie-tunnel@$(hostname)" -f "$TUN_DIR/id_ed25519"
  fi
  chown root:"$TUN_USER" "$TUN_DIR/id_ed25519" "$TUN_DIR/id_ed25519.pub"
  chmod 0640 "$TUN_DIR/id_ed25519"

  echo
  say "1/3 On the server ($host), run as an admin (replace USER by the account running Claude Code):"
  echo
  echo "  curl -fsSLO https://github.com/$REPO/releases/latest/download/install-server.sh"
  echo "  sudo bash install-server.sh --user USER --pi-key '$(cat "$TUN_DIR/id_ed25519.pub")'"
  echo
  say "2/3 It prints a server token (pxs_…)."
  token="$(ask "    Paste it here:")"
  [[ "$token" == pxs_* ]] || die "this is not a server token"

  say "3/3 Checking the server's SSH identity"
  ssh-keyscan -T 10 -p "$port" -t ed25519 "$host" 2>/dev/null > "$TMP/hostkey" || true
  [ -s "$TMP/hostkey" ] || die "cannot reach SSH on $host:$port"
  fp="$(ssh-keygen -lf "$TMP/hostkey" | awk '{print $2}')"
  echo "    $host presents the key $fp"
  echo "    (on the server: ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub)"
  [ "$(ask "    Is it the right server? [y/N]")" = y ] || die "aborted"
  touch "$TUN_DIR/known_hosts"
  ssh-keygen -R "[$host]:$port" -f "$TUN_DIR/known_hosts" >/dev/null 2>&1 || true
  ssh-keygen -R "$host" -f "$TUN_DIR/known_hosts" >/dev/null 2>&1 || true
  cat "$TMP/hostkey" >> "$TUN_DIR/known_hosts"
  chown root:"$TUN_USER" "$TUN_DIR/known_hosts"; chmod 0640 "$TUN_DIR/known_hosts"
  rm -f "$TUN_DIR/known_hosts.old"

  # A free local port per server.
  lport=17423
  while grep -qs "LOCAL_PORT=$lport$" "$TUN_DIR"/*.env; do lport=$((lport + 1)); done
  name="$(systemd-escape "$label")"
  [ -f "$TUN_DIR/$name.env" ] && lport="$(sed -n 's/^LOCAL_PORT=//p' "$TUN_DIR/$name.env")"
  printf 'HOST=%s\nPORT=%s\nLOCAL_PORT=%s\n' "$host" "$port" "$lport" > "$TUN_DIR/$name.env"
  chown root:"$TUN_USER" "$TUN_DIR/$name.env"; chmod 0640 "$TUN_DIR/$name.env"

  tunnel_unit
  systemctl daemon-reload
  systemctl enable "pixregie-tunnel@$name.service" >/dev/null
  systemctl restart "pixregie-tunnel@$name.service"
  printf '%s\n' "$token" | as_svc "$BIN" add-server -config "$CONF" "$label" "http://127.0.0.1:$lport" >/dev/null
  sleep 5
  if systemctl is-active --quiet "pixregie-tunnel@$name.service"; then
    say "Server \"$label\" added: its room appears on the screen within seconds."
  else
    warn "The tunnel does not stay up: journalctl -u pixregie-tunnel@$name -n 20"
  fi
}

remove_server() {
  need_installed
  local name
  name="$(systemd-escape "$1")"
  systemctl disable --now "pixregie-tunnel@$name.service" 2>/dev/null || true
  rm -f "$TUN_DIR/$name.env"
  as_svc "$BIN" remove-server -config "$CONF" "$1"
}

# --- SSH by key only ---------------------------------------------------------
# Only offered when the admin account already has a key, and checked with
# sshd -t before reloading, so it cannot lock you out.
harden_ssh() {
  local user="${SUDO_USER:-}" keys
  [ -n "$user" ] && [ "$user" != root ] || { warn "run with sudo from your own account"; return 0; }
  keys="$(getent passwd "$user" | cut -d: -f6)/.ssh/authorized_keys"
  if [ ! -s "$keys" ]; then
    warn "No SSH key for $user yet. From your Mac: ssh-copy-id $user@$(hostname).local"
    warn "then: sudo bash install-pi.sh --harden-ssh"
    return 0
  fi
  cat >/etc/ssh/sshd_config.d/10-pixregie-hardening.conf <<'EOF'
# Pixregie: SSH by key only. Remove this file to allow passwords again.
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin no
EOF
  if sshd -t; then
    systemctl reload ssh 2>/dev/null || systemctl reload sshd
    say "SSH now accepts keys only (your key for $user works)."
  else
    rm -f /etc/ssh/sshd_config.d/10-pixregie-hardening.conf
    warn "sshd rejected the change; nothing modified"
  fi
}

case "${1:-}" in
  --uninstall) uninstall ;;
  --agents) need_installed; as_svc "$BIN" agents -config "$CONF"; exit 0 ;;
  --add-agent) [ -n "${2:-}" ] || die "usage: --add-agent NAME"; add_agent "$2"; exit 0 ;;
  --remove-agent) [ -n "${2:-}" ] || die "usage: --remove-agent NAME"; need_installed; as_svc "$BIN" remove-agent -config "$CONF" "$2"; exit 0 ;;
  --rename-agent) [ -n "${3:-}" ] || die "usage: --rename-agent OLD NEW"; need_installed; as_svc "$BIN" rename-agent -config "$CONF" "$2" "$3"; exit 0 ;;
  --add-server) [ -n "${3:-}" ] || die "usage: --add-server NAME user@host[:port]"; add_server "$2" "$3"; exit 0 ;;
  --remove-server) [ -n "${2:-}" ] || die "usage: --remove-server NAME"; remove_server "$2"; exit 0 ;;
  --harden-ssh) harden_ssh; exit 0 ;;
  "") ;;
  *) die "unknown option: $1" ;;
esac

# --- binaries ------------------------------------------------------------------
say "Downloading $ASSET ($VERSION), checking the release signature"
fetch "$ASSET"
install -m 0755 "$TMP/$ASSET" "$BIN"
# Earlier versions installed an automatic updater: remove it.
systemctl disable --now pixregie-update.timer >/dev/null 2>&1 || true
rm -f "$UPDATER" /etc/systemd/system/pixregie-update.service /etc/systemd/system/pixregie-update.timer
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

# --- screen ------------------------------------------------------------------
# The picture adapts to the screen, so the HDMI mode is left to the monitor.
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
# Let the service put the screen to sleep (framebuffer blank control).
cat >/etc/udev/rules.d/70-pixregie-fb.rules <<'EOF'
SUBSYSTEM=="graphics", KERNEL=="fb[0-9]*", RUN+="/bin/chgrp video /sys%p/blank", RUN+="/bin/chmod g+w /sys%p/blank"
EOF
udevadm control --reload 2>/dev/null || true
udevadm trigger --subsystem-match=graphics 2>/dev/null || true

# --- SD card: fewer writes ---------------------------------------------------
# Logs in RAM (lost at reboot, capped), no swap file, no access-time writes.
# Pixregie itself only writes its config when a machine is paired.
install -d /etc/systemd/journald.conf.d
cat >/etc/systemd/journald.conf.d/pixregie.conf <<'EOF'
[Journal]
Storage=volatile
RuntimeMaxUse=32M
EOF
systemctl restart systemd-journald 2>/dev/null || true
if systemctl list-unit-files dphys-swapfile.service >/dev/null 2>&1 && systemctl is-enabled --quiet dphys-swapfile 2>/dev/null; then
  dphys-swapfile swapoff 2>/dev/null || true
  systemctl disable --now dphys-swapfile >/dev/null 2>&1 || true
  say "Swap file disabled (protects the SD card)"
fi
if awk '$2=="/" && $3=="ext4" && $4 !~ /noatime/ {found=1} END {exit !found}' /etc/fstab; then
  cp -n /etc/fstab /etc/fstab.pixregie-backup
  awk 'BEGIN{OFS="\t"} $2=="/" && $3=="ext4" && $4 !~ /noatime/ {$4=$4",noatime"} {print}' /etc/fstab > "$TMP/fstab"
  cat "$TMP/fstab" > /etc/fstab
  NEED_REBOOT=1
fi

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
# Screen sleep.
ReadWritePaths=-/sys/class/graphics/fb0/blank
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

[ -f /etc/systemd/system/pixregie-tunnel@.service ] && tunnel_unit
systemctl daemon-reload
systemctl enable --now pixregie-display >/dev/null
systemctl restart pixregie-display

# --- SSH ---------------------------------------------------------------------
# Asked once; later: sudo bash install-pi.sh --harden-ssh
if [ ! -f /etc/ssh/sshd_config.d/10-pixregie-hardening.conf ] && [ ! -f "$CONF_DIR/.ssh-asked" ]; then
  touch "$CONF_DIR/.ssh-asked"
  if [ "$(ask "Allow SSH logins by key only (recommended)? [Y/n]")" != n ]; then
    harden_ssh
  fi
fi

IP="$(hostname -I | awk '{print $1}')"
echo
say "Done. Debug page: http://$IP:$PORT  (any user name, your password)"
if [ "$FIRST_RUN" -eq 1 ]; then
  say "Now open the Pixregie app on your Mac or PC: it shows a code on this screen to pair."
fi
say "To update later, re-run this installer."
if [ "$NEED_REBOOT" -eq 1 ]; then
  warn "Reboot once to apply the screen and SD card settings: sudo reboot"
fi
