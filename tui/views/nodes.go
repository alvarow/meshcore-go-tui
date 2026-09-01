package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/companion/client"
)

type nodeEntry struct {
	name      string
	nodeType  byte
	snr       float32
	rssi      int8
	pubKey    [32]byte
	lastSeen  time.Time
	pingRTT   string // last ping RTT, empty if never pinged
	traceHops string // last trace result, empty if never traced
}

type NodesView struct {
	client    *client.Client
	nodes     []nodeEntry
	selected  int
	pingStart map[[6]byte]time.Time
	width     int
	height    int
}

func NewNodesView() *NodesView {
	return &NodesView{pingStart: make(map[[6]byte]time.Time)}
}

func (v *NodesView) Title() string      { return "Nodes" }
func (v *NodesView) InputFocused() bool { return false }

func (v *NodesView) Init() tea.Cmd { return nil }

func (v *NodesView) Update(msg tea.Msg) (View, tea.Cmd) {
	switch m := msg.(type) {
	case SessionReadyMsg:
		if m.Client != nil {
			v.client = m.Client
		}
		return v, nil

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

	case PeerStatusMsg:
		for i := range v.nodes {
			var p6 [6]byte
			copy(p6[:], v.nodes[i].pubKey[:6])
			if p6 == m.PubKeyPrefix {
				if start, ok := v.pingStart[p6]; ok {
					rtt := time.Since(start).Round(time.Millisecond)
					delete(v.pingStart, p6)
					v.nodes[i].pingRTT = rtt.String()
				}
				break
			}
		}
		return v, nil

	case PathDiscoveryMsg:
		for i := range v.nodes {
			var p6 [6]byte
			copy(p6[:], v.nodes[i].pubKey[:6])
			if p6 == m.PubKeyPrefix {
				v.nodes[i].traceHops = fmt.Sprintf("%d↑ %d↓", m.OutHops, m.InHops)
				break
			}
		}
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
		case "p":
			if v.client != nil && v.selected < len(v.nodes) {
				node := v.nodes[v.selected]
				var p6 [6]byte
				copy(p6[:], node.pubKey[:6])
				v.pingStart[p6] = time.Now()
				return v, pingNode(v.client, node)
			}
		case "t":
			if v.client != nil && v.selected < len(v.nodes) {
				return v, traceNode(v.client, v.nodes[v.selected])
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
		extra := ""
		if n.pingRTT != "" {
			extra += "  ping:" + n.pingRTT
		}
		if n.traceHops != "" {
			extra += "  hops:" + n.traceHops
		}
		row := fmt.Sprintf("  %-20s %-6s %5.1fdB %5ddBm  %-15s%s",
			truncate(n.name, 20), typeName, n.snr, n.rssi, lastSeen, extra)
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

	hint := lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).
		Render("  p=ping  t=trace  ↑↓/jk=navigate")
	rows = append(rows, "", hint)

	return lipgloss.NewStyle().
		Width(v.width - 2).Height(v.height - 4).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#475569")).
		Render(strings.Join(rows, "\n"))
}

func pingNode(c *client.Client, n nodeEntry) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		identity := meshcore.NewIdentity(n.pubKey)
		_ = c.SendStatusReq(ctx, identity)
		return nil
	}
}

func traceNode(c *client.Client, n nodeEntry) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		identity := meshcore.NewIdentity(n.pubKey)
		_, _ = c.SendPathDiscoveryReq(ctx, identity)
		return nil
	}
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
