package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/alvarow/meshcore-go-tui/ble"
	"github.com/alvarow/meshcore-go-tui/config"
	"github.com/alvarow/meshcore-go-tui/storage"
	"github.com/alvarow/meshcore-go-tui/tui"
	"github.com/alvarow/meshcore-go-tui/tui/scanner"
	"github.com/alvarow/meshcore-go-tui/tui/views"
	"github.com/meshcore-go/meshcore-go/companion"
	meshclient "github.com/meshcore-go/meshcore-go/companion/client"
	companionTransport "github.com/meshcore-go/meshcore-go/companion/transport"
)

// Version is stamped at build time via -ldflags.
var Version = "dev"

var (
	flagDevice    string
	flagTransport string
	flagProfile   string
)

func init() {
	flag.StringVar(&flagDevice, "device", "", "BLE address, serial port (/dev/ttyUSB0), or TCP host:port")
	flag.StringVar(&flagTransport, "transport", "", "Transport type: ble, serial, tcp (overrides config)")
	flag.StringVar(&flagProfile, "profile", "", "Config profile name to use")
}

func Execute() {
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load config: %v\n", err)
		cfg = config.DefaultConfig()
	}

	transport, device := resolveTransportAndDevice(cfg)

	// If BLE and no device address, run the scanner picker first.
	if (transport == config.TransportBLE || transport == "") && device == "" {
		result, err := scanner.Run(cfg.ScanNameFilter)
		if err != nil {
			if errors.Is(err, scanner.ErrBLEUnavailable) {
				fmt.Fprintln(os.Stderr, `No Bluetooth adapter on this machine.

To connect via serial:  meshcore-tui --transport serial --device /dev/ttyUSB0
To connect via TCP:     meshcore-tui --transport tcp --device host:port

To set a default in config (~/.config/meshcore/config.toml):
  default_transport = "serial"
  default_device    = "/dev/ttyUSB0"`)
				os.Exit(0)
			}
			fmt.Fprintf(os.Stderr, "scanner error: %v\n", err)
			os.Exit(1)
		}
		if result.Canceled {
			os.Exit(0)
		}
		device = result.Address
	}

	store, err := storage.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not open message store: %v\n", err)
		store = nil
	}
	if store != nil {
		defer store.Close()
	}

	// Create client placeholder; will be set after transport connects.
	app := tui.New(device, nil, store, cfg)
	p := tea.NewProgram(app, tea.WithAltScreen())

	go func() {
		t, err := buildTransport(transport, device)
		if err != nil {
			p.Send(tui.ErrorMsg{Err: err})
			return
		}

		ctx := context.Background()
		if err := t.Connect(ctx); err != nil {
			p.Send(tui.ErrorMsg{Err: fmt.Errorf("connect: %w", err)})
			return
		}

		// Wire BLE disconnect/reconnect events into the TUI status bar.
		if bt, ok := t.(*ble.Transport); ok {
			bt.SetDisconnectHandler(func() {
				p.Send(views.ReconnectingMsg{})
			})
			bt.SetReconnectHandler(func() {
				p.Send(views.ReconnectedMsg{DeviceName: device})
			})
		}

		c := meshclient.New(t)

		// Session startup sequence.
		deviceName := device
		if _, err := c.DeviceQuery(ctx); err != nil {
			// Non-fatal: older firmware may not support DeviceQuery.
			fmt.Fprintf(os.Stderr, "device query: %v\n", err)
		}

		self, err := c.AppStart(ctx, 1, "meshcore-tui")
		if err != nil {
			p.Send(tui.ErrorMsg{Err: fmt.Errorf("app start: %w", err)})
			return
		}
		if self.Name != "" {
			deviceName = self.Name
		}

		contacts, err := c.GetContacts(ctx)
		if err != nil {
			// Non-fatal: continue with empty contacts.
			fmt.Fprintf(os.Stderr, "get contacts: %v\n", err)
		}

		var channels []companion.ChannelInfoResponse
		for i := byte(0); i < 8; i++ {
			ch, err := c.GetChannel(ctx, i)
			if err != nil {
				break
			}
			if ch.Name != "" {
				channels = append(channels, ch)
			}
		}

		p.Send(tui.ConnectedMsg{DeviceName: deviceName})
		p.Send(views.SessionReadyMsg{
			Self:     self,
			Contacts: contacts,
			Channels: channels,
		})

		// Wire async push handlers.
		c.OnPush(companion.PushMsgWaiting, func(_ companion.Response) {
			msgs, err := c.GetWaitingMessages(ctx)
			if err != nil {
				return
			}
			for _, m := range msgs {
				if m.Contact != nil {
					p.Send(views.InboundDirectMsg{
						PubKeyPrefix: m.Contact.PubKeyPrefix,
						Text:         m.Contact.Text,
						Timestamp:    time.Unix(int64(m.Contact.SenderTimestamp), 0),
					})
				}
				if m.Channel != nil {
					p.Send(views.InboundChannelMsg{
						ChannelIdx: int(m.Channel.ChannelIdx),
						Text:       m.Channel.Text,
						Timestamp:  time.Unix(int64(m.Channel.SenderTimestamp), 0),
					})
				}
			}
		})

		c.OnPush(companion.PushSendConfirmed, func(resp companion.Response) {
			confirmed := resp.Data.(companion.PushSendConfirmedResponse)
			p.Send(views.OutboundAckMsg{RoundTripMs: confirmed.RoundTrip})
		})

		c.OnPush(companion.PushNewAdvert, func(resp companion.Response) {
			advert := resp.Data.(companion.PushNewAdvertResponse)
			p.Send(views.NodeAdvertMsg{
				Name:     advert.AdvertName,
				NodeType: advert.Type,
				PubKey:   advert.PublicKey,
			})
		})

		c.OnPush(companion.PushAdvert, func(resp companion.Response) {
			// Older advert push — only carries pubkey, no name. Upsert with empty name.
			advert := resp.Data.(companion.PushAdvertResponse)
			p.Send(views.NodeAdvertMsg{PubKey: advert.PublicKey})
		})

		c.OnPush(companion.PushContactDeleted, func(resp companion.Response) {
			del := resp.Data.(companion.PushContactDeletedResponse)
			p.Send(views.ContactDeletedMsg{PublicKey: del.PublicKey})
		})
	}()

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func resolveTransportAndDevice(cfg *config.Config) (config.Transport, string) {
	if flagProfile != "" {
		if prof, ok := cfg.Profile[flagProfile]; ok {
			device := flagDevice
			if device == "" {
				device = prof.Device
			}
			transport := config.Transport(flagTransport)
			if transport == "" {
				transport = prof.Transport
			}
			return transport, device
		}
		fmt.Fprintf(os.Stderr, "warning: profile %q not found in config\n", flagProfile)
	}

	device := flagDevice
	transport := config.Transport(flagTransport)

	if device == "" {
		device = cfg.DefaultDevice
	}
	if transport == "" {
		transport = cfg.DefaultTransport
	}
	if transport == "" {
		transport = config.TransportBLE
	}

	return transport, device
}

func buildTransport(transport config.Transport, device string) (companionTransport.Transport, error) {
	switch transport {
	case config.TransportBLE, "":
		return ble.New(device), nil

	case config.TransportSerial:
		if device == "" {
			return nil, fmt.Errorf("--device is required for serial transport")
		}
		return companionTransport.NewSerialTransport(companionTransport.SerialConfig{Port: device}), nil

	case config.TransportTCP:
		if device == "" {
			return nil, fmt.Errorf("--device host:port is required for TCP transport")
		}
		return companionTransport.NewTCPTransport(companionTransport.TCPConfig{Address: device}), nil

	default:
		return nil, fmt.Errorf("unknown transport %q (valid: ble, serial, tcp)", transport)
	}
}
