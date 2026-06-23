package components

import (
	"fmt"
	"io"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// CheckboxItem is a list.Item that supports checkbox rendering.
// Items passed to CheckboxDelegate must implement this interface.
type CheckboxItem interface {
	list.Item
	IsChecked() bool
	// Prefix returns an optional prefix character like "+", "-", "↑", or "".
	Prefix() string
	// PrefixStyle returns the lipgloss style for the prefix (only used when
	// Prefix returns a non-empty string).
	PrefixStyle() lipgloss.Style
}

// CheckboxDelegate renders list items with [x]/[ ] checkboxes, an optional
// prefix, and a selection cursor. It implements list.ItemDelegate.
type CheckboxDelegate struct {
	SelectedStyle   lipgloss.Style
	UnselectedStyle lipgloss.Style
	CheckedStyle    lipgloss.Style
}

func (d CheckboxDelegate) Height() int                             { return 1 }
func (d CheckboxDelegate) Spacing() int                            { return 0 }
func (d CheckboxDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d CheckboxDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	ci, ok := item.(CheckboxItem)
	if !ok {
		return
	}

	// Checkbox symbol
	check := "[ ]"
	if ci.IsChecked() {
		check = d.CheckedStyle.Render("[x]")
	}

	// Selection cursor
	cursor := "  "
	if index == m.Index() {
		cursor = d.SelectedStyle.Render("> ")
	}

	// Optional prefix (e.g. + / - / ↑)
	var prefix string
	if p := ci.Prefix(); p != "" {
		prefix = ci.PrefixStyle().Render(p) + " "
	}

	label := ci.FilterValue()
	fmt.Fprintf(w, "%s%s%s%s", cursor, check, prefix, label)
}
