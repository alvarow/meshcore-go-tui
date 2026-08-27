package views

import tea "github.com/charmbracelet/bubbletea"

type View interface {
	Title() string
	Init() tea.Cmd
	Update(msg tea.Msg) (View, tea.Cmd)
	View() string
}
