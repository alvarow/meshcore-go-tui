package views

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/alvarow/meshcore-go-tui/config"
	"github.com/alvarow/meshcore-go-tui/storage"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/companion"
	"github.com/meshcore-go/meshcore-go/companion/client"
)

type MsgStatus int

const (
	StatusReceived MsgStatus = iota
	StatusSending            // BLE write in progress
	StatusSent               // device accepted (SentResponse received); waiting for remote ack
	StatusAcked              // remote ack received (PushSendConfirmed)
	StatusNoAck              // no remote ack within timeout
	StatusFailed             // send error
)

type chatMessage struct {
	from        string
	text        string
	status      MsgStatus
	timestamp   time.Time
	roundTripMs uint32
	hasAckCode  bool // StatusSent: device had a path (false = blind flood)
	attempt     int  // retry count, 0 = first send
	isSystem    bool
}

// sentResultMsg is returned by sendDirectMsg when the BLE write completes.
type sentResultMsg struct {
	contactKey string
	timestamp  time.Time
	hasAckCode bool
}

// ackTimeoutMsg fires when a StatusSent message doesn't get a PushSendConfirmed.
type ackTimeoutMsg struct {
	contactKey string
	timestamp  time.Time
}

type contactItem struct {
	contact  companion.ContactResponse
	messages []chatMessage
	lastRead time.Time
	lastSeen time.Time
}

type ChatView struct {
	client       *client.Client
	store        *storage.Store
	km           config.KeyMap
	contacts     []contactItem
	selected     int
	vp           viewport.Model
	vpReady      bool
	loadingMore  bool
	input        textinput.Model
	searchMode   bool
	searchInput  textinput.Model
	selectMode   bool
	selectedMsg  int
	offRecord    bool
	clearPending bool
	pingStart    map[[6]byte]time.Time // pending pings keyed by pubkey prefix
	width        int
	height       int
}

const listWidth = 22

func NewChatView(c *client.Client, store *storage.Store, km config.KeyMap) *ChatView {
	ti := textinput.New()
	ti.Placeholder = "Type a message..."
	ti.CharLimit = 200
	ti.Focus()

	si := textinput.New()
	si.Placeholder = "search..."
	si.CharLimit = 40
	si.Width = listWidth - 4

	return &ChatView{client: c, store: store, km: km, input: ti, searchInput: si, pingStart: make(map[[6]byte]time.Time)}
}

func (v *ChatView) Title() string { return "Chat" }
func (v *ChatView) InputFocused() bool {
	return v.input.Focused() || v.searchInput.Focused()
}

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
	selectHL := lipgloss.NewStyle().Background(lipgloss.Color("#312E81")).Foreground(lipgloss.Color("#E2E8F0"))

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
			if m.isSystem {
				lines = append(lines, dimStyle.Render("  — "+m.text+" —"))
				continue
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
				case StatusSent:
					if m.hasAckCode {
						indicator = dimStyle.Render(" ● sent")
					} else {
						indicator = dimStyle.Render(" ● sent (no path)")
					}
				case StatusAcked:
					rtt := formatRTT(m.roundTripMs)
					indicator = ackStyle.Render(" ✓" + rtt)
				case StatusNoAck:
					retry := ""
					if m.attempt < 3 {
						retry = "  r=retry"
					}
					indicator = errStyle.Render(" ? no ack" + retry)
				case StatusFailed:
					indicator = errStyle.Render(" ✗")
				}
				line = fmt.Sprintf("%s  %s%s", ts, sentStyle.Render("you: "+m.text), indicator)
			}
			if v.selectMode && i == v.selectedMsg {
				line = selectHL.Width(v.vp.Width).Render("› " + strings.TrimLeft(line, " "))
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
		if m.Client != nil {
			v.client = m.Client
		}
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
				if v.store != nil && !v.offRecord {
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

	case sentResultMsg:
		for i := range v.contacts {
			if hex.EncodeToString(v.contacts[i].contact.PublicKey[:]) == m.contactKey {
				msgs := v.contacts[i].messages
				for j := len(msgs) - 1; j >= 0; j-- {
					if msgs[j].status == StatusSending && msgs[j].timestamp.Equal(m.timestamp) {
						v.contacts[i].messages[j].status = StatusSent
						v.contacts[i].messages[j].hasAckCode = m.hasAckCode
						if i == v.selected {
							v.rebuildViewport()
						}
						break
					}
				}
				break
			}
		}
		// Start 30s ack timeout — if PushSendConfirmed doesn't arrive, show ? no ack.
		return v, tea.Tick(30*time.Second, func(time.Time) tea.Msg {
			return ackTimeoutMsg{contactKey: m.contactKey, timestamp: m.timestamp}
		})

	case ackTimeoutMsg:
		for i := range v.contacts {
			if hex.EncodeToString(v.contacts[i].contact.PublicKey[:]) == m.contactKey {
				msgs := v.contacts[i].messages
				for j := range msgs {
					if msgs[j].status == StatusSent && msgs[j].timestamp.Equal(m.timestamp) {
						msg := &v.contacts[i].messages[j]
						if msg.attempt >= 3 && v.client != nil {
							// Retries exhausted — trigger path rediscovery (flood fallback)
							// then retry once more with the refreshed route.
							contact := v.contacts[i].contact
							now := time.Now()
							msg.attempt++
							msg.status = StatusSending
							msg.timestamp = now
							text := msg.text
							if i == v.selected {
								v.rebuildViewport()
							}
							return v, tea.Batch(
								pathDiscovery(v.client, contact),
								sendDirectMsg(v.client, contact, text, now),
							)
						}
						msg.status = StatusNoAck
						if i == v.selected {
							v.rebuildViewport()
						}
						break
					}
				}
				break
			}
		}
		return v, nil

	case OutboundAckMsg:
		// Match the oldest StatusSent message (sends are serial, FIFO).
		for i := range v.contacts {
			msgs := v.contacts[i].messages
			for j, msg := range msgs {
				if msg.status == StatusSent {
					v.contacts[i].messages[j].status = StatusAcked
					v.contacts[i].messages[j].roundTripMs = m.RoundTripMs
					if v.store != nil {
						k := hex.EncodeToString(v.contacts[i].contact.PublicKey[:])
						_ = v.store.MarkAcked(k, msg.timestamp)
					}
					if i == v.selected {
						v.rebuildViewport()
					}
					return v, nil
				}
			}
		}
		// Fallback: match oldest StatusSending (in case sentResultMsg hasn't arrived yet).
		for i := range v.contacts {
			msgs := v.contacts[i].messages
			for j, msg := range msgs {
				if msg.status == StatusSending {
					v.contacts[i].messages[j].status = StatusAcked
					v.contacts[i].messages[j].roundTripMs = m.RoundTripMs
					if i == v.selected {
						v.rebuildViewport()
					}
					return v, nil
				}
			}
		}
		return v, nil

	case NodeAdvertMsg:
		for i := range v.contacts {
			if v.contacts[i].contact.PublicKey == m.PubKey {
				v.contacts[i].lastSeen = time.Now()
				v.rebuildViewport()
				break
			}
		}
		return v, nil

	case PeerStatusMsg:
		for i := range v.contacts {
			var p6 [6]byte
			copy(p6[:], v.contacts[i].contact.PublicKey[:6])
			if p6 == m.PubKeyPrefix {
				sys := "ping: no reply recorded"
				if start, ok := v.pingStart[p6]; ok {
					rtt := time.Since(start).Round(time.Millisecond)
					delete(v.pingStart, p6)
					sys = fmt.Sprintf("ping reply: %v", rtt)
				}
				v.contacts[i].messages = append(v.contacts[i].messages,
					chatMessage{text: sys, isSystem: true, timestamp: time.Now()})
				if i == v.selected {
					v.rebuildViewport()
				}
				break
			}
		}
		return v, nil

	case PathDiscoveryMsg:
		for i := range v.contacts {
			var p6 [6]byte
			copy(p6[:], v.contacts[i].contact.PublicKey[:6])
			if p6 == m.PubKeyPrefix {
				sys := fmt.Sprintf("trace: %d hops out, %d hops back", m.OutHops, m.InHops)
				v.contacts[i].messages = append(v.contacts[i].messages,
					chatMessage{text: sys, isSystem: true, timestamp: time.Now()})
				if i == v.selected {
					v.rebuildViewport()
				}
				break
			}
		}
		return v, nil

	case sendErrMsg:
		for i := range v.contacts {
			if m.contactKey != "" && hex.EncodeToString(v.contacts[i].contact.PublicKey[:]) != m.contactKey {
				continue
			}
			msgs := v.contacts[i].messages
			for j := len(msgs) - 1; j >= 0; j-- {
				if msgs[j].status == StatusSending &&
					(m.timestamp.IsZero() || msgs[j].timestamp.Equal(m.timestamp)) {
					v.contacts[i].messages[j].status = StatusFailed
					if i == v.selected {
						v.rebuildViewport()
					}
					break
				}
			}
			break
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

	case AdvertResultMsg:
		sys := "advert sent"
		if m.Err != nil {
			sys = "advert failed: " + m.Err.Error()
		}
		if v.selected < len(v.contacts) {
			v.contacts[v.selected].messages = append(v.contacts[v.selected].messages,
				chatMessage{text: sys, isSystem: true, timestamp: time.Now()})
			v.rebuildViewport()
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
		switch {
		case key.Matches(m, v.km.OffRecord):
			v.offRecord = !v.offRecord
			if v.offRecord {
				v.input.Placeholder = "⊘ off the record"
			} else {
				v.input.Placeholder = "Type a message..."
			}
			return v, nil

		case key.Matches(m, v.km.SelectMode) && !v.input.Focused():
			if v.selectMode {
				v.selectMode = false
				v.input.Focus()
			} else if v.selected < len(v.contacts) && len(v.contacts[v.selected].messages) > 0 {
				v.selectMode = true
				v.selectedMsg = len(v.contacts[v.selected].messages) - 1
				v.input.Blur()
			}
			v.clearPending = false
			v.rebuildViewport()
			return v, nil

		case key.Matches(m, v.km.DeleteMsg) && v.selectMode && !v.input.Focused():
			if v.selected < len(v.contacts) {
				msgs := v.contacts[v.selected].messages
				if v.selectedMsg < len(msgs) {
					ts := msgs[v.selectedMsg].timestamp
					v.contacts[v.selected].messages = append(msgs[:v.selectedMsg], msgs[v.selectedMsg+1:]...)
					if v.selectedMsg >= len(v.contacts[v.selected].messages) && v.selectedMsg > 0 {
						v.selectedMsg--
					}
					if v.store != nil {
						k := hex.EncodeToString(v.contacts[v.selected].contact.PublicKey[:])
						_ = v.store.DeleteDirectMessage(k, ts)
					}
					v.rebuildViewport()
				}
				return v, nil
			}

		case key.Matches(m, v.km.ClearAll):
			if v.selected < len(v.contacts) {
				if v.clearPending {
					v.clearPending = false
					v.contacts[v.selected].messages = nil
					if v.store != nil {
						k := hex.EncodeToString(v.contacts[v.selected].contact.PublicKey[:])
						_ = v.store.ClearDirectMessages(k)
					}
					v.rebuildViewport()
				} else {
					v.clearPending = true
					v.contacts[v.selected].messages = append(v.contacts[v.selected].messages,
						chatMessage{text: "press " + v.km.ClearAll.Help().Key + " again to clear all messages", isSystem: true, timestamp: time.Now()})
					v.rebuildViewport()
				}
				return v, nil
			}

		case m.String() == "esc":
			if v.selectMode {
				v.selectMode = false
				v.clearPending = false
				v.input.Focus()
				v.rebuildViewport()
				return v, nil
			}
			// Blur the input so 1-5, q, s, etc. work as commands.
			v.input.Blur()
			return v, nil

		case key.Matches(m, v.km.Advert):
			if v.client != nil {
				return v, sendAdvert(v.client)
			}
			return v, nil
		case key.Matches(m, v.km.Search) && v.input.Value() == "":
			// Only open search when the input is empty — if the user has typed
			// something (e.g. starting a /command), let it go to the input.
			v.searchMode = true
			v.input.Blur()
			v.searchInput.Focus()
			return v, nil
		case m.String() == "r" && !v.input.Focused():
			// Retry the last StatusNoAck message for the selected contact.
			if v.selected < len(v.contacts) {
				msgs := v.contacts[v.selected].messages
				for i := len(msgs) - 1; i >= 0; i-- {
					if msgs[i].status == StatusNoAck && msgs[i].attempt < 3 {
						attempt := msgs[i].attempt + 1
						text := msgs[i].text
						contact := v.contacts[v.selected].contact
						now := time.Now()
						v.contacts[v.selected].messages[i].status = StatusSending
						v.contacts[v.selected].messages[i].attempt = attempt
						v.contacts[v.selected].messages[i].timestamp = now
						v.rebuildViewport()
						return v, sendDirectMsg(v.client, contact, text, now)
					}
				}
			}
			return v, nil

		case key.Matches(m, v.km.Send):
			raw := strings.TrimSpace(v.input.Value())
			// Slash commands: /ping and /trace act on the selected contact.
			if strings.ToLower(raw) == "/ping" && v.client != nil && v.selected < len(v.contacts) {
				v.input.Reset()
				contact := v.contacts[v.selected].contact
				var p6 [6]byte
				copy(p6[:], contact.PublicKey[:6])
				v.pingStart[p6] = time.Now()
				v.contacts[v.selected].messages = append(v.contacts[v.selected].messages,
					chatMessage{text: "ping sent…", isSystem: true, timestamp: time.Now()})
				v.rebuildViewport()
				return v, pingContact(v.client, contact)
			}
			if strings.HasPrefix(strings.ToLower(raw), "/location ") && v.client != nil {
				v.input.Reset()
				return v, setLocation(v.client, raw[len("/location "):], func(sys string) {
					if v.selected < len(v.contacts) {
						v.contacts[v.selected].messages = append(v.contacts[v.selected].messages,
							chatMessage{text: sys, isSystem: true, timestamp: time.Now()})
						v.rebuildViewport()
					}
				})
			}
			if strings.ToLower(raw) == "/trace" && v.client != nil && v.selected < len(v.contacts) {
				v.input.Reset()
				contact := v.contacts[v.selected].contact
				v.contacts[v.selected].messages = append(v.contacts[v.selected].messages,
					chatMessage{text: "trace sent…", isSystem: true, timestamp: time.Now()})
				v.rebuildViewport()
				return v, traceContact(v.client, contact)
			}
			text := expandShortcodes(raw)
			if text == "" || v.client == nil || len(v.contacts) == 0 {
				return v, nil
			}
			contact := v.contacts[v.selected].contact
			now := time.Now()
			v.contacts[v.selected].messages = append(v.contacts[v.selected].messages, chatMessage{
				from: "me", text: text, status: StatusSending, timestamp: now,
			})
			if v.store != nil && !v.offRecord {
				k := hex.EncodeToString(contact.PublicKey[:])
				_ = v.store.SaveDirectMessage(k, storage.StoredMessage{
					Timestamp: now, From: "me", Text: text, Direction: storage.Outbound,
				})
			}
			v.input.Reset()
			v.rebuildViewport()
			return v, sendDirectMsg(v.client, contact, text, now)
		case m.String() == "up":
			if v.selectMode {
				if v.selectedMsg > 0 {
					v.selectedMsg--
					v.rebuildViewport()
				}
			} else if v.selected > 0 {
				v.saveLastRead(v.selected)
				v.selected--
				v.rebuildViewport()
			}
			return v, nil
		case m.String() == "down":
			if v.selectMode {
				if v.selected < len(v.contacts) && v.selectedMsg < len(v.contacts[v.selected].messages)-1 {
					v.selectedMsg++
					v.rebuildViewport()
				}
			} else if v.selected < len(v.contacts)-1 {
				v.saveLastRead(v.selected)
				v.selected++
				v.rebuildViewport()
			}
			return v, nil
		case key.Matches(m, v.km.ScrollUp) || m.String() == "ctrl+u":
			v.vp, cmd = v.vp.Update(msg)
			if v.vp.AtTop() && !v.loadingMore && v.store != nil &&
				v.selected < len(v.contacts) && len(v.contacts[v.selected].messages) > 0 {
				v.loadingMore = true
				oldest := v.contacts[v.selected].messages[0].timestamp
				k := hex.EncodeToString(v.contacts[v.selected].contact.PublicKey[:])
				return v, loadOlderDirectMsgs(v.store, k, oldest)
			}
			return v, cmd
		case key.Matches(m, v.km.ScrollDown) || m.String() == "ctrl+d":
			v.vp, cmd = v.vp.Update(msg)
			return v, cmd
		}
		// Any printable key while input is blurred re-focuses so the user
		// can start typing immediately without an extra keypress.
		if !v.input.Focused() && !v.selectMode && !v.searchMode &&
			len(m.String()) == 1 && m.String() != "esc" {
			v.input.Focus()
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
			if len(name) > listWidth-4 {
				name = name[:listWidth-4]
			}
			dot := freshnessStyle(c.lastSeen).Render("●")
			if i == v.selected {
				line := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7C3AED")).Render("> " + name)
				listLines = append(listLines, dot+" "+line)
			} else {
				listLines = append(listLines, dot+" "+name)
			}
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

	inputBorderColor := lipgloss.Color("#7C3AED")
	if v.offRecord {
		inputBorderColor = lipgloss.Color("#DC2626")
	} else if v.selectMode {
		inputBorderColor = lipgloss.Color("#0EA5E9")
	}
	inputContent := v.input.View()
	if v.selectMode {
		inputContent = lipgloss.NewStyle().Foreground(lipgloss.Color("#0EA5E9")).
			Render("select mode  ↑↓ navigate  d delete  s exit")
	}
	inputBox := lipgloss.NewStyle().
		Width(v.width - 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(inputBorderColor).
		Render(inputContent)

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

// pingContact sends a status request to the peer. PeerStatusMsg arrives when
// the peer responds; the view computes RTT from its stored pingStart time.
func pingContact(c *client.Client, contact companion.ContactResponse) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.SendStatusReq(ctx, meshcore.NewIdentity(contact.PublicKey))
		return nil
	}
}

// traceContact sends a path-discovery request. PathDiscoveryMsg arrives with
// the hop counts when the mesh responds.
func traceContact(c *client.Client, contact companion.ContactResponse) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = c.SendPathDiscoveryReq(ctx, meshcore.NewIdentity(contact.PublicKey))
		return nil
	}
}

// setLocation pushes GPS coordinates to the device's advertised location.
// args is the remainder after "/location " — expected: "lat lon" in decimal degrees.
// The firmware stores them (× 1e7 as int32) and includes them in future adverts.
func setLocation(c *client.Client, args string, done func(string)) tea.Cmd {
	return func() tea.Msg {
		parts := strings.Fields(args)
		if len(parts) != 2 {
			done("usage: /location <lat> <lon>  (decimal degrees, e.g. 4.099645 -7.403468)")
			return nil
		}
		var lat, lon float64
		if _, err := fmt.Sscanf(parts[0], "%f", &lat); err != nil {
			done("location: invalid latitude: " + parts[0])
			return nil
		}
		if _, err := fmt.Sscanf(parts[1], "%f", &lon); err != nil {
			done("location: invalid longitude: " + parts[1])
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := c.SetAdvertLatLon(ctx, int32(lat*1e7), int32(lon*1e7))
		if err != nil {
			done(fmt.Sprintf("location: %v", err))
		} else {
			done(fmt.Sprintf("location set: %.6f, %.6f", lat, lon))
		}
		return nil
	}
}

// pathDiscovery sends a path-discovery request to force the firmware to find a
// fresh route to the peer. Called automatically after 3 retries with no ack
// (flood fallback for mobile mesh scenarios where the cached path is stale).
func pathDiscovery(c *client.Client, contact companion.ContactResponse) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		identity := meshcore.NewIdentity(contact.PublicKey)
		_, _ = c.SendPathDiscoveryReq(ctx, identity)
		return nil
	}
}

func sendDirectMsg(c *client.Client, contact companion.ContactResponse, text string, ts time.Time) tea.Cmd {
	contactKey := hex.EncodeToString(contact.PublicKey[:])
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		identity := meshcore.NewIdentity(contact.PublicKey)
		resp, err := c.SendTextMessage(ctx, identity, text, 0)
		if err != nil && ctx.Err() == nil {
			return sendErrMsg{err: err, timestamp: ts, contactKey: contactKey}
		}
		// On ctx timeout the device likely queued it anyway; treat as sent.
		return sentResultMsg{contactKey: contactKey, timestamp: ts, hasAckCode: resp.HasAckCode}
	}
}

type sendErrMsg struct {
	err        error
	timestamp  time.Time
	contactKey string
}

// AdvertResultMsg is returned by sendAdvert and handled by the view that sent it.
type AdvertResultMsg struct{ Err error }

// sendAdvert broadcasts a self-advertisement and returns an AdvertResultMsg.
func sendAdvert(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return AdvertResultMsg{Err: c.SendSelfAdvert(ctx, 0)}
	}
}

// freshnessStyle returns a lipgloss style whose colour reflects how recently a
// contact was last seen: green <5 min, yellow <1 hr, dim red otherwise.
func freshnessStyle(lastSeen time.Time) lipgloss.Style {
	if lastSeen.IsZero() {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#475569"))
	}
	ago := time.Since(lastSeen)
	switch {
	case ago < 5*time.Minute:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E"))
	case ago < time.Hour:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#EAB308"))
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))
	}
}

// formatRTT formats a round-trip time for display next to the ack checkmark.
func formatRTT(ms uint32) string {
	if ms == 0 {
		return ""
	}
	if ms < 1000 {
		return fmt.Sprintf(" %dms", ms)
	}
	return fmt.Sprintf(" %.1fs", float64(ms)/1000)
}

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
