package views

import (
	"time"

	"github.com/meshcore-go/meshcore-go/companion"
	"github.com/meshcore-go/meshcore-go/companion/client"
)

// SessionReadyMsg is sent once the session startup sequence completes.
type SessionReadyMsg struct {
	Client   *client.Client
	Self     companion.SelfInfoResponse
	Contacts []companion.ContactResponse
	Channels []companion.ChannelInfoResponse
}

// InboundDirectMsg is sent when a direct message arrives from a contact.
type InboundDirectMsg struct {
	PubKeyPrefix [6]byte
	Text         string
	Timestamp    time.Time
}

// OutboundAckMsg is sent when the device confirms delivery of a sent message.
type OutboundAckMsg struct {
	RoundTripMs uint32
}

// NodeAdvertMsg is sent when a new peer advertisement is received.
type NodeAdvertMsg struct {
	Name     string
	NodeType byte
	SNR      float32
	RSSI     int8
	PubKey   [32]byte
}

// InboundChannelMsg is sent when a group channel message arrives.
type InboundChannelMsg struct {
	ChannelIdx int
	Text       string
	Timestamp  time.Time
}

// ContactDeletedMsg is sent when a contact is removed from the device.
type ContactDeletedMsg struct {
	PublicKey [32]byte
}

// ReconnectingMsg is sent when the BLE transport detects a disconnect and begins reconnecting.
type ReconnectingMsg struct{}

// ReconnectedMsg is sent when the BLE transport successfully reconnects.
type ReconnectedMsg struct{ DeviceName string }
