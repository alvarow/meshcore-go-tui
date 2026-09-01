package config

import "github.com/charmbracelet/bubbles/key"

// KeysConfig holds raw key strings loaded from config.toml [keys].
// Each field maps to a single key binding; leave empty to use the default.
type KeysConfig struct {
	NextTab    string `toml:"next_tab"`
	PrevTab    string `toml:"prev_tab"`
	Quit       string `toml:"quit"`
	Send       string `toml:"send"`
	ScrollUp   string `toml:"scroll_up"`
	ScrollDown string `toml:"scroll_down"`
	Advert     string `toml:"advert"`
	Search     string `toml:"search"`
	SelectMode string `toml:"select_mode"`
	DeleteMsg  string `toml:"delete_msg"`
	ClearAll   string `toml:"clear_all"`
	OffRecord  string `toml:"off_record"`
	JoinChan   string `toml:"join_channel"`
	LeaveChan  string `toml:"leave_channel"`
}

// KeyMap holds compiled key.Bindings derived from KeysConfig + defaults.
type KeyMap struct {
	NextTab    key.Binding
	PrevTab    key.Binding
	Quit       key.Binding
	Send       key.Binding
	ScrollUp   key.Binding
	ScrollDown key.Binding
	Advert     key.Binding
	Search     key.Binding
	SelectMode key.Binding
	DeleteMsg  key.Binding
	ClearAll   key.Binding
	OffRecord  key.Binding
	JoinChan   key.Binding
	LeaveChan  key.Binding
}

// defaults maps each binding name to its default key string and help text.
var defaults = []struct {
	name, defaultKey, help string
}{
	{"next_tab", "alt+n", "next tab"},
	{"prev_tab", "alt+p", "prev tab"},
	{"quit", "q", "quit"},
	{"send", "enter", "send"},
	{"scroll_up", "pgup", "scroll up"},
	{"scroll_down", "pgdn", "scroll down"},
	{"advert", "ctrl+a", "send advert"},
	{"search", "/", "search"},
	{"select_mode", "s", "select mode"},
	{"delete_msg", "d", "delete message"},
	{"clear_all", "X", "clear all"},
	{"off_record", "ctrl+o", "off the record"},
	{"join_channel", "n", "join channel"},
	{"leave_channel", "d", "leave channel"},
}

func resolve(override, def string) string {
	if override != "" {
		return override
	}
	return def
}

// BuildKeyMap constructs a KeyMap from a KeysConfig, filling in defaults for
// any unset fields.
func BuildKeyMap(k KeysConfig) KeyMap {
	b := func(override, def, help string) key.Binding {
		k := resolve(override, def)
		return key.NewBinding(key.WithKeys(k), key.WithHelp(k, help))
	}
	return KeyMap{
		NextTab:    b(k.NextTab, "alt+n", "next tab"),
		PrevTab:    b(k.PrevTab, "alt+p", "prev tab"),
		Quit:       b(k.Quit, "q", "quit"),
		Send:       b(k.Send, "enter", "send"),
		ScrollUp:   b(k.ScrollUp, "pgup", "scroll up"),
		ScrollDown: b(k.ScrollDown, "pgdn", "scroll down"),
		Advert:     b(k.Advert, "ctrl+a", "send advert"),
		Search:     b(k.Search, "/", "search contacts"),
		SelectMode: b(k.SelectMode, "s", "select mode"),
		DeleteMsg:  b(k.DeleteMsg, "d", "delete message"),
		ClearAll:   b(k.ClearAll, "X", "clear all"),
		OffRecord:  b(k.OffRecord, "ctrl+o", "off the record"),
		JoinChan:   b(k.JoinChan, "n", "join channel"),
		LeaveChan:  b(k.LeaveChan, "d", "leave channel"),
	}
}

// DefaultKeyMap returns a KeyMap with all defaults applied.
func DefaultKeyMap() KeyMap { return BuildKeyMap(KeysConfig{}) }

// Defaults returns the default key string for each named binding.
// Used by the Settings tab to display current vs default values.
func DefaultKey(name string) string {
	for _, d := range defaults {
		if d.name == name {
			return d.defaultKey
		}
	}
	return ""
}
