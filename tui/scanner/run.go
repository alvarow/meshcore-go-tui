package scanner

import (
	"errors"

	tea "github.com/charmbracelet/bubbletea"
)

// ErrBLEUnavailable is returned by Run when no Bluetooth adapter is found.
var ErrBLEUnavailable = errors.New("no Bluetooth adapter (BlueZ not running or no hardware)")

// Run launches the BLE scanner picker as a full-screen BubbleTea program.
// Returns the selected device or Result{Canceled:true} on q/Ctrl+C.
// Returns ErrBLEUnavailable when BlueZ is not available on this machine.
func Run() (Result, error) {
	m := New()
	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return Result{}, err
	}
	final := finalModel.(*Model)
	if final.bleUnavailable {
		return Result{}, ErrBLEUnavailable
	}
	return final.result, nil
}
