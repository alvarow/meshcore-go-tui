package views

import (
	"encoding/hex"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/meshcore-go/meshcore-go/companion"
)

type DeviceView struct {
	self  *companion.SelfInfoResponse
	width int
	height int
}

func NewDeviceView() *DeviceView {
	return &DeviceView{}
}

func (v *DeviceView) Title() string     { return "Device" }
func (v *DeviceView) InputFocused() bool { return false }

func (v *DeviceView) Init() tea.Cmd { return nil }

func (v *DeviceView) Update(msg tea.Msg) (View, tea.Cmd) {
	switch m := msg.(type) {
	case SessionReadyMsg:
		self := m.Self
		v.self = &self
	case tea.WindowSizeMsg:
		v.width = m.Width
		v.height = m.Height
	}
	return v, nil
}

func (v *DeviceView) View() string {
	if v.width == 0 {
		return ""
	}

	style := lipgloss.NewStyle().
		Width(v.width-2).Height(v.height-4).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#475569"))

	if v.self == nil {
		return style.Render(lipgloss.NewStyle().
			Foreground(lipgloss.Color("#475569")).
			Render("  connecting..."))
	}

	label := lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED")).Bold(true)
	value := lipgloss.NewStyle().Foreground(lipgloss.Color("#E2E8F0"))

	pubkeyHex := hex.EncodeToString(v.self.PublicKey[:6]) + "..."
	freqMHz := float64(v.self.RadioFrequency) / 1_000.0  // field is in kHz
	bwKHz := float64(v.self.RadioBandwidth) / 1_000.0

	var lines []string
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  %s  %s", label.Render("Name:     "), value.Render(v.self.Name)))
	lines = append(lines, fmt.Sprintf("  %s  %s", label.Render("PubKey:   "), value.Render(pubkeyHex)))
	lines = append(lines, fmt.Sprintf("  %s  %s", label.Render("TX Power: "), value.Render(fmt.Sprintf("%d dBm (max %d)", v.self.TxPower, v.self.MaxTxPower))))
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  %s", label.Render("Radio:")))
	lines = append(lines, fmt.Sprintf("    %s  %s", label.Render("Frequency:"), value.Render(fmt.Sprintf("%.3f MHz", freqMHz))))
	lines = append(lines, fmt.Sprintf("    %s  %s", label.Render("Bandwidth:"), value.Render(fmt.Sprintf("%.1f kHz", bwKHz))))
	lines = append(lines, fmt.Sprintf("    %s  %s", label.Render("SF:       "), value.Render(fmt.Sprintf("%d", v.self.RadioSpreadFactor))))
	lines = append(lines, fmt.Sprintf("    %s  %s", label.Render("CR:       "), value.Render(fmt.Sprintf("%d", v.self.RadioCodingRate))))

	if v.self.AdvertLatitude != 0 || v.self.AdvertLongitude != 0 {
		lat := float64(v.self.AdvertLatitude) / 1e7
		lon := float64(v.self.AdvertLongitude) / 1e7
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("  %s  %s", label.Render("Location: "), value.Render(fmt.Sprintf("%.6f, %.6f", lat, lon))))
	}

	return style.Render(strings.Join(lines, "\n"))
}
