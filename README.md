# Pixregie

**A pixel-art control room for your Claude Code sessions.**

Pixregie turns a Raspberry Pi and a spare screen into a passive status board:
every Claude Code session running on your machines (Mac, Windows, Linux
servers) shows up as a little desk whose screen tells you, at a glance,
whether it is working, waiting for you, idle or stuck.

It only watches. It never sends prompts, approves tools or touches your
Claude account.

> Status: early development.

## How it works

```
 Mac / Windows                     Linux server (VPS)
 ┌──────────────────┐              ┌──────────────────────┐
 │ pixregie-agent   │              │ pixregie-agent       │
 │ (tray icon)      │              │ (systemd, 127.0.0.1) │
 └────────┬─────────┘              └──────────▲───────────┘
          │ push over LAN (mDNS)              │ pulled through a
          ▼                                   │ locked-down SSH tunnel
 ┌────────────────────────────────────────────┴───┐
 │ Raspberry Pi: pixregie-display                 │
 │ collector + pixel-art screen + debug page      │
 └────────────────────────────────────────────────┘
```

- Agents read Claude Code's local transcripts (`~/.claude/projects`) and
  reduce each session to **metadata only**: project folder name, git branch,
  current tool name, status, timestamps.
- Prompts, commands, tool output and file contents **never leave the machine**.
- No Claude login, no API key, no tokens consumed.

## Statuses

| Status    | Meaning                                         |
|-----------|-------------------------------------------------|
| `working` | the model is generating or a tool is running    |
| `waiting` | Claude Code needs you (permission, question)    |
| `idle`    | the turn is over                                |
| `stale`   | was working, but nothing moved for a long time  |

## Install on the Raspberry Pi

Raspberry Pi OS Lite (64-bit), on the same network as your machines:

```sh
curl -fsSLO https://github.com/dmckminard/pixregie/releases/latest/download/install-pi.sh
sudo bash install-pi.sh
```

The script installs a hardened systemd service, hides the console,
asks for the debug page password and prints a token for your first machine.
Re-run it to upgrade; `--add-agent NAME` creates a token for another machine.

## Install the agent

**macOS** (install or update):

```sh
curl -fsSL https://raw.githubusercontent.com/dmckminard/pixregie/main/install/mac/install.sh | bash
```

The app is not notarised yet; installed this way (checksum verified),
macOS does not block it. **Windows**: run `Pixregie.exe`.

On first launch the app asks for the token printed by the Pi installer,
stores it in the Keychain (macOS) or encrypted with DPAPI (Windows), finds
the Pi on the network by itself and starts at login.

It also adds three hooks to Claude Code (`SessionStart`, `SessionEnd`,
`Notification` in `~/.claude/settings.json`, original saved as
`settings.json.pixregie-backup`). They send the session id to the agent on
`127.0.0.1` so the board knows which sessions are open and which one waits
for a permission. `pixregie-agent hooks uninstall` removes them.

**Linux servers**: `pixregie-agent pair` (paste the token), then
`pixregie-agent run`.

## Preview the screen without a Pi

```sh
go run ./cmd/pixregie-display render -o preview.png
```

## License

MIT. Pixregie is an independent project, not affiliated with Anthropic.
"Claude" and "Claude Code" are trademarks of Anthropic.
