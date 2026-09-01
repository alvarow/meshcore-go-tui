package views

import tea "github.com/charmbracelet/bubbletea"

type View interface {
	Title() string
	Init() tea.Cmd
	Update(msg tea.Msg) (View, tea.Cmd)
	View() string
	// InputFocused reports whether the view currently has a text input focused.
	// app.go uses this to suppress global key bindings (1–5, q) while typing.
	InputFocused() bool
}
