package ble

import (
	"context"
	"fmt"
	"sync"
	"time"

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

// New returns a Transport that connects directly to addr (BLE MAC address
// string). If addr is empty the first NUS-advertising device found by scan
// is used.
func New(addr string) *Transport {
	return &Transport{
		addr:      addr,
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
		parser:     companion.NewFrameParser(),
		chunkSize:  defaultChunkSize,
		done:       make(chan struct{}),
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

	device, err := adapter.Connect(addr, bluetooth.ConnectionParams{})
	if err != nil {
		return fmt.Errorf("connect to %s: %w", addr, err)
	}

	svcs, err := device.DiscoverServices([]bluetooth.UUID{nusSvcUUID})
	if err != nil {
		_ = device.Disconnect()
		return fmt.Errorf("discover NUS service: %w", err)
	}
	if len(svcs) == 0 {
		_ = device.Disconnect()
		return fmt.Errorf("NUS service not found on device")
	}

	chars, err := svcs[0].DiscoverCharacteristics([]bluetooth.UUID{nusRxUUID, nusTxUUID})
	if err != nil {
		_ = device.Disconnect()
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

	if err := txChar.EnableNotifications(t.onNotification); err != nil {
		_ = device.Disconnect()
		return fmt.Errorf("enable NUS TX notifications: %w", err)
	}

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
	frame, err := companion.FrameEncode(companion.FrameTypeOutgoing, command)
	if err != nil {
		return fmt.Errorf("encode frame: %w", err)
	}

	t.mu.Lock()
	if !t.connected {
		t.mu.Unlock()
		return fmt.Errorf("not connected")
	}
	rxChar := t.rxChar
	chunk := t.chunkSize
	t.mu.Unlock()

	for len(frame) > 0 {
		n := chunk
		if n > len(frame) {
			n = len(frame)
		}
		if _, err := rxChar.WriteWithoutResponse(frame[:n]); err != nil {
			return fmt.Errorf("write chunk: %w", err)
		}
		frame = frame[n:]
	}
	return nil
}

func (t *Transport) onNotification(buf []byte) {
	t.mu.Lock()
	frames := t.parser.Feed(buf)
	rh := t.responseHandler
	eh := t.errorHandler
	t.mu.Unlock()

	for _, frame := range frames {
		resp, err := companion.ParseResponse(frame.Data)
		if err != nil {
			if eh != nil {
				eh(fmt.Errorf("parse response: %w", err))
			}
			continue
		}
		if rh != nil {
			rh(resp)
		}
	}
}

func (t *Transport) resolveAddress(ctx context.Context, adapter *bluetooth.Adapter) (bluetooth.Address, error) {
	if t.addr != "" {
		var addr bluetooth.Address
		addr.Set(t.addr)
		return addr, nil
	}

	found := make(chan bluetooth.ScanResult, 1)
	scanErr := make(chan error, 1)

	go func() {
		err := adapter.Scan(func(a *bluetooth.Adapter, result bluetooth.ScanResult) {
			if t.matches(result) {
				found <- result
				_ = a.StopScan()
			}
		})
		if err != nil {
			scanErr <- err
		}
	}()

	select {
	case result := <-found:
		return result.Address, nil
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
