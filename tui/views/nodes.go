package views

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type nodeEntry struct {
	name     string
	nodeType byte
	snr      float32
	rssi     int8
	pubKey   [32]byte
	lastSeen time.Time
}

type NodesView struct {
	nodes    []nodeEntry
	selected int
	width    int
	height   int
}

func NewNodesView() *NodesView {
	return &NodesView{}
}

func (v *NodesView) Title() string   { return "Nodes" }
func (v *NodesView) InputFocused() bool { return false }

func (v *NodesView) Init() tea.Cmd { return nil }

func (v *NodesView) Update(msg tea.Msg) (View, tea.Cmd) {
	switch m := msg.(type) {
	case NodeAdvertMsg:
		for i := range v.nodes {
			if v.nodes[i].pubKey == m.PubKey {
				v.nodes[i].name = m.Name
				v.nodes[i].nodeType = m.NodeType
				v.nodes[i].snr = m.SNR
				v.nodes[i].rssi = m.RSSI
				v.nodes[i].lastSeen = time.Now()
				return v, nil
			}
		}
		v.nodes = append(v.nodes, nodeEntry{
			name:     m.Name,
			nodeType: m.NodeType,
			snr:      m.SNR,
			rssi:     m.RSSI,
			pubKey:   m.PubKey,
			lastSeen: time.Now(),
		})
		return v, nil

	case tea.KeyMsg:
		switch m.String() {
		case "up", "k":
			if v.selected > 0 {
				v.selected--
			}
		case "down", "j":
			if v.selected < len(v.nodes)-1 {
				v.selected++
			}
		}

	case tea.WindowSizeMsg:
		v.width = m.Width
		v.height = m.Height
	}
	return v, nil
}

func (v *NodesView) View() string {
	if v.width == 0 {
		return ""
	}

	header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7C3AED")).
		Render(fmt.Sprintf("  %-20s %-6s %6s %6s  %-15s", "Name", "Type", "SNR", "RSSI", "Last Seen"))
	divider := strings.Repeat("─", v.width-2)

	var rows []string
	rows = append(rows, header, divider)

	if len(v.nodes) == 0 {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).
			Render("  (no nodes discovered yet)"))
	}

	for i, n := range v.nodes {
		lastSeen := "never"
		if !n.lastSeen.IsZero() {
			ago := time.Since(n.lastSeen).Truncate(time.Second)
			lastSeen = ago.String() + " ago"
		}
		typeName := nodeTypeName(n.nodeType)
		row := fmt.Sprintf("  %-20s %-6s %5.1fdB %5ddBm  %-15s",
			truncate(n.name, 20), typeName, n.snr, n.rssi, lastSeen)
		if i == v.selected {
			row = lipgloss.NewStyle().
				Background(lipgloss.Color("#1E293B")).
				Foreground(lipgloss.Color("#E2E8F0")).
				Render(row)
		} else {
			row = lipgloss.NewStyle().Foreground(lipgloss.Color("#CBD5E1")).Render(row)
		}
		rows = append(rows, row)
	}

	return lipgloss.NewStyle().
		Width(v.width-2).Height(v.height-4).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#475569")).
		Render(strings.Join(rows, "\n"))
}

func nodeTypeName(t byte) string {
	switch t {
	case 0:
		return "chat"
	case 1:
		return "repeat"
	case 2:
		return "room"
	case 3:
		return "sensor"
	default:
		return fmt.Sprintf("%d", t)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
