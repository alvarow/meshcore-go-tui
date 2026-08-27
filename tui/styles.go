package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorPrimary = lipgloss.Color("#7C3AED")
	colorText    = lipgloss.Color("#E2E8F0")
	colorSubtle  = lipgloss.Color("#475569")
	colorBg      = lipgloss.Color("#0F172A")
	colorGreen   = lipgloss.Color("#22C55E")
	colorRed     = lipgloss.Color("#EF4444")

	ActiveTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorText).
			Background(colorPrimary).
			Padding(0, 2)

	InactiveTabStyle = lipgloss.NewStyle().
				Foreground(colorSubtle).
				Padding(0, 2)

	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary)

	StatusBarStyle = lipgloss.NewStyle().
			Foreground(colorSubtle).
			Background(colorBg).
			Padding(0, 1)

	ConnectedStyle = lipgloss.NewStyle().
			Foreground(colorGreen)

	DisconnectedStyle = lipgloss.NewStyle().
				Foreground(colorRed)

	MessageStyle = lipgloss.NewStyle().
			Foreground(colorText).
			PaddingLeft(1)

	SentMessageStyle = lipgloss.NewStyle().
				Foreground(colorPrimary).
				PaddingLeft(1)

	BorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorSubtle)
)
