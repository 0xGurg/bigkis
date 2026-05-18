// Package tui provides shared Bubble Tea components for bigkis interactive
// TUI screens (import picker, status dashboard, apply review, rollback browser).
package tui

import "github.com/charmbracelet/lipgloss"

// Theme holds lipgloss styles for all TUI screens. Colors are aligned with
// the ANSI escape codes used in app/internal/ui/ui.go (ansiRed = 31,
// ansiGreen = 32, ansiYellow = 33, ansiBlue = 34, ansiCyan = 36,
// ansiGrey = 90) translated to lipgloss indexed-color values.
var Theme = struct {
	// Named colors (exposed so screens can build custom styles).
	Red, Green, Yellow, Blue, Cyan, Grey lipgloss.Color

	// Pre-built styles matching ui.UI methods.
	Add        lipgloss.Style
	Remove     lipgloss.Style
	Info       lipgloss.Style
	Step       lipgloss.Style
	Warn       lipgloss.Style
	Error      lipgloss.Style
	Dim        lipgloss.Style
	ActiveTab  lipgloss.Style
	InactiveTab lipgloss.Style
	Bar        lipgloss.Style
	Title      lipgloss.Style
	Highlight  lipgloss.Style
	Selected   lipgloss.Style

	Border lipgloss.Style
}{
	// Colors use 8-bit ANSI palette indices rather than truecolor hex values
	// so they render identically to the ANSI escape codes in ui.ansi* — the
	// exact appearance depends on the terminal's color scheme, just like the
	// existing line-based output. Index mapping:
	//   "1" = ANSI 31 (red),   "2" = ANSI 32 (green), "3" = ANSI 33 (yellow),
	//   "4" = ANSI 34 (blue),  "6" = ANSI 36 (cyan),  "8" = ANSI 90 (grey).
	Red:    lipgloss.Color("1"),
	Green:  lipgloss.Color("2"),
	Yellow: lipgloss.Color("3"),
	Blue:   lipgloss.Color("4"),
	Cyan:   lipgloss.Color("6"),
	Grey:   lipgloss.Color("8"),

	Add:   lipgloss.NewStyle().Foreground(lipgloss.Color("2")),  // green
	Remove: lipgloss.NewStyle().Foreground(lipgloss.Color("1")),  // red
	Info:  lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true),  // blue+bold
	Step:  lipgloss.NewStyle().Foreground(lipgloss.Color("6")),  // cyan
	Warn:  lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true),  // yellow+bold
	Error: lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true),  // red+bold
	Dim:   lipgloss.NewStyle().Foreground(lipgloss.Color("8")),  // grey

	ActiveTab:   lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true),   // green+bold
	InactiveTab: lipgloss.NewStyle().Foreground(lipgloss.Color("8")),              // grey
	Bar:         lipgloss.NewStyle().Foreground(lipgloss.Color("8")),              // grey
	Title:       lipgloss.NewStyle().Bold(true),
	Highlight:   lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true),   // cyan+bold
	Selected:    lipgloss.NewStyle().Foreground(lipgloss.Color("2")),              // green

	// Subtle border for viewports and list frames.
	Border: lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("8")),  // grey
}

// StyleAddPrefix returns a styled "+ <text>" line.
func StyleAddPrefix(s string) string {
	return Theme.Add.Render("+") + " " + s
}

// StyleRemovePrefix returns a styled "- <text>" line.
func StyleRemovePrefix(s string) string {
	return Theme.Remove.Render("-") + " " + s
}
