package scanner

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"tinygo.org/x/bluetooth"
)

var nusSvcUUID = bluetooth.NewUUID([16]byte{
	0x6E, 0x40, 0x00, 0x01, 0xB5, 0xA3, 0xF3, 0x93,
	0xE0, 0xA9, 0xE5, 0x0E, 0x24, 0xDC, 0xCA, 0x9E,
})

// Result is returned by Run() when the user selects a device or cancels.
type Result struct {
	Address  string
	Name     string
	Canceled bool
}

// device is a discovered BLE device (implements list.Item).
type device struct {
	address string
	name    string
	rssi    int16
}

func (d device) Title() string {
	if d.name != "" {
		return d.name
	}
	return d.address
}

func (d device) Description() string {
	if d.name != "" {
		return fmt.Sprintf("%s  RSSI %d dBm", d.address, d.rssi)
	}
	return fmt.Sprintf("RSSI %d dBm", d.rssi)
}

func (d device) FilterValue() string { return d.Title() }

type foundMsg device
type scanErrMsg struct{ err error }

// Model is a BubbleTea model for BLE device scanning and selection.
type Model struct {
	list           list.Model
	seen           map[string]int // address → list index
	nameFilter     string         // case-insensitive substring; replaces "mesh" default when set
	result         Result
	err            error
	bleUnavailable bool
	width          int
	height         int
	done           chan struct{}
}

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#E2E8F0")).
			Background(lipgloss.Color("#7C3AED")).
			Padding(0, 2)

	itemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E2E8F0")).
			PaddingLeft(2)

	selectedItemStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#7C3AED")).
				Bold(true).
				PaddingLeft(1)

	paginationStyle = list.DefaultStyles().PaginationStyle.
			Foreground(lipgloss.Color("#475569"))

	helpStyle = list.DefaultStyles().HelpStyle.
			Foreground(lipgloss.Color("#475569"))

	outerStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#0F172A")).
			Foreground(lipgloss.Color("#E2E8F0"))
)

func newDelegate() list.DefaultDelegate {
	d := list.NewDefaultDelegate()
	d.Styles.NormalTitle = itemStyle
	d.Styles.NormalDesc = itemStyle.Foreground(lipgloss.Color("#475569"))
	d.Styles.SelectedTitle = selectedItemStyle
	d.Styles.SelectedDesc = selectedItemStyle.Foreground(lipgloss.Color("#A78BFA"))
	return d
}

func New() *Model { return NewWithFilter("") }

func NewWithFilter(nameFilter string) *Model {
	l := list.New(nil, newDelegate(), 0, 0)
	l.Title = "Select MeshCore device"
	l.Styles.Title = titleStyle
	l.Styles.PaginationStyle = paginationStyle
	l.Styles.HelpStyle = helpStyle
	l.SetShowStatusBar(true)
	l.SetFilteringEnabled(false)

	return &Model{
		list:       l,
		seen:       make(map[string]int),
		nameFilter: strings.ToLower(nameFilter),
		done:       make(chan struct{}),
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.list.StartSpinner(),
		startScan(m.done, m.nameFilter),
	)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.list.SetSize(msg.Width, msg.Height-4)
		return m, nil

	case foundMsg:
		updated, insertCmd := m.handleFound(device(msg))
		return updated, tea.Batch(insertCmd, startScan(m.done, m.nameFilter))

	case scanErrMsg:
		errStr := msg.err.Error()
		if strings.Contains(errStr, "org.bluez") || strings.Contains(errStr, "enable adapter") || strings.Contains(errStr, "no adapter") {
			m.bleUnavailable = true
		}
		m.err = msg.err
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if i, ok := m.list.SelectedItem().(device); ok {
				close(m.done)
				m.done = make(chan struct{})
				m.result = Result{Address: i.address, Name: i.name}
				return m, tea.Quit
			}
		case "r":
			// restart scan
			close(m.done)
			m.done = make(chan struct{})
			m.seen = make(map[string]int)
			m.list.SetItems(nil)
			m.err = nil
			return m, tea.Batch(m.list.StartSpinner(), startScan(m.done, m.nameFilter))
		case "q", "ctrl+c":
			close(m.done)
			m.result = Result{Canceled: true}
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m *Model) handleFound(d device) (*Model, tea.Cmd) {
	if idx, exists := m.seen[d.address]; exists {
		// Update RSSI on existing entry.
		items := m.list.Items()
		if idx < len(items) {
			existing := items[idx].(device)
			existing.rssi = d.rssi
			_ = m.list.SetItem(idx, existing)
		}
		return m, nil
	}
	m.seen[d.address] = len(m.list.Items())
	cmd := m.list.InsertItem(len(m.list.Items()), d)
	if len(m.list.Items()) == 1 {
		m.list.StopSpinner()
	}
	return m, cmd
}

func (m *Model) View() string {
	if m.width == 0 {
		return "Scanning for MeshCore devices…"
	}

	if m.bleUnavailable {
		msg := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E2E8F0")).
			Padding(2, 4).
			Render(`No Bluetooth adapter found.

This machine has no Bluetooth hardware
or BlueZ is not running.

Use --transport serial or --transport tcp
to connect without Bluetooth.

Press q to quit.`)
		return outerStyle.Width(m.width).Height(m.height).Render(msg)
	}

	var status string
	if m.err != nil {
		status = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444")).
			Render(fmt.Sprintf("  scan error: %v", m.err))
	} else if len(m.list.Items()) == 0 {
		status = lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).
			Render("  Scanning… (r=rescan, q=quit)")
	} else {
		status = lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).
			Render("  ↑/↓ select  enter=connect  r=rescan  q=quit")
	}

	return outerStyle.
		Width(m.width).
		Height(m.height).
		Render(lipgloss.JoinVertical(lipgloss.Left,
			m.list.View(),
			status,
		))
}

// startScan returns a Cmd that runs one scan callback iteration.
// It sends a foundMsg for any qualifying device and then returns,
// allowing the BubbleTea loop to call it again via the returned Cmd chain.
func startScan(done chan struct{}, nameFilter string) tea.Cmd {
	return func() tea.Msg {
		adapter := bluetooth.DefaultAdapter
		if err := adapter.Enable(); err != nil {
			return scanErrMsg{err: fmt.Errorf("enable adapter: %w", err)}
		}

		result := make(chan tea.Msg, 1)

		go func() {
			_ = adapter.Scan(func(a *bluetooth.Adapter, r bluetooth.ScanResult) {
				select {
				case <-done:
					_ = a.StopScan()
					return
				default:
				}

				name := r.LocalName()
				hasNUS := r.HasServiceUUID(nusSvcUUID)

				// Name filter: use configured filter if set, fall back to "mesh".
				check := nameFilter
				if check == "" {
					check = "mesh"
				}
				hasName := strings.Contains(strings.ToLower(name), check)

				if !hasNUS && !hasName {
					return
				}

				select {
				case result <- foundMsg{
					address: r.Address.String(),
					name:    name,
					rssi:    int16(r.RSSI),
				}:
					_ = a.StopScan()
				default:
				}
			})
		}()

		select {
		case msg := <-result:
			return msg
		case <-done:
			_ = adapter.StopScan()
			return nil
		}
	}
}
