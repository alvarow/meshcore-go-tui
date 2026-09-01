package views

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/alvarow/meshcore-go-tui/storage"
	"github.com/meshcore-go/meshcore-go/companion"
	"github.com/meshcore-go/meshcore-go/companion/client"
)

type chanMode int

const (
	modeChanChat chanMode = iota
	modeChanJoinName
	modeChanJoinPSK
)

type channelMessage struct {
	from      string
	text      string
	sent      bool
	timestamp time.Time
	isSystem  bool
}

type channelItem struct {
	info     companion.ChannelInfoResponse
	messages []channelMessage
	lastRead time.Time
}

type ChannelView struct {
	client              *client.Client
	store               *storage.Store
	channels            []channelItem
	selected            int
	vp                  viewport.Model
	vpReady             bool
	loadingMore         bool
	input               textinput.Model
	mode                chanMode
	joinName            string
	leaveConfirmPending bool
	selectMode          bool
	selectedMsg         int
	offRecord           bool
	clearPending        bool
	width               int
	height              int
}

func NewChannelView(c *client.Client, store *storage.Store) *ChannelView {
	ti := textinput.New()
	ti.Placeholder = "Type a message..."
	ti.CharLimit = 200
	ti.Focus()
	return &ChannelView{client: c, store: store, input: ti}
}

func (v *ChannelView) Title() string { return "Channels" }
func (v *ChannelView) InputFocused() bool { return v.input.Focused() }

func (v *ChannelView) Init() tea.Cmd { return textinput.Blink }

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
	selectHL := lipgloss.NewStyle().Background(lipgloss.Color("#312E81")).Foreground(lipgloss.Color("#E2E8F0"))

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
			if m.isSystem {
				lines = append(lines, dimStyle.Render("  — "+m.text+" —"))
				continue
			}
			ts := dimStyle.Render(m.timestamp.Format("15:04"))
			var line string
			if m.sent {
				line = fmt.Sprintf("%s  %s", ts, sentStyle.Render("you: "+m.text))
			} else {
				line = fmt.Sprintf("%s  %s: %s", ts, m.from, recvStyle.Render(m.text))
			}
			if v.selectMode && i == v.selectedMsg {
				line = selectHL.Width(v.vp.Width).Render("› " + strings.TrimLeft(line, " "))
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
		if m.Client != nil {
			v.client = m.Client
		}
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
			if v.store != nil && !v.offRecord {
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

	case AdvertResultMsg:
		sys := "advert sent"
		if m.Err != nil {
			sys = "advert failed: " + m.Err.Error()
		}
		v.appendSystem(sys)
		return v, nil

	case channelJoinedMsg:
		v.channels = append(v.channels, channelItem{info: companion.ChannelInfoResponse{
			ChannelIdx: m.idx, Name: m.name,
		}})
		v.selected = len(v.channels) - 1
		v.appendSystem("joined #" + m.name)
		v.rebuildViewport()
		return v, nil

	case channelLeftMsg:
		if m.err != nil {
			v.appendSystem("leave failed: " + m.err.Error())
		} else {
			name := m.name
			for i, ch := range v.channels {
				if ch.info.ChannelIdx == m.idx {
					v.channels = append(v.channels[:i], v.channels[i+1:]...)
					if v.selected >= len(v.channels) && v.selected > 0 {
						v.selected--
					}
					break
				}
			}
			v.appendSystem("left #" + name)
		}
		v.rebuildViewport()
		return v, nil

	case tea.KeyMsg:
		switch m.String() {
		case "esc":
			if v.selectMode {
				v.selectMode = false
				v.clearPending = false
				v.rebuildViewport()
				return v, nil
			}
			if v.mode != modeChanChat {
				v.mode = modeChanChat
				v.joinName = ""
				v.input.SetValue("")
				v.input.Placeholder = "Type a message..."
			}
			v.leaveConfirmPending = false
			return v, nil

		case "ctrl+o":
			v.offRecord = !v.offRecord
			if v.offRecord {
				v.input.Placeholder = "⊘ off the record"
			} else {
				v.input.Placeholder = "Type a message..."
			}
			return v, nil

		case "s":
			if v.selectMode {
				v.selectMode = false
			} else if v.selected < len(v.channels) && len(v.channels[v.selected].messages) > 0 {
				v.selectMode = true
				v.selectedMsg = len(v.channels[v.selected].messages) - 1
			}
			v.clearPending = false
			v.rebuildViewport()
			return v, nil

		case "X":
			if v.selected < len(v.channels) {
				if v.clearPending {
					v.clearPending = false
					idx := int(v.channels[v.selected].info.ChannelIdx)
					v.channels[v.selected].messages = nil
					if v.store != nil {
						_ = v.store.ClearChannelMessages(idx)
					}
					v.rebuildViewport()
				} else {
					v.clearPending = true
					v.appendSystem("press X again to clear all messages")
					v.rebuildViewport()
				}
				return v, nil
			}

		case "ctrl+a":
			if v.client != nil {
				return v, sendAdvert(v.client)
			}
			return v, nil

		case "n":
			if v.mode == modeChanChat && !v.input.Focused() {
				v.mode = modeChanJoinName
				v.input.SetValue("")
				v.input.Placeholder = "Channel name (Enter to continue):"
				v.input.Focus()
				return v, nil
			}

		case "d":
			if v.selectMode && v.selected < len(v.channels) {
				msgs := v.channels[v.selected].messages
				if v.selectedMsg < len(msgs) {
					ts := msgs[v.selectedMsg].timestamp
					v.channels[v.selected].messages = append(msgs[:v.selectedMsg], msgs[v.selectedMsg+1:]...)
					if v.selectedMsg >= len(v.channels[v.selected].messages) && v.selectedMsg > 0 {
						v.selectedMsg--
					}
					if v.store != nil {
						idx := int(v.channels[v.selected].info.ChannelIdx)
						_ = v.store.DeleteChannelMessage(idx, ts)
					}
					v.rebuildViewport()
				}
				return v, nil
			}
			if v.mode == modeChanChat && v.client != nil && len(v.channels) > 0 {
				if v.leaveConfirmPending {
					v.leaveConfirmPending = false
					ch := v.channels[v.selected]
					return v, leaveChannel(v.client, ch.info.ChannelIdx, ch.info.Name)
				}
				v.leaveConfirmPending = true
				v.appendSystem("press d again to leave #" + v.channels[v.selected].info.Name)
				v.rebuildViewport()
				return v, nil
			}
			v.leaveConfirmPending = false

		case "enter":
			switch v.mode {
			case modeChanJoinName:
				name := strings.TrimSpace(v.input.Value())
				if name == "" {
					v.mode = modeChanChat
					v.input.Placeholder = "Type a message..."
					return v, nil
				}
				v.joinName = name
				v.mode = modeChanJoinPSK
				v.input.SetValue("")
				v.input.Placeholder = "PSK (optional — blank = auto-derive):"
				return v, nil

			case modeChanJoinPSK:
				psk := strings.TrimSpace(v.input.Value())
				v.mode = modeChanChat
				v.input.SetValue("")
				v.input.Placeholder = "Type a message..."
				if v.client != nil {
					return v, joinChannel(v.client, v.channels, v.joinName, psk)
				}
				return v, nil

			default:
				text := strings.TrimSpace(v.input.Value())
				v.leaveConfirmPending = false
				if text == "" || v.client == nil || len(v.channels) == 0 {
					return v, nil
				}
				idx := v.channels[v.selected].info.ChannelIdx
				now := time.Now()
				v.channels[v.selected].messages = append(v.channels[v.selected].messages, channelMessage{
					from: "me", text: text, sent: true, timestamp: now,
				})
				if v.store != nil && !v.offRecord {
					_ = v.store.SaveChannelMessage(int(idx), storage.StoredMessage{
						Timestamp: now, From: "me", Text: text, Direction: storage.Outbound,
					})
				}
				v.input.Reset()
				v.rebuildViewport()
				return v, sendChannelMsg(v.client, idx, text)
			}

		case "up":
			if v.selectMode {
				if v.selectedMsg > 0 {
					v.selectedMsg--
					v.rebuildViewport()
				}
				return v, nil
			}
			if v.mode == modeChanChat {
				if v.selected > 0 {
					v.saveChannelLastRead(v.selected)
					v.selected--
					v.rebuildViewport()
				}
				return v, nil
			}
		case "down":
			if v.selectMode {
				if v.selected < len(v.channels) && v.selectedMsg < len(v.channels[v.selected].messages)-1 {
					v.selectedMsg++
					v.rebuildViewport()
				}
				return v, nil
			}
			if v.mode == modeChanChat {
				if v.selected < len(v.channels)-1 {
					v.saveChannelLastRead(v.selected)
					v.selected++
					v.rebuildViewport()
				}
				return v, nil
			}
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

	borderColor := lipgloss.Color("#7C3AED")
	switch {
	case v.offRecord:
		borderColor = lipgloss.Color("#DC2626")
	case v.selectMode:
		borderColor = lipgloss.Color("#0EA5E9")
	case v.mode != modeChanChat:
		borderColor = lipgloss.Color("#EAB308")
	}
	inputContent := v.input.View()
	if v.selectMode {
		inputContent = lipgloss.NewStyle().Foreground(lipgloss.Color("#0EA5E9")).
			Render("select mode  ↑↓ navigate  d delete  s exit")
	}
	inputBox := lipgloss.NewStyle().
		Width(v.width - 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Render(inputContent)

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

// appendSystem appends a system message to the currently selected channel.
func (v *ChannelView) appendSystem(text string) {
	if v.selected < len(v.channels) {
		v.channels[v.selected].messages = append(v.channels[v.selected].messages,
			channelMessage{text: text, isSystem: true, timestamp: time.Now()})
	}
}

// publicChannelSecret is the well-known shared secret used by all MeshCore
// clients for the "Public" channel. Must match tui-meshcore / meshtui.
var publicChannelSecret = [16]byte{
	0x8b, 0x33, 0x87, 0xe9, 0xc5, 0xcd, 0xea, 0x6a,
	0xc9, 0xe5, 0xed, 0xba, 0xa1, 0x15, 0xcd, 0x72,
}

type channelJoinedMsg struct {
	idx  byte
	name string
}

type channelLeftMsg struct {
	idx  byte
	name string
	err  error
}

func joinChannel(c *client.Client, existing []channelItem, name, psk string) tea.Cmd {
	return func() tea.Msg {
		// Find first free slot (0-7).
		used := make(map[byte]bool)
		for _, ch := range existing {
			used[ch.info.ChannelIdx] = true
		}
		var idx byte = 255
		for i := byte(0); i < 8; i++ {
			if !used[i] {
				idx = i
				break
			}
		}
		if idx == 255 {
			return channelJoinedMsg{name: name} // all slots full — caller won't find it, but won't crash
		}

		var secret [16]byte
		switch {
		case strings.EqualFold(name, "Public"):
			secret = publicChannelSecret
		case psk != "":
			b, err := hex.DecodeString(psk)
			if err == nil && len(b) == 16 {
				copy(secret[:], b)
			} else {
				// treat as raw passphrase: SHA-256 truncated to 16 bytes
				h := sha256.Sum256([]byte(psk))
				copy(secret[:], h[:16])
			}
		default:
			h := sha256.Sum256([]byte(name))
			copy(secret[:], h[:16])
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := c.SetChannel(ctx, idx, name, secret); err != nil {
			return channelLeftMsg{err: err} // reuse left msg for error reporting
		}
		return channelJoinedMsg{idx: idx, name: name}
	}
}

func leaveChannel(c *client.Client, idx byte, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := c.SetChannel(ctx, idx, "", [16]byte{})
		return channelLeftMsg{idx: idx, name: name, err: err}
	}
}

func sendChannelMsg(c *client.Client, idx byte, text string) tea.Cmd {
	return func() tea.Msg {
		// The device acknowledges with RespOk (not RespSent), so the SDK call
		// blocks indefinitely without a timeout. 15 s is generous; in practice
		// the device responds within milliseconds.
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := c.SendChannelTextMessage(ctx, idx, text, 0)
		if err != nil && ctx.Err() == nil {
			return sendErrMsg{err: err}
		}
		return nil
	}
}
