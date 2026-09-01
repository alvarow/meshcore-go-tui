package storage

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

type Direction int

const (
	Inbound  Direction = 0
	Outbound Direction = 1
)

type StoredMessage struct {
	Timestamp time.Time `json:"ts"`
	From      string    `json:"from"`
	Text      string    `json:"text"`
	Direction Direction `json:"dir"`
	Acked     bool      `json:"acked"`
}

var (
	bucketContacts = []byte("contacts")
	bucketChannels = []byte("channels")
	bucketLastRead = []byte("lastread")
)

type Store struct {
	db *bolt.DB
}

func DBPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	// Use XDG data home when available.
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		dir = xdg
	} else {
		base, err2 := os.UserHomeDir()
		if err2 == nil {
			dir = filepath.Join(base, ".local", "share")
		}
	}
	return filepath.Join(dir, "meshcore-go-tui", "messages.db")
}

func Open() (*Store, error) {
	path := DBPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// Ensure top-level buckets exist.
	err = db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(bucketContacts); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(bucketChannels); err != nil {
			return err
		}
		_, err := tx.CreateBucketIfNotExists(bucketLastRead)
		return err
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// OpenAt opens a DB at a custom path — used by tests.
func OpenAt(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(bucketContacts); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(bucketChannels); err != nil {
			return err
		}
		_, err := tx.CreateBucketIfNotExists(bucketLastRead)
		return err
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func tsKey(t time.Time) []byte {
	key := make([]byte, 8)
	binary.BigEndian.PutUint64(key, uint64(t.UnixNano()))
	return key
}

func encode(msg StoredMessage) ([]byte, error) { return json.Marshal(msg) }
func decode(data []byte) (StoredMessage, error) {
	var m StoredMessage
	return m, json.Unmarshal(data, &m)
}

// SaveDirectMessage stores a direct message under contacts/<contactKey>.
func (s *Store) SaveDirectMessage(contactKey string, msg StoredMessage) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		parent := tx.Bucket(bucketContacts)
		sub, err := parent.CreateBucketIfNotExists([]byte(contactKey))
		if err != nil {
			return err
		}
		val, err := encode(msg)
		if err != nil {
			return err
		}
		return sub.Put(tsKey(msg.Timestamp), val)
	})
}

// LoadDirectMessages returns up to limit messages for a contact, oldest first.
func (s *Store) LoadDirectMessages(contactKey string, limit int) ([]StoredMessage, error) {
	var msgs []StoredMessage
	err := s.db.View(func(tx *bolt.Tx) error {
		parent := tx.Bucket(bucketContacts)
		sub := parent.Bucket([]byte(contactKey))
		if sub == nil {
			return nil
		}
		msgs = loadLast(sub, limit)
		return nil
	})
	return msgs, err
}

// MarkAcked sets Acked=true on the message with the given timestamp.
func (s *Store) MarkAcked(contactKey string, timestamp time.Time) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		parent := tx.Bucket(bucketContacts)
		sub := parent.Bucket([]byte(contactKey))
		if sub == nil {
			return nil
		}
		key := tsKey(timestamp)
		val := sub.Get(key)
		if val == nil {
			return nil
		}
		msg, err := decode(val)
		if err != nil {
			return err
		}
		msg.Acked = true
		newVal, err := encode(msg)
		if err != nil {
			return err
		}
		return sub.Put(key, newVal)
	})
}

// SaveChannelMessage stores a channel message under channels/<idx>.
func (s *Store) SaveChannelMessage(channelIdx int, msg StoredMessage) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		parent := tx.Bucket(bucketChannels)
		key := []byte(fmt.Sprintf("%d", channelIdx))
		sub, err := parent.CreateBucketIfNotExists(key)
		if err != nil {
			return err
		}
		val, err := encode(msg)
		if err != nil {
			return err
		}
		return sub.Put(tsKey(msg.Timestamp), val)
	})
}

// LoadChannelMessages returns up to limit messages for a channel, oldest first.
func (s *Store) LoadChannelMessages(channelIdx int, limit int) ([]StoredMessage, error) {
	var msgs []StoredMessage
	err := s.db.View(func(tx *bolt.Tx) error {
		parent := tx.Bucket(bucketChannels)
		sub := parent.Bucket([]byte(fmt.Sprintf("%d", channelIdx)))
		if sub == nil {
			return nil
		}
		msgs = loadLast(sub, limit)
		return nil
	})
	return msgs, err
}

// LoadDirectMessagesBefore returns up to limit direct messages with timestamp < before, oldest first.
func (s *Store) LoadDirectMessagesBefore(contactKey string, before time.Time, limit int) ([]StoredMessage, error) {
	var msgs []StoredMessage
	err := s.db.View(func(tx *bolt.Tx) error {
		parent := tx.Bucket(bucketContacts)
		sub := parent.Bucket([]byte(contactKey))
		if sub == nil {
			return nil
		}
		msgs = loadBefore(sub, before, limit)
		return nil
	})
	return msgs, err
}

// LoadChannelMessagesBefore returns up to limit channel messages with timestamp < before, oldest first.
func (s *Store) LoadChannelMessagesBefore(channelIdx int, before time.Time, limit int) ([]StoredMessage, error) {
	var msgs []StoredMessage
	err := s.db.View(func(tx *bolt.Tx) error {
		parent := tx.Bucket(bucketChannels)
		sub := parent.Bucket([]byte(fmt.Sprintf("%d", channelIdx)))
		if sub == nil {
			return nil
		}
		msgs = loadBefore(sub, before, limit)
		return nil
	})
	return msgs, err
}

// SetLastRead records the last-read timestamp for a direct message contact.
func (s *Store) SetLastRead(contactKey string, ts time.Time) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketLastRead)
		return b.Put([]byte("contact/"+contactKey), tsKey(ts))
	})
}

// GetLastRead returns the last-read timestamp for a contact. Returns zero time if not set.
func (s *Store) GetLastRead(contactKey string) (time.Time, error) {
	var t time.Time
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketLastRead)
		v := b.Get([]byte("contact/" + contactKey))
		if v == nil {
			return nil
		}
		t = time.Unix(0, int64(binary.BigEndian.Uint64(v)))
		return nil
	})
	return t, err
}

// SetChannelLastRead records the last-read timestamp for a channel.
func (s *Store) SetChannelLastRead(channelIdx int, ts time.Time) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketLastRead)
		return b.Put([]byte(fmt.Sprintf("channel/%d", channelIdx)), tsKey(ts))
	})
}

// GetChannelLastRead returns the last-read timestamp for a channel. Returns zero time if not set.
func (s *Store) GetChannelLastRead(channelIdx int) (time.Time, error) {
	var t time.Time
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketLastRead)
		v := b.Get([]byte(fmt.Sprintf("channel/%d", channelIdx)))
		if v == nil {
			return nil
		}
		t = time.Unix(0, int64(binary.BigEndian.Uint64(v)))
		return nil
	})
	return t, err
}

// ClearDirectMessages deletes all stored messages for a contact.
func (s *Store) ClearDirectMessages(contactKey string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		parent := tx.Bucket(bucketContacts)
		return parent.DeleteBucket([]byte(contactKey))
	})
}

// ClearChannelMessages deletes all stored messages for a channel.
func (s *Store) ClearChannelMessages(channelIdx int) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		parent := tx.Bucket(bucketChannels)
		return parent.DeleteBucket([]byte(fmt.Sprintf("%d", channelIdx)))
	})
}

// DeleteDirectMessage removes a single direct message identified by its timestamp.
func (s *Store) DeleteDirectMessage(contactKey string, ts time.Time) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		parent := tx.Bucket(bucketContacts)
		sub := parent.Bucket([]byte(contactKey))
		if sub == nil {
			return nil
		}
		return sub.Delete(tsKey(ts))
	})
}

// DeleteChannelMessage removes a single channel message identified by its timestamp.
func (s *Store) DeleteChannelMessage(channelIdx int, ts time.Time) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		parent := tx.Bucket(bucketChannels)
		sub := parent.Bucket([]byte(fmt.Sprintf("%d", channelIdx)))
		if sub == nil {
			return nil
		}
		return sub.Delete(tsKey(ts))
	})
}

// loadBefore walks a bucket backwards from just before `before`, returning up to limit messages oldest first.
func loadBefore(b *bolt.Bucket, before time.Time, limit int) []StoredMessage {
	var buf []StoredMessage
	c := b.Cursor()
	seek := tsKey(before)
	// Position at the first key >= before, then step back one.
	var v []byte
	k, _ := c.Seek(seek) // discard value at seek position; we step back immediately
	if k == nil {
		// before is past the last key — start from the end.
		k, v = c.Last()
	} else {
		k, v = c.Prev()
	}
	for ; k != nil && len(buf) < limit; k, v = c.Prev() {
		if msg, err := decode(v); err == nil {
			buf = append(buf, msg)
		}
	}
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return buf
}

// loadLast walks a bucket's cursor in reverse and returns up to limit messages, oldest first.
func loadLast(b *bolt.Bucket, limit int) []StoredMessage {
	var buf []StoredMessage
	c := b.Cursor()
	for k, v := c.Last(); k != nil && len(buf) < limit; k, v = c.Prev() {
		if msg, err := decode(v); err == nil {
			buf = append(buf, msg)
		}
	}
	// Reverse so result is oldest-first.
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return buf
}
