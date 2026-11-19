# Pixregie

**A pixel-art control room for your Claude Code sessions.**

Pixregie turns a Raspberry Pi and a spare screen into a passive status board.
Every Claude Code session running on your machines shows up as an operator at
a desk, and the desk's screen tells you at a glance whether the session is
working, waiting for you, finished or stuck.

![The board, with demo data](docs/preview.png)

It only watches. It never sends prompts, approves tools or touches your
Claude account, and it consumes no tokens.

## How it works

```
 Mac / Windows                                  Raspberry Pi
 ┌──────────────────────────┐    LAN, HTTP     ┌──────────────────────────┐
 │ Pixregie (menu-bar app)  │ ───────────────▶ │ pixregie-display         │
 │  reads ~/.claude/projects│  every 3 s       │  collector + screen      │
 │  + 3 Claude Code hooks   │                  │  + debug page            │
 └──────────────────────────┘                  └────────────┬─────────────┘
                                                            │ SSH tunnel opened
 Server (VPS, VS Code Remote)                               │ by the Pi, can only
 ┌──────────────────────────┐   read-only, 127.0.0.1 only   │ reach 127.0.0.1:7423
 │ pixregie-agent serve     │ ◀─────────────────────────────┘
 └──────────────────────────┘
```

- The agent reads Claude Code's local session files and keeps **metadata
  only**: project folder name, git branch, session title, current tool name,
  status and timestamps.
- Prompts, commands, tool output and file contents **never leave the
  machine**. The session title is the one exception derived from the
  conversation (it is the name Claude Code shows for the session); it only
  travels to the Pi on your local network.
- Three Claude Code hooks tell the agent when a session opens, closes, or
  waits for a permission. They only carry the session id.
- Machines on your network find the Pi with mDNS (`_pixregie._tcp`) and
  push to it with a per-machine token. Servers elsewhere cannot reach the
  Pi, so the Pi pulls from them through an SSH tunnel it opens itself.

## Statuses

| On the screen                 | Meaning                                         |
|-------------------------------|-------------------------------------------------|
| green screen, scrolling lines | Claude is writing or a tool is running          |
| amber screen + `!` bubble     | Claude Code waits for your permission           |
| blinking red screen           | was working, nothing moved for 10 minutes       |
| dark screen, eyes closed      | the turn is over: your move                     |

A room's **ON AIR** light is on while any of its sessions works. Closed
sessions disappear; idle sessions not known to be open are hidden after an
hour. Lamps turn on at night (19:00 to 07:00). After 30 minutes without
activity the screen goes to sleep, and wakes up as soon as a session works
or waits (`"sleep_after"` in `/etc/pixregie/display.json`, `"0"` to
disable). The picture moves by two pixels every ten minutes so that static
parts do not mark the panel.

## Install

You need a Raspberry Pi (3B or newer) on the same network as your machines,
a screen on its HDMI port, and Claude Code on the machines to watch.

### 1. The Raspberry Pi

Flash **Raspberry Pi OS Lite (64-bit)**, enable SSH, then on the Pi:

```sh
curl -fsSLO https://github.com/dmckminard/pixregie/releases/latest/download/install-pi.sh
sudo bash install-pi.sh
sudo reboot
```

The script asks for a password for the debug page, then:

- installs a hardened systemd service and announces the board on the
  network;
- offers **SSH by key only** (only if your account already has a key, and
  checked with `sshd -t` first, so it cannot lock you out). From your Mac:
  `ssh-copy-id you@pi.local`, then `sudo bash install-pi.sh --harden-ssh`;
- **protects the SD card**: logs in RAM, no swap file, no access-time
  writes;
- turns on Debian security updates. Automatic Pixregie updates are
  available but **off by default** (`--auto-update on`): an automatic
  update runs new code as root without you.

The picture adapts to the screen: native 1080p, 720p, and pixel-perfect
upscales for 1440p and 4K.

### 2. macOS

```sh
curl -fsSL https://github.com/dmckminard/pixregie/releases/latest/download/install-mac.sh | bash
```

The script downloads the latest `Pixregie.app`, checks it against the
release's signed checksums, installs it in Applications and starts it. The app is not
notarised yet; installed this way macOS does not block it. (Downloading the
zip with a browser works too, but macOS then asks you to allow it in
System Settings › Privacy & Security.)

On first launch, allow **local network access** when macOS asks.

### 3. Windows

Download [`Pixregie.exe`](https://github.com/dmckminard/pixregie/releases/latest/download/Pixregie.exe),
put it where you like and run it. SmartScreen warns about an unknown
publisher: *More info › Run anyway*.

### 4. Pair each machine

On first launch the app finds the Pi, and **the Pi's screen shows a
six-digit code**. Type it in the app's window: done. The code expires after
2 minutes and three wrong tries cancel it.

The app then stores its token in the Keychain (macOS) or encrypted with
DPAPI (Windows), installs its Claude Code hooks, and turns on **launch at
login**.

If the Pi cannot be found (another network, mDNS blocked), the app offers
to paste a token instead. Create one on the Pi with
`sudo bash install-pi.sh --add-agent NAME`.

### Servers (VPS, VS Code Remote SSH)

Sessions started on a server, including through VS Code Remote SSH, run
on the server: they need an agent there. On the Pi:

```sh
sudo bash install-pi.sh --add-server VPS admin@my-server.example
```

It prints the command to run on the server, asks for the token the server
prints back, shows the server's SSH key fingerprint for you to confirm,
and starts the tunnel. On the server, the installer only adds the agent
(read-only, localhost only), the hooks for the given user and a
`pixregie-tunnel` account with no shell whose key may only reach the
agent. It does not touch `sshd_config`.

### Linux machines on your network (headless)

```sh
curl -fsSLo pixregie-agent https://github.com/dmckminard/pixregie/releases/latest/download/pixregie-agent-linux-amd64
chmod +x pixregie-agent
./pixregie-agent pair          # type the code shown on the Pi
./pixregie-agent hooks install
./pixregie-agent run
```

## Everyday use

The menu-bar (macOS) or tray (Windows) icon shows the most urgent state.
Its menu:

| Item                     | What it does                                          |
|--------------------------|-------------------------------------------------------|
| status line              | connected to the Pi, or what is wrong                 |
| counters                 | working, waiting and stuck sessions on this machine   |
| Rafraîchir               | rescan sessions now and look for the Pi again         |
| Ouvrir la page de debug  | the Pi's page: live picture of the screen and data    |
| Lancer au démarrage      | launch at login; your choice is remembered            |
| Associer à nouveau…      | pair again (new Pi, or after a reset)                 |
| Mettre à jour vers vX    | shown when a new version is out; installs it          |
| Quitter Pixregie         | quit                                                  |

Only one copy of the app runs at a time.

### Managing machines (on the Pi)

```sh
sudo bash install-pi.sh --agents                     # paired machines and servers
sudo bash install-pi.sh --rename-agent OLD NEW       # quote names with spaces
sudo bash install-pi.sh --remove-agent NAME          # revoke: its token stops working at once
sudo bash install-pi.sh --remove-server NAME
```

Changes apply immediately, without restarting anything.

## Update

- **Pi and servers**: re-run the installer; the configuration is kept.
  Or turn on daily signed updates: `sudo bash install-pi.sh --auto-update on`
  on the Pi, `--auto-update` when installing a server.
- **macOS and Windows**: the app checks every few hours and shows
  *Mettre à jour vers vX* in its menu; one click installs it and restarts
  the app. The install command and a fresh `Pixregie.exe` also work.

Pairing is kept across updates.

## Uninstall

- **Pi**: `sudo bash install-pi.sh --uninstall` (keeps `/etc/pixregie`;
  delete it to forget passwords, paired machines and the tunnel key).
- **Server**: `sudo bash install-server.sh --user USER --uninstall`, then on
  the Pi `--remove-server NAME`.
- **macOS**:
  ```sh
  /Applications/Pixregie.app/Contents/MacOS/pixregie-agent hooks uninstall
  rm -f ~/Library/LaunchAgents/fr.pixregie.agent.plist
  rm -rf /Applications/Pixregie.app "$HOME/Library/Application Support/Pixregie"
  security delete-generic-password -s fr.pixregie.agent
  ```
- **Windows**: untick *Lancer au démarrage*, run
  `Pixregie.exe hooks uninstall`, then delete `Pixregie.exe` and
  `%AppData%\Pixregie`.

`hooks uninstall` removes only Pixregie's entries from
`~/.claude/settings.json`; the original file was saved once as
`settings.json.pixregie-backup`.

## Troubleshooting

| Symptom                                  | Fix                                                                 |
|------------------------------------------|---------------------------------------------------------------------|
| “Pi introuvable sur le réseau”           | Same network? On macOS, allow local network access (System Settings › Privacy & Security › Local Network). Otherwise pair with a token. |
| “Le Pi doit être mis à jour”             | Update the Pi (see *Update*).                                       |
| “Token refusé par le Pi”                 | *Associer à nouveau…*                                               |
| A closed session stays on the screen     | It was opened before the hooks were installed: it disappears after an hour idle. |
| Black borders on the Pi's screen         | Reboot once after installing.                                       |
| A server's room never appears            | On the Pi: `journalctl -u 'pixregie-tunnel@*' -n 20`; on the server: `journalctl -u pixregie-agent -n 20`. |

The debug page (`http://<pi-address>:7421`, any user name and your
password) shows the live screen and what each machine reports.

## Security

- **Signed releases**: `SHA256SUMS` is signed with an SSH ed25519 key
  ([`release.pub`](release.pub)). The installers (`ssh-keygen -Y verify`),
  the Pi and server updaters and the app's updater all refuse a release
  whose signature does not verify. To check by hand:
  ```sh
  printf 'pixregie-release namespaces="pixregie-release" %s\n' "$(cat release.pub)" > allowed
  ssh-keygen -Y verify -f allowed -I pixregie-release -n pixregie-release -s SHA256SUMS.sig < SHA256SUMS
  ```
- **Servers**: the Pi's tunnel key is restricted on the server to
  `restrict,port-forwarding,permitopen="127.0.0.1:7423"`: no shell, no
  command, no other port. The agent there only answers read-only requests
  on loopback, carries its token as a systemd credential and cannot open
  connections outside localhost.
- The agent's only network traffic is to the Pi; its hook endpoint listens
  on `127.0.0.1` and refuses browser requests.
- Tokens are random (256 bits), stored hashed on the Pi; the debug password
  is stored with PBKDF2. A leaked Pi configuration reveals no secret.
- The Pi takes a machine's name from its token, so one machine cannot
  report as another. Reports are size-limited and sanitised.
- The display service runs as a dedicated user under systemd sandboxing
  (no capabilities, read-only system, access to the framebuffer only).

## Development

Go 1.24+.

```sh
go test ./...
go run ./cmd/pixregie-agent scan                          # what the agent sees
go run ./cmd/pixregie-display render -scale 1.5 -o out.png # the screen, demo data
```

- `cmd/pixregie-agent`: menu-bar app (macOS, Windows) and CLI.
- `cmd/pixregie-display`: the Pi service.
- `internal/session`: session file parser; `internal/hooks`: Claude Code
  hooks; `internal/screen`: the pixel-art renderer; `internal/collector`:
  the Pi's API and pairing.
- Fonts (Silkscreen, VT323, OFL) are pre-rasterised by `tools/fontgen`.

Releases are built by GitHub Actions: bumping `VERSION` on `main` publishes
`v<VERSION>` with every binary, `SHA256SUMS` and its signature. The
private key exists only as the `RELEASE_SIGNING_KEY` secret; if it is ever
lost or leaked, generate a new one, replace `release.pub` (a test checks
every copy of it) and reinstall the machines once.

## License

MIT. Pixregie is an independent project, not affiliated with Anthropic.
"Claude" and "Claude Code" are trademarks of Anthropic.
