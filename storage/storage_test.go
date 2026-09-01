package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	store, err := OpenAt(filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = store.Close() }()

	contactKey := "aabbccddeeff"
	base := time.Unix(1_700_000_000, 0)

	// Save 3 direct messages.
	for i := 0; i < 3; i++ {
		msg := StoredMessage{
			Timestamp: base.Add(time.Duration(i) * time.Second),
			From:      "Alice",
			Text:      "hello " + string(rune('A'+i)),
			Direction: Inbound,
		}
		if err := store.SaveDirectMessage(contactKey, msg); err != nil {
			t.Fatalf("save direct msg %d: %v", i, err)
		}
	}

	msgs, err := store.LoadDirectMessages(contactKey, 100)
	if err != nil {
		t.Fatalf("load direct: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("want 3 direct messages, got %d", len(msgs))
	}
	// Oldest first.
	if !msgs[0].Timestamp.Before(msgs[1].Timestamp) {
		t.Errorf("messages not in chronological order")
	}

	// Mark the first message acked.
	if err := store.MarkAcked(contactKey, msgs[0].Timestamp); err != nil {
		t.Fatalf("mark acked: %v", err)
	}
	reloaded, _ := store.LoadDirectMessages(contactKey, 100)
	if !reloaded[0].Acked {
		t.Errorf("expected first message to be acked")
	}

	// Save 2 channel messages for channel 0.
	for i := 0; i < 2; i++ {
		msg := StoredMessage{
			Timestamp: base.Add(time.Duration(i) * time.Second),
			From:      "Bob",
			Text:      "channel msg " + string(rune('0'+i)),
			Direction: Inbound,
		}
		if err := store.SaveChannelMessage(0, msg); err != nil {
			t.Fatalf("save channel msg %d: %v", i, err)
		}
	}

	chMsgs, err := store.LoadChannelMessages(0, 100)
	if err != nil {
		t.Fatalf("load channel: %v", err)
	}
	if len(chMsgs) != 2 {
		t.Fatalf("want 2 channel messages, got %d", len(chMsgs))
	}
	if !chMsgs[0].Timestamp.Before(chMsgs[1].Timestamp) {
		t.Errorf("channel messages not in chronological order")
	}

	// Channels are isolated from contacts.
	empty, _ := store.LoadChannelMessages(1, 100)
	if len(empty) != 0 {
		t.Errorf("channel 1 should be empty, got %d messages", len(empty))
	}
}

func TestLoadBefore(t *testing.T) {
	store, err := OpenAt(filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = store.Close() }()

	base := time.Unix(1_700_000_000, 0)
	contactKey := "deadbeef"
	for i := 0; i < 5; i++ {
		_ = store.SaveDirectMessage(contactKey, StoredMessage{
			Timestamp: base.Add(time.Duration(i) * time.Second),
			From:      "x", Text: "msg", Direction: Inbound,
		})
	}

	// Ask for messages before index 2 (i.e. timestamps 0 and 1).
	cutoff := base.Add(2 * time.Second)
	msgs, err := store.LoadDirectMessagesBefore(contactKey, cutoff, 10)
	if err != nil {
		t.Fatalf("load before: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages before cutoff, got %d", len(msgs))
	}
	if !msgs[0].Timestamp.Before(msgs[1].Timestamp) {
		t.Errorf("messages not in chronological order")
	}
	if !msgs[1].Timestamp.Before(cutoff) {
		t.Errorf("last message not before cutoff")
	}

	// Ask for at most 1.
	limited, err := store.LoadDirectMessagesBefore(contactKey, cutoff, 1)
	if err != nil {
		t.Fatalf("load before limited: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("want 1, got %d", len(limited))
	}
}

func TestLastRead(t *testing.T) {
	store, err := OpenAt(filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Zero time before any set.
	ts, err := store.GetLastRead("contact1")
	if err != nil {
		t.Fatalf("get before set: %v", err)
	}
	if !ts.IsZero() {
		t.Errorf("expected zero time, got %v", ts)
	}

	now := time.Unix(1_700_000_123, 456789000)
	if err := store.SetLastRead("contact1", now); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := store.GetLastRead("contact1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.UnixNano() != now.UnixNano() {
		t.Errorf("want %v, got %v", now.UnixNano(), got.UnixNano())
	}

	// Channel last-read.
	ts2, err := store.GetChannelLastRead(3)
	if err != nil {
		t.Fatalf("channel get before set: %v", err)
	}
	if !ts2.IsZero() {
		t.Errorf("expected zero time for channel, got %v", ts2)
	}
	if err := store.SetChannelLastRead(3, now); err != nil {
		t.Fatalf("channel set: %v", err)
	}
	got2, err := store.GetChannelLastRead(3)
	if err != nil {
		t.Fatalf("channel get: %v", err)
	}
	if got2.UnixNano() != now.UnixNano() {
		t.Errorf("channel: want %v, got %v", now.UnixNano(), got2.UnixNano())
	}

	// Different contacts/channels are isolated.
	zero, _ := store.GetLastRead("other")
	if !zero.IsZero() {
		t.Errorf("unset contact should be zero")
	}
	zero2, _ := store.GetChannelLastRead(0)
	if !zero2.IsZero() {
		t.Errorf("unset channel should be zero")
	}
}

func TestLoadLimit(t *testing.T) {
	store, err := OpenAt(filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = store.Close() }()

	base := time.Unix(1_700_000_000, 0)
	for i := 0; i < 20; i++ {
		_ = store.SaveDirectMessage("key", StoredMessage{
			Timestamp: base.Add(time.Duration(i) * time.Second),
			From:      "x",
			Text:      "msg",
			Direction: Outbound,
		})
	}

	msgs, _ := store.LoadDirectMessages("key", 5)
	if len(msgs) != 5 {
		t.Fatalf("limit=5: want 5, got %d", len(msgs))
	}
	// Should be the 5 newest in oldest-first order.
	if msgs[0].Timestamp.Before(base.Add(14 * time.Second)) {
		t.Errorf("expected newest 5, got older messages")
	}
}
