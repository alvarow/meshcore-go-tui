package views

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/meshcore-go/go-cli/storage"
	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/companion"
	"github.com/meshcore-go/meshcore-go/companion/client"
)

type MsgStatus int

const (
	StatusReceived MsgStatus = iota
	StatusSending
	StatusAcked
	StatusFailed
)

type chatMessage struct {
	from      string
	text      string
	status    MsgStatus
	timestamp time.Time
}

type contactItem struct {
	contact  companion.ContactResponse
	messages []chatMessage
	lastRead time.Time
}

type ChatView struct {
	client      *client.Client
	store       *storage.Store
	contacts    []contactItem
	selected    int
	vp          viewport.Model
	vpReady     bool
	loadingMore bool
	input       textinput.Model
	searchMode  bool
	searchInput textinput.Model
	width       int
	height      int
}

const listWidth = 22

func NewChatView(c *client.Client, store *storage.Store) *ChatView {
	ti := textinput.New()
	ti.Placeholder = "Type a message..."
	ti.CharLimit = 200
	ti.Focus()

	si := textinput.New()
	si.Placeholder = "search..."
	si.CharLimit = 40
	si.Width = listWidth - 4

	return &ChatView{client: c, store: store, input: ti, searchInput: si}
}

func (v *ChatView) Title() string { return "Chat" }

func (v *ChatView) Init() tea.Cmd { return textinput.Blink }

// filteredIndices returns the indices into v.contacts that match the current search query.
// Returns all indices when not in search mode or query is empty.
func (v *ChatView) filteredIndices() []int {
	query := strings.ToLower(strings.TrimSpace(v.searchInput.Value()))
	out := make([]int, 0, len(v.contacts))
	for i, c := range v.contacts {
		if !v.searchMode || query == "" {
			out = append(out, i)
			continue
		}
		name := strings.ToLower(c.contact.AdvertName)
		if name == "" {
			name = hex.EncodeToString(c.contact.PublicKey[:3])
		}
		if strings.Contains(name, query) {
			out = append(out, i)
		}
	}
	return out
}

func chatUnreadSeparator(count, width int) string {
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

func (v *ChatView) buildLines() []string {
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#475569"))
	sentStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED"))
	recvStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#E2E8F0"))
	ackStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED"))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))

	var lines []string
	if v.selected < len(v.contacts) {
		msgs := v.contacts[v.selected].messages
		lr := v.contacts[v.selected].lastRead

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
				lines = append(lines, chatUnreadSeparator(len(msgs)-unreadFrom, v.vp.Width))
			}
			ts := dimStyle.Render(m.timestamp.Format("15:04"))
			var line string
			if m.status == StatusReceived {
				line = fmt.Sprintf("%s  %s: %s", ts, m.from, recvStyle.Render(m.text))
			} else {
				var indicator string
				switch m.status {
				case StatusSending:
					indicator = dimStyle.Render(" …")
				case StatusAcked:
					indicator = ackStyle.Render(" ✓")
				case StatusFailed:
					indicator = errStyle.Render(" ✗")
				}
				line = fmt.Sprintf("%s  %s%s", ts, sentStyle.Render("you: "+m.text), indicator)
			}
			lines = append(lines, line)
		}
	}
	return lines
}

func (v *ChatView) rebuildViewport() {
	if !v.vpReady {
		return
	}
	v.vp.SetContent(strings.Join(v.buildLines(), "\n"))
	v.vp.GotoBottom()
}

func (v *ChatView) rebuildViewportKeepOffset(prependedCount int) {
	if !v.vpReady {
		return
	}
	v.vp.SetContent(strings.Join(v.buildLines(), "\n"))
	v.vp.SetYOffset(prependedCount)
}

func (v *ChatView) Update(msg tea.Msg) (View, tea.Cmd) {
	var cmd tea.Cmd

	switch m := msg.(type) {
	case SessionReadyMsg:
		v.contacts = make([]contactItem, len(m.Contacts))
		for i, c := range m.Contacts {
			v.contacts[i] = contactItem{contact: c}
			if v.store != nil {
				key := hex.EncodeToString(c.PublicKey[:])
				if stored, err := v.store.LoadDirectMessages(key, 100); err == nil {
					for _, sm := range stored {
						status := StatusReceived
						if sm.Direction == storage.Outbound {
							if sm.Acked {
								status = StatusAcked
							} else {
								status = StatusSending
							}
						}
						v.contacts[i].messages = append(v.contacts[i].messages, chatMessage{
							from: sm.From, text: sm.Text, status: status, timestamp: sm.Timestamp,
						})
					}
				}
				if ts, err := v.store.GetLastRead(key); err == nil {
					v.contacts[i].lastRead = ts
				}
			}
		}
		v.rebuildViewport()
		return v, nil

	case InboundDirectMsg:
		for i := range v.contacts {
			var p6 [6]byte
			copy(p6[:], v.contacts[i].contact.PublicKey[:6])
			if p6 == m.PubKeyPrefix {
				name := v.contacts[i].contact.AdvertName
				if name == "" {
					name = hex.EncodeToString(p6[:])
				}
				v.contacts[i].messages = append(v.contacts[i].messages, chatMessage{
					from: name, text: m.Text, status: StatusReceived, timestamp: m.Timestamp,
				})
				if v.store != nil {
					key := hex.EncodeToString(v.contacts[i].contact.PublicKey[:])
					_ = v.store.SaveDirectMessage(key, storage.StoredMessage{
						Timestamp: m.Timestamp, From: name, Text: m.Text, Direction: storage.Inbound,
					})
				}
				if i == v.selected {
					if v.store != nil {
						key := hex.EncodeToString(v.contacts[i].contact.PublicKey[:])
						_ = v.store.SetLastRead(key, m.Timestamp)
						v.contacts[i].lastRead = m.Timestamp
					}
					v.rebuildViewport()
				}
				return v, nil
			}
		}
		return v, nil

	case OutboundAckMsg:
		if v.selected < len(v.contacts) {
			msgs := v.contacts[v.selected].messages
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i].status == StatusSending {
					v.contacts[v.selected].messages[i].status = StatusAcked
					if v.store != nil {
						key := hex.EncodeToString(v.contacts[v.selected].contact.PublicKey[:])
						_ = v.store.MarkAcked(key, msgs[i].timestamp)
					}
					break
				}
			}
			v.rebuildViewport()
		}
		return v, nil

	case sendErrMsg:
		if v.selected < len(v.contacts) {
			msgs := v.contacts[v.selected].messages
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i].status == StatusSending {
					v.contacts[v.selected].messages[i].status = StatusFailed
					break
				}
			}
			v.rebuildViewport()
		}
		return v, nil

	case olderDirectMsgsMsg:
		v.loadingMore = false
		if len(m.messages) == 0 {
			return v, nil
		}
		// Find the contact matching this key.
		for i := range v.contacts {
			if hex.EncodeToString(v.contacts[i].contact.PublicKey[:]) == m.contactKey {
				converted := make([]chatMessage, 0, len(m.messages))
				for _, sm := range m.messages {
					status := StatusReceived
					if sm.Direction == storage.Outbound {
						if sm.Acked {
							status = StatusAcked
						} else {
							status = StatusSending
						}
					}
					converted = append(converted, chatMessage{
						from: sm.From, text: sm.Text, status: status, timestamp: sm.Timestamp,
					})
				}
				v.contacts[i].messages = append(converted, v.contacts[i].messages...)
				if i == v.selected {
					v.rebuildViewportKeepOffset(len(converted))
				}
				break
			}
		}
		return v, nil

	case tea.KeyMsg:
		if v.searchMode {
			switch m.String() {
			case "esc":
				v.searchMode = false
				v.searchInput.Reset()
				v.searchInput.Blur()
				v.input.Focus()
				return v, nil
			case "enter":
				fi := v.filteredIndices()
				if len(fi) > 0 {
					// find which filtered index is currently selected, pick first otherwise
					found := false
					for _, idx := range fi {
						if idx == v.selected {
							found = true
							break
						}
					}
					if !found {
						v.saveLastRead(v.selected)
						v.selected = fi[0]
					}
				}
				v.searchMode = false
				v.searchInput.Reset()
				v.searchInput.Blur()
				v.input.Focus()
				v.rebuildViewport()
				return v, nil
			case "up":
				fi := v.filteredIndices()
				for i, idx := range fi {
					if idx == v.selected && i > 0 {
						v.selected = fi[i-1]
						v.rebuildViewport()
						break
					}
				}
				v.searchInput, cmd = v.searchInput.Update(msg)
				return v, cmd
			case "down":
				fi := v.filteredIndices()
				for i, idx := range fi {
					if idx == v.selected && i < len(fi)-1 {
						v.selected = fi[i+1]
						v.rebuildViewport()
						break
					}
				}
				v.searchInput, cmd = v.searchInput.Update(msg)
				return v, cmd
			default:
				v.searchInput, cmd = v.searchInput.Update(msg)
				// Ensure selected is in filtered list after query changes.
				fi := v.filteredIndices()
				if len(fi) > 0 {
					inList := false
					for _, idx := range fi {
						if idx == v.selected {
							inList = true
							break
						}
					}
					if !inList {
						v.selected = fi[0]
						v.rebuildViewport()
					}
				}
				return v, cmd
			}
		}

		// Normal (non-search) key handling.
		switch m.String() {
		case "/":
			v.searchMode = true
			v.input.Blur()
			v.searchInput.Focus()
			return v, nil
		case "enter":
			text := strings.TrimSpace(v.input.Value())
			if text == "" || v.client == nil || len(v.contacts) == 0 {
				return v, nil
			}
			contact := v.contacts[v.selected].contact
			now := time.Now()
			v.contacts[v.selected].messages = append(v.contacts[v.selected].messages, chatMessage{
				from: "me", text: text, status: StatusSending, timestamp: now,
			})
			if v.store != nil {
				key := hex.EncodeToString(contact.PublicKey[:])
				_ = v.store.SaveDirectMessage(key, storage.StoredMessage{
					Timestamp: now, From: "me", Text: text, Direction: storage.Outbound,
				})
			}
			v.input.Reset()
			v.rebuildViewport()
			return v, sendDirectMsg(v.client, contact, text)
		case "up":
			if v.selected > 0 {
				v.saveLastRead(v.selected)
				v.selected--
				v.rebuildViewport()
			}
			return v, nil
		case "down":
			if v.selected < len(v.contacts)-1 {
				v.saveLastRead(v.selected)
				v.selected++
				v.rebuildViewport()
			}
			return v, nil
		case "pgup", "ctrl+u":
			v.vp, cmd = v.vp.Update(msg)
			if v.vp.AtTop() && !v.loadingMore && v.store != nil &&
				v.selected < len(v.contacts) && len(v.contacts[v.selected].messages) > 0 {
				v.loadingMore = true
				oldest := v.contacts[v.selected].messages[0].timestamp
				key := hex.EncodeToString(v.contacts[v.selected].contact.PublicKey[:])
				return v, loadOlderDirectMsgs(v.store, key, oldest)
			}
			return v, cmd
		case "pgdn", "ctrl+d":
			v.vp, cmd = v.vp.Update(msg)
			return v, cmd
		}

	case tea.WindowSizeMsg:
		v.width = m.Width
		v.height = m.Height
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
		v.searchInput.Width = listWidth - 4
		v.rebuildViewport()
	}

	v.input, cmd = v.input.Update(msg)
	return v, cmd
}

func (v *ChatView) View() string {
	if v.width == 0 {
		return ""
	}

	msgWidth := v.width - listWidth - 3
	innerHeight := v.height - 6

	fi := v.filteredIndices()

	// Build contact list lines.
	var listLines []string
	if v.searchMode {
		searchBar := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#7C3AED")).
			Width(listWidth - 2).
			Render("/ " + v.searchInput.View())
		listLines = append(listLines, searchBar)
	}

	if len(v.contacts) == 0 {
		listLines = append(listLines, lipgloss.NewStyle().
			Foreground(lipgloss.Color("#475569")).Render(" (no contacts)"))
	} else if v.searchMode && len(fi) == 0 {
		listLines = append(listLines, lipgloss.NewStyle().
			Foreground(lipgloss.Color("#475569")).Render(" (no matches)"))
	} else {
		indices := fi
		if !v.searchMode {
			indices = fi // same: all
		}
		for _, i := range indices {
			c := v.contacts[i]
			name := c.contact.AdvertName
			if name == "" {
				name = hex.EncodeToString(c.contact.PublicKey[:3])
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
	}

	// Pad to fill height.
	for len(listLines) < innerHeight {
		listLines = append(listLines, "")
	}

	contactList := lipgloss.NewStyle().
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

	top := lipgloss.JoinHorizontal(lipgloss.Top, contactList, " ", msgThread)
	inputBox := lipgloss.NewStyle().
		Width(v.width - 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7C3AED")).
		Render(v.input.View())

	return lipgloss.JoinVertical(lipgloss.Left, top, inputBox)
}

func (v *ChatView) saveLastRead(idx int) {
	if v.store == nil || idx >= len(v.contacts) {
		return
	}
	msgs := v.contacts[idx].messages
	if len(msgs) == 0 {
		return
	}
	latest := msgs[len(msgs)-1].timestamp
	key := hex.EncodeToString(v.contacts[idx].contact.PublicKey[:])
	_ = v.store.SetLastRead(key, latest)
	v.contacts[idx].lastRead = latest
}

func sendDirectMsg(c *client.Client, contact companion.ContactResponse, text string) tea.Cmd {
	return func() tea.Msg {
		identity := meshcore.NewIdentity(contact.PublicKey)
		_, err := c.SendTextMessage(context.Background(), identity, text, 0)
		if err != nil {
			return sendErrMsg{err: err}
		}
		return nil
	}
}

type sendErrMsg struct{ err error }

type olderDirectMsgsMsg struct {
	contactKey string
	messages   []storage.StoredMessage
}

func loadOlderDirectMsgs(s *storage.Store, key string, before time.Time) tea.Cmd {
	return func() tea.Msg {
		msgs, _ := s.LoadDirectMessagesBefore(key, before, 50)
		return olderDirectMsgsMsg{contactKey: key, messages: msgs}
	}
}
