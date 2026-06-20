// Package rollback provides a Bubble Tea split-pane browser for rollback
// scripts. The left pane lists available scripts (newest first); the right
// pane previews the selected script body.
package rollback

import (
	"fmt"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	rollbackdata "github.com/0xGurg/bigkis/internal/rollback"
	"github.com/0xGurg/bigkis/internal/tui"
	"github.com/0xGurg/bigkis/internal/tui/components"
)

// ──────────────────────────────────────────────
// Key bindings
// ──────────────────────────────────────────────

type browserKeyMap struct {
	tui.CommonKeymap
	Select key.Binding // enter — select for run
}

var defaultBrowserKeyMap = browserKeyMap{
	CommonKeymap: tui.DefaultCommonKeymap,
	Select: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "select"),
	),
}

// ──────────────────────────────────────────────
// List items
// ──────────────────────────────────────────────

type scriptItem struct {
	script  rollbackdata.Script
	opCount int
	active  bool // whether this script is selected for run
}

func (s scriptItem) FilterValue() string { return s.script.ID }
func (s scriptItem) Title() string {
	marker := " "
	if s.active {
		marker = ">"
	}
	if s.opCount > 0 {
		return fmt.Sprintf("%s %s  (%d ops)", marker, s.script.ID, s.opCount)
	}
	return fmt.Sprintf("%s %s", marker, s.script.ID)
}
func (s scriptItem) Description() string { return s.script.Path }

// ──────────────────────────────────────────────
// Model
// ──────────────────────────────────────────────

// RollbackBrowser is the exported Bubble Tea model.
type RollbackBrowser struct {
	*rollbackBrowserModel
}

type rollbackBrowserModel struct {
	keymap   browserKeyMap
	scripts  []rollbackdata.Script // newest first
	items    []scriptItem          // list items mirroring scripts
	list     list.Model
	viewport viewport.Model

	// Cached previews: script ID -> body
	previews map[string]string

	// Confirmation
	confirm    components.ConfirmBar
	confirming bool
	confirmed  bool

	// Outcome
	done      bool
	cancelled bool
	runTarget rollbackdata.Script
	err       error

	width  int
	height int
}

// NewRollbackBrowser creates a rollback browser TUI.
func NewRollbackBrowser() (*RollbackBrowser, error) {
	scripts, err := rollbackdata.List()
	if err != nil {
		return nil, err
	}
	// Reverse to newest first
	reversed := make([]rollbackdata.Script, len(scripts))
	for i, s := range scripts {
		reversed[len(scripts)-1-i] = s
	}
	scripts = reversed

	// Build list items with lazy-loaded op counts
	items := make([]list.Item, len(scripts))
	scriptItems := make([]scriptItem, len(scripts))
	for i, s := range scripts {
		item := scriptItem{script: s}
		scriptItems[i] = item
		items[i] = item
	}

	l := list.New(items, list.NewDefaultDelegate(), 30, 20)
	l.SetShowTitle(false)
	l.SetShowStatusBar(true)
	l.SetShowHelp(false)
	l.SetShowFilter(false)
	l.SetShowPagination(true)
	l.Title = "Rollback Scripts"
	l.SetStatusBarItemName("script", "scripts")

	vp := viewport.New(50, 20)
	vp.SetContent("(select a script to preview)")

	m := &rollbackBrowserModel{
		keymap:   defaultBrowserKeyMap,
		scripts:  scripts,
		items:    scriptItems,
		list:     l,
		viewport: vp,
		previews: make(map[string]string),
		confirm:  components.NewConfirmBar("execute this rollback script?", false),
	}

	// Load preview for first script
	if len(scripts) > 0 {
		body, err := rollbackdata.Read(scripts[0])
		if err == nil {
			m.previews[scripts[0].ID] = body
			m.viewport.SetContent(body)
		}
	}

	return &RollbackBrowser{rollbackBrowserModel: m}, nil
}

// Exported accessors.

// RunTarget returns the script the user selected for execution.
func (m *rollbackBrowserModel) RunTarget() rollbackdata.Script { return m.runTarget }

// Cancelled returns true if the user quit without selecting a script.
func (m *rollbackBrowserModel) Cancelled() bool { return m.cancelled }

// Confirmed returns true if the user confirmed execution.
func (m *rollbackBrowserModel) Confirmed() bool { return m.confirmed }

// Err returns any error encountered during the session.
func (m *rollbackBrowserModel) Err() error { return m.err }

// ──────────────────────────────────────────────
// tea.Model interface
// ──────────────────────────────────────────────

func (m *rollbackBrowserModel) Init() tea.Cmd {
	return nil
}

func (m *rollbackBrowserModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.done {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizePanes()
		return m, nil

	case tea.KeyMsg:
		// Always available: quit
		if key.Matches(msg, m.keymap.Quit) {
			m.done = true
			m.cancelled = true
			return m, tea.Quit
		}

		// Confirmation mode
		if m.confirming {
			// Esc always cancels confirmation (before delegating to ConfirmBar)
			if key.Matches(msg, m.keymap.Back) {
				m.confirming = false
				m.confirm.Reset()
				return m, nil
			}
			result := m.confirm.Update(msg)
			switch result {
			case components.ConfirmYes:
				m.done = true
				m.confirmed = true
				return m, tea.Quit
			case components.ConfirmNo, components.ConfirmQuit:
				m.confirming = false
				m.confirm.Reset()
				return m, nil
			}
			return m, nil
		}

		// Normal mode
		switch {
		case key.Matches(msg, m.keymap.Select):
			// Toggle active script
			idx := m.list.Index()
			if idx >= 0 && idx < len(m.scripts) {
				// Deactivate all, activate selected
				for i := range m.items {
					m.items[i].active = false
				}
				m.items[idx].active = true
				m.runTarget = m.scripts[idx]
				m.confirming = true
				m.confirm.Reset()
				m.rebuildList()
			}
			return m, nil

		case key.Matches(msg, m.keymap.Back):
			// Esc in normal mode — no-op (confirmation Esc handled above)
			return m, nil

		default:
			// Delegate to list (up/down navigation)
			cmd := m.updateList(msg)
			// When selection changes, load preview
			m.loadPreview(m.list.Index())
			return m, cmd
		}
	}

	// Non-key messages -> delegate to list
	cmd := m.updateList(msg)
	return m, cmd
}

func (m *rollbackBrowserModel) View() string {
	if m.done {
		return ""
	}

	if len(m.scripts) == 0 {
		return tui.Theme.Dim.Render("No rollback scripts found.")
	}

	listView := m.list.View()
	viewportView := m.viewport.View()

	listWidth, rightWidth := m.paneWidths()

	header := tui.Theme.Title.Render("Rollback Browser") + "\n"

	content := lipgloss.JoinHorizontal(
		lipgloss.Top,
		tui.Theme.Border.Width(listWidth).Render(listView),
		tui.Theme.Border.Width(rightWidth).Render(viewportView),
	)

	var footer string
	if m.confirming {
		footer = "\n" + m.confirm.View()
	} else {
		footer = "\n" + components.NewHelpBar(
			components.HelpBinding{Key: "↑↓", Desc: "navigate"},
			components.HelpBinding{Key: "enter", Desc: "select"},
			components.HelpBinding{Key: "q", Desc: "quit"},
		).View()
	}

	return header + content + footer
}

// ──────────────────────────────────────────────
// Internal helpers
// ──────────────────────────────────────────────

func (m *rollbackBrowserModel) updateList(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return cmd
}

func (m *rollbackBrowserModel) loadPreview(idx int) {
	if idx < 0 || idx >= len(m.scripts) {
		return
	}
	s := m.scripts[idx]
	body, ok := m.previews[s.ID]
	if !ok {
		var err error
		body, err = rollbackdata.Read(s)
		if err != nil {
			m.err = err
			body = tui.Theme.Error.Render("error reading script: " + err.Error())
			m.previews[s.ID] = body
			m.viewport.SetContent(body)
			m.viewport.GotoTop()
			return
		}
		m.previews[s.ID] = body
		// Also compute op count and update item
		opCount := rollbackdata.OpCount(body)
		m.items[idx].opCount = opCount
		m.rebuildList()
	}
	m.viewport.SetContent(body)
	m.viewport.GotoTop()
}

func (m *rollbackBrowserModel) rebuildList() {
	listItems := make([]list.Item, len(m.items))
	for i, item := range m.items {
		listItems[i] = item
	}
	m.list.SetItems(listItems)
}

// paneWidths returns the left (list) and right (preview) pane widths for the
// current total width. The result is shared by View() (read-only) and
// resizePanes() (which applies the sizes).
func (m *rollbackBrowserModel) paneWidths() (int, int) {
	listWidth := m.width * 40 / 100
	rightWidth := m.width - listWidth - 4
	return listWidth, rightWidth
}

func (m *rollbackBrowserModel) resizePanes() {
	listWidth, rightWidth := m.paneWidths()
	m.list.SetWidth(listWidth)
	m.list.SetHeight(m.height - 6)
	m.viewport.Width = rightWidth
	m.viewport.Height = m.height - 6
}
