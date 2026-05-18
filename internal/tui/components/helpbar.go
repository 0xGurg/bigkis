package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"codeberg.org/gurg/bigkis/internal/tui"
)

// HelpBinding describes a single key binding for the help bar.
type HelpBinding struct {
	Key  string // e.g. "space", "enter", "q"
	Desc string // e.g. "toggle", "confirm", "quit"
}

// HelpBar is a reusable model component that renders a compact, dynamic
// footer of key-binding hints. It is a pure view helper — it has no Update
// logic of its own.
type HelpBar struct {
	Bindings []HelpBinding
}

// NewHelpBar creates a HelpBar with the given bindings.
func NewHelpBar(bindings ...HelpBinding) HelpBar {
	return HelpBar{Bindings: bindings}
}

// SetBindings replaces the current bindings at runtime (e.g. when switching
// between modes in a screen).
func (h *HelpBar) SetBindings(bindings ...HelpBinding) {
	h.Bindings = bindings
}

// View renders the help bar as a single line of key/description pairs.
//
// Example output:
//
//	[a] all  [n] none  [space] toggle  [q] quit
func (h HelpBar) View() string {
	if len(h.Bindings) == 0 {
		return ""
	}

	keyStyle := tui.Theme.Highlight
	descStyle := tui.Theme.Dim
	sep := "  "

	parts := make([]string, 0, len(h.Bindings))
	for _, b := range h.Bindings {
		part := fmt.Sprintf("[%s] %s",
			keyStyle.Render(b.Key),
			descStyle.Render(b.Desc),
		)
		parts = append(parts, part)
	}

	return lipgloss.NewStyle().Padding(0, 1).Render(strings.Join(parts, sep))
}
