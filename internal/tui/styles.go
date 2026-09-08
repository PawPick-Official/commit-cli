package tui

import (
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
)

var (
	// paneStyle wraps the file list, the preview gets its own bordered pane.
	paneStyle = lipgloss.NewStyle().Padding(0, 1)

	previewBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#45475a"))

	// previewStyle wraps the live commit message preview in the commit writer.
	previewStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#45475a")).
			Padding(0, 1)

	diffAddStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#a6d189"))
	diffRemoveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#e78284"))
	diffMetaStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#8caaee"))

	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))

	// cliErrorStyle formats fatal errors printed when the TUI is not running:
	// a bold red "Error:" prefix followed by the italic message.
	cliErrorStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e78284"))
	cliErrorMsgStyle = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("#eba2a6"))
	cliInfoStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#8caaee"))
)

// Fatal prints a styled error to stderr and exits with code 1.
func Fatal(err error) {
	fmt.Fprintln(os.Stderr, cliErrorStyle.Render("Error:")+" "+cliErrorMsgStyle.Render(err.Error()))
	os.Exit(1)
}

// Info prints a styled informational message to stdout.
func Info(msg string) {
	fmt.Println(cliInfoStyle.Render(msg))
}
