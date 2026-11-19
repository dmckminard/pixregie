# Pixregie

**A pixel-art control room for your Claude Code sessions.**

Pixregie turns a Raspberry Pi and a spare screen into a passive status board:
every Claude Code session running on your machines (Mac, Windows, Linux
servers) shows up as a little desk whose screen tells you, at a glance,
whether it is working, waiting for you, idle or stuck.

It only watches. It never sends prompts, approves tools or touches your
Claude account.

> Status: early development. Not usable yet.

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

## Try the agent

Requires Go 1.24+.

```sh
go run ./cmd/pixregie-agent scan
```

## License

MIT. Pixregie is an independent project, not affiliated with Anthropic.
"Claude" and "Claude Code" are trademarks of Anthropic.
