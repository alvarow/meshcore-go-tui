# meshcore-go-tui

A terminal UI companion client for [MeshCore](https://github.com/ripplebiz/MeshCore) mesh radio nodes,
built in Go using the [meshcore-go](https://github.com/meshcore-go/meshcore-go) companion SDK and
[BubbleTea](https://github.com/charmbracelet/bubbletea) for the TUI.

Connects to a MeshCore node over **Bluetooth LE**, **Serial**, or **TCP**.
Runs on Linux today; macOS and Windows support is a planned next step.

## Features

- Tab-based TUI: Chat · Channels · Nodes · Device
- BLE transport via Nordic UART Service (NUS) with auto-reconnect and MTU negotiation
- Interactive BLE device picker at startup (scan + select from live list)
- Serial and TCP transports
- Full session lifecycle: device query, contact sync, channel sync on connect
- Live push events: inbound messages, delivery ACKs, peer discovery, contact changes
- Scrollable message threads with timestamps and delivery status (… / ✓ 42ms / ✗)
- Contact freshness dots: green/yellow/red based on time since last advert
- Join and leave channels with optional PSK; well-known "Public" channel secret built in
- Send Advert (`Ctrl+A`) to re-announce yourself to the mesh
- Desktop notifications via `notify-send` for messages received on background tabs
- Unread message badges per tab
- Reconnect spinner in status bar on BLE disconnect
- Auto-dismiss error bar (5 seconds)
- Message persistence: full history in `~/.local/share/meshcore/messages.db` (bbolt)
- Config profiles (`~/.config/meshcore/config.toml`)
- Cross-compile targets for Linux, macOS, Windows

## Requirements

- Go 1.22+
- Linux: BlueZ (`bluez` package) for BLE
- macOS: CoreBluetooth (built-in, CGo required)
- Windows: WinRT Bluetooth API (built-in, CGo required)

> **No Bluetooth on your machine?** Use serial or TCP instead — see Usage below.
> The app prints helpful instructions if BlueZ is not available.

## Build

```bash
make            # build for current platform → ./meshcore-tui
make build-all  # cross-compile all targets
```

## Usage

```bash
# BLE scan — interactive picker when no device is configured
./meshcore-tui

# BLE — direct address (skips scan)
./meshcore-tui --device AA:BB:CC:DD:EE:FF

# Serial (no Bluetooth needed)
./meshcore-tui --transport serial --device /dev/ttyUSB0

# TCP
./meshcore-tui --transport tcp --device 192.168.1.100:3000

# Named profile from config
./meshcore-tui --profile home

# Print version and exit
./meshcore-tui --version
```

## Configuration

> **Quick reference — all config keys**
>
> | Key | Type | Default | Purpose |
> |-----|------|---------|---------|
> | `default_transport` | `"ble"` \| `"serial"` \| `"tcp"` | `"ble"` | Transport to use when `--transport` is not set |
> | `default_device` | string | `""` | Device address/path; empty triggers BLE scan picker |
> | `scan_name_filter` | string | `""` | BLE name substring filter; `""` uses built-in `"mesh"` default |
> | `[profile.<name>]` | section | — | Named profile with its own `transport` + `device` |

Config file location:

| Platform | Path |
|----------|------|
| Linux | `~/.config/meshcore/config.toml` |
| macOS | `~/Library/Application Support/meshcore/config.toml` |
| Windows | `%AppData%\meshcore\config.toml` |

The file is created automatically on first `config.Save()` call. If it doesn't exist, all defaults apply.

### Full reference

```toml
# Transport to use when no --transport flag is given.
# Valid values: "ble" (default), "serial", "tcp"
default_transport = "ble"

# Device address / path / host:port to connect to without scanning.
# BLE:    "AA:BB:CC:DD:EE:FF"
# Serial: "/dev/ttyUSB0"  (Linux/macOS)  or  "COM3"  (Windows)
# TCP:    "192.168.1.100:3000"
# Leave empty to trigger the interactive BLE scan picker.
default_device = ""

# BLE scan name filter: only show devices whose advertised name contains
# this substring (case-insensitive). Replaces the built-in "mesh" default.
# Leave empty to keep the default "mesh" filter.
# Example: "L1" for Wio L1 Pro, "MeshCore" for nodes advertising that name.
scan_name_filter = ""

# Named profiles — use with --profile <name>
# A profile's fields are merged with CLI flags; flags take priority.

[profile.home]
transport = "serial"
device    = "/dev/ttyUSB0"

[profile.wio]
transport = "ble"
device    = "AA:BB:CC:DD:EE:FF"   # Wio L1 Pro address

[profile.remote]
transport = "tcp"
device    = "192.168.1.100:3000"
```

### Flag resolution order (highest priority first)

1. `--device` / `--transport` CLI flags
2. `--profile <name>` profile fields (flags override individual profile fields)
3. `default_device` / `default_transport` from config file
4. Built-in default: `transport = "ble"`, `device = ""` (triggers BLE scan picker)

### Common setups

```bash
# Build machine (no Bluetooth) — always use serial
echo 'default_transport = "serial"
default_device    = "/dev/ttyUSB0"' > ~/.config/meshcore/config.toml

# Laptop — BLE by default, serial fallback as a named profile
cat > ~/.config/meshcore/config.toml <<EOF
default_transport = "ble"
default_device    = "AA:BB:CC:DD:EE:FF"

[profile.serial]
transport = "serial"
device    = "/dev/ttyUSB0"
EOF
```

## Desktop notifications

When a direct or channel message arrives while you are on a different tab,
`notify-send` is invoked with the message text. If `notify-send` is not
installed the feature is silently disabled.

Install on Debian/Ubuntu:

```bash
sudo apt install libnotify-bin
```

## Message storage

All messages are persisted automatically to `~/.local/share/meshcore/messages.db`
(bbolt embedded database, no external process required). History is loaded per
contact and channel on every connection.

## Configurable key bindings

Any key binding can be overridden in `~/.config/meshcore/config.toml` under a
`[keys]` section. Key strings use BubbleTea notation (`"alt+n"`, `"ctrl+a"`,
`"enter"`, `"pgup"`, etc.).

```toml
[keys]
next_tab     = "alt+n"   # default
prev_tab     = "alt+p"   # default
advert       = "ctrl+a"
send         = "enter"
scroll_up    = "pgup"
scroll_down  = "pgdn"
search       = "/"
select_mode  = "s"
delete_msg   = "d"
clear_all    = "X"
off_record   = "ctrl+o"
join_channel = "n"
leave_channel = "d"
quit         = "q"
```

Omit a key to keep its default. The **Settings tab** (`5`) shows all current
bindings and their defaults in a read-only panel.

## Keyboard shortcuts

### Global

| Key | Action |
|-----|--------|
| `1` – `5` | Switch directly to tab 1–5 (suppressed while a text input is focused — press `Esc` first if needed) |
| `Alt+N` | Next tab |
| `Alt+P` | Previous tab |
| `q` / `Ctrl+C` | Quit (`q` suppressed while typing) |

### BLE scan picker (startup)

| Key | Action |
|-----|--------|
| `↑` / `↓` or `j` / `k` | Navigate device list |
| `Enter` | Connect to selected device |
| `r` | Clear list and restart scan |
| `q` / `Ctrl+C` | Cancel and exit |

### Chat tab

Contact names are prefixed with a freshness dot: `●` green = seen <5 min,
yellow = <1 hr, red = older or never seen.

Outbound direct messages show a delivery status indicator after the text:

| Indicator | Meaning |
|-----------|---------|
| `…` | BLE write in progress |
| `● sent` | Device accepted; has a known path to recipient |
| `● sent (no path)` | Device sent as blind flood; no cached route to recipient |
| `✓ 42ms` | Remote node confirmed receipt; round-trip time shown |
| `? no ack  r=retry` | No confirmation after 30 s; press `r` to retry (up to 3×) |

After 3 retries with no acknowledgement the app automatically triggers a
**flood fallback**: `SendPathDiscoveryReq` is sent concurrently with a final
send attempt, forcing the mesh firmware to discover a fresh route.  This
handles the common mobile-mesh case where either party has moved and the
cached path to the recipient is stale — no user action required.

| Key | Action |
|-----|--------|
| `↑` / `↓` | Move between contacts |
| `/` | Open contact search filter |
| `Escape` | Clear search / exit select mode / blur input |
| `PgUp` / `PgDn` | Scroll message history |
| `Ctrl+U` / `Ctrl+D` | Half-page scroll |
| Type + `Enter` | Compose and send a direct message |
| `/ping` + `Enter` | Ping selected contact; RTT appears inline |
| `/trace` + `Enter` | Trace path to selected contact; hop counts appear inline |
| `/location <lat> <lon>` + `Enter` | Set your advertised GPS coordinates on the device (decimal degrees) |
| `r` | Retry last unacknowledged message (input blurred; up to 3 attempts) |
| `Ctrl+A` | Broadcast self-advertisement to mesh |
| `s` | Enter select mode (blue border); `↑`/`↓` moves cursor, `d` deletes message, `s`/`Esc` exits |
| `X` | Clear all messages — press twice to confirm (local only) |
| `Ctrl+O` | Toggle off-the-record: messages shown in-session but not saved; border turns red |

> **Tip:** typing `/` only opens contact search when the input is empty.
> Typing `/ping`, `/trace`, or `/location` does **not** trigger search.

### Channels tab

Channels can be joined or left at any time. The well-known "Public" channel
secret is built in — typing `Public` as the channel name will automatically
use it, making the channel interoperable with other MeshCore clients.

Outbound channel messages show `✓ sent (broadcast)` once the device confirms
receipt. Channels are broadcast; there is no per-message remote ack or retry.

| Key | Action |
|-----|--------|
| `↑` / `↓` | Move between channels |
| `PgUp` / `PgDn` | Scroll message history |
| `Ctrl+U` / `Ctrl+D` | Half-page scroll |
| Type + `Enter` | Compose and send a channel message |
| `Ctrl+A` | Broadcast self-advertisement to mesh |
| `n` | Join / create a channel (two-step: name → optional PSK) |
| `d` `d` | Leave the selected channel (press twice to confirm) |
| `Escape` | Cancel join mode or exit select mode |
| `s` | Enter select mode (blue border); `↑`/`↓` moves cursor, `d` deletes message, `s`/`Esc` exits |
| `X` | Clear all messages — press twice to confirm (local only) |
| `Ctrl+O` | Toggle off-the-record: messages shown in-session but not saved; border turns red |

### Nodes tab

Discovered nodes are listed with Name, Type, SNR, RSSI, and Last Seen.
The list is session-only and resets on reconnect.

Selecting a node shows a **detail bar** beneath the list with:
hops (mesh hops to reach it), loc (lat,lon if the node advertises GPS),
last\_advert (firmware's own timestamp), and ping/trace results if measured.

| Key | Action |
|-----|--------|
| `↑` / `k` | Move selection up |
| `↓` / `j` | Move selection down |
| `p` | Ping selected node — RTT appears in detail bar |
| `t` | Trace path to selected node — hop counts appear in detail bar |

## Acknowledgements

This project stands on the shoulders of the [Charm](https://charm.sh) team's
exceptional work.  [Bubble Tea](https://github.com/charmbracelet/bubbletea),
[Bubbles](https://github.com/charmbracelet/bubbles), and
[Lip Gloss](https://github.com/charmbracelet/lipgloss) make building polished
terminal UIs in Go a genuine pleasure — if you've enjoyed using this app,
go give them a ⭐.

The [meshcore-go SDK](https://github.com/meshcore-go/meshcore-go) does the
heavy lifting of the MeshCore companion protocol — typed commands, response
parsing, push event dispatch, and the cryptographic identity layer.
Without it this client would have taken months instead of days.

## Dependencies

| Package | Role |
|---------|------|
| [meshcore-go/meshcore-go](https://github.com/meshcore-go/meshcore-go) | MeshCore companion SDK — protocol, crypto, typed client API |
| [charmbracelet/bubbletea](https://github.com/charmbracelet/bubbletea) | TUI framework (Elm architecture) |
| [charmbracelet/bubbles](https://github.com/charmbracelet/bubbles) | TUI components (viewport, textinput, list, spinner) |
| [charmbracelet/lipgloss](https://github.com/charmbracelet/lipgloss) | TUI styling and layout |
| [tinygo-org/bluetooth](https://tinygo.org/x/bluetooth) | BLE transport (Linux/macOS/Windows) |
| [etcd-io/bbolt](https://github.com/etcd-io/bbolt) | Message persistence (embedded key-value store) |
| [BurntSushi/toml](https://github.com/BurntSushi/toml) | Config file parsing |

See `ARCHITECTURE.md` for protocol details and design decisions.
