package tui

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/alvarow/meshcore-go-tui/config"
	"github.com/alvarow/meshcore-go-tui/storage"
	"github.com/alvarow/meshcore-go-tui/tui/views"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/meshcore-go/meshcore-go/companion/client"
)

type ConnectedMsg struct{ DeviceName string }
type DisconnectedMsg struct{}
type ErrorMsg struct{ Err error }

// clearErrMsg is sent by the auto-dismiss tick after an error times out.
type clearErrMsg struct{}

type App struct {
	tabs         []views.View
	activeTab    int
	unread       [5]int
	connected    bool
	reconnecting bool
	deviceName   string
	width        int
	height       int
	client       *client.Client
	spinner      spinner.Model
	lastErr      string
	errExpiry    time.Time
	km           config.KeyMap
}

func New(deviceName string, c *client.Client, store *storage.Store, cfg *config.Config) *App {
	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = lipgloss.NewStyle().Foreground(colorPrimary)
	km := config.BuildKeyMap(cfg.Keys)
	return &App{
		deviceName: deviceName,
		client:     c,
		spinner:    s,
		km:         km,
		tabs: []views.View{
			views.NewChatView(c, store, km),
			views.NewChannelView(c, store, km),
			views.NewNodesView(),
			views.NewDeviceView(),
			views.NewSettingsView(cfg),
		},
	}
}

func (a *App) Init() tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range a.tabs {
		if cmd := t.Init(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

func isBroadcastMsg(msg tea.Msg) bool {
	switch msg.(type) {
	case views.SessionReadyMsg, views.InboundDirectMsg, views.OutboundAckMsg,
		views.NodeAdvertMsg, views.InboundChannelMsg, views.ContactDeletedMsg,
		views.PeerStatusMsg, views.PathDiscoveryMsg:
		return true
	}
	return false
}

func (a *App) switchTab(idx int) {
	a.activeTab = idx
	a.unread[idx] = 0
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = m.Width
		a.height = m.Height

	case tea.KeyMsg:
		inputBusy := a.tabs[a.activeTab].InputFocused()
		switch {
		case m.String() == "ctrl+c":
			return a, tea.Quit
		case key.Matches(m, a.km.Quit) && !inputBusy:
			return a, tea.Quit
		case key.Matches(m, a.km.NextTab):
			a.switchTab((a.activeTab + 1) % len(a.tabs))
			return a, nil
		case key.Matches(m, a.km.PrevTab):
			a.switchTab((a.activeTab - 1 + len(a.tabs)) % len(a.tabs))
			return a, nil
		case !inputBusy && len(m.String()) == 1 && m.String()[0] >= '1' && m.String()[0] <= '5':
			idx := int(m.String()[0] - '1')
			if idx < len(a.tabs) {
				a.switchTab(idx)
				return a, nil
			}
		}

	case ConnectedMsg:
		a.connected = true
		a.reconnecting = false
		a.deviceName = m.DeviceName
		return a, nil

	case DisconnectedMsg:
		a.connected = false
		a.reconnecting = false
		return a, nil

	case views.ReconnectingMsg:
		a.connected = false
		a.reconnecting = true
		return a, a.spinner.Tick

	case views.ReconnectedMsg:
		a.connected = true
		a.reconnecting = false
		a.deviceName = m.DeviceName
		return a, nil

	case spinner.TickMsg:
		if a.reconnecting {
			var cmd tea.Cmd
			a.spinner, cmd = a.spinner.Update(msg)
			return a, cmd
		}
		return a, nil

	case ErrorMsg:
		a.lastErr = m.Err.Error()
		a.errExpiry = time.Now().Add(5 * time.Second)
		return a, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return clearErrMsg{} })

	case clearErrMsg:
		a.lastErr = ""
		return a, nil

	case views.InboundDirectMsg:
		if a.activeTab != 0 {
			a.unread[0]++
			go desktopNotify("Direct message", m.Text)
		}

	case views.InboundChannelMsg:
		if a.activeTab != 1 {
			a.unread[1]++
			go desktopNotify("Channel message", m.Text)
		}
	}

	if isBroadcastMsg(msg) {
		var cmds []tea.Cmd
		for i, t := range a.tabs {
			updated, cmd := t.Update(msg)
			a.tabs[i] = updated
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return a, tea.Batch(cmds...)
	}

	var cmd tea.Cmd
	a.tabs[a.activeTab], cmd = a.tabs[a.activeTab].Update(msg)
	return a, cmd
}

func (a *App) View() string {
	if a.width == 0 {
		return "Loading..."
	}

	// Tab bar with unread badges
	var tabParts []string
	for i, t := range a.tabs {
		label := fmt.Sprintf(" %d:%s", i+1, t.Title())
		if a.unread[i] > 0 {
			badge := lipgloss.NewStyle().
				Bold(true).
				Foreground(colorPrimary).
				Render(fmt.Sprintf("(%d)", a.unread[i]))
			label = label + " " + badge
		}
		label += " "
		if i == a.activeTab {
			tabParts = append(tabParts, ActiveTabStyle.Render(label))
		} else {
			tabParts = append(tabParts, InactiveTabStyle.Render(label))
		}
	}
	tabBar := lipgloss.NewStyle().
		Background(colorBg).
		Width(a.width).
		Render(strings.Join(tabParts, ""))

	// Height budget: tab bar (1) + status bar (1) + optional error bar (1)
	contentHeight := a.height - 2

	// Error bar
	var errorBar string
	if a.lastErr != "" {
		errorBar = lipgloss.NewStyle().
			Background(lipgloss.Color("#7F1D1D")).
			Foreground(lipgloss.Color("#FCA5A5")).
			Width(a.width).
			Render("⚠ " + a.lastErr)
		contentHeight--
	}

	contentMsg := tea.WindowSizeMsg{Width: a.width, Height: contentHeight}
	a.tabs[a.activeTab].Update(contentMsg) //nolint — size-only update, no cmd needed
	content := a.tabs[a.activeTab].View()

	// Status bar
	var connStatus string
	switch {
	case a.reconnecting:
		connStatus = lipgloss.NewStyle().Foreground(colorPrimary).
			Render(a.spinner.View() + " reconnecting...")
	case a.connected:
		connStatus = ConnectedStyle.Render("● " + a.deviceName)
	default:
		connStatus = DisconnectedStyle.Render("○ disconnected")
	}
	help := StatusBarStyle.Render("tab/1-5:switch  q:quit")
	gap := strings.Repeat(" ", max(0, a.width-lipgloss.Width(connStatus)-lipgloss.Width(help)-2))
	statusBar := StatusBarStyle.Width(a.width).Render(connStatus + gap + help)

	parts := []string{tabBar}
	if errorBar != "" {
		parts = append(parts, errorBar)
	}
	parts = append(parts, content, statusBar)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// desktopNotify fires a desktop notification via notify-send if available.
// Errors and absence of notify-send are silently ignored.
func desktopNotify(title, body string) {
	_ = exec.Command("notify-send", "-i", "network-wireless", title, body).Run()
}
