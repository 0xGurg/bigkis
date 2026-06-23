package components

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/0xGurg/bigkis/internal/tui"
)

// TabBar renders a horizontal tab bar with one active tab highlighted.
type TabBar struct {
	Tabs          []string
	Active        int
	ActiveStyle   lipgloss.Style
	InactiveStyle lipgloss.Style
}

// NewTabBar creates a TabBar using the default theme styles.
func NewTabBar(tabs []string, active int) TabBar {
	return TabBar{
		Tabs:          tabs,
		Active:        active,
		ActiveStyle:   tui.Theme.ActiveTab,
		InactiveStyle: tui.Theme.InactiveTab,
	}
}

// View renders the tab bar as a single line.
func (t TabBar) View() string {
	var parts []string
	for i, tab := range t.Tabs {
		if i == t.Active {
			parts = append(parts, t.ActiveStyle.Render(tab))
		} else {
			parts = append(parts, t.InactiveStyle.Render(tab))
		}
	}
	return strings.Join(parts, "   ")
}
