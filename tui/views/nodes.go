package views

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type nodeEntry struct {
	name       string
	nodeType   byte
	snr        float32
	rssi       int8
	pubKey     [32]byte
	outHops    byte      // hops to reach this node
	lat        int32     // × 1e7; 0 = not set
	lon        int32     // × 1e7; 0 = not set
	lastAdvert time.Time // node's own last advert time (from firmware)
	lastSeen   time.Time // when we received the advert
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

func (v *NodesView) Title() string      { return "Nodes" }
func (v *NodesView) InputFocused() bool { return false }

func (v *NodesView) Init() tea.Cmd { return nil }

func (v *NodesView) Update(msg tea.Msg) (View, tea.Cmd) {
	switch m := msg.(type) {
	case NodeAdvertMsg:
		now := time.Now()
		var lastAdvert time.Time
		if m.LastAdvert > 0 {
			lastAdvert = time.Unix(int64(m.LastAdvert), 0)
		}
		for i := range v.nodes {
			if v.nodes[i].pubKey == m.PubKey {
				v.nodes[i].name = m.Name
				v.nodes[i].nodeType = m.NodeType
				v.nodes[i].snr = m.SNR
				v.nodes[i].rssi = m.RSSI
				v.nodes[i].outHops = m.OutHops
				v.nodes[i].lat = m.Lat
				v.nodes[i].lon = m.Lon
				v.nodes[i].lastAdvert = lastAdvert
				v.nodes[i].lastSeen = now
				return v, nil
			}
		}
		v.nodes = append(v.nodes, nodeEntry{
			name:       m.Name,
			nodeType:   m.NodeType,
			snr:        m.SNR,
			rssi:       m.RSSI,
			pubKey:     m.PubKey,
			outHops:    m.OutHops,
			lat:        m.Lat,
			lon:        m.Lon,
			lastAdvert: lastAdvert,
			lastSeen:   now,
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
		row := fmt.Sprintf("  %-20s %-6s %5.1fdB %5ddBm  %s",
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

	// Detail bar for selected node.
	if v.selected < len(v.nodes) {
		n := v.nodes[v.selected]
		var details []string
		details = append(details, fmt.Sprintf("hops:%d", n.outHops))
		if n.lat != 0 || n.lon != 0 {
			details = append(details, fmt.Sprintf("loc:%.6f,%.6f",
				float64(n.lat)/1e7, float64(n.lon)/1e7))
		}
		if !n.lastAdvert.IsZero() {
			details = append(details, "last_advert:"+n.lastAdvert.Format("2006-01-02 15:04:05"))
		}
		detail := lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA")).
			Render("  " + strings.Join(details, "  "))
		rows = append(rows, divider, detail)
	}

	hint := lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).
		Render("  ↑↓/jk=navigate")
	rows = append(rows, hint)

	return lipgloss.NewStyle().
		Width(v.width - 2).Height(v.height - 4).
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
