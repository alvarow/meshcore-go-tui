# Build Guide

Step-by-step instructions to build `meshcore-cli` on each supported platform.

---

## Prerequisites (all platforms)

- **Go 1.22 or newer** — https://go.dev/dl/
- **meshcore-go SDK** cloned locally (the library is not yet published to a Go module proxy)

```bash
git clone https://github.com/ripplebiz/meshcore-go /path/to/meshcore-go-main
```

The `go.mod` in this repo points to the SDK via a `replace` directive:
```
replace github.com/meshcore-go/meshcore-go => /path/to/meshcore-go-main
```

If you cloned the SDK to a different path, update that line in `go.mod`.

---

## Linux

### BLE transport (default)

BlueZ is required. No CGo needed — the BLE library talks to BlueZ over D-Bus.

```bash
# Debian / Ubuntu
sudo apt install bluez libbluetooth-dev

# Fedora / RHEL
sudo dnf install bluez bluez-libs-devel

# Arch
sudo pacman -S bluez bluez-libs
```

Make sure the BlueZ daemon is running:
```bash
sudo systemctl enable --now bluetooth
```

Build:
```bash
go build -o meshcore-cli .
# or
make
```

### Serial / TCP transport only (no Bluetooth hardware needed)

No extra packages required beyond Go itself:
```bash
go build -o meshcore-cli .
```

Use with:
```bash
./meshcore-cli --transport serial --device /dev/ttyUSB0
./meshcore-cli --transport tcp    --device 192.168.1.100:3000
```

---

## macOS

### BLE transport

CGo is required — the BLE library wraps CoreBluetooth (an Objective-C framework).

**1. Install Xcode Command Line Tools**
```bash
xcode-select --install
```
This provides `clang`, the Apple SDK headers, and CoreBluetooth.framework.
Full Xcode is not needed.

**2. Build**
```bash
go build -o meshcore-cli .
# or
make
```

**3. Sign the binary**

macOS 10.15+ requires a signed binary to access Bluetooth:
```bash
# Ad-hoc signing — for local use only
codesign --sign - ./meshcore-cli
```

For distribution (sharing the binary with others), a Developer ID certificate
from an Apple Developer account is required instead of `--sign -`.

**4. Run**

On first launch the OS will show a Bluetooth permission dialog. Grant access.
If the dialog doesn't appear, check System Settings → Privacy & Security → Bluetooth.

### Serial / TCP transport only

No Xcode or signing needed:
```bash
go build -o meshcore-cli .
./meshcore-cli --transport serial --device /dev/tty.usbserial-0001
./meshcore-cli --transport tcp    --device 192.168.1.100:3000
```

---

## Windows

### BLE transport

CGo is required — the BLE library wraps the WinRT Bluetooth API via generated
C++ bindings.

**1. Install MSYS2**

Download and install from https://www.msys2.org.
Default install path: `C:\msys64`.

**2. Install MinGW-w64 GCC inside MSYS2**

Open the MSYS2 MinGW64 terminal and run:
```bash
pacman -S mingw-w64-x86_64-gcc
```

**3. Add MinGW to your PATH**

In Windows Settings → System → Advanced system settings → Environment Variables,
add `C:\msys64\mingw64\bin` to the system `PATH`.

Verify in a regular PowerShell or CMD:
```powershell
gcc --version   # should print mingw-w64 gcc
```

**4. Build**

From PowerShell or CMD (not MSYS2):
```powershell
set CGO_ENABLED=1
go build -o meshcore-cli.exe .
```

Or with make (if you have GNU make via MSYS2):
```bash
make
```

**5. Run**

Windows will prompt for Bluetooth permission on first use via a system dialog.

### Serial / TCP transport only

No MinGW or CGo needed:
```powershell
set CGO_ENABLED=0
go build -o meshcore-cli.exe .
meshcore-cli.exe --transport serial --device COM3
meshcore-cli.exe --transport tcp    --device 192.168.1.100:3000
```

---

## Cross-compilation

| Target | From Linux | From macOS | From Windows |
|--------|-----------|------------|--------------|
| Linux BLE | ✓ native | ✗ | ✗ |
| Linux Serial/TCP | ✓ native | ✓ `GOOS=linux` | ✓ `GOOS=linux` |
| macOS BLE | ✗ | ✓ native | ✗ |
| macOS Serial/TCP | ✓ `GOOS=darwin` | ✓ native | ✓ `GOOS=darwin` |
| Windows BLE | ✗ | ✗ | ✓ native |
| Windows Serial/TCP | ✓ `GOOS=windows` | ✓ `GOOS=windows` | ✓ native |

**BLE binaries must be built natively** — CoreBluetooth (macOS) and WinRT
(Windows) require the platform SDK at compile time, which cannot be
cross-compiled from a different OS.

Serial and TCP builds have no CGo dependencies and cross-compile freely.

### Cross-compile examples (Serial/TCP only)

```bash
# From Linux → macOS arm64 (Apple Silicon)
GOOS=darwin  GOARCH=arm64  CGO_ENABLED=0 go build -o meshcore-cli-darwin-arm64  .

# From Linux → macOS amd64 (Intel)
GOOS=darwin  GOARCH=amd64  CGO_ENABLED=0 go build -o meshcore-cli-darwin-amd64  .

# From Linux → Windows amd64
GOOS=windows GOARCH=amd64  CGO_ENABLED=0 go build -o meshcore-cli-windows-amd64.exe .
```

Or use the Makefile targets:
```bash
make build-all          # all five targets
make build-linux-amd64
make build-linux-arm64
make build-darwin-amd64
make build-darwin-arm64
make build-windows-amd64
```

> **Note:** `make build-darwin-*` and `make build-windows-amd64` from Linux
> produce Serial/TCP-only binaries (CGO_ENABLED=0). For BLE-capable binaries,
> build on the matching platform.

---

## Verify the build

```bash
./meshcore-cli --help          # shows flags
./meshcore-cli --transport tcp --device 127.0.0.1:3000   # connect without BLE
```

Expected output on a machine without Bluetooth when run without flags:
```
No Bluetooth adapter found.

To connect via serial:  meshcore-cli --transport serial --device /dev/ttyUSB0
To connect via TCP:     meshcore-cli --transport tcp    --device host:port
```

---

## Makefile reference

```bash
make                 # build for current platform
make run ARGS="..."  # build and run with arguments
make test            # go test ./...
make lint            # go vet ./...
make install         # install to $GOPATH/bin
make clean           # remove built binaries
make build-all       # cross-compile all 5 targets
```
