package presentation

import (
	"io"

	"golang.org/x/sys/windows"
)

// Legacy PowerShell consoles require virtual terminal output. Restore the
// original console mode after each write; input mode is never changed.
func enableANSI(w io.Writer) func() {
	file, ok := w.(interface{ Fd() uintptr })
	if !ok {
		return func() {}
	}
	handle := windows.Handle(file.Fd())
	var mode uint32
	if windows.GetConsoleMode(handle, &mode) != nil || mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return func() {}
	}
	if windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) != nil {
		return func() {}
	}
	return func() { _ = windows.SetConsoleMode(handle, mode) }
}
