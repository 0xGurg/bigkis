// Package status provides a Bubble Tea split-pane dashboard for
// inspecting the drift between the declared config and the live system.
package status

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"codeberg.org/gurg/bigkis/internal/plugin"
	"codeberg.org/gurg/bigkis/internal/tui"
	"codeberg.org/gurg/bigkis/internal/tui/components"
)

// ──────────────────────────────────────────────
// PluginStatus
// ──────────────────────────────────────────────

// PluginStatus holds the plan result for one plugin.
type PluginStatus struct {
	Name      string
	Available bool
	Error     string               // short error message when unavailable
	Report    plugin.Report        // drift operations (add/remove)
	Upgrades  plugin.UpgradeReport // pending upgrades (OpUpdate only)
}

// HasChanges returns true when the plugin is available and its report
// contains at least one pending operation.
func (ps PluginStatus) HasChanges() bool { return ps.Available && ps.Report.HasChanges() }

// HasUpgrades returns true when the plugin has pending upgrades.
func (ps PluginStatus) HasUpgrades() bool { return ps.Available && ps.Upgrades.HasUpgrades() }

// ──────────────────────────────────────────────
// Key bindings
// ──────────────────────────────────────────────

type statusKeyMap struct {
	tui.CommonKeymap
	Apply key.Binding // a — suggest apply, exit
}

var defaultStatusKeyMap = statusKeyMap{
	CommonKeymap: tui.DefaultCommonKeymap,
	Apply: key.NewBinding(
		key.WithKeys("a"),
		key.WithHelp("a", "suggest apply"),
	),
}

// ──────────────────────────────────────────────
// List items
// ──────────────────────────────────────────────

type pluginItem struct {
	status PluginStatus
}

func (p pluginItem) FilterValue() string { return p.status.Name }
func (p pluginItem) Title() string {
	// Right-aligned name then badge
	switch {
	case !p.status.Available:
		return fmt.Sprintf("%-12s %s", p.status.Name, tui.Theme.Error.Render("unavailable"))
	case !p.status.HasChanges() && !p.status.HasUpgrades():
		return fmt.Sprintf("%-12s %s", p.status.Name, tui.Theme.Add.Render("in sync"))
	default:
		var parts []string
		if p.status.HasChanges() {
			parts = append(parts, fmt.Sprintf("%d changes", len(p.status.Report.Operations)))
		}
		if p.status.HasUpgrades() {
			parts = append(parts, fmt.Sprintf("%d upgrades", len(p.status.Upgrades.Operations)))
		}
		return fmt.Sprintf("%-12s %s", p.status.Name, tui.Theme.Warn.Render(strings.Join(parts, " · ")))
	}
}
func (p pluginItem) Description() string {
	if !p.status.Available {
		return p.status.Error
	}
	return ""
}

// ──────────────────────────────────────────────
// Model
// ──────────────────────────────────────────────

// StatusDashboard is the exported Bubble Tea model.
type StatusDashboard struct {
	*statusDashboardModel
}

type statusDashboardModel struct {
	keymap     statusKeyMap
	plugins    []PluginStatus
	list       list.Model
	viewport   viewport.Model
	configPath string

	done           bool
	cancelled      bool
	applyRequested bool

	width  int
	height int
}

// NewStatusDashboard creates a fully populated StatusDashboard model.
func NewStatusDashboard(configPath string, statuses []PluginStatus) *StatusDashboard {
	items := make([]list.Item, len(statuses))
	for i, s := range statuses {
		items[i] = pluginItem{status: s}
	}
	l := list.New(items, list.NewDefaultDelegate(), 30, 20)
	l.SetShowTitle(false)
	l.SetShowStatusBar(true)
	l.SetShowHelp(false)
	l.SetShowFilter(false)
	l.SetShowPagination(true)
	l.Title = "Plugins"
	l.SetStatusBarItemName("plugin", "plugins")

	vp := viewport.New(50, 20)

	m := &statusDashboardModel{
		keymap:     defaultStatusKeyMap,
		plugins:    statuses,
		list:       l,
		viewport:   vp,
		configPath: configPath,
	}

	// Load first plugin's details
	if len(statuses) > 0 {
		m.updateViewport(0)
	}

	return &StatusDashboard{statusDashboardModel: m}
}

// ──────────────────────────────────────────────
// Exported accessors
// ──────────────────────────────────────────────

// Cancelled returns true if the user quit without requesting apply.
func (m *statusDashboardModel) Cancelled() bool { return m.cancelled }

// ApplyRequested returns true if the user pressed 'a' to request apply.
func (m *statusDashboardModel) ApplyRequested() bool { return m.applyRequested }

// ──────────────────────────────────────────────
// tea.Model interface
// ──────────────────────────────────────────────

func (m *statusDashboardModel) Init() tea.Cmd { return nil }

func (m *statusDashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		if key.Matches(msg, m.keymap.Quit) {
			m.done = true
			m.cancelled = true
			return m, tea.Quit
		}
		if key.Matches(msg, m.keymap.Apply) {
			m.done = true
			m.applyRequested = true
			return m, tea.Quit
		}
		// Delegate to list (up/down navigation)
		cmd := m.updateList(msg)
		m.updateViewport(m.list.Index())
		return m, cmd

	default:
		cmd := m.updateList(msg)
		return m, cmd
	}
}

func (m *statusDashboardModel) View() string {
	if m.done {
		return ""
	}

	// Status bar at top
	statusBar := m.statusBarView()

	// Split layout: left 30%, right 70%
	listWidth := m.width * 30 / 100
	rightWidth := m.width - listWidth - 4

	listView := m.list.View()
	viewportView := m.viewport.View()

	content := lipgloss.JoinHorizontal(
		lipgloss.Top,
		tui.Theme.Border.Width(listWidth).Render(listView),
		tui.Theme.Border.Width(rightWidth).Render(viewportView),
	)

	// Footer with key hints
	footer := components.NewHelpBar(
		components.HelpBinding{Key: "↑↓", Desc: "navigate"},
		components.HelpBinding{Key: "/", Desc: "filter"},
		components.HelpBinding{Key: "a", Desc: "suggest apply"},
		components.HelpBinding{Key: "q", Desc: "quit"},
	).View()

	return statusBar + "\n" + content + "\n" + footer
}

// ──────────────────────────────────────────────
// Status bar
// ──────────────────────────────────────────────

func (m *statusDashboardModel) statusBarView() string {
	var changes int
	changedPlugins := 0
	var totalUpgrades int
	for _, ps := range m.plugins {
		if ps.HasChanges() {
			changedPlugins++
			changes += len(ps.Report.Operations)
		}
		totalUpgrades += len(ps.Upgrades.Operations)
	}

	configPart := fmt.Sprintf("config: %s", m.configPath)
	driftPart := fmt.Sprintf("drift: %d changes across %d plugins", changes, changedPlugins)
	if changes == 0 {
		driftPart = "drift: in sync"
	}

	var parts []string
	parts = append(parts, tui.Theme.Title.Render("Status Dashboard"))
	parts = append(parts, tui.Theme.Dim.Render(configPart))
	parts = append(parts, tui.Theme.Dim.Render(driftPart))
	if totalUpgrades > 0 {
		parts = append(parts, tui.Theme.Warn.Render(fmt.Sprintf("%d upgrades available", totalUpgrades)))
	}

	return strings.Join(parts, "  ")
}

// ──────────────────────────────────────────────
// Viewport content builder
// ──────────────────────────────────────────────

func (m *statusDashboardModel) updateViewport(idx int) {
	if idx < 0 || idx >= len(m.plugins) {
		return
	}

	ps := m.plugins[idx]
	var b strings.Builder

	// Header
	b.WriteString(tui.Theme.Title.Render(ps.Name))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 40))
	b.WriteString("\n\n")

	if !ps.Available {
		b.WriteString(tui.Theme.Error.Render("unavailable: " + ps.Error))
		m.viewport.SetContent(b.String())
		m.viewport.GotoTop()
		return
	}

	if !ps.HasChanges() && !ps.HasUpgrades() {
		b.WriteString(tui.Theme.Add.Render("in sync"))
		m.viewport.SetContent(b.String())
		m.viewport.GotoTop()
		return
	}

	// Group by kind: adds first, then removes, then upgrades
	var adds, removes, upgrades []plugin.Operation
	for _, op := range ps.Report.Operations {
		if op.Kind == plugin.OpAdd {
			adds = append(adds, op)
		} else {
			removes = append(removes, op)
		}
	}
	upgrades = append(upgrades, ps.Upgrades.Operations...)

	if len(adds) > 0 {
		b.WriteString(tui.Theme.Add.Render("+ adds"))
		b.WriteString("\n")
		for _, op := range adds {
			label := op.Target
			if op.Detail != "" {
				label = fmt.Sprintf("%s (%s)", op.Target, op.Detail)
			}
			b.WriteString("  ")
			b.WriteString(label)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(removes) > 0 {
		b.WriteString(tui.Theme.Remove.Render("- removes"))
		b.WriteString("\n")
		for _, op := range removes {
			label := op.Target
			if op.Detail != "" {
				label = fmt.Sprintf("%s (%s)", op.Target, op.Detail)
			}
			b.WriteString("  ")
			b.WriteString(label)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(upgrades) > 0 {
		b.WriteString(tui.Theme.Warn.Render("↑ upgrades"))
		b.WriteString("\n")
		for _, op := range upgrades {
			label := op.Target
			if op.Detail != "" {
				label = fmt.Sprintf("%s  %s", op.Target, op.Detail)
			}
			b.WriteString("  ")
			b.WriteString(label)
			b.WriteString("\n")
		}
	}

	m.viewport.SetContent(b.String())
	m.viewport.GotoTop()
}

// ──────────────────────────────────────────────
// Internal helpers
// ──────────────────────────────────────────────

func (m *statusDashboardModel) updateList(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return cmd
}

func (m *statusDashboardModel) resizePanes() {
	listWidth := m.width * 30 / 100
	if listWidth < 10 {
		listWidth = 10
	}
	rightWidth := m.width - listWidth - 4
	if rightWidth < 10 {
		rightWidth = 10
	}
	listHeight := m.height - 6
	if listHeight < 5 {
		listHeight = 5
	}
	m.list.SetWidth(listWidth)
	m.list.SetHeight(listHeight)
	m.viewport.Width = rightWidth
	m.viewport.Height = listHeight
}
