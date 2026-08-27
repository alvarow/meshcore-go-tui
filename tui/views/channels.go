package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/meshcore-go/go-cli/storage"
	"github.com/meshcore-go/meshcore-go/companion"
	"github.com/meshcore-go/meshcore-go/companion/client"
)

type channelMessage struct {
	from      string
	text      string
	sent      bool
	timestamp time.Time
}

type channelItem struct {
	info     companion.ChannelInfoResponse
	messages []channelMessage
	lastRead time.Time
}

type ChannelView struct {
	client      *client.Client
	store       *storage.Store
	channels    []channelItem
	selected    int
	vp          viewport.Model
	vpReady     bool
	loadingMore bool
	input       textinput.Model
	width       int
	height      int
}

func NewChannelView(c *client.Client, store *storage.Store) *ChannelView {
	ti := textinput.New()
	ti.Placeholder = "Type a message..."
	ti.CharLimit = 200
	return &ChannelView{client: c, store: store, input: ti}
}

func (v *ChannelView) Title() string { return "Channels" }

func (v *ChannelView) Init() tea.Cmd { return nil }

func chanUnreadSeparator(count, width int) string {
	label := fmt.Sprintf(" %d unread ", count)
	padLen := (width - len(label)) / 2
	if padLen < 1 {
		padLen = 1
	}
	pad := strings.Repeat("─", padLen)
	line := pad + label + pad
	if len(line) < width {
		line += "─"
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Render(line)
}

func (v *ChannelView) buildLines() []string {
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#475569"))
	sentStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED"))
	recvStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#E2E8F0"))

	var lines []string
	if v.selected < len(v.channels) {
		msgs := v.channels[v.selected].messages
		lr := v.channels[v.selected].lastRead

		unreadFrom := -1
		if !lr.IsZero() {
			for i, m := range msgs {
				if m.timestamp.After(lr) {
					unreadFrom = i
					break
				}
			}
		}

		for i, m := range msgs {
			if i == unreadFrom {
				lines = append(lines, chanUnreadSeparator(len(msgs)-unreadFrom, v.vp.Width))
			}
			ts := dimStyle.Render(m.timestamp.Format("15:04"))
			var line string
			if m.sent {
				line = fmt.Sprintf("%s  %s", ts, sentStyle.Render("you: "+m.text))
			} else {
				line = fmt.Sprintf("%s  %s: %s", ts, m.from, recvStyle.Render(m.text))
			}
			lines = append(lines, line)
		}
	}
	return lines
}

func (v *ChannelView) rebuildViewport() {
	if !v.vpReady {
		return
	}
	v.vp.SetContent(strings.Join(v.buildLines(), "\n"))
	v.vp.GotoBottom()
}

func (v *ChannelView) rebuildViewportKeepOffset(prependedCount int) {
	if !v.vpReady {
		return
	}
	v.vp.SetContent(strings.Join(v.buildLines(), "\n"))
	v.vp.SetYOffset(prependedCount)
}

func (v *ChannelView) Update(msg tea.Msg) (View, tea.Cmd) {
	var cmd tea.Cmd

	switch m := msg.(type) {
	case SessionReadyMsg:
		v.channels = make([]channelItem, len(m.Channels))
		for i, ch := range m.Channels {
			v.channels[i] = channelItem{info: ch}
			if v.store != nil {
				if stored, err := v.store.LoadChannelMessages(int(ch.ChannelIdx), 100); err == nil {
					for _, sm := range stored {
						v.channels[i].messages = append(v.channels[i].messages, channelMessage{
							from:      sm.From,
							text:      sm.Text,
							sent:      sm.Direction == storage.Outbound,
							timestamp: sm.Timestamp,
						})
					}
				}
				if ts, err := v.store.GetChannelLastRead(int(ch.ChannelIdx)); err == nil {
					v.channels[i].lastRead = ts
				}
			}
		}
		v.rebuildViewport()
		return v, nil

	case InboundChannelMsg:
		if m.ChannelIdx >= 0 && m.ChannelIdx < len(v.channels) {
			v.channels[m.ChannelIdx].messages = append(v.channels[m.ChannelIdx].messages, channelMessage{
				from:      "peer",
				text:      m.Text,
				timestamp: m.Timestamp,
			})
			if v.store != nil {
				_ = v.store.SaveChannelMessage(m.ChannelIdx, storage.StoredMessage{
					Timestamp: m.Timestamp, From: "peer", Text: m.Text, Direction: storage.Inbound,
				})
			}
			if m.ChannelIdx == v.selected {
				if v.store != nil {
					_ = v.store.SetChannelLastRead(m.ChannelIdx, m.Timestamp)
					v.channels[m.ChannelIdx].lastRead = m.Timestamp
				}
				v.rebuildViewport()
			}
		}
		return v, nil

	case olderChannelMsgsMsg:
		v.loadingMore = false
		if len(m.messages) == 0 {
			return v, nil
		}
		if m.channelIdx >= 0 && m.channelIdx < len(v.channels) {
			converted := make([]channelMessage, 0, len(m.messages))
			for _, sm := range m.messages {
				converted = append(converted, channelMessage{
					from:      sm.From,
					text:      sm.Text,
					sent:      sm.Direction == storage.Outbound,
					timestamp: sm.Timestamp,
				})
			}
			v.channels[m.channelIdx].messages = append(converted, v.channels[m.channelIdx].messages...)
			if m.channelIdx == v.selected {
				v.rebuildViewportKeepOffset(len(converted))
			}
		}
		return v, nil

	case tea.KeyMsg:
		switch m.String() {
		case "enter":
			text := strings.TrimSpace(v.input.Value())
			if text == "" || v.client == nil || len(v.channels) == 0 {
				return v, nil
			}
			idx := v.channels[v.selected].info.ChannelIdx
			now := time.Now()
			v.channels[v.selected].messages = append(v.channels[v.selected].messages, channelMessage{
				from: "me", text: text, sent: true, timestamp: now,
			})
			if v.store != nil {
				_ = v.store.SaveChannelMessage(int(idx), storage.StoredMessage{
					Timestamp: now, From: "me", Text: text, Direction: storage.Outbound,
				})
			}
			v.input.Reset()
			v.rebuildViewport()
			return v, sendChannelMsg(v.client, idx, text)
		case "up":
			if v.selected > 0 {
				v.saveChannelLastRead(v.selected)
				v.selected--
				v.rebuildViewport()
			}
			return v, nil
		case "down":
			if v.selected < len(v.channels)-1 {
				v.saveChannelLastRead(v.selected)
				v.selected++
				v.rebuildViewport()
			}
			return v, nil
		case "pgup", "ctrl+u":
			v.vp, cmd = v.vp.Update(msg)
			if v.vp.AtTop() && !v.loadingMore && v.store != nil &&
				v.selected < len(v.channels) && len(v.channels[v.selected].messages) > 0 {
				v.loadingMore = true
				oldest := v.channels[v.selected].messages[0].timestamp
				idx := int(v.channels[v.selected].info.ChannelIdx)
				return v, loadOlderChannelMsgs(v.store, idx, oldest)
			}
			return v, cmd
		case "pgdn", "ctrl+d":
			v.vp, cmd = v.vp.Update(msg)
			return v, cmd
		}

	case tea.WindowSizeMsg:
		v.width = m.Width
		v.height = m.Height
		listWidth := 18
		msgWidth := v.width - listWidth - 3
		innerHeight := v.height - 6
		vpW := msgWidth - 2
		vpH := innerHeight - 2
		if vpW < 1 {
			vpW = 1
		}
		if vpH < 1 {
			vpH = 1
		}
		if !v.vpReady {
			v.vp = viewport.New(vpW, vpH)
			v.vpReady = true
		} else {
			v.vp.Width = vpW
			v.vp.Height = vpH
		}
		v.rebuildViewport()
	}

	v.input, cmd = v.input.Update(msg)
	return v, cmd
}

func (v *ChannelView) View() string {
	if v.width == 0 {
		return ""
	}

	listWidth := 18
	msgWidth := v.width - listWidth - 3
	innerHeight := v.height - 6

	var listLines []string
	if len(v.channels) == 0 {
		listLines = append(listLines, lipgloss.NewStyle().
			Foreground(lipgloss.Color("#475569")).Render(" (no channels)"))
	}
	for i, ch := range v.channels {
		name := ch.info.Name
		if name == "" {
			name = fmt.Sprintf("ch%d", ch.info.ChannelIdx)
		}
		if len(name) > listWidth-3 {
			name = name[:listWidth-3]
		}
		line := fmt.Sprintf(" %s", name)
		if i == v.selected {
			line = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7C3AED")).Render("> " + name)
		}
		listLines = append(listLines, line)
	}
	for len(listLines) < innerHeight {
		listLines = append(listLines, "")
	}
	chanList := lipgloss.NewStyle().
		Width(listWidth).Height(innerHeight).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#475569")).
		Render(strings.Join(listLines, "\n"))

	var vpContent string
	if v.vpReady {
		vpContent = v.vp.View()
	}
	msgThread := lipgloss.NewStyle().
		Width(msgWidth).Height(innerHeight).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#475569")).
		Render(vpContent)

	top := lipgloss.JoinHorizontal(lipgloss.Top, chanList, " ", msgThread)
	inputBox := lipgloss.NewStyle().
		Width(v.width - 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7C3AED")).
		Render(v.input.View())

	return lipgloss.JoinVertical(lipgloss.Left, top, inputBox)
}

type olderChannelMsgsMsg struct {
	channelIdx int
	messages   []storage.StoredMessage
}

func loadOlderChannelMsgs(s *storage.Store, idx int, before time.Time) tea.Cmd {
	return func() tea.Msg {
		msgs, _ := s.LoadChannelMessagesBefore(idx, before, 50)
		return olderChannelMsgsMsg{channelIdx: idx, messages: msgs}
	}
}

func (v *ChannelView) saveChannelLastRead(idx int) {
	if v.store == nil || idx >= len(v.channels) {
		return
	}
	msgs := v.channels[idx].messages
	if len(msgs) == 0 {
		return
	}
	latest := msgs[len(msgs)-1].timestamp
	chIdx := int(v.channels[idx].info.ChannelIdx)
	_ = v.store.SetChannelLastRead(chIdx, latest)
	v.channels[idx].lastRead = latest
}

func sendChannelMsg(c *client.Client, idx byte, text string) tea.Cmd {
	return func() tea.Msg {
		_, err := c.SendChannelTextMessage(context.Background(), idx, text, 0)
		if err != nil {
			return sendErrMsg{err: err}
		}
		return nil
	}
}
