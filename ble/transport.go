package ble

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/meshcore-go/meshcore-go/companion"
	"tinygo.org/x/bluetooth"
)

var (
	nusSvcUUID = bluetooth.NewUUID([16]byte{
		0x6E, 0x40, 0x00, 0x01, 0xB5, 0xA3, 0xF3, 0x93,
		0xE0, 0xA9, 0xE5, 0x0E, 0x24, 0xDC, 0xCA, 0x9E,
	})
	nusRxUUID = bluetooth.NewUUID([16]byte{
		0x6E, 0x40, 0x00, 0x02, 0xB5, 0xA3, 0xF3, 0x93,
		0xE0, 0xA9, 0xE5, 0x0E, 0x24, 0xDC, 0xCA, 0x9E,
	})
	nusTxUUID = bluetooth.NewUUID([16]byte{
		0x6E, 0x40, 0x00, 0x03, 0xB5, 0xA3, 0xF3, 0x93,
		0xE0, 0xA9, 0xE5, 0x0E, 0x24, 0xDC, 0xCA, 0x9E,
	})
)

const defaultChunkSize = 20

// Transport implements companion/transport.Transport over BLE Nordic UART Service.
//
// MTU: BlueZ negotiates ATT MTU automatically during connection. After connect,
// GetMTU() is called on the TX characteristic to discover the usable payload
// size (MTU − 3 ATT overhead). If that fails the chunk size stays at 20 bytes.
//
// Reconnect: a background goroutine watches for BlueZ disconnect events via
// adapter.SetConnectHandler and retries with exponential backoff (1s→30s max).
//
// Name filter: if nameFilter is set, the scanner accepts devices whose
// advertised LocalName contains the filter string (case-insensitive substring),
// in addition to the NUS service UUID check. Useful when the device does not
// include service UUIDs in its advertisement packet.
type Transport struct {
	addr       string // empty = scan
	nameFilter string // optional advertised-name filter
	debugf     func(string, ...any)

	mu                sync.Mutex
	adapter           *bluetooth.Adapter
	device            bluetooth.Device
	devAddr           bluetooth.Address
	rxChar            bluetooth.DeviceCharacteristic
	parser            *companion.FrameParser
	chunkSize         int
	connected         bool
	done              chan struct{}
	responseHandler   func(companion.Response)
	errorHandler      func(error)
	disconnectHandler func()
	reconnectHandler  func()
}

func noop(string, ...any) {}

// New returns a Transport that connects directly to addr (BLE MAC address
// string). If addr is empty the first NUS-advertising device found by scan
// is used.
func New(addr string) *Transport {
	return &Transport{
		addr:      addr,
		debugf:    noop,
		parser:    companion.NewFrameParser(),
		chunkSize: defaultChunkSize,
		done:      make(chan struct{}),
	}
}

// NewWithNameFilter returns a Transport that scans for a device whose
// advertised name contains nameFilter. The NUS service UUID match is tried
// first; the name match is a fallback for devices that omit service UUIDs
// from their advertisement.
func NewWithNameFilter(nameFilter string) *Transport {
	return &Transport{
		nameFilter: nameFilter,
		debugf:     noop,
		parser:     companion.NewFrameParser(),
		chunkSize:  defaultChunkSize,
		done:       make(chan struct{}),
	}
}

// EnableDebug routes debug output to stderr.
func (t *Transport) EnableDebug() {
	t.debugf = func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "[ble] "+format+"\n", args...)
	}
}

func (t *Transport) SetResponseHandler(h func(companion.Response)) {
	t.mu.Lock()
	t.responseHandler = h
	t.mu.Unlock()
}

func (t *Transport) SetErrorHandler(h func(error)) {
	t.mu.Lock()
	t.errorHandler = h
	t.mu.Unlock()
}

func (t *Transport) SetDisconnectHandler(h func()) {
	t.mu.Lock()
	t.disconnectHandler = h
	t.mu.Unlock()
}

func (t *Transport) SetReconnectHandler(h func()) {
	t.mu.Lock()
	t.reconnectHandler = h
	t.mu.Unlock()
}

// SetMTU overrides the chunk size used when writing to the RX characteristic.
// Call this before Connect if automatic MTU detection is not working on your
// platform. The value should be the negotiated ATT MTU minus 3 bytes of
// overhead (e.g. MTU=247 → SetMTU(244)).
func (t *Transport) SetMTU(payloadBytes int) {
	t.mu.Lock()
	t.chunkSize = payloadBytes
	t.mu.Unlock()
}

func (t *Transport) Connect(ctx context.Context) error {
	adapter := bluetooth.DefaultAdapter
	if err := adapter.Enable(); err != nil {
		return fmt.Errorf("BLE unavailable (no adapter or BlueZ not running): %w", err)
	}

	t.mu.Lock()
	t.adapter = adapter
	t.done = make(chan struct{})
	t.mu.Unlock()

	// Register connect/disconnect events from BlueZ. The handler fires for any
	// device; filter to our device by address after we know it.
	adapter.SetConnectHandler(func(dev bluetooth.Device, connected bool) {
		t.mu.Lock()
		myAddr := t.devAddr
		dh := t.disconnectHandler
		t.mu.Unlock()

		if dev.Address.String() != myAddr.String() {
			return
		}
		if !connected {
			t.mu.Lock()
			t.connected = false
			t.mu.Unlock()
			if dh != nil {
				dh()
			}
			go t.reconnectLoop()
		}
	})

	return t.connectDevice(ctx, adapter)
}

// connectDevice performs the full GATT setup: resolve address, connect,
// discover NUS service + characteristics, enable notifications, detect MTU.
func (t *Transport) connectDevice(ctx context.Context, adapter *bluetooth.Adapter) error {
	addr, err := t.resolveAddress(ctx, adapter)
	if err != nil {
		return err
	}

	// tinygo/bluetooth v0.13.0 has a race: Device1.Connect() blocks until the
	// connection is established, but the "Connected" D-Bus signal fires while
	// that blocking call is in progress. Because the signal channel is
	// unbuffered, godbus drops it, and the post-call goroutine waiting for the
	// signal blocks forever. Work around it by driving the BlueZ connection
	// ourselves with a context-aware D-Bus call first; adapter.Connect() then
	// sees the device already connected and takes the fast path.
	t.debugf("bleDirectConnect %s", addr)
	if err := bleDirectConnect(ctx, addr.String()); err != nil {
		return fmt.Errorf("connect to %s: %w", addr, err)
	}
	t.debugf("bleDirectConnect done")

	t.debugf("adapter.Connect")
	device, err := adapter.Connect(addr, bluetooth.ConnectionParams{})
	if err != nil {
		return fmt.Errorf("connect to %s: %w", addr, err)
	}
	t.debugf("adapter.Connect done")

	t.debugf("DiscoverServices")
	svcs, err := device.DiscoverServices([]bluetooth.UUID{nusSvcUUID})
	t.debugf("DiscoverServices done: err=%v len=%d", err, len(svcs))
	if err != nil {
		return fmt.Errorf("discover NUS service: %w", err)
	}
	if len(svcs) == 0 {
		return fmt.Errorf("NUS service not found on device")
	}

	t.debugf("DiscoverCharacteristics")
	chars, err := svcs[0].DiscoverCharacteristics([]bluetooth.UUID{nusRxUUID, nusTxUUID})
	t.debugf("DiscoverCharacteristics done: err=%v", err)
	if err != nil {
		return fmt.Errorf("discover NUS characteristics: %w", err)
	}

	var rxChar, txChar bluetooth.DeviceCharacteristic
	for _, c := range chars {
		switch c.UUID() {
		case nusRxUUID:
			rxChar = c
		case nusTxUUID:
			txChar = c
		}
	}

	// MeshCore firmware disconnects the BLE link after a short idle timeout if
	// no protocol data is received. Send a raw DeviceQuery to the RX characteristic
	// to reset the firmware's idle timer before the CCCD write in StartNotify.
	// BLE uses unframed raw bytes — no FrameEncode wrapper.
	keepalive := companion.DeviceQueryCommand{AppTargetVersion: companion.SupportedProtocolVersion}.ToBytes()
	_, _ = rxChar.WriteWithoutResponse(keepalive)

	t.debugf("EnableNotifications")
	if err := txChar.EnableNotifications(t.onNotification); err != nil {
		return fmt.Errorf("enable NUS TX notifications: %w", err)
	}
	t.debugf("EnableNotifications done")

	// Read the ATT MTU negotiated by BlueZ. Subtract 3 bytes of ATT overhead.
	// Fall back to the current chunkSize if unavailable.
	if mtu, err := txChar.GetMTU(); err == nil && mtu > 3 {
		t.mu.Lock()
		t.chunkSize = int(mtu) - 3
		t.mu.Unlock()
	}

	t.mu.Lock()
	t.device = device
	t.devAddr = addr
	t.rxChar = rxChar
	t.connected = true
	t.mu.Unlock()

	return nil
}

// reconnectLoop retries connectDevice with exponential backoff until it
// succeeds or the Transport is closed.
func (t *Transport) reconnectLoop() {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		t.mu.Lock()
		done := t.done
		adapter := t.adapter
		t.mu.Unlock()

		select {
		case <-done:
			return
		case <-time.After(backoff):
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := t.connectDevice(ctx, adapter)
		cancel()

		if err == nil {
			t.mu.Lock()
			rh := t.reconnectHandler
			t.mu.Unlock()
			if rh != nil {
				rh()
			}
			return
		}

		t.mu.Lock()
		eh := t.errorHandler
		t.mu.Unlock()
		if eh != nil {
			eh(fmt.Errorf("reconnect attempt failed: %w", err))
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (t *Transport) Close() error {
	t.mu.Lock()
	close(t.done)
	t.connected = false
	dev := t.device
	t.mu.Unlock()
	return dev.Disconnect()
}

func (t *Transport) Send(command []byte) error {
	t.debugf("Send: cmd[0]=%d len=%d", command[0], len(command))
	// BLE uses raw (unframed) bytes — no FrameEncode wrapper.
	// Each write is a discrete GATT packet; framing is only needed for streams.
	t.mu.Lock()
	if !t.connected {
		t.mu.Unlock()
		return fmt.Errorf("not connected")
	}
	rxChar := t.rxChar
	chunk := t.chunkSize
	t.mu.Unlock()

	data := command
	for len(data) > 0 {
		n := chunk
		if n > len(data) {
			n = len(data)
		}
		if _, err := rxChar.WriteWithoutResponse(data[:n]); err != nil {
			return fmt.Errorf("write chunk: %w", err)
		}
		data = data[n:]
	}
	return nil
}

func (t *Transport) onNotification(buf []byte) {
	t.debugf("notification: %d bytes code=0x%02x", len(buf), buf[0])
	// BLE uses raw (unframed) responses — each notification IS a complete response.
	t.mu.Lock()
	rh := t.responseHandler
	eh := t.errorHandler
	t.mu.Unlock()

	resp, err := companion.ParseResponse(buf)
	if err != nil {
		if eh != nil {
			eh(fmt.Errorf("parse response: %w", err))
		}
		return
	}
	if rh != nil {
		rh(resp)
	}
}

func (t *Transport) resolveAddress(ctx context.Context, adapter *bluetooth.Adapter) (bluetooth.Address, error) {
	if t.addr != "" {
		// Device address is known — connect directly. BlueZ can reconnect to a
		// bonded device without an active scan. Address type is ignored on Linux.
		var addr bluetooth.Address
		addr.Set(t.addr)
		return addr, nil
	}

	found := make(chan bluetooth.Address, 1)
	scanErr := make(chan error, 1)

	go func() {
		err := adapter.Scan(func(a *bluetooth.Adapter, result bluetooth.ScanResult) {
			if t.matches(result) {
				found <- result.Address
				_ = a.StopScan()
			}
		})
		if err != nil {
			scanErr <- err
		}
	}()

	select {
	case addr := <-found:
		return addr, nil
	case err := <-scanErr:
		return bluetooth.Address{}, fmt.Errorf("BLE scan: %w", err)
	case <-ctx.Done():
		_ = adapter.StopScan()
		return bluetooth.Address{}, ctx.Err()
	}
}

// matches returns true if the scan result should be selected. Accepts on NUS
// service UUID presence, or on nameFilter substring match when set.
func (t *Transport) matches(result bluetooth.ScanResult) bool {
	if result.HasServiceUUID(nusSvcUUID) {
		return true
	}
	if t.nameFilter != "" {
		name := result.LocalName()
		return containsFold(name, t.nameFilter)
	}
	return false
}

func containsFold(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	sl, subl := len(s), len(sub)
	for i := 0; i <= sl-subl; i++ {
		if equalFold(s[i:i+subl], sub) {
			return true
		}
	}
	return false
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 32
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// bleDirectConnect calls BlueZ's Device1.Connect() directly via D-Bus,
// bypassing tinygo/bluetooth's adapter.Connect() which has a race condition:
// it uses an unbuffered signal channel so the "Connected" D-Bus signal gets
// dropped while the blocking Connect call is in progress. This function uses
// CallWithContext so the timeout is properly honoured.
func bleDirectConnect(ctx context.Context, macAddr string) error {
	bus, err := dbus.SystemBus()
	if err != nil {
		return fmt.Errorf("D-Bus system bus: %w", err)
	}

	devPath := dbus.ObjectPath("/org/bluez/hci0/dev_" +
		strings.ReplaceAll(strings.ToUpper(macAddr), ":", "_"))
	dev := bus.Object("org.bluez", devPath)

	// Fast path: already connected.
	prop, err := dev.GetProperty("org.bluez.Device1.Connected")
	if err == nil {
		if c, _ := prop.Value().(bool); c {
			return nil
		}
	}

	call := dev.CallWithContext(ctx, "org.bluez.Device1.Connect", 0)
	return call.Err
}
