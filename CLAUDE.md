# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
make                  # build → ./meshcore-tui
make run ARGS="..."   # build and run with flags
make test             # go test ./...
make lint             # go vet ./...
make build-all        # cross-compile linux/darwin/windows amd64+arm64

# Single package test
go test ./storage/...
go test ./storage/... -run TestLoadBefore -v

# Cross-compile (Serial/TCP only — BLE requires native build per platform)
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o meshcore-tui-darwin-arm64 .
```

BLE on Linux requires BlueZ: `sudo apt install bluez libbluetooth-dev`. macOS and Windows require CGo (see BUILD.md).

## Architecture

### Data flow

```
cmd/root.go
  ├─ resolves transport + device from flags/config
  ├─ runs scanner.Run() (BLE picker) if needed
  ├─ opens storage.Store
  ├─ launches tea.Program(tui.App)
  └─ goroutine: connects transport → creates client.Client → session startup → registers push handlers
       └─ push handlers call p.Send(views.*Msg) into the BubbleTea event loop
```

### SDK dependency

`github.com/meshcore-go/meshcore-go` is the companion **client** SDK — it connects to a hardware MeshCore node (e.g. Wio L1 Pro) and drives it via 65 typed commands. Key types used:

- `companion/client.Client` — high-level client; `c.AppStart`, `c.GetContacts`, `c.GetWaitingMessages`, `c.SendTextMessage`, `c.SendChannelTextMessage`, `c.OnPush`
- `companion/transport.Transport` — interface implemented by `ble.Transport`, `SerialTransport`, `TCPTransport`
- `companion.SelfInfoResponse`, `ContactResponse`, `ChannelInfoResponse` — session data structs
- Push codes: `PushMsgWaiting(0x83)`, `PushSendConfirmed(0x82)`, `PushNewAdvert(0x8A)`, `PushAdvert(0x80)`, `PushContactDeleted(0x8F)`

### BubbleTea message bus

All SDK events reach the TUI as `tea.Msg` values sent via `p.Send()` from background goroutines. `tui/views/messages.go` defines all shared message types. `tui/app.go` broadcasts push messages to all views via `isBroadcastMsg()`; key/resize messages go only to the active tab.

### Views

Five tabs in `tui/views/`: `ChatView`, `ChannelView`, `NodesView`, `DeviceView`, `SettingsView`. All implement the `View` interface (`Init / Update / View / Title`). Chat and Channel views hold `bubbles/viewport` for scrollable threads and call `rebuildViewport()` on every state change.

### Storage

`storage/storage.go` uses bbolt (`~/.local/share/meshcore-go-tui/messages.db`). Buckets: `contacts/<pubkey-hex>` and `channels/<idx>` for messages, `lastread` for per-contact read positions. Keys are 8-byte big-endian `UnixNano` — gives free chronological cursor order. All store calls from views are fire-and-forget (`_ = store.Save...`).

### Config

`config/config.go` loads `~/.config/meshcore-go-tui/config.toml`. Three fields: `default_transport`, `default_device`, `scan_name_filter`. Named profiles under `[profile.<name>]`. `SettingsView` writes directly to the live `*config.Config` pointer and calls `config.Save()`.

### BLE transport

`ble/transport.go` implements `companion/transport.Transport` using Nordic UART Service UUIDs (`6E400001/02/03`). After connect, reads `txChar.GetMTU()` to set chunk size. Reconnect loop uses exponential backoff (1s→30s) via `adapter.SetConnectHandler`. `NewWithNameFilter(name)` adds a name-based scan fallback alongside UUID matching.
