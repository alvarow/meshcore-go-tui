package views

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/meshcore-go/go-cli/config"
)

type settingsFocus int

const (
	focusTransport settingsFocus = iota
	focusDevice
	focusScanFilter
	focusProfileList
	focusSave
	focusDiscard
)

type profileEntry struct {
	name      string
	transport config.Transport
	device    string
}

type SettingsView struct {
	cfg      *config.Config
	original config.Config
	dirty    bool

	focus settingsFocus

	deviceInput textinput.Model
	filterInput textinput.Model

	profiles     []profileEntry
	profSelected int

	editing       bool
	editIdx       int // -1 = new
	editName      textinput.Model
	editDevice    textinput.Model
	editTransport config.Transport
	editFocus     int // 0=name 1=transport 2=device

	status string
	width  int
	height int
}

func configToProfiles(cfg config.Config) []profileEntry {
	entries := make([]profileEntry, 0, len(cfg.Profile))
	for name, p := range cfg.Profile {
		entries = append(entries, profileEntry{name: name, transport: p.Transport, device: p.Device})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	return entries
}

func NewSettingsView(cfg *config.Config) *SettingsView {
	di := textinput.New()
	di.Placeholder = "AA:BB:CC:DD:EE:FF or /dev/ttyUSB0"
	di.CharLimit = 80
	di.SetValue(cfg.DefaultDevice)

	fi := textinput.New()
	fi.Placeholder = "e.g. mesh or L1"
	fi.CharLimit = 40
	fi.SetValue(cfg.ScanNameFilter)

	en := textinput.New()
	en.Placeholder = "profile name"
	en.CharLimit = 32

	ed := textinput.New()
	ed.Placeholder = "device address / path"
	ed.CharLimit = 80

	orig := *cfg
	return &SettingsView{
		cfg:         cfg,
		original:    orig,
		deviceInput: di,
		filterInput: fi,
		profiles:    configToProfiles(*cfg),
		editIdx:     -1,
		editName:    en,
		editDevice:  ed,
	}
}

func (v *SettingsView) Title() string {
	if v.dirty {
		return "Settings *"
	}
	return "Settings"
}

func (v *SettingsView) Init() tea.Cmd { return textinput.Blink }

func cycleTransport(t config.Transport) config.Transport {
	switch t {
	case config.TransportBLE:
		return config.TransportSerial
	case config.TransportSerial:
		return config.TransportTCP
	default:
		return config.TransportBLE
	}
}

func (v *SettingsView) openEditForm(idx int) {
	v.editing = true
	v.editIdx = idx
	v.editFocus = 0
	if idx >= 0 && idx < len(v.profiles) {
		p := v.profiles[idx]
		v.editName.SetValue(p.name)
		v.editTransport = p.transport
		v.editDevice.SetValue(p.device)
	} else {
		v.editName.SetValue("")
		v.editTransport = config.TransportBLE
		v.editDevice.SetValue("")
	}
	v.editName.Focus()
	v.editDevice.Blur()
}

func (v *SettingsView) commitEdit() {
	name := strings.TrimSpace(v.editName.Value())
	if name == "" {
		return
	}
	entry := profileEntry{name: name, transport: v.editTransport, device: v.editDevice.Value()}
	if v.editIdx >= 0 && v.editIdx < len(v.profiles) {
		v.profiles[v.editIdx] = entry
	} else {
		v.profiles = append(v.profiles, entry)
		sort.Slice(v.profiles, func(i, j int) bool { return v.profiles[i].name < v.profiles[j].name })
	}
	v.dirty = true
	v.editing = false
}

func (v *SettingsView) save() {
	v.cfg.DefaultDevice = v.deviceInput.Value()
	v.cfg.ScanNameFilter = v.filterInput.Value()
	v.cfg.Profile = make(map[string]config.Profile)
	for _, p := range v.profiles {
		v.cfg.Profile[p.name] = config.Profile{Transport: p.transport, Device: p.device}
	}
	if err := config.Save(v.cfg); err != nil {
		v.status = fmt.Sprintf("Save failed: %v", err)
	} else {
		v.status = "Saved — quit and reconnect to apply"
		v.dirty = false
		orig := *v.cfg
		v.original = orig
	}
}

func (v *SettingsView) discard() {
	*v.cfg = v.original
	v.deviceInput.SetValue(v.original.DefaultDevice)
	v.filterInput.SetValue(v.original.ScanNameFilter)
	v.profiles = configToProfiles(v.original)
	v.dirty = false
	v.status = ""
}

func (v *SettingsView) Update(msg tea.Msg) (View, tea.Cmd) {
	var cmd tea.Cmd

	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		v.width = m.Width
		v.height = m.Height
		return v, nil

	case tea.KeyMsg:
		// ── editing profile form ──────────────────────────────────────
		if v.editing {
			switch m.String() {
			case "esc":
				v.editing = false
				return v, nil
			case "tab", "shift+tab":
				if m.String() == "tab" {
					v.editFocus = (v.editFocus + 1) % 3
				} else {
					v.editFocus = (v.editFocus + 2) % 3
				}
				switch v.editFocus {
				case 0:
					v.editName.Focus()
					v.editDevice.Blur()
				case 1:
					v.editName.Blur()
					v.editDevice.Blur()
				case 2:
					v.editName.Blur()
					v.editDevice.Focus()
				}
				return v, nil
			case "left", "right", " ":
				if v.editFocus == 1 {
					v.editTransport = cycleTransport(v.editTransport)
					v.dirty = true
				}
				return v, nil
			case "enter":
				if v.editFocus == 2 || v.editFocus == 1 {
					v.commitEdit()
					return v, nil
				}
				v.editFocus++
				if v.editFocus == 1 {
					v.editName.Blur()
					v.editDevice.Blur()
				} else if v.editFocus == 2 {
					v.editDevice.Focus()
				}
				return v, nil
			}
			if v.editFocus == 0 {
				v.editName, cmd = v.editName.Update(msg)
			} else if v.editFocus == 2 {
				v.editDevice, cmd = v.editDevice.Update(msg)
			}
			return v, cmd
		}

		// ── normal mode ───────────────────────────────────────────────
		switch m.String() {
		case "tab":
			v.blurAll()
			v.focus = (v.focus + 1) % (focusDiscard + 1)
			v.focusActive()
			return v, nil
		case "shift+tab":
			v.blurAll()
			v.focus = (v.focus + focusDiscard) % (focusDiscard + 1)
			v.focusActive()
			return v, nil
		case "up":
			if v.focus == focusProfileList && v.profSelected > 0 {
				v.profSelected--
			}
			return v, nil
		case "down":
			if v.focus == focusProfileList && v.profSelected < len(v.profiles)-1 {
				v.profSelected++
			}
			return v, nil
		case "left", "right", " ":
			if v.focus == focusTransport {
				v.cfg.DefaultTransport = cycleTransport(v.cfg.DefaultTransport)
				v.dirty = true
			}
			return v, nil
		case "enter":
			switch v.focus {
			case focusSave:
				v.save()
			case focusDiscard:
				v.discard()
			case focusProfileList:
				if len(v.profiles) > 0 {
					v.openEditForm(v.profSelected)
				}
			}
			return v, nil
		case "n":
			v.openEditForm(-1)
			return v, nil
		case "d":
			if v.focus == focusProfileList && len(v.profiles) > 0 {
				v.profiles = append(v.profiles[:v.profSelected], v.profiles[v.profSelected+1:]...)
				if v.profSelected > 0 && v.profSelected >= len(v.profiles) {
					v.profSelected--
				}
				v.dirty = true
			}
			return v, nil
		}

		switch v.focus {
		case focusDevice:
			v.deviceInput, cmd = v.deviceInput.Update(msg)
			v.dirty = true
		case focusScanFilter:
			v.filterInput, cmd = v.filterInput.Update(msg)
			v.dirty = true
		}
		return v, cmd
	}

	return v, nil
}

func (v *SettingsView) blurAll() {
	v.deviceInput.Blur()
	v.filterInput.Blur()
}

func (v *SettingsView) focusActive() {
	switch v.focus {
	case focusDevice:
		v.deviceInput.Focus()
	case focusScanFilter:
		v.filterInput.Focus()
	}
}

func (v *SettingsView) View() string {
	if v.width == 0 {
		return ""
	}

	purple := lipgloss.Color("#7C3AED")
	subtle := lipgloss.Color("#475569")
	text := lipgloss.Color("#E2E8F0")
	red := lipgloss.Color("#EF4444")
	bg := lipgloss.Color("#1E293B")

	activeBorder := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(purple)
	inactiveBorder := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(subtle)
	label := lipgloss.NewStyle().Foreground(text).Width(18)
	dimStyle := lipgloss.NewStyle().Foreground(subtle)

	fieldW := v.width - 22
	if fieldW < 20 {
		fieldW = 20
	}

	border := func(active bool) lipgloss.Style {
		if active {
			return activeBorder
		}
		return inactiveBorder
	}

	// Transport row
	transVal := fmt.Sprintf(" %-8s ▼ ", v.cfg.DefaultTransport)
	transportRow := lipgloss.JoinHorizontal(lipgloss.Left,
		label.Render("Transport"),
		border(v.focus == focusTransport).Width(fieldW).Render(
			lipgloss.NewStyle().Foreground(purple).Render(transVal),
		),
	)

	// Device row
	v.deviceInput.Width = fieldW - 2
	deviceRow := lipgloss.JoinHorizontal(lipgloss.Left,
		label.Render("Device"),
		border(v.focus == focusDevice).Render(v.deviceInput.View()),
	)

	// Scan filter row
	v.filterInput.Width = fieldW - 2
	filterRow := lipgloss.JoinHorizontal(lipgloss.Left,
		label.Render("Scan name filter"),
		border(v.focus == focusScanFilter).Render(v.filterInput.View()),
	)

	// Divider
	divider := dimStyle.Render("── Profiles " + strings.Repeat("─", max(0, v.width-14)))

	// Profile list
	var profLines []string
	if len(v.profiles) == 0 {
		profLines = append(profLines, dimStyle.Render("  (no profiles)"))
	}
	for i, p := range v.profiles {
		prefix := "  "
		if i == v.profSelected && v.focus == focusProfileList {
			prefix = lipgloss.NewStyle().Foreground(purple).Bold(true).Render("▶ ")
		}
		row := fmt.Sprintf("%-14s %-8s %s", p.name, p.transport, p.device)
		if i == v.profSelected && v.focus == focusProfileList {
			row = lipgloss.NewStyle().Foreground(text).Render(row)
		} else {
			row = dimStyle.Render(row)
		}
		profLines = append(profLines, prefix+row)
	}
	profHint := dimStyle.Render("  n=add  d=delete  enter=edit")

	profBlock := border(v.focus == focusProfileList).
		Width(v.width - 4).
		Render(strings.Join(append(profLines, profHint), "\n"))

	// Save / Discard buttons
	saveStyle := lipgloss.NewStyle().Padding(0, 2).Border(lipgloss.RoundedBorder())
	if v.focus == focusSave {
		saveStyle = saveStyle.Background(purple).Foreground(text).BorderForeground(purple)
	} else {
		saveStyle = saveStyle.Foreground(text).BorderForeground(subtle)
	}
	discardStyle := lipgloss.NewStyle().Padding(0, 2).Border(lipgloss.RoundedBorder())
	if v.focus == focusDiscard {
		discardStyle = discardStyle.Background(subtle).Foreground(text).BorderForeground(subtle)
	} else {
		discardStyle = discardStyle.Foreground(subtle).BorderForeground(subtle)
	}
	buttons := lipgloss.JoinHorizontal(lipgloss.Left,
		saveStyle.Render("Save"),
		"  ",
		discardStyle.Render("Discard"),
	)

	// Status line
	var statusLine string
	if v.status != "" {
		st := lipgloss.NewStyle().Foreground(subtle)
		if strings.HasPrefix(v.status, "Save failed") {
			st = lipgloss.NewStyle().Foreground(red)
		}
		statusLine = st.Render(v.status)
	}

	// Assemble main view
	parts := []string{
		"",
		transportRow,
		deviceRow,
		filterRow,
		"",
		divider,
		profBlock,
		"",
		buttons,
	}
	if statusLine != "" {
		parts = append(parts, "", statusLine)
	}
	main := lipgloss.NewStyle().
		Background(bg).
		Width(v.width).
		Height(v.height).
		Padding(0, 2).
		Render(strings.Join(parts, "\n"))

	// Editing overlay
	if v.editing {
		nameB := inactiveBorder
		devB := inactiveBorder
		transB := inactiveBorder
		switch v.editFocus {
		case 0:
			nameB = activeBorder
		case 1:
			transB = activeBorder
		case 2:
			devB = activeBorder
		}
		formW := 46
		v.editName.Width = formW - 12
		v.editDevice.Width = formW - 12
		editTransVal := fmt.Sprintf(" %-8s ▼ ", v.editTransport)
		formLabel := lipgloss.NewStyle().Foreground(text).Width(10)
		formLines := []string{
			lipgloss.JoinHorizontal(lipgloss.Left, formLabel.Render("Name"), nameB.Render(v.editName.View())),
			lipgloss.JoinHorizontal(lipgloss.Left, formLabel.Render("Transport"), transB.Width(formW-12).Render(lipgloss.NewStyle().Foreground(purple).Render(editTransVal))),
			lipgloss.JoinHorizontal(lipgloss.Left, formLabel.Render("Device"), devB.Render(v.editDevice.View())),
			"",
			dimStyle.Render("  enter=save  esc=cancel"),
		}
		formContent := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(purple).
			Background(lipgloss.Color("#0F172A")).
			Padding(1, 2).
			Render("─ Edit profile ─\n" + strings.Join(formLines, "\n"))

		// Overlay: place form roughly centered
		formH := strings.Count(formContent, "\n") + 1
		formLineW := lipgloss.Width(formContent)
		topPad := (v.height - formH) / 3
		if topPad < 0 {
			topPad = 0
		}
		leftPad := (v.width - formLineW) / 2
		if leftPad < 0 {
			leftPad = 0
		}
		overlay := strings.Repeat("\n", topPad) +
			strings.Repeat(" ", leftPad) + strings.ReplaceAll(formContent, "\n", "\n"+strings.Repeat(" ", leftPad))

		_ = overlay
		// Render overlay on top of main by joining vertically with the form
		// re-rendered inline (BubbleTea has no z-index, so we composite manually)
		mainLines := strings.Split(main, "\n")
		overlayLines := strings.Split(overlay, "\n")
		for i, ol := range overlayLines {
			if i < len(mainLines) && strings.TrimSpace(ol) != "" {
				mainLines[i] = ol
			}
		}
		return strings.Join(mainLines, "\n")
	}

	return main
}

