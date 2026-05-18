package tui

import (
	"os"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
)

// ──────────────────────────────────────────────
// ShouldUse — TUI activation gate
// ──────────────────────────────────────────────

// ShouldUse returns true if the TUI should be launched instead of the
// traditional line-based output. All of the following must hold:
//
//  1. stdout AND stdin are connected to a terminal (character device).
//  2. --json is not set.
//  3. --quiet is not set.
//  4. The NO_COLOR environment variable is not set.
//  5. The BIGKIS_NO_TUI environment variable is not set.
//
// The Bools correspond to CLI flags so callers pass c.Bool("json") etc.
func ShouldUse(stdout *os.File, stdin *os.File, json, quiet bool) bool {
	if json || quiet {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("BIGKIS_NO_TUI") != "" {
		return false
	}
	return isTerminal(stdout) && isTerminal(stdin)
}

// isTerminal reports whether f is a terminal (ptty or tty device).
// It uses go-isatty for the correct check across platforms (the raw
// os.ModeCharDevice bit includes /dev/null on some systems).
// This is equivalent to ui.IsColorTTY but without NO_COLOR/FORCE_COLOR
// checks (those are handled separately in ShouldUse).
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	return isatty.IsTerminal(f.Fd())
}

// ──────────────────────────────────────────────
// Common key bindings
// ──────────────────────────────────────────────

// CommonKeymap holds key bindings shared across all TUI screens. Each screen
// adds its own screen-specific bindings alongside these.
type CommonKeymap struct {
	Quit  key.Binding
	Enter key.Binding
	Back  key.Binding
	Help  key.Binding
}

// DefaultCommonKeymap is the canonical shared keymap. Screens should embed
// or compose with this, adding their own screen-specific bindings.
var DefaultCommonKeymap = CommonKeymap{
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "confirm"),
	),
	Back: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "back"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "help"),
	),
}

// FullHelp returns all four bindings as a single key.Binding slice for use
// with tea.KeyMap or lipgloss help rendering.
func (k CommonKeymap) FullHelp() []key.Binding {
	return []key.Binding{k.Quit, k.Enter, k.Back, k.Help}
}

// ShortHelp returns the subset suitable for a one-line footer.
func (k CommonKeymap) ShortHelp() []key.Binding {
	return []key.Binding{k.Quit, k.Help}
}

// ──────────────────────────────────────────────
// Program helper
// ──────────────────────────────────────────────

// NewProgram creates a Bubble Tea program with standard settings:
//   - Alt-screen mode (restores terminal on quit)
//   - Mouse cell motion (for clickable list items)
//
// Callers may pass additional tea.ProgramOption values (e.g. for tests with
// tea.WithInput / tea.WithOutput). Extra opts are appended so the defaults
// above can be overridden when needed.
func NewProgram(model tea.Model, opts ...tea.ProgramOption) *tea.Program {
	defaultOpts := []tea.ProgramOption{
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	}
	return tea.NewProgram(model, append(defaultOpts, opts...)...)
}
