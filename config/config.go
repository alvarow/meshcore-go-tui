package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Transport string

const (
	TransportBLE    Transport = "ble"
	TransportSerial Transport = "serial"
	TransportTCP    Transport = "tcp"
)

type Profile struct {
	Transport Transport `toml:"transport"`
	Device    string    `toml:"device"`
}

type Config struct {
	DefaultTransport Transport          `toml:"default_transport"`
	DefaultDevice    string             `toml:"default_device"`
	ScanNameFilter   string             `toml:"scan_name_filter"`
	Profile          map[string]Profile `toml:"profile"`
	Keys             KeysConfig         `toml:"keys"`
}

func DefaultConfig() *Config {
	return &Config{
		DefaultTransport: TransportBLE,
		Profile:          make(map[string]Profile),
	}
}

func ConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.Getenv("HOME")
	}
	return filepath.Join(dir, "meshcore", "config.toml")
}

func Load() (*Config, error) {
	path := ConfigPath()
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if cfg.Profile == nil {
		cfg.Profile = make(map[string]Profile)
	}
	return cfg, nil
}

func Save(cfg *Config) error {
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return toml.NewEncoder(f).Encode(cfg)
}
