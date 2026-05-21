// Package apply provides a Bubble Tea split-pane apply-plan review screen.
// The left pane lists plugins; the right pane shows the add/remove details
// for the selected plugin. The user confirms (y) or cancels (q/ctrl+c).
//
// In selective mode (--select flag), the right pane becomes an interactive
// operation list with checkboxes. Space toggles a single operation, A toggles
// all, Tab switches focus between panes, and Enter proceeds with the checked
// subset. In non-selective mode, the right pane is a read-only viewport with
// a y/n confirmation prompt (Phase 4 behavior).
package apply

import (
	"fmt"
	"io"
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
// Types
// ──────────────────────────────────────────────

// PluginPlan holds the plan result for one plugin in the apply review.
type PluginPlan struct {
	Name     string
	InSync   bool                 // true when the plugin has no changes
	Report   plugin.Report        // drift operations (add/remove)
	Upgrades plugin.UpgradeReport // pending upgrades (OpUpdate only)
}

// checkedOp is a single operation with a checkbox state (Phase 5 selective).
type checkedOp struct {
	op      plugin.Operation
	checked bool
}

func (c checkedOp) FilterValue() string { return c.op.Target }
func (c checkedOp) Title() string {
	check := "[ ]"
	if c.checked {
		check = tui.Theme.Add.Render("[x]")
	}
	prefix := "+"
	if c.op.Kind == plugin.OpRemove {
		prefix = "-"
	} else if c.op.Kind == plugin.OpUpdate {
		prefix = "↑"
	}
	label := c.op.Target
	if c.op.Detail != "" {
		label = fmt.Sprintf("%s (%s)", c.op.Target, c.op.Detail)
	}
	return fmt.Sprintf("%s %s %s", check, prefix, label)
}
func (c checkedOp) Description() string { return "" }

// ──────────────────────────────────────────────
// Key bindings
// ──────────────────────────────────────────────

type applyReviewKeyMap struct {
	tui.CommonKeymap
	Confirm   key.Binding // y — confirm (Phase 4 read-only)
	Toggle    key.Binding // space — toggle current op (Phase 5)
	ToggleAll key.Binding // A — toggle all ops in current plugin (Phase 5)
	Proceed   key.Binding // enter — proceed with checked subset (Phase 5)
	TabFocus  key.Binding // tab — switch focus between panes (Phase 5)
}

var defaultApplyReviewKeyMap = applyReviewKeyMap{
	CommonKeymap: tui.DefaultCommonKeymap,
	Confirm: key.NewBinding(
		key.WithKeys("y"),
		key.WithHelp("y", "confirm"),
	),
	Toggle: key.NewBinding(
		key.WithKeys(" "),
		key.WithHelp("space", "toggle"),
	),
	ToggleAll: key.NewBinding(
		key.WithKeys("A"),
		key.WithHelp("A", "all"),
	),
	Proceed: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "proceed"),
	),
	TabFocus: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "switch"),
	),
}

// ──────────────────────────────────────────────
// List items
// ──────────────────────────────────────────────

type pluginPlanItem struct {
	plan PluginPlan
}

func (p pluginPlanItem) FilterValue() string { return p.plan.Name }
func (p pluginPlanItem) Title() string {
	if p.plan.InSync && !p.plan.Upgrades.HasUpgrades() {
		return fmt.Sprintf("%-12s %s", p.plan.Name, tui.Theme.Add.Render("in sync"))
	}
	var parts []string
	if !p.plan.InSync {
		parts = append(parts, fmt.Sprintf("%d changes", len(p.plan.Report.Operations)))
	}
	if p.plan.Upgrades.HasUpgrades() {
		parts = append(parts, fmt.Sprintf("%d upgrades", len(p.plan.Upgrades.Operations)))
	}
	return fmt.Sprintf("%-12s %s", p.plan.Name, tui.Theme.Warn.Render(strings.Join(parts, " · ")))
}
func (p pluginPlanItem) Description() string { return "" }

// ──────────────────────────────────────────────
// Checkbox delegate (Phase 5)
// ──────────────────────────────────────────────

type checkboxDelegate struct {
	selectedStyle   lipgloss.Style
	unselectedStyle lipgloss.Style
	checkedStyle    lipgloss.Style
	addStyle        lipgloss.Style
	removeStyle     lipgloss.Style
	upgradeStyle    lipgloss.Style
}

func (d checkboxDelegate) Height() int                             { return 1 }
func (d checkboxDelegate) Spacing() int                            { return 0 }
func (d checkboxDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d checkboxDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	co, ok := item.(checkedOp)
	if !ok {
		return
	}

	check := "[ ]"
	if co.checked {
		check = d.checkedStyle.Render("[x]")
	}

	prefix := d.addStyle.Render("+")
	if co.op.Kind == plugin.OpRemove {
		prefix = d.removeStyle.Render("-")
	} else if co.op.Kind == plugin.OpUpdate {
		prefix = d.upgradeStyle.Render("↑")
	}

	label := co.op.Target
	if co.op.Detail != "" {
		label = fmt.Sprintf("%s (%s)", co.op.Target, co.op.Detail)
	}

	cursor := "  "
	if index == m.Index() {
		cursor = d.selectedStyle.Render("> ")
	}

	fmt.Fprintf(w, "%s%s %s %s", cursor, check, prefix, label)
}

// ──────────────────────────────────────────────
// Model
// ──────────────────────────────────────────────

// ApplyReview is the exported Bubble Tea model.
type ApplyReview struct {
	*applyReviewModel
}

type applyReviewModel struct {
	keymap      applyReviewKeyMap
	plans       []PluginPlan
	list        list.Model
	viewport    viewport.Model
	configPath  string
	dryRun      bool
	willUpgrade bool

	done      bool
	cancelled bool
	confirmed bool

	confirm components.ConfirmBar
	width   int
	height  int

	// Phase 5: selective apply
	selective     bool                    // true when --select is passed
	opItems       []checkedOp             // current plugin's operations with checkboxes
	opList        list.Model              // interactive operation list (replaces viewport when selective)
	opDelegate    checkboxDelegate        // custom delegate for opList
	checkedOps    map[int]map[string]bool // per-plugin checked state by "kind:target"
	focusRight    bool                    // false = left pane (plugin list), true = right pane (op list)
	filteredPlans []PluginPlan            // filtered plans with only checked ops
}

// ──────────────────────────────────────────────
// Constructor
// ──────────────────────────────────────────────

// NewApplyReview creates an apply-plan review TUI with the given config path,
// plugin plans, dry-run flag, whether upgrades will be run, and whether
// selective mode (interactive checkboxes) should be enabled.
//
// When selective is false, the right pane is a read-only viewport with a
// y/n confirm prompt (Phase 4 behavior). When selective is true, the right
// pane is an interactive operation list with checkboxes.
func NewApplyReview(configPath string, plans []PluginPlan, dryRun bool, willUpgrade bool, selective bool) *ApplyReview {
	items := make([]list.Item, len(plans))
	for i, p := range plans {
		items[i] = pluginPlanItem{plan: p}
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

	m := &applyReviewModel{
		keymap:      defaultApplyReviewKeyMap,
		plans:       plans,
		list:        l,
		viewport:    vp,
		configPath:  configPath,
		dryRun:      dryRun,
		willUpgrade: willUpgrade,
		confirm:     components.NewConfirmBar("proceed to apply changes?", false),
		selective:   selective,
	}

	if selective {
		m.checkedOps = make(map[int]map[string]bool)
		m.opDelegate = checkboxDelegate{
			selectedStyle:   tui.Theme.Selected,
			unselectedStyle: lipgloss.NewStyle(),
			checkedStyle:    tui.Theme.Add,
			addStyle:        tui.Theme.Add,
			removeStyle:     tui.Theme.Remove,
			upgradeStyle:    tui.Theme.Warn,
		}

		m.opList = list.New([]list.Item{}, m.opDelegate, 50, 20)
		m.opList.SetShowTitle(false)
		m.opList.SetShowStatusBar(false)
		m.opList.SetShowHelp(false)
		m.opList.SetShowFilter(false)
		m.opList.SetShowPagination(false)

		// Build from first plugin
		if len(plans) > 0 {
			m.rebuildOpItemsForPlugin(0)
		}
	}

	// Load first plugin's details
	if len(plans) > 0 {
		m.updateViewport(0)
	}

	return &ApplyReview{applyReviewModel: m}
}

// ──────────────────────────────────────────────
// Accessors
// ──────────────────────────────────────────────

// Cancelled returns true if the user quit without confirming.
func (m *applyReviewModel) Cancelled() bool { return m.cancelled }

// Confirmed returns true if the user confirmed (y or enter with selection).
func (m *applyReviewModel) Confirmed() bool { return m.confirmed }

// FilteredPlans returns the filtered subset of plans with only checked operations.
// Valid only after Confirmed() is true in selective mode.
func (m *applyReviewModel) FilteredPlans() []PluginPlan { return m.filteredPlans }

// ──────────────────────────────────────────────
// tea.Model interface
// ──────────────────────────────────────────────

func (m *applyReviewModel) Init() tea.Cmd { return nil }

func (m *applyReviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		// Quit — always available
		if key.Matches(msg, m.keymap.Quit) {
			m.done = true
			m.cancelled = true
			return m, tea.Quit
		}

		if m.selective {
			return m.updateSelective(msg)
		}

		// Non-selective (Phase 4) path
		if key.Matches(msg, m.keymap.Confirm) {
			m.done = true
			m.confirmed = true
			return m, tea.Quit
		}

		// Delegate navigation to list
		cmd := m.updateList(msg)
		m.updateViewport(m.list.Index())
		return m, cmd

	default:
		cmd := m.updateList(msg)
		return m, cmd
	}
}

// updateSelective handles key messages in selective mode.
func (m *applyReviewModel) updateSelective(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Tab switches focus
	if key.Matches(msg, m.keymap.TabFocus) {
		m.focusRight = !m.focusRight
		return m, nil
	}

	if m.focusRight {
		// ── Right pane (op list) focused ──
		switch {
		case key.Matches(msg, m.keymap.Toggle):
			// Space: toggle current op
			idx := m.opList.Index()
			if idx < 0 || idx >= len(m.opItems) {
				return m, nil
			}
			m.opItems[idx].checked = !m.opItems[idx].checked
			// Persist to checkedOps
			pluginIdx := m.list.Index()
			op := m.opItems[idx].op
			key := fmt.Sprintf("%d:%s", op.Kind, op.Target)
			if m.checkedOps[pluginIdx] == nil {
				m.checkedOps[pluginIdx] = make(map[string]bool)
			}
			m.checkedOps[pluginIdx][key] = m.opItems[idx].checked
			m.refreshOpList()
			return m, nil

		case key.Matches(msg, m.keymap.ToggleAll):
			// A: toggle all ops in current plugin
			allChecked := m.allOpsChecked()
			pluginIdx := m.list.Index()
			for i := range m.opItems {
				m.opItems[i].checked = !allChecked
			}
			if m.checkedOps[pluginIdx] == nil {
				m.checkedOps[pluginIdx] = make(map[string]bool)
			}
			for _, item := range m.opItems {
				key := fmt.Sprintf("%d:%s", item.op.Kind, item.op.Target)
				m.checkedOps[pluginIdx][key] = item.checked
			}
			m.refreshOpList()
			return m, nil

		case key.Matches(msg, m.keymap.Proceed):
			// Enter: proceed with checked subset
			if !m.hasAnyChecked() {
				// Nothing checked — silently ignore Enter
				return m, nil
			}
			m.buildFilteredPlans()
			m.done = true
			m.confirmed = true
			return m, tea.Quit

		default:
			// Delegate navigation to op list
			var cmd tea.Cmd
			m.opList, cmd = m.opList.Update(msg)
			return m, cmd
		}
	}

	// ── Left pane (plugin list) focused ──
	// Ignore right-pane-only keys
	if key.Matches(msg, m.keymap.Toggle) || key.Matches(msg, m.keymap.ToggleAll) || key.Matches(msg, m.keymap.Proceed) {
		return m, nil
	}
	oldIdx := m.list.Index()
	cmd := m.updateList(msg)
	newIdx := m.list.Index()
	if oldIdx != newIdx {
		m.rebuildOpItemsForPlugin(newIdx)
		m.updateViewport(newIdx)
	}
	return m, cmd
}

func (m *applyReviewModel) View() string {
	if m.done {
		return ""
	}

	// Header
	header := m.headerView()

	// Split layout: left 30%, right 70%
	listWidth := m.width * 30 / 100
	rightWidth := m.width - listWidth - 4

	listView := m.list.View()

	var rightView string
	if m.selective {
		rightView = m.opList.View()
	} else {
		rightView = m.viewport.View()
	}

	content := lipgloss.JoinHorizontal(
		lipgloss.Top,
		tui.Theme.Border.Width(listWidth).Render(listView),
		tui.Theme.Border.Width(rightWidth).Render(rightView),
	)

	// Footer
	var footer string
	if m.selective {
		footer = components.NewHelpBar(
			components.HelpBinding{Key: "space", Desc: "toggle"},
			components.HelpBinding{Key: "A", Desc: "all"},
			components.HelpBinding{Key: "tab", Desc: "switch"},
			components.HelpBinding{Key: "enter", Desc: "proceed"},
			components.HelpBinding{Key: "q", Desc: "abort"},
		).View()
	} else {
		footer = m.confirm.View()
		if footer == "" {
			footer = components.NewHelpBar(
				components.HelpBinding{Key: "y", Desc: "confirm"},
				components.HelpBinding{Key: "q", Desc: "abort"},
				components.HelpBinding{Key: "↑↓", Desc: "navigate"},
			).View()
		}
	}

	return header + "\n" + content + "\n\n" + footer
}

// ──────────────────────────────────────────────
// Internal helpers
// ──────────────────────────────────────────────

func (m *applyReviewModel) headerView() string {
	// Count totals
	var totalChanges int
	changedPlugins := 0
	var totalUpgrades int
	for _, p := range m.plans {
		if !p.InSync {
			changedPlugins++
			totalChanges += len(p.Report.Operations)
		}
		totalUpgrades += len(p.Upgrades.Operations)
	}

	title := tui.Theme.Title.Render("Apply Review")
	configInfo := tui.Theme.Dim.Render(fmt.Sprintf("config: %s", m.configPath))

	var summary string
	if m.dryRun {
		summary = tui.Theme.Dim.Render("(dry-run — no changes will be applied)")
	} else {
		var parts []string
		if m.willUpgrade {
			parts = append(parts, "will upgrade")
		}
		if totalUpgrades > 0 {
			parts = append(parts, fmt.Sprintf("%d upgrades available", totalUpgrades))
		}
		if totalChanges > 0 {
			parts = append(parts, fmt.Sprintf("%d changes across %d plugins", totalChanges, changedPlugins))
		}
		if len(parts) > 0 {
			summary = tui.Theme.Warn.Render(strings.Join(parts, " · "))
		} else {
			summary = tui.Theme.Add.Render("system matches declaration")
		}
	}

	return fmt.Sprintf("%s  %s\n%s", title, configInfo, summary)
}

func (m *applyReviewModel) updateViewport(idx int) {
	if idx < 0 || idx >= len(m.plans) {
		return
	}

	p := m.plans[idx]
	var b strings.Builder

	// Plugin name header
	b.WriteString(tui.Theme.Title.Render(p.Name))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 40))
	b.WriteString("\n\n")

	if p.InSync && !p.Upgrades.HasUpgrades() {
		b.WriteString(tui.Theme.Add.Render("in sync — no changes"))
		m.viewport.SetContent(b.String())
		m.viewport.GotoTop()
		return
	}

	// Group by kind: adds first, then removes, then upgrades
	var adds, removes, upgrades []plugin.Operation
	for _, op := range p.Report.Operations {
		if op.Kind == plugin.OpAdd {
			adds = append(adds, op)
		} else {
			removes = append(removes, op)
		}
	}
	for _, op := range p.Upgrades.Operations {
		upgrades = append(upgrades, op)
	}

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

func (m *applyReviewModel) updateList(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return cmd
}

func (m *applyReviewModel) resizePanes() {
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
	if m.selective {
		m.opList.SetWidth(rightWidth)
		m.opList.SetHeight(listHeight)
	} else {
		m.viewport.Width = rightWidth
		m.viewport.Height = listHeight
	}
}

// ──────────────────────────────────────────────
// Selective mode helpers (Phase 5)
// ──────────────────────────────────────────────

// rebuildOpItemsForPlugin populates m.opItems from the given plugin index
// and refreshes the opList. Checked state is restored from m.checkedOps.
func (m *applyReviewModel) rebuildOpItemsForPlugin(pluginIdx int) {
	if pluginIdx < 0 || pluginIdx >= len(m.plans) {
		return
	}
	p := m.plans[pluginIdx]
	if p.InSync && !p.Upgrades.HasUpgrades() {
		m.opItems = nil
		m.opList.SetItems([]list.Item{})
		return
	}

	// Combine drift ops and upgrade ops
	var allOps []plugin.Operation
	allOps = append(allOps, p.Report.Operations...)
	allOps = append(allOps, p.Upgrades.Operations...)

	m.opItems = make([]checkedOp, len(allOps))
	for i, op := range allOps {
		key := fmt.Sprintf("%d:%s", op.Kind, op.Target)
		checked := true // default: checked
		if m.checkedOps[pluginIdx] != nil {
			if val, ok := m.checkedOps[pluginIdx][key]; ok {
				checked = val
			}
		}
		m.opItems[i] = checkedOp{op: op, checked: checked}
	}
	m.refreshOpList()
}

// refreshOpList syncs m.opItems into the list.Model.
func (m *applyReviewModel) refreshOpList() {
	items := make([]list.Item, len(m.opItems))
	for i, co := range m.opItems {
		items[i] = co
	}
	m.opList.SetItems(items)
}

// allOpsChecked returns true when every op in m.opItems is checked.
func (m *applyReviewModel) allOpsChecked() bool {
	for _, item := range m.opItems {
		if !item.checked {
			return false
		}
	}
	return true
}

// hasAnyChecked returns true when at least one operation is checked across all plugins.
// It mirrors the default-checked semantics of buildFilteredPlans: an operation is
// considered checked if it is not yet present in checkedOps (default) or is explicitly true.
func (m *applyReviewModel) hasAnyChecked() bool {
	for pi, p := range m.plans {
		if p.InSync && !p.Upgrades.HasUpgrades() {
			continue
		}
		// Check drift ops
		for _, op := range p.Report.Operations {
			key := fmt.Sprintf("%d:%s", op.Kind, op.Target)
			if m.checkedOps[pi] != nil {
				if checked, ok := m.checkedOps[pi][key]; ok {
					if checked {
						return true
					}
					continue
				}
			}
			// Not stored in map yet — default to checked
			return true
		}
		// Check upgrade ops
		for _, op := range p.Upgrades.Operations {
			key := fmt.Sprintf("%d:%s", op.Kind, op.Target)
			if m.checkedOps[pi] != nil {
				if checked, ok := m.checkedOps[pi][key]; ok {
					if checked {
						return true
					}
					continue
				}
			}
			return true
		}
	}
	return false
}

// buildFilteredPlans constructs m.filteredPlans from m.plans using the
// per-operation checked state stored in m.checkedOps. In-sync plugins
// are passed through unchanged. Out-of-sync plugins only include their
// checked drift operations. Upgrade operations are informational only and
// are not included in the filtered Report (they don't flow into Apply).
func (m *applyReviewModel) buildFilteredPlans() {
	m.filteredPlans = make([]PluginPlan, 0, len(m.plans))
	for pi, p := range m.plans {
		if p.InSync {
			m.filteredPlans = append(m.filteredPlans, p)
			continue
		}

		var checkedOps []plugin.Operation
		for _, op := range p.Report.Operations {
			key := fmt.Sprintf("%d:%s", op.Kind, op.Target)
			if m.checkedOps[pi] != nil {
				if checked, ok := m.checkedOps[pi][key]; ok {
					if checked {
						checkedOps = append(checkedOps, op)
					}
					continue
				}
			}
			// Not stored in map yet — default to checked
			checkedOps = append(checkedOps, op)
		}

		fp := PluginPlan{
			Name:     p.Name,
			InSync:   len(checkedOps) == 0,
			Report:   plugin.Report{Operations: checkedOps},
			Upgrades: p.Upgrades,
		}
		m.filteredPlans = append(m.filteredPlans, fp)
	}
}
