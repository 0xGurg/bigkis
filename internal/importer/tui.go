package importer

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/0xGurg/bigkis/internal/tui"
	"github.com/0xGurg/bigkis/internal/tui/components"
)

// ──────────────────────────────────────────────
// Key bindings
// ──────────────────────────────────────────────

// pickKeyMap holds key bindings for the import picker screen.
type pickKeyMap struct {
	tui.CommonKeymap
	Space      key.Binding
	SelectAll  key.Binding
	SelectNone key.Binding
	Filter     key.Binding
	Tab        key.Binding
	Write      key.Binding
}

var defaultPickKeyMap = pickKeyMap{
	CommonKeymap: tui.DefaultCommonKeymap,
	Space: key.NewBinding(
		key.WithKeys(" "),
		key.WithHelp("space", "toggle"),
	),
	SelectAll: key.NewBinding(
		key.WithKeys("a"),
		key.WithHelp("a", "all"),
	),
	SelectNone: key.NewBinding(
		key.WithKeys("n"),
		key.WithHelp("n", "none"),
	),
	Filter: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "filter"),
	),
	Tab: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "next tab"),
	),
	Write: key.NewBinding(
		key.WithKeys("o"),
		key.WithHelp("o", "write & quit"),
	),
}

// ──────────────────────────────────────────────
// List items
// ──────────────────────────────────────────────

// pickerItem is a single package item in a checkbox list.
type pickerItem struct {
	name    string
	manager string // empty for non-node items; used for node key matching
	checked bool
}

func (i pickerItem) FilterValue() string { return i.name }
func (i pickerItem) Title() string       { return i.name }
func (i pickerItem) Description() string { return "" }
func (i pickerItem) IsChecked() bool     { return i.checked }
func (i pickerItem) Prefix() string      { return "" }
func (i pickerItem) PrefixStyle() lipgloss.Style { return lipgloss.Style{} }

// nodeKey returns the composite key for node package selection, mirroring
// how selectedNode is indexed in importPickerModel.
func (i pickerItem) nodeKey() string {
	if i.manager == "" {
		return i.name
	}
	return i.name + "\x00" + i.manager
}

// newImportDelegate returns a CheckboxDelegate configured for the import picker.
func newImportDelegate() components.CheckboxDelegate {
	return components.CheckboxDelegate{
		SelectedStyle:   tui.Theme.ActiveTab,
		UnselectedStyle: lipgloss.NewStyle(),
		CheckedStyle:    tui.Theme.Add,
	}
}

// ──────────────────────────────────────────────
// Model
// ──────────────────────────────────────────────

// ImportPicker is the exported Bubble Tea model for the interactive import
// picker. Call NewImportPicker to create one, then run it with tui.NewProgram.
type ImportPicker struct {
	*importPickerModel
}

// importPickerModel is the internal Bubble Tea model.
type importPickerModel struct {
	keymap pickKeyMap
	tabs   []string // ["pacman", "aur", "flatpak", "node"] or subset from --only
	active int      // current tab index

	// One list per tab
	pacmanList  list.Model
	aurList     list.Model
	flatpakList list.Model
	nodeList    list.Model

	// Original scanned packages per tab
	pacmanPkgs  []string
	aurPkgs     []string
	flatpakPkgs []string
	nodePkgs    []NodePackage

	// Selected state per package name
	selectedPacman  map[string]bool
	selectedAUR     map[string]bool
	selectedFlatpak map[string]bool
	selectedNode    map[string]bool // key is "name\x00manager"

	filterInput textinput.Model
	filtering   bool

	// scanErrs stores scan errors per tab name (e.g. "pacman").
	// Set by NewImportPicker; nil means no errors.
	scanErrs map[string]error

	err       error
	done      bool
	cancelled bool
	selection Selection
	opts      Options
	width     int
	height    int

	delegate components.CheckboxDelegate
}

// NewImportPicker creates a fully populated ImportPicker model.
// It scans the system for packages based on opts.Only.
func NewImportPicker(opts Options) *ImportPicker {
	// Determine tabs
	tabs := opts.Only
	if len(tabs) == 0 {
		tabs = []string{"pacman", "aur", "flatpak", "node"}
	}

	// Scan — store errors for display in the TUI
	scanErrs := make(map[string]error)
	pacmanPkgs, err := ScanPacman()
	if err != nil {
		scanErrs["pacman"] = err
	}
	aurPkgs, err := ScanAUR()
	if err != nil {
		scanErrs["aur"] = err
	}
	flatpakPkgs, err := ScanFlatpak()
	if err != nil {
		scanErrs["flatpak"] = err
	}
	nodePkgs, err := ScanNode()
	if err != nil {
		scanErrs["node"] = err
	}

	m := newImportPickerWithData(tabs, opts, pacmanPkgs, aurPkgs, flatpakPkgs, nodePkgs, scanErrs)
	return &ImportPicker{importPickerModel: m}
}

// validTabs is the set of known tab names.
var validTabs = map[string]bool{"pacman": true, "aur": true, "flatpak": true, "node": true}

// newImportPickerWithData creates a model with pre-populated data (used in tests).
// scanErrs may be nil.
func newImportPickerWithData(
	tabs []string,
	opts Options,
	pacmanPkgs []string,
	aurPkgs []string,
	flatpakPkgs []string,
	nodePkgs []NodePackage,
	scanErrs map[string]error,
) *importPickerModel {
	// Validate tab names early — skip unknown names
	filtered := make([]string, 0, len(tabs))
	for _, t := range tabs {
		if validTabs[t] {
			filtered = append(filtered, t)
		}
	}
	tabs = filtered
	if len(tabs) == 0 {
		tabs = []string{"pacman", "aur", "flatpak", "node"}
	}

	// Build selected maps (all checked by default)
	selectedPacman := make(map[string]bool, len(pacmanPkgs))
	for _, p := range pacmanPkgs {
		selectedPacman[p] = true
	}
	selectedAUR := make(map[string]bool, len(aurPkgs))
	for _, p := range aurPkgs {
		selectedAUR[p] = true
	}
	selectedFlatpak := make(map[string]bool, len(flatpakPkgs))
	for _, p := range flatpakPkgs {
		selectedFlatpak[p] = true
	}
	selectedNode := make(map[string]bool, len(nodePkgs))
	for _, p := range nodePkgs {
		selectedNode[p.Name+"\x00"+p.Manager] = true
	}

	delegate := newImportDelegate()

	makeList := func(items []list.Item) list.Model {
		l := list.New(items, delegate, 80, 20)
		l.SetShowTitle(false)
		l.SetShowStatusBar(false)
		l.SetShowPagination(false)
		l.SetShowFilter(false)
		l.SetShowHelp(false)
		return l
	}

	var pacmanItems, aurItems, flatpakItems []list.Item
	var nodeItems []list.Item
	for _, p := range pacmanPkgs {
		pacmanItems = append(pacmanItems, pickerItem{name: p, checked: true})
	}
	for _, p := range aurPkgs {
		aurItems = append(aurItems, pickerItem{name: p, checked: true})
	}
	for _, p := range flatpakPkgs {
		flatpakItems = append(flatpakItems, pickerItem{name: p, checked: true})
	}
	for _, p := range nodePkgs {
		nodeItems = append(nodeItems, pickerItem{name: p.Name, manager: p.Manager, checked: true})
	}

	// Filter input
	ti := textinput.New()
	ti.Placeholder = "filter packages..."
	ti.CharLimit = 100
	ti.Width = 40

	m := &importPickerModel{
		keymap:          defaultPickKeyMap,
		tabs:            tabs,
		active:          0,
		pacmanList:      makeList(pacmanItems),
		aurList:         makeList(aurItems),
		flatpakList:     makeList(flatpakItems),
		nodeList:        makeList(nodeItems),
		pacmanPkgs:      pacmanPkgs,
		aurPkgs:         aurPkgs,
		flatpakPkgs:     flatpakPkgs,
		nodePkgs:        nodePkgs,
		selectedPacman:  selectedPacman,
		selectedAUR:     selectedAUR,
		selectedFlatpak: selectedFlatpak,
		selectedNode:    selectedNode,
		scanErrs:        scanErrs,
		filterInput:     ti,
		opts:            opts,
		width:           80,
		height:          24,
		delegate:        delegate,
	}
	return m
}

// ──────────────────────────────────────────────
// tea.Model interface
// ──────────────────────────────────────────────

func (m *importPickerModel) Init() tea.Cmd {
	// Bubble Tea sends the real window size before the first Update,
	// so no synthetic WindowSizeMsg is needed.
	return nil
}

func (m *importPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.done {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeLists()

	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	}

	// Delegate non-key messages to the active list.
	cmd := m.updateActiveList(msg)
	return m, cmd
}

func (m *importPickerModel) View() string {
	if m.done {
		return ""
	}

	var b strings.Builder

	// Tab bar
	b.WriteString(m.tabView())
	b.WriteString("\n\n")

	// Active list
	b.WriteString(m.activeListView())
	b.WriteString("\n")

	// All-tabs-empty message (after tab bar)
	if m.allTabsEmpty() {
		b.WriteString(tui.Theme.Dim.Render("All tabs are empty — this system has no packages to import."))
		b.WriteString("\n")
	}

	// Footer with counts
	b.WriteString(m.countView())
	b.WriteString("\n")

	// Filter input (when active)
	if m.filtering {
		b.WriteString(m.filterInput.View())
		b.WriteString("\n")
	}

	// Help bar
	b.WriteString("\n")
	b.WriteString(components.NewHelpBar(
		components.HelpBinding{Key: "space", Desc: "toggle"},
		components.HelpBinding{Key: "a", Desc: "all"},
		components.HelpBinding{Key: "n", Desc: "none"},
		components.HelpBinding{Key: "/", Desc: "filter"},
		components.HelpBinding{Key: "tab", Desc: "next"},
		components.HelpBinding{Key: "o", Desc: "write"},
		components.HelpBinding{Key: "q", Desc: "quit"},
	).View())

	return b.String()
}

// ──────────────────────────────────────────────
// Exported query methods (on the internal model so
// Program.Run can retrieve them via the returned tea.Model)
// ──────────────────────────────────────────────

// Selection returns the final selection (valid after done is true).
func (m *importPickerModel) Selection() Selection { return m.selection }

// Cancelled returns true if the user cancelled without writing.
func (m *importPickerModel) Cancelled() bool { return m.cancelled }

// Err returns any error encountered during the session.
func (m *importPickerModel) Err() error { return m.err }

// ──────────────────────────────────────────────
// Internal helpers
// ──────────────────────────────────────────────

// forEachActivePkg calls fn for each package key in the active tab.
// The key is the string used to index the selected* maps (composite
// "name\x00manager" for node, plain name for others).
func (m *importPickerModel) forEachActivePkg(fn func(key string)) {
	switch m.tabs[m.active] {
	case "pacman":
		for _, p := range m.pacmanPkgs {
			fn(p)
		}
	case "aur":
		for _, p := range m.aurPkgs {
			fn(p)
		}
	case "flatpak":
		for _, p := range m.flatpakPkgs {
			fn(p)
		}
	case "node":
		for _, p := range m.nodePkgs {
			fn(p.Name + "\x00" + p.Manager)
		}
	}
}

// activeSelection returns a pointer to the active tab's selection map.
func (m *importPickerModel) activeSelection() *map[string]bool {
	switch m.tabs[m.active] {
	case "pacman":
		return &m.selectedPacman
	case "aur":
		return &m.selectedAUR
	case "flatpak":
		return &m.selectedFlatpak
	case "node":
		return &m.selectedNode
	}
	return &m.selectedPacman
}

// totalPackages returns the total number of packages for the given tab name.
func (m *importPickerModel) totalPackages(tab string) int {
	switch tab {
	case "pacman":
		return len(m.pacmanPkgs)
	case "aur":
		return len(m.aurPkgs)
	case "flatpak":
		return len(m.flatpakPkgs)
	case "node":
		return len(m.nodePkgs)
	}
	return 0
}

func (m *importPickerModel) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Quit — always available regardless of mode
	if key.Matches(msg, m.keymap.Quit) {
		m.done = true
		m.cancelled = true
		return m, tea.Quit
	}

	// Tab — always available (cycle tabs)
	if key.Matches(msg, m.keymap.Tab) {
		m.cycleTab()
		return m, nil
	}

	// ── Filtering mode ──
	if m.filtering {
		// Esc — exit filter mode
		if key.Matches(msg, m.keymap.Back) {
			m.filtering = false
			m.filterInput.Blur()
			m.filterInput.SetValue("")
			m.rebuildActiveList()
			return m, nil
		}
		// Enter — exit filter mode (keep filter applied)
		if key.Matches(msg, m.keymap.Enter) {
			m.filtering = false
			m.filterInput.Blur()
			return m, nil
		}
		// Delegate all other keys to the filter input
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		m.rebuildActiveList()
		return m, cmd
	}

	// ── Normal mode ──

	// Filter toggle
	if key.Matches(msg, m.keymap.Filter) {
		m.filtering = true
		m.filterInput.SetValue("")
		cmd := m.filterInput.Focus()
		return m, cmd
	}

	switch {
	case key.Matches(msg, m.keymap.Space):
		m.toggleSelected()

	case key.Matches(msg, m.keymap.SelectAll):
		m.setAllChecked(true)

	case key.Matches(msg, m.keymap.SelectNone):
		m.setAllChecked(false)

	case key.Matches(msg, m.keymap.Write):
		m.buildSelection()
		m.done = true
		m.cancelled = false
		return m, tea.Quit

	case key.Matches(msg, m.keymap.Back):
		// Esc in normal mode — no-op
		return m, nil

	default:
		// Delegate to the active list (navigation, etc.)
		cmd := m.updateActiveList(msg)
		return m, cmd
	}

	return m, nil
}

func (m *importPickerModel) toggleSelected() {
	lst := m.activeListPtr()
	item := lst.SelectedItem()
	if item == nil {
		return
	}
	pi, ok := item.(pickerItem)
	if !ok {
		return
	}

	sel := m.activeSelection()
	key := pi.nodeKey()
	if pi.manager == "" {
		key = pi.name
	}
	(*sel)[key] = !(*sel)[key]
	m.rebuildActiveList()
}

func (m *importPickerModel) setAllChecked(checked bool) {
	sel := m.activeSelection()
	m.forEachActivePkg(func(key string) {
		(*sel)[key] = checked
	})
	m.rebuildActiveList()
}

func (m *importPickerModel) cycleTab() {
	m.active = (m.active + 1) % len(m.tabs)
	// Clear any filter state when switching tabs
	m.filtering = false
	m.filterInput.Blur()
	m.filterInput.SetValue("")
	m.rebuildActiveList()
}

func (m *importPickerModel) buildSelection() {
	sel := Selection{}

	for _, p := range m.pacmanPkgs {
		if m.selectedPacman[p] {
			sel.Pacman = append(sel.Pacman, p)
		}
	}
	sort.Strings(sel.Pacman)

	for _, p := range m.aurPkgs {
		if m.selectedAUR[p] {
			sel.AUR = append(sel.AUR, p)
		}
	}
	sort.Strings(sel.AUR)

	for _, p := range m.flatpakPkgs {
		if m.selectedFlatpak[p] {
			sel.Flatpak = append(sel.Flatpak, p)
		}
	}
	sort.Strings(sel.Flatpak)

	for _, p := range m.nodePkgs {
		key := p.Name + "\x00" + p.Manager
		if m.selectedNode[key] {
			sel.Node = append(sel.Node, p)
		}
	}
	sort.Slice(sel.Node, func(i, j int) bool {
		if sel.Node[i].Manager != sel.Node[j].Manager {
			return sel.Node[i].Manager < sel.Node[j].Manager
		}
		return sel.Node[i].Name < sel.Node[j].Name
	})

	m.selection = sel
}

func (m *importPickerModel) rebuildActiveList() {
	filter := strings.ToLower(m.filterInput.Value())
	lst := m.activeListPtr()

	switch m.tabs[m.active] {
	case "pacman":
		m.setListItems(lst, m.pacmanPkgs, func(p string) bool { return m.selectedPacman[p] }, filter)
	case "aur":
		m.setListItems(lst, m.aurPkgs, func(p string) bool { return m.selectedAUR[p] }, filter)
	case "flatpak":
		m.setListItems(lst, m.flatpakPkgs, func(p string) bool { return m.selectedFlatpak[p] }, filter)
	case "node":
		nodeFiltered := make([]list.Item, 0)
		for _, p := range m.nodePkgs {
			if filter != "" && !strings.Contains(strings.ToLower(p.Name), filter) {
				continue
			}
			nodeFiltered = append(nodeFiltered, pickerItem{
				name:    p.Name,
				manager: p.Manager,
				checked: m.selectedNode[p.Name+"\x00"+p.Manager],
			})
		}
		lst.SetItems(nodeFiltered)
	}
}

func (m *importPickerModel) setListItems(lst *list.Model, pkgs []string, isSelected func(string) bool, filter string) {
	items := make([]list.Item, 0, len(pkgs))
	for _, p := range pkgs {
		if filter != "" && !strings.Contains(strings.ToLower(p), filter) {
			continue
		}
		items = append(items, pickerItem{name: p, checked: isSelected(p)})
	}
	lst.SetItems(items)
}

// tabView renders the tab bar.
func (m *importPickerModel) tabView() string {
	return components.NewTabBar(m.tabs, m.active).View()
}

// activeListView renders the list for the currently active tab.
func (m *importPickerModel) activeListView() string {
	// Check for scan errors on the active tab
	if err, ok := m.scanErrs[m.tabs[m.active]]; ok && err != nil {
		if m.tabs[m.active] == "flatpak" && errors.Is(err, errFlatpakNotInstalled) {
			return tui.Theme.Dim.Render("  flatpak not installed")
		}
		return tui.Theme.Error.Render("error: " + err.Error())
	}

	lst := m.activeListPtr()
	if lst == nil {
		return ""
	}
	if len(lst.VisibleItems()) == 0 {
		return tui.Theme.Dim.Render("  (no packages)")
	}
	return lst.View()
}

// countView renders the per-tab selection counts.
func (m *importPickerModel) countView() string {
	counts := make([]string, 0, len(m.tabs))
	for _, tab := range m.tabs {
		sel, total := m.tabCounts(tab)
		style := tui.Theme.Dim
		if tab == m.tabs[m.active] {
			style = tui.Theme.ActiveTab
		}
		counts = append(counts, style.Render(fmt.Sprintf("%s %d/%d", tab, sel, total)))
	}
	return strings.Join(counts, "   ")
}

func (m *importPickerModel) tabCounts(tab string) (selected, total int) {
	total = m.totalPackages(tab)
	switch tab {
	case "pacman":
		for _, p := range m.pacmanPkgs {
			if m.selectedPacman[p] {
				selected++
			}
		}
	case "aur":
		for _, p := range m.aurPkgs {
			if m.selectedAUR[p] {
				selected++
			}
		}
	case "flatpak":
		for _, p := range m.flatpakPkgs {
			if m.selectedFlatpak[p] {
				selected++
			}
		}
	case "node":
		for _, p := range m.nodePkgs {
			if m.selectedNode[p.Name+"\x00"+p.Manager] {
				selected++
			}
		}
	}
	return
}

// allTabsEmpty returns true when every tab has zero packages.
func (m *importPickerModel) allTabsEmpty() bool {
	for _, tab := range m.tabs {
		if m.totalPackages(tab) > 0 {
			return false
		}
	}
	return true
}

func (m *importPickerModel) activeListPtr() *list.Model {
	switch m.tabs[m.active] {
	case "pacman":
		return &m.pacmanList
	case "aur":
		return &m.aurList
	case "flatpak":
		return &m.flatpakList
	case "node":
		return &m.nodeList
	}
	return &m.pacmanList
}

func (m *importPickerModel) setActiveList(l list.Model) {
	switch m.tabs[m.active] {
	case "pacman":
		m.pacmanList = l
	case "aur":
		m.aurList = l
	case "flatpak":
		m.flatpakList = l
	case "node":
		m.nodeList = l
	}
}

func (m *importPickerModel) updateActiveList(msg tea.Msg) tea.Cmd {
	lst := m.activeListPtr()
	updatedList, cmd := lst.Update(msg)
	m.setActiveList(updatedList)
	return cmd
}

func (m *importPickerModel) resizeLists() {
	listHeight := m.height - 8
	if listHeight < 5 {
		listHeight = 5
	}
	listWidth := m.width - 4
	if listWidth < 40 {
		listWidth = 40
	}

	for _, tab := range m.tabs {
		var lst *list.Model
		switch tab {
		case "pacman":
			lst = &m.pacmanList
		case "aur":
			lst = &m.aurList
		case "flatpak":
			lst = &m.flatpakList
		case "node":
			lst = &m.nodeList
		default:
			continue
		}
		lst.SetWidth(listWidth)
		lst.SetHeight(listHeight)
	}
}
