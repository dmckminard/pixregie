#!/usr/bin/env bash
# Pixregie — server installer (Debian/Ubuntu VPS where Claude Code runs, for
# instance through VS Code Remote SSH).
#
# Normally started from the command printed by the Pi
# (sudo bash install-pi.sh --add-server NAME user@host):
#
#   curl -fsSLO https://github.com/dmckminard/pixregie/releases/latest/download/install-server.sh
#   sudo bash install-server.sh --user USER --pi-key 'ssh-ed25519 AAAA… pixregie-tunnel@pi'
#   sudo bash install-server.sh --uninstall
#
# What it sets up, and nothing more:
#   - pixregie-agent as a systemd service running as USER, read-only on the
#     home folder, listening on 127.0.0.1:7423 only, unable to send anything
#     anywhere but localhost;
#   - Claude Code hooks for USER (session open/closed, permission prompts);
#   - a "pixregie-tunnel" account with no shell and no password, whose only
#     SSH key may open a tunnel to 127.0.0.1:7423 and nothing else;
#   - daily signed updates of the agent.
# sshd_config is not modified.
set -euo pipefail

REPO="dmckminard/pixregie"
RELEASE_KEY="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPrsmtv1WZ/gg6Fg2dz99VsjIpErQ0acaMFV1hBaFQLo pixregie-release"
BIN="/usr/local/bin/pixregie-agent"
UPDATER="/usr/local/sbin/pixregie-update"
CONF_DIR="/etc/pixregie-agent"
TOKEN_FILE="$CONF_DIR/server-token"
TUN_USER="pixregie-tunnel"
TUN_HOME="/var/lib/pixregie-tunnel"
UNIT="/etc/systemd/system/pixregie-agent.service"
BASE="https://github.com/$REPO/releases/latest/download"

say()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mxx\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run with sudo"

USER_NAME="${SUDO_USER:-}"
PI_KEY=""
UNINSTALL=0
while [ $# -gt 0 ]; do
  case "$1" in
    --user) USER_NAME="${2:-}"; shift 2 ;;
    --pi-key) PI_KEY="${2:-}"; shift 2 ;;
    --uninstall) UNINSTALL=1; shift ;;
    *) die "unknown option: $1" ;;
  esac
done
[ -n "$USER_NAME" ] && [ "$USER_NAME" != root ] || die "--user USER: the account running Claude Code"
id "$USER_NAME" >/dev/null 2>&1 || die "no such user: $USER_NAME"

if [ "$UNINSTALL" -eq 1 ]; then
  say "Removing Pixregie"
  runuser -u "$USER_NAME" -- "$BIN" hooks uninstall 2>/dev/null || true
  systemctl disable --now pixregie-agent pixregie-agent-update.timer 2>/dev/null || true
  rm -f "$UNIT" /etc/systemd/system/pixregie-agent-update.{service,timer} "$BIN" "$UPDATER"
  systemctl daemon-reload
  userdel "$TUN_USER" 2>/dev/null || true
  rm -rf "$TUN_HOME" "$CONF_DIR" /var/lib/pixregie-agent
  say "Done. On the Pi: sudo bash install-pi.sh --remove-server NAME"
  exit 0
fi

case "$(uname -m)" in
  x86_64) ASSET="pixregie-agent-linux-amd64" ;;
  aarch64) ASSET="pixregie-agent-linux-arm64" ;;
  *) die "unsupported CPU: $(uname -m)" ;;
esac
command -v ssh-keygen >/dev/null || die "ssh-keygen is required (openssh-client)"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# --- signed download ---------------------------------------------------------
say "Downloading $ASSET, checking the release signature"
for f in SHA256SUMS SHA256SUMS.sig "$ASSET" pixregie-update.sh; do
  curl -fsSL -o "$TMP/$f" "$BASE/$f"
done
printf 'pixregie-release namespaces="pixregie-release" %s\n' "$RELEASE_KEY" > "$TMP/allowed_signers"
ssh-keygen -Y verify -f "$TMP/allowed_signers" -I pixregie-release -n pixregie-release \
  -s "$TMP/SHA256SUMS.sig" < "$TMP/SHA256SUMS" >/dev/null 2>&1 || die "release signature is NOT valid, aborting"
for f in "$ASSET" pixregie-update.sh; do
  (cd "$TMP" && grep " $f\$" SHA256SUMS | sha256sum -c --quiet -) || die "checksum mismatch for $f"
done
install -m 0755 "$TMP/$ASSET" "$BIN"
install -m 0755 "$TMP/pixregie-update.sh" "$UPDATER"
say "Installed $("$BIN" version)"

# --- token the Pi presents ------------------------------------------------------
install -d -m 0700 "$CONF_DIR"
NEW_TOKEN=0
if [ ! -s "$TOKEN_FILE" ]; then
  umask 077
  printf 'pxs_%s\n' "$(head -c 32 /dev/urandom | base64 | tr '+/' '-_' | tr -d '=\n')" > "$TOKEN_FILE"
  NEW_TOKEN=1
fi
chmod 0600 "$TOKEN_FILE"

# --- agent service -------------------------------------------------------------
cat >"$UNIT" <<EOF
[Unit]
Description=Pixregie agent (Claude Code sessions of $USER_NAME, read by the Pi)
After=network.target

[Service]
User=$USER_NAME
ExecStart=$BIN serve -listen 127.0.0.1:7423
# The token reaches the service without being readable by $USER_NAME's files.
LoadCredential=server-token:$TOKEN_FILE
StateDirectory=pixregie-agent
Restart=always
RestartSec=5

# Hardening: reads ~/.claude, talks to localhost only.
NoNewPrivileges=yes
CapabilityBoundingSet=
AmbientCapabilities=
ProtectSystem=strict
ProtectHome=read-only
PrivateTmp=yes
PrivateDevices=yes
IPAddressDeny=any
IPAddressAllow=localhost
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
ProtectClock=yes
ProtectHostname=yes
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

cat >/etc/systemd/system/pixregie-agent-update.service <<EOF
[Unit]
Description=Pixregie agent update
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=$UPDATER $ASSET $BIN pixregie-agent
EOF
cat >/etc/systemd/system/pixregie-agent-update.timer <<'EOF'
[Unit]
Description=Daily Pixregie agent update

[Timer]
OnCalendar=daily
RandomizedDelaySec=4h
Persistent=true

[Install]
WantedBy=timers.target
EOF

say "Adding Claude Code hooks for $USER_NAME"
runuser -u "$USER_NAME" -- "$BIN" hooks install

systemctl daemon-reload
systemctl enable --now pixregie-agent pixregie-agent-update.timer >/dev/null
systemctl restart pixregie-agent

# --- tunnel account ---------------------------------------------------------------
if [ -n "$PI_KEY" ]; then
  [[ "$PI_KEY" != *$'\n'* && "$PI_KEY" =~ ^ssh-ed25519\ [A-Za-z0-9+/=]+(\ [^\"]*)?$ ]] || die "--pi-key must be the Pi's ssh-ed25519 public key"
  if ! id "$TUN_USER" >/dev/null 2>&1; then
    useradd --system --home-dir "$TUN_HOME" --shell /usr/sbin/nologin "$TUN_USER"
  fi
  passwd -l "$TUN_USER" >/dev/null 2>&1 || true
  # Owned by root: the account cannot change its own key or options.
  install -d -m 0755 -o root -g root "$TUN_HOME" "$TUN_HOME/.ssh"
  printf 'restrict,port-forwarding,permitopen="127.0.0.1:7423",command="/bin/false" %s\n' "$PI_KEY" \
    > "$TUN_HOME/.ssh/authorized_keys"
  chmod 0644 "$TUN_HOME/.ssh/authorized_keys"
  say "Tunnel account ready: the Pi's key may only reach 127.0.0.1:7423"
elif [ ! -s "$TUN_HOME/.ssh/authorized_keys" ]; then
  warn "No --pi-key: the Pi cannot connect yet. Re-run with the command printed by the Pi."
fi

# --- checks that would silently break the tunnel ---------------------------------
if command -v sshd >/dev/null; then
  eff="$(sshd -T -C "user=$TUN_USER,host=pi,addr=127.0.0.1" 2>/dev/null || true)"
  case "$(printf '%s\n' "$eff" | awk '$1=="allowtcpforwarding"{print $2}')" in
    no|remote) warn "sshd has AllowTcpForwarding off for $TUN_USER: allow 'local' for it (Match User $TUN_USER)" ;;
  esac
  for k in allowusers allowgroups; do
    v="$(printf '%s\n' "$eff" | awk -v k="$k" '$1==k{$1=""; print}')"
    [ -n "$v" ] && warn "sshd restricts logins ($k:$v): add $TUN_USER there, or the Pi will be refused"
  done
fi

sleep 1
if ! systemctl is-active --quiet pixregie-agent; then
  warn "The agent does not run: journalctl -u pixregie-agent -n 20"
fi

echo
if [ "$NEW_TOKEN" -eq 1 ]; then
  say "New server token, to paste on the Pi:"
else
  say "Server token (unchanged), to paste on the Pi:"
fi
echo
cat "$TOKEN_FILE"
echo
say "SSH host key of this server, to compare with what the Pi shows:"
ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub 2>/dev/null | awk '{print "    " $2}'
