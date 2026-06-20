package status

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/0xGurg/bigkis/internal/plugin"
)

// ──────────────────────────────────────────────
// Helper
// ──────────────────────────────────────────────

func newTestDashboard() *statusDashboardModel {
	plugins := []PluginStatus{
		{Name: "pacman", Available: true, Report: plugin.Report{Operations: []plugin.Operation{
			{Kind: plugin.OpAdd, Target: "vim"},
			{Kind: plugin.OpRemove, Target: "nano"},
		}}},
		{Name: "aur", Available: true, Report: plugin.Report{}},
		{Name: "flatpak", Available: false, Error: "flatpak not found"},
	}
	d := NewStatusDashboard("/etc/bigkis/system.toml", plugins)
	return d.statusDashboardModel
}

// ──────────────────────────────────────────────
// Quit / Cancel / Apply
// ──────────────────────────────────────────────

func TestStatusDashboard_QuitReturnsCancelled(t *testing.T) {
	m := newTestDashboard()
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("expected Quit command")
	}
	mm := model.(*statusDashboardModel)
	if !mm.done {
		t.Error("expected done after pressing q")
	}
	if !mm.cancelled {
		t.Error("expected cancelled after pressing q")
	}
	if mm.applyRequested {
		t.Error("expected applyRequested=false after q")
	}
}

func TestStatusDashboard_ApplyRequested(t *testing.T) {
	m := newTestDashboard()
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("expected Quit command")
	}
	mm := model.(*statusDashboardModel)
	if !mm.done {
		t.Error("expected done after pressing a")
	}
	if mm.cancelled {
		t.Error("expected cancelled=false after a")
	}
	if !mm.applyRequested {
		t.Error("expected applyRequested=true after a")
	}
}

func TestStatusDashboard_CtrlCQuits(t *testing.T) {
	m := newTestDashboard()
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("expected Quit command")
	}
	mm := model.(*statusDashboardModel)
	if !mm.done {
		t.Error("expected done after ctrl+c")
	}
	if !mm.cancelled {
		t.Error("expected cancelled after ctrl+c")
	}
}

// ──────────────────────────────────────────────
// View
// ──────────────────────────────────────────────

func TestStatusDashboard_ViewShowsConfigPath(t *testing.T) {
	m := newTestDashboard()
	m.width = 100
	m.height = 30

	v := m.View()
	if !strings.Contains(v, "/etc/bigkis/system.toml") {
		t.Errorf("View() should contain config path; got:\n%s", v)
	}
}

func TestStatusDashboard_ViewShowsDriftCount(t *testing.T) {
	m := newTestDashboard()
	m.width = 100
	m.height = 30

	v := m.View()
	if !strings.Contains(v, "2 changes") {
		t.Errorf("View() should contain drift count '2 changes'; got:\n%s", v)
	}
	if !strings.Contains(v, "1 plugin") {
		t.Errorf("View() should contain '1 plugin'; got:\n%s", v)
	}
}

func TestStatusDashboard_ViewShowsHeader(t *testing.T) {
	m := newTestDashboard()
	m.width = 100
	m.height = 30

	v := m.View()
	if !strings.Contains(v, "Status Dashboard") {
		t.Errorf("View() should contain 'Status Dashboard'; got:\n%s", v)
	}
}

func TestStatusDashboard_ViewShowsHelpBar(t *testing.T) {
	m := newTestDashboard()
	m.width = 100
	m.height = 30

	v := m.View()
	if !strings.Contains(v, "navigate") {
		t.Errorf("View() should contain navigation hint; got:\n%s", v)
	}
	if !strings.Contains(v, "suggest apply") {
		t.Errorf("View() should contain apply hint; got:\n%s", v)
	}
	if !strings.Contains(v, "quit") {
		t.Errorf("View() should contain quit hint; got:\n%s", v)
	}
}

// ──────────────────────────────────────────────
// Navigation
// ──────────────────────────────────────────────

func TestStatusDashboard_ListNavigationChangesViewport(t *testing.T) {
	m := newTestDashboard()

	// Initial viewport should show pacman (index 0)
	if !strings.Contains(m.viewport.View(), "vim") {
		t.Errorf("initial viewport should contain 'vim'; got:\n%s", m.viewport.View())
	}
	if !strings.Contains(m.viewport.View(), "nano") {
		t.Errorf("initial viewport should contain 'nano'; got:\n%s", m.viewport.View())
	}

	// Navigate down to aur (index 1) — in sync
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if !strings.Contains(m.viewport.View(), "in sync") {
		t.Errorf("viewport after selecting aur should show 'in sync'; got:\n%s", m.viewport.View())
	}

	// Navigate down to flatpak (index 2) — unavailable
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if !strings.Contains(m.viewport.View(), "unavailable") {
		t.Errorf("viewport after selecting flatpak should show 'unavailable'; got:\n%s", m.viewport.View())
	}
}

// ──────────────────────────────────────────────
// pluginItem title
// ──────────────────────────────────────────────

func TestPluginItem_Title_InSync(t *testing.T) {
	item := pluginItem{status: PluginStatus{Name: "aur", Available: true, Report: plugin.Report{}}}
	title := item.Title()
	if !strings.Contains(title, "in sync") {
		t.Errorf("Title for in-sync plugin should contain 'in sync'; got %q", title)
	}
}

func TestPluginItem_Title_Changes(t *testing.T) {
	item := pluginItem{status: PluginStatus{Name: "pacman", Available: true, Report: plugin.Report{
		Operations: []plugin.Operation{
			{Kind: plugin.OpAdd, Target: "vim"},
		},
	}}}
	title := item.Title()
	if !strings.Contains(title, "1 changes") {
		t.Errorf("Title for plugin with changes should contain '1 changes'; got %q", title)
	}
}

func TestPluginItem_Title_Unavailable(t *testing.T) {
	item := pluginItem{status: PluginStatus{Name: "flatpak", Available: false, Error: "not found"}}
	title := item.Title()
	if !strings.Contains(title, "unavailable") {
		t.Errorf("Title for unavailable plugin should contain 'unavailable'; got %q", title)
	}
}

func TestPluginItem_Description_Unavailable(t *testing.T) {
	item := pluginItem{status: PluginStatus{Name: "flatpak", Available: false, Error: "flatpak not found"}}
	if desc := item.Description(); desc != "flatpak not found" {
		t.Errorf("Description for unavailable plugin should be the error; got %q", desc)
	}
}

func TestPluginItem_Description_Available(t *testing.T) {
	item := pluginItem{status: PluginStatus{Name: "pacman", Available: true}}
	if desc := item.Description(); desc != "" {
		t.Errorf("Description for available plugin should be empty; got %q", desc)
	}
}

func TestPluginItem_FilterValue(t *testing.T) {
	item := pluginItem{status: PluginStatus{Name: "pacman"}}
	if item.FilterValue() != "pacman" {
		t.Errorf("FilterValue should be 'pacman'; got %q", item.FilterValue())
	}
}

// ──────────────────────────────────────────────
// Empty / edge cases
// ──────────────────────────────────────────────

func TestStatusDashboard_EmptyPlugins(t *testing.T) {
	d := NewStatusDashboard("/etc/bigkis/system.toml", nil)
	m := d.statusDashboardModel

	if len(m.plugins) != 0 {
		t.Errorf("expected 0 plugins, got %d", len(m.plugins))
	}
	if m.list.Index() != 0 {
		t.Errorf("empty list index should be 0, got %d", m.list.Index())
	}

	// View should not panic
	m.width = 100
	m.height = 30
	v := m.View()
	if v == "" {
		t.Error("View() should not be empty even with no plugins")
	}
}

func TestStatusDashboard_EmptyPluginsNavigate(t *testing.T) {
	// Navigation on empty list should not panic
	m := newTestDashboard()
	m.plugins = nil

	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
}

func TestStatusDashboard_DoneReturnsEmptyView(t *testing.T) {
	m := newTestDashboard()
	m.done = true
	if v := m.View(); v != "" {
		t.Errorf("View() after done should be empty; got %q", v)
	}
}

func TestStatusDashboard_DoneUpdateNoops(t *testing.T) {
	m := newTestDashboard()
	m.done = true

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd != nil {
		t.Errorf("expected nil cmd when already done, got %v", cmd)
	}
	mm := model.(*statusDashboardModel)
	if !mm.done {
		t.Error("expected done to remain true")
	}
}

// ──────────────────────────────────────────────
// Window resize
// ──────────────────────────────────────────────

func TestStatusDashboard_WindowSizeMsg(t *testing.T) {
	m := newTestDashboard()

	model, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	mm := model.(*statusDashboardModel)

	if mm.width != 120 {
		t.Errorf("width = %d, want 120", mm.width)
	}
	if mm.height != 40 {
		t.Errorf("height = %d, want 40", mm.height)
	}
	// List should have been resized
	if mm.list.Width() <= 0 {
		t.Errorf("list width should be > 0 after resize")
	}
}

// ──────────────────────────────────────────────
// Model interface
// ──────────────────────────────────────────────

func TestStatusDashboard_InitReturnsNil(t *testing.T) {
	m := newTestDashboard()
	cmd := m.Init()
	if cmd != nil {
		t.Errorf("Init() should return nil, got %v", cmd)
	}
}

func TestStatusDashboard_ImplementsModel(t *testing.T) {
	// Compile-time check that the model satisfies tea.Model.
	var _ tea.Model = (*statusDashboardModel)(nil)
}

// ──────────────────────────────────────────────
// Key matching
// ──────────────────────────────────────────────

func TestStatusDashboard_keyMatchQuit(t *testing.T) {
	m := newTestDashboard()

	if !key.Matches(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}, m.keymap.Quit) {
		t.Error("q should match Quit binding")
	}
	if !key.Matches(tea.KeyMsg{Type: tea.KeyCtrlC}, m.keymap.Quit) {
		t.Error("ctrl+c should match Quit binding")
	}
}

func TestStatusDashboard_keyMatchApply(t *testing.T) {
	m := newTestDashboard()

	if !key.Matches(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}, m.keymap.Apply) {
		t.Error("a should match Apply binding")
	}
}

// ──────────────────────────────────────────────
// Upgrades
// ──────────────────────────────────────────────

func TestStatusDashboard_UpgradeCountInPluginList(t *testing.T) {
	plugins := []PluginStatus{
		{Name: "pacman", Available: true, Report: plugin.Report{Operations: []plugin.Operation{
			{Kind: plugin.OpAdd, Target: "vim"},
		}}, Upgrades: plugin.UpgradeReport{Operations: []plugin.Operation{
			{Kind: plugin.OpUpdate, Target: "firefox", Detail: "1.0 → 2.0"},
			{Kind: plugin.OpUpdate, Target: "chrome", Detail: "3.0 → 4.0"},
		}}},
		{Name: "aur", Available: true, Report: plugin.Report{}},
		{Name: "flatpak", Available: false, Error: "flatpak not found"},
	}
	d := NewStatusDashboard("/etc/bigkis/system.toml", plugins)
	m := d.statusDashboardModel
	m.width = 100
	m.height = 30

	v := m.View()
	if !strings.Contains(v, "2 upgrades") {
		t.Errorf("View() should contain '2 upgrades' in plugin list; got:\n%s", v)
	}
}

func TestStatusDashboard_UpgradeEntriesInViewport(t *testing.T) {
	plugins := []PluginStatus{
		{Name: "pacman", Available: true, Upgrades: plugin.UpgradeReport{Operations: []plugin.Operation{
			{Kind: plugin.OpUpdate, Target: "firefox", Detail: "1.0 → 2.0"},
		}}},
		{Name: "aur", Available: true, Report: plugin.Report{}},
	}
	d := NewStatusDashboard("/etc/bigkis/system.toml", plugins)
	m := d.statusDashboardModel

	vpContent := m.viewport.View()
	if !strings.Contains(vpContent, "↑ upgrades") {
		t.Errorf("viewport should contain '↑ upgrades'; got:\n%s", vpContent)
	}
	if !strings.Contains(vpContent, "firefox") {
		t.Errorf("viewport should contain 'firefox'; got:\n%s", vpContent)
	}
}

func TestStatusDashboard_UpgradeCountInStatusBar(t *testing.T) {
	plugins := []PluginStatus{
		{Name: "pacman", Available: true, Upgrades: plugin.UpgradeReport{Operations: []plugin.Operation{
			{Kind: plugin.OpUpdate, Target: "firefox"},
		}}},
		{Name: "aur", Available: true, Upgrades: plugin.UpgradeReport{Operations: []plugin.Operation{
			{Kind: plugin.OpUpdate, Target: "chrome"},
		}}},
	}
	d := NewStatusDashboard("/etc/bigkis/system.toml", plugins)
	m := d.statusDashboardModel
	m.width = 100
	m.height = 30

	v := m.View()
	if !strings.Contains(v, "2 upgrades available") {
		t.Errorf("View() should contain '2 upgrades available'; got:\n%s", v)
	}
}

func TestPluginStatus_HasChanges(t *testing.T) {
	// Available + ops -> true
	ps := PluginStatus{Name: "p", Available: true, Report: plugin.Report{Operations: []plugin.Operation{
		{Kind: plugin.OpAdd, Target: "x"},
	}}}
	if !ps.HasChanges() {
		t.Error("HasChanges should be true for available plugin with operations")
	}

	// Available + no ops -> false
	ps2 := PluginStatus{Name: "p", Available: true, Report: plugin.Report{}}
	if ps2.HasChanges() {
		t.Error("HasChanges should be false for available plugin with no operations")
	}

	// Unavailable -> false even with ops
	ps3 := PluginStatus{Name: "p", Available: false, Report: plugin.Report{Operations: []plugin.Operation{
		{Kind: plugin.OpAdd, Target: "x"},
	}}}
	if ps3.HasChanges() {
		t.Error("HasChanges should be false for unavailable plugin")
	}
}
