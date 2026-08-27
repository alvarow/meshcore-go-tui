# Architecture

## Overview

`meshcore-go-cli` is a thin TUI shell around the `meshcore-go` SDK. It provides
transport (BLE), configuration, persistence, and a BubbleTea UI. All protocol
logic — packet framing, crypto, routing, contact management — lives in the SDK.

```
┌──────────────────────────────────────┐
│              BubbleTea TUI            │
│  tui/app.go  +  tui/views/*.go       │
└────────────────┬─────────────────────┘
                 │ tea.Msg  /  tea.Cmd
┌────────────────▼─────────────────────┐
│            companion/client.Client    │  ← from SDK
│  65 typed commands, 17 push events   │
└────────────────┬─────────────────────┘
                 │ Transport interface
       ┌─────────┼──────────┐
       ▼         ▼          ▼
   BLE (ble/)  Serial     TCP
   NUS UUIDs   SDK        SDK
```

## Directory layout

```
meshcore-go-cli/
├── main.go               entry point → cmd.Execute()
├── Makefile
├── go.mod / go.sum
│
├── cmd/root.go           CLI flags, config resolution, transport wiring,
│                         session startup, push handler registration
│
├── config/config.go      TOML config at ~/.config/meshcore/config.toml
│                         Load / Save / DefaultConfig / ConfigPath
│
├── storage/
│   ├── storage.go        bbolt message store (direct messages + channels)
│   └── storage_test.go   round-trip and limit tests
│
├── ble/transport.go      BLETransport — implements companion/transport.Transport
│                         using tinygo.org/x/bluetooth + Nordic UART Service
│                         Reconnect, MTU negotiation, name filter
│
└── tui/
    ├── app.go            Root BubbleTea model: tab bar, unread badges,
    │                     reconnect spinner, error bar, sub-view delegation
    ├── styles.go         lipgloss palette (single source of truth for colors)
    ├── scanner/
    │   ├── scanner.go    BLE scan picker BubbleTea model
    │   └── run.go        Run() entry point — returns selected address
    └── views/
        ├── view.go       View interface (Init / Update / View / Title)
        ├── messages.go   Shared BubbleTea message types for all views
        ├── chat.go       Direct messages: contacts, scrollable thread,
        │                 timestamps, send/ack/fail status, storage wired
        ├── channels.go   Group channels: channel list, scrollable thread,
        │                 timestamps, storage wired
        ├── nodes.go      Peer table: Name / Type / SNR / RSSI / LastSeen
        ├── device.go     Self info: name, pubkey prefix, radio params, battery
        └── settings.go   Config editor: transport, device, scan filter, profiles
```

## SDK dependency

Reference library: `github.com/meshcore-go/meshcore-go`
Local path: `/home/alvaro/src/meshcore-go-main`

The `go.mod` uses a `replace` directive until the library is published:
```
replace github.com/meshcore-go/meshcore-go => /home/alvaro/src/meshcore-go-main
```

Key SDK packages used:
- `companion/client` — `Client` struct, all typed commands
- `companion/transport` — `Transport` interface, `SerialTransport`, `TCPTransport`
- `companion` — `Frame`, `FrameParser`, `Response`, push codes, constants

## BLE transport

File: `ble/transport.go`

### Why tinygo-org/bluetooth

Only library with native support for all three target platforms:
- Linux → BlueZ via D-Bus
- macOS → CoreBluetooth (CGo)
- Windows → WinRT (CGo)

Alternatives considered:
- `muka/go-bluetooth` — Linux only
- `go-ble/ble` — Linux + macOS, no Windows

### Nordic UART Service (NUS)

MeshCore firmware exposes a BLE GATT service using the NUS profile:

| Role | UUID | Direction |
|------|------|-----------|
| Service | `6E400001-B5A3-F393-E0A9-E50E24DCCA9E` | — |
| RX char (write to node) | `6E400002-B5A3-F393-E0A9-E50E24DCCA9E` | host → node |
| TX char (notify from node) | `6E400003-B5A3-F393-E0A9-E50E24DCCA9E` | node → host |

### Frame format (companion layer)

```
[type 1B] [length 2B LE] [payload N bytes]

type: 0x3e = incoming (node → host)
      0x3c = outgoing (host → node)
max payload: 173 bytes (frame cap is 176 bytes)
```

`companion.FrameParser` is a streaming parser — BLE notifications may arrive
fragmented. Feed every notification into `parser.Feed(buf)` and it yields
complete frames.

### MTU and chunking

BlueZ negotiates the ATT MTU automatically during connection establishment.
After characteristics are discovered, `ble.Transport` calls `txChar.GetMTU()`
to read the result and sets `chunkSize = mtu - 3` (3 bytes of ATT overhead).
Fallback is 20 bytes if `GetMTU` fails (e.g. on older BlueZ).

To override manually call `transport.SetMTU(payloadBytes)` before `Connect()`.
Example: `SetMTU(244)` for a 247-byte MTU device.

### Reconnect

`adapter.SetConnectHandler` (tinygo-org/bluetooth v0.15+) fires with
`connected=false` when BlueZ drops the device. The transport:

1. Sets `connected = false`, calls `disconnectHandler` if registered.
2. Starts `reconnectLoop()` in a goroutine.
3. Retries `connectDevice()` with exponential backoff: 1s → 2s → 4s → … → 30s cap.
4. On success, calls `reconnectHandler` if registered.
5. Stops if `Close()` is called (closes the `done` channel).

The handler is registered at the adapter level and fires for all devices;
the transport filters by device address to ignore unrelated connections.

`cmd/root.go` wires disconnect/reconnect into the TUI:
```go
bt.SetDisconnectHandler(func() { p.Send(views.ReconnectingMsg{}) })
bt.SetReconnectHandler(func()  { p.Send(views.ReconnectedMsg{DeviceName: device}) })
```

### Name filter

`ble.NewWithNameFilter(name string)` creates a transport that scans for
devices whose advertised `LocalName` contains `name` (case-insensitive
substring). This is a fallback for devices that omit service UUIDs from
their advertisement packets. The NUS service UUID match takes priority;
name matching is used only when UUID matching fails.

### Scan vs direct connect

- `addr != ""` (from flag or config): skip scan entirely, connect directly.
- `addr == ""` and no name filter: scan for first NUS UUID match.
- `addr == ""` with name filter (`NewWithNameFilter`): scan, accept on UUID or name.
- Interactive picker (`tui/scanner`): used by `cmd/root.go` when no address
  is known; shows all discovered devices in a BubbleTea list.

### scan_name_filter

`config.ScanNameFilter` is an optional string read from `scan_name_filter` in
`config.toml`. It narrows the BLE scan picker to devices whose advertised local
name contains the configured substring (case-insensitive).

**Filter precedence in the scanner:**

```
NUS service UUID match  →  always accepted (hardware guarantee)
name contains filter    →  accepted when filter != ""
name contains "mesh"    →  accepted when filter == "" (built-in default)
```

Setting the filter does not disable UUID matching — a device advertising the
NUS UUID is always shown regardless. The filter only changes the name-based
fallback, which covers devices that omit service UUIDs from their advertisement
packet to save space.

**Example** — once the Wio L1 Pro's advertised name is known:
```toml
scan_name_filter = "L1"   # accepts "L1 Pro", "MeshCore-L1", etc.
```

The filter is passed to `scanner.Run(nameFilter)` → `NewWithFilter(nameFilter)`
→ `startScan(done, nameFilter)`. It is also stored on `ble.Transport` when
`ble.NewWithNameFilter` is called directly (non-picker path). Both code paths
use the same case-fold logic: `strings.Contains(strings.ToLower(name), filter)`.

### BLE unavailable

If BlueZ is not running or there is no Bluetooth adapter, `adapter.Enable()`
returns an error containing `"org.bluez"`. The transport wraps this with a
readable message. The scanner TUI detects the error and shows a help screen
with serial/TCP instructions. `cmd/root.go` checks `errors.Is(err, scanner.ErrBLEUnavailable)`
and prints usage hints, then exits 0.

## Configuration

`config.Config` is a TOML struct at `~/.config/meshcore/config.toml`.

```
Config.DefaultTransport  string  "ble" | "serial" | "tcp"
Config.DefaultDevice     string  BLE addr | /dev/ttyUSBx | host:port
Config.Profile           map     name → {Transport, Device}
```

Resolution order in `cmd/root.go`:
1. `--device` / `--transport` flags
2. `--profile <name>` (merges with flags: flag overrides profile field)
3. `Config.DefaultDevice` / `Config.DefaultTransport`
4. Fallback: BLE scan picker

## TUI architecture

### Message bus

`cmd/root.go` runs the connection and session startup in a background goroutine
and sends BubbleTea messages into the program:

```go
// Connection state
p.Send(tui.ConnectedMsg{DeviceName: device})
p.Send(tui.ErrorMsg{Err: err})
p.Send(views.ReconnectingMsg{})
p.Send(views.ReconnectedMsg{DeviceName: device})

// Session data
p.Send(views.SessionReadyMsg{Self, Contacts, Channels})

// Push events (async from device)
p.Send(views.InboundDirectMsg{...})
p.Send(views.InboundChannelMsg{...})
p.Send(views.OutboundAckMsg{...})
p.Send(views.NodeAdvertMsg{...})
p.Send(views.ContactDeletedMsg{...})
```

Push messages are broadcast to all views by `App.Update` so background state
(peer table, unread counts) stays current even when a tab is not visible.

### View interface

```go
type View interface {
    Init() tea.Cmd
    Update(msg tea.Msg) (View, tea.Cmd)
    View() string
    Title() string
}
```

Each view is self-contained. `App.Update` delegates key and window-resize
messages to the active view only. Push messages are broadcast to all views.

### Tab layout

```
┌─[1:Chat (3)]──[2:Channels]──[3:Nodes]──[4:Device]─────────────┐
│ ⚠ connection timeout                                            │  ← error bar (5s auto-dismiss)
│                                                                  │
│                    active view content                           │
│                                                                  │
├──↻ reconnecting...──────────────────────tab/1-4:switch  q:quit─┤
```

Height budget: `terminal height − 2` normally; `terminal height − 3` when the error bar is visible.

### Unread badges

`App.unread [4]int` tracks unseen messages per tab:
- `InboundDirectMsg` increments `unread[0]` (Chat) when Chat is not the active tab.
- `InboundChannelMsg` increments `unread[1]` (Channels) when Channels is not active.
- `NodeAdvertMsg` does not increment (peer discovery is ambient, not a message).
- Switching to a tab resets its counter to zero.

Badges render inline in the tab label: ` 1:Chat (3) `.

### Reconnect state machine

```
connected ──disconnect──▶ reconnecting ──success──▶ connected
                                       ──Close()──▶ (stopped)
```

`ReconnectingMsg` starts the `bubbles/spinner` and sets `reconnecting=true`.
Spinner ticks only while reconnecting — no wasted ticks when idle.
`ReconnectedMsg` clears the reconnecting state and restores the device name.

### Error bar

`ErrorMsg.Err` sets `App.lastErr` and schedules a `tea.Tick(5s)` that sends
`clearErrMsg{}` to auto-dismiss. The bar appears between the tab bar and content
area (dark red bg, light red text, full width). Content height shrinks by 1 line
while the error is visible.

### Settings view

File: `tui/views/settings.go`

The 5th tab. Edits `*config.Config` in place; writes to disk on Save.
No live connection changes — a status message prompts the user to quit and reconnect.

**Focus cycle** (`Tab` / `Shift+Tab`):
```
Transport → Device → Scan name filter → Profile list → [Save] → [Discard] → (wrap)
```

**Global keys (normal mode):**

| Key | Condition | Action |
|-----|-----------|--------|
| `←` / `→` / `Space` | Transport focused | Cycle `ble → serial → tcp` |
| `↑` / `↓` | Profile list focused | Move selection |
| `Enter` | Save focused | Write config to disk, show status message |
| `Enter` | Discard focused | Revert all fields to last-saved snapshot |
| `Enter` | Profile list focused | Open edit form for selected profile |
| `n` | Any | Open new-profile form |
| `d` | Profile list focused | Delete selected profile |

**Profile edit form** (overlay, `editing=true`):

Three fields: Name → Transport → Device. `Tab`/`Shift+Tab` cycle between them.
`Enter` on Device (or Transport) confirms and closes the form.
`Esc` cancels with no changes.

**Save / Discard mechanics:**

- `save()` writes `deviceInput`, `filterInput`, and rebuilt `Profile` map back to `*cfg`, then calls `config.Save(cfg)`. On success sets `status = "Saved — quit and reconnect to apply"` and snapshots `original = *cfg`.
- `discard()` restores `*cfg = original`, resets all inputs from the snapshot, clears dirty flag.
- Tab title shows `Settings *` whenever `dirty == true`.

## Protocol — key flows

### Session startup
```
client.DeviceQuery()   → DeviceInfoResponse  (firmware version, model) [non-fatal]
client.AppStart()      → SelfInfoResponse    (own pubkey, name, radio params, lat/lon)
client.GetContacts()   → []ContactResponse   (initial contact list)    [non-fatal]
client.GetChannel(0…7) → ChannelInfoResponse (channel definitions)     [stops on first error]
→ p.Send(SessionReadyMsg{...})
```

### Receiving a direct message
```
push: PushMsgWaiting (0x83)
  → client.GetWaitingMessages() → []WaitingMessage
  → p.Send(InboundDirectMsg{...}) per message
  → p.Send(InboundChannelMsg{...}) per channel message
```

### Sending a direct message
```
Enter key → sendDirectMsg tea.Cmd → client.SendTextMessage(identity, text, txtType)
  → push: PushSendConfirmed (0x82) → p.Send(OutboundAckMsg{RoundTripMs})
  → view marks last StatusSending message as StatusAcked
```

### Peer discovery
```
push: PushNewAdvert (0x8A) → p.Send(NodeAdvertMsg{Name, NodeType, PubKey})
push: PushAdvert    (0x80) → p.Send(NodeAdvertMsg{PubKey})  [older firmware]
```

### Group channels
```
Enter key → sendChannelMsg tea.Cmd → client.SendChannelTextMessage(idx, text, 0)
push: channel message → p.Send(InboundChannelMsg{ChannelIdx, Text, Timestamp})
```

## BLE Scanner Picker

Package: `tui/scanner`
Files: `scanner.go`, `run.go`

### Startup flow

```
cmd/root.go
  if transport == "ble" && device == "" {
      result, err := scanner.Run()
      if errors.Is(err, scanner.ErrBLEUnavailable) { /* print help, exit 0 */ }
      if result.Canceled { os.Exit(0) }
      device = result.Address
  }
```

### How it works

`scanner.Run()` launches a full-screen BubbleTea program that:
1. Enables the BLE adapter and starts scanning via `tinygo-org/bluetooth`
2. Filters qualifying devices (NUS UUID or name contains "mesh")
3. Presents a live-updating `bubbles/list` — devices appear as found, RSSI updates in place
4. Returns `Result{Address, Name}` on Enter, or `Result{Canceled: true}` on `q`/`Ctrl+C`
5. On adapter failure, shows a help screen with serial/TCP instructions

### Scan filter

A device is included if it meets **either**:
- Advertises the NUS service UUID `6E400001-B5A3-F393-E0A9-E50E24DCCA9E`
- Local name contains `"mesh"` (case-insensitive)

Devices are deduplicated by address; RSSI updates in place.

## Message persistence

Package: `storage`
File: `storage/storage.go`
DB: `~/.local/share/meshcore/messages.db`

### Why bbolt

Pure Go, no CGo, no external process. Single-file database. Works identically
on Linux, macOS, Windows. No schema migrations needed.

### Bucket layout

```
contacts/
  <pubkey-hex>/        ← one sub-bucket per contact
    <ts_nanosec_BE>    → JSON StoredMessage
channels/
  <idx>/               ← one sub-bucket per channel index (0–7)
    <ts_nanosec_BE>    → JSON StoredMessage
```

Keys are 8-byte big-endian `int64(time.UnixNano())` — byte order gives free
chronological sort via `Cursor.Last()` / `Cursor.Prev()`.

### StoredMessage

```go
type StoredMessage struct {
    Timestamp time.Time `json:"ts"`
    From      string    `json:"from"`  // contact name or "me"
    Text      string    `json:"text"`
    Direction Direction `json:"dir"`   // Inbound=0, Outbound=1
    Acked     bool      `json:"acked"`
}
```

### View integration

`*storage.Store` is opened in `cmd/root.go` before TUI launch and passed to
`tui.New(device, client, store)` → `NewChatView(client, store)` / `NewChannelView(client, store)`.

In `ChatView`:
- `SessionReadyMsg` → `store.LoadDirectMessages(pubkeyHex, 100)` per contact
- `InboundDirectMsg` → `store.SaveDirectMessage(..., Inbound)`
- `Enter` (send) → `store.SaveDirectMessage(..., Outbound)`
- `OutboundAckMsg` → `store.MarkAcked(pubkeyHex, timestamp)`

In `ChannelView`:
- `SessionReadyMsg` → `store.LoadChannelMessages(idx, 100)` per channel
- `InboundChannelMsg` → `store.SaveChannelMessage(..., Inbound)`
- `Enter` (send) → `store.SaveChannelMessage(..., Outbound)`

All store calls are fire-and-forget (`_ =` errors). The UI never blocks on storage.

## Message status indicators (Chat)

Each outbound `chatMessage` carries a `MsgStatus`:

| Status | Display | Meaning |
|--------|---------|---------|
| `StatusSending` | `…` | Sent to transport, awaiting ACK |
| `StatusAcked` | `✓` | Device confirmed delivery (`PushSendConfirmed`) |
| `StatusFailed` | `✗` | Transport error on send |
| `StatusReceived` | (none) | Inbound message |

Historical outbound messages loaded from storage with `Acked=false` are shown
as `StatusSending` (no retry — just an honest display of unconfirmed state).

## CGo toolchain requirements per platform

The BLE transport (`tinygo-org/bluetooth`) requires platform-specific CGo
toolchains on macOS and Windows. Linux does **not** need CGo — the library uses
D-Bus to talk to BlueZ (pure Go via `godbus/dbus`).

### Linux (no CGo)

```bash
apt install bluez libbluetooth-dev   # BlueZ daemon + headers
go build .                           # CGO_ENABLED not required
```

### macOS (CGo required — CoreBluetooth)

```bash
# 1. Install Xcode Command Line Tools (includes clang + Apple SDK headers)
xcode-select --install

# 2. Build normally — CoreBluetooth.framework is bundled with the OS
go build .

# 3. Sign the binary (required on macOS 10.15+ to use Bluetooth)
codesign --sign - ./meshcore-cli
```

The OS will prompt for Bluetooth permission on first run. For distribution,
a Developer ID certificate is needed; for local use, ad-hoc signing (`--sign -`)
is sufficient.

### Windows (CGo required — WinRT Bluetooth)

```powershell
# 1. Install MSYS2: https://www.msys2.org
# 2. In MSYS2 MinGW64 terminal:
pacman -S mingw-w64-x86_64-gcc

# 3. Add to PATH: C:\msys64\mingw64\bin
# 4. Build from a regular PowerShell/CMD with MinGW in PATH:
set CGO_ENABLED=1
go build .
```

The WinRT backend (`saltosystems/winrt-go`) is already in `go.sum` — no
additional Windows SDK install is needed beyond MinGW.

### Cross-compilation

**Not possible** for BLE targets. CoreBluetooth and WinRT require the native
SDK at compile time. The `make build-darwin-*` and `make build-windows-*`
Makefile targets must be run on the matching platform.

Serial and TCP builds (`GOOS=linux/darwin/windows`) cross-compile freely from
any platform since they have no CGo dependencies.

## Planned improvements

- **macOS / Windows build**: toolchain requirements documented in BUILD.md;
  needs native build environment per platform.

## Design decisions

| Decision | Choice | Reason |
|----------|--------|--------|
| BLE library | `tinygo-org/bluetooth` | Only option covering Linux + macOS + Windows |
| TUI framework | BubbleTea | Elm architecture suits event-driven radio app; Charm ecosystem |
| Config format | TOML | Human-editable, no schema noise, matches MeshCore firmware style |
| CLI flags | stdlib `flag` | No subcommands needed; avoids cobra dependency |
| Transport abstraction | SDK's `companion/transport.Transport` | BLE, Serial, TCP share one interface; swap without changing upper layers |
| Message store | bbolt | Pure Go, single file, no CGo, chronological key sort is free |
| Storage errors | fire-and-forget | Persistence failures must never block or crash the UI |
| BLE reconnect | exponential backoff goroutine in transport | Mirrors Serial/TCP auto-reconnect pattern; keeps upper layers unaware |
