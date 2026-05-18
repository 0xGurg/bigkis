// Package components provides reusable Bubble Tea model components shared
// across TUI screens (import picker, status dashboard, apply review, rollback
// browser).
package components

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"codeberg.org/gurg/bigkis/internal/tui"
)

// ConfirmResult is the outcome of a confirmation prompt.
type ConfirmResult int

const (
	// ConfirmNone means the user has not answered yet.
	ConfirmNone ConfirmResult = iota
	// ConfirmYes means the user answered yes / y.
	ConfirmYes
	// ConfirmNo means the user answered no / n.
	ConfirmNo
	// ConfirmQuit means the user requested to quit / abort.
	ConfirmQuit
)

// confirmStyles wraps the reusable lipgloss styles for the confirm bar.
var confirmStyles = struct {
	prompt lipgloss.Style
	hint   lipgloss.Style
	key    lipgloss.Style
}{
	prompt: tui.Theme.Info,
	hint:   tui.Theme.Dim,
	key:    tui.Theme.Highlight,
}

// ConfirmBar is a reusable confirmation footer. Embed it in a parent model
// and call Update with tea.KeyMsg to collect user input.
type ConfirmBar struct {
	Prompt    string
	assumeYes bool
	result    ConfirmResult
}

// NewConfirmBar returns a ConfirmBar with the given prompt string.
// When assumeYes is true, the bar immediately returns ConfirmYes without
// rendering (useful for --yes / non-interactive paths).
func NewConfirmBar(prompt string, assumeYes bool) ConfirmBar {
	c := ConfirmBar{Prompt: prompt, assumeYes: assumeYes}
	if assumeYes {
		c.result = ConfirmYes
	}
	return c
}

// Update handles a key message and returns the result. The caller should
// check the result after each call; once it's non-zero the bar is done.
// When assumeYes is true, Update always returns ConfirmYes and ignores
// all input — the bar should not be rendered in this case.
func (c *ConfirmBar) Update(msg tea.KeyMsg) ConfirmResult {
	if c.assumeYes || c.result != ConfirmNone {
		return c.result // already decided or auto-confirmed
	}
	switch msg.String() {
	case "y", "Y":
		c.result = ConfirmYes
	case "n", "N":
		c.result = ConfirmNo
	case "q", "Q", "ctrl+c":
		c.result = ConfirmQuit
	}
	return c.result
}

// Result returns the current confirmation state without processing input.
func (c ConfirmBar) Result() ConfirmResult { return c.result }

// Reset clears the result so the bar can be reused for a new prompt.
func (c *ConfirmBar) Reset() { c.result = ConfirmNone }

// View renders the confirmation prompt line. Returns an empty string when
// the result is already decided (the parent should stop showing the bar).
func (c ConfirmBar) View() string {
	if c.result != ConfirmNone {
		return ""
	}

	prompt := c.Prompt
	if prompt == "" {
		prompt = "proceed?"
	}

	return fmt.Sprintf(
		"%s %s",
		confirmStyles.prompt.Render(prompt),
		confirmStyles.hint.Render(fmt.Sprintf(
			"[%s/%s/%s]",
			confirmStyles.key.Render("y"),
			confirmStyles.key.Render("N"),
			confirmStyles.key.Render("q"),
		)),
	)
}
