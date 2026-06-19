package apply

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"codeberg.org/gurg/bigkis/internal/plugin"
)

// ──────────────────────────────────────────────
// Test helpers
// ──────────────────────────────────────────────

func newTestReview() *applyReviewModel {
	plans := []PluginPlan{
		{Name: "pacman", InSync: false, Report: plugin.Report{Operations: []plugin.Operation{
			{Kind: plugin.OpAdd, Target: "vim"},
			{Kind: plugin.OpRemove, Target: "nano"},
		}}},
		{Name: "aur", InSync: true, Report: plugin.Report{}},
	}
	r := NewApplyReview("/etc/bigkis/system.toml", plans, false, true, false)
	return r.applyReviewModel
}

func newTestReviewSelective() *applyReviewModel {
	plans := []PluginPlan{
		{Name: "pacman", InSync: false, Report: plugin.Report{Operations: []plugin.Operation{
			{Kind: plugin.OpAdd, Target: "vim"},
			{Kind: plugin.OpRemove, Target: "nano"},
		}}},
		{Name: "aur", InSync: true, Report: plugin.Report{}},
	}
	r := NewApplyReview("/etc/bigkis/system.toml", plans, false, true, true)
	return r.applyReviewModel
}

// ──────────────────────────────────────────────
// Existing tests (Phase 4)
// ──────────────────────────────────────────────

func TestApplyReview_QuitCancels(t *testing.T) {
	m := newTestReview()

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("expected Quit command")
	}
	mm := model.(*applyReviewModel)
	if !mm.done {
		t.Error("expected done after quit")
	}
	if !mm.cancelled {
		t.Error("expected cancelled after quit")
	}
	if mm.confirmed {
		t.Error("expected not confirmed after quit")
	}
}

func TestApplyReview_ConfirmProceeds(t *testing.T) {
	m := newTestReview()

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected Quit command after confirm")
	}
	mm := model.(*applyReviewModel)
	if !mm.done {
		t.Error("expected done after confirm")
	}
	if !mm.confirmed {
		t.Error("expected confirmed after y")
	}
	if mm.cancelled {
		t.Error("expected cancelled=false after confirm")
	}
}

func TestApplyReview_ViewShowsHeader(t *testing.T) {
	m := newTestReview()
	m.width = 100
	m.height = 30

	v := m.View()
	if !strings.Contains(v, "Apply Review") {
		t.Errorf("View() should contain header 'Apply Review'; got:\n%s", v)
	}
}

func TestApplyReview_ViewShowsConfigPath(t *testing.T) {
	m := newTestReview()
	m.width = 100
	m.height = 30

	v := m.View()
	if !strings.Contains(v, "/etc/bigkis/system.toml") {
		t.Errorf("View() should contain config path; got:\n%s", v)
	}
}

func TestApplyReview_ViewShowsChanges(t *testing.T) {
	m := newTestReview()
	m.width = 100
	m.height = 30

	v := m.View()
	if !strings.Contains(v, "changes") {
		t.Errorf("View() should contain 'changes'; got:\n%s", v)
	}
}

func TestApplyReview_ListNavigationUpdatesViewport(t *testing.T) {
	m := newTestReview()
	m.width = 100
	m.height = 30

	// Initially at index 0 (pacman with changes), viewport should show vim and nano
	v0 := m.viewport.View()
	if !strings.Contains(v0, "vim") || !strings.Contains(v0, "nano") {
		t.Errorf("initial viewport should show pacman details; got:\n%s", v0)
	}

	// Navigate down to index 1 (aur in sync)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})

	v1 := m.viewport.View()
	if !strings.Contains(v1, "in sync") {
		t.Errorf("viewport after navigation should show 'in sync' for aur; got:\n%s", v1)
	}
}

func TestPluginPlanItem_Title_Changes(t *testing.T) {
	item := pluginPlanItem{
		plan: PluginPlan{
			Name:   "pacman",
			InSync: false,
			Report: plugin.Report{Operations: []plugin.Operation{
				{Kind: plugin.OpAdd, Target: "vim"},
			}},
		},
	}
	title := item.Title()
	if !strings.Contains(title, "1 changes") {
		t.Errorf("Title for changed plugin should show '1 changes'; got %q", title)
	}
}

func TestPluginPlanItem_Title_InSync(t *testing.T) {
	item := pluginPlanItem{
		plan: PluginPlan{
			Name:   "aur",
			InSync: true,
			Report: plugin.Report{},
		},
	}
	title := item.Title()
	if !strings.Contains(title, "in sync") {
		t.Errorf("Title for in-sync plugin should contain 'in sync'; got %q", title)
	}
}

func TestApplyReview_EmptyPlans(t *testing.T) {
	plans := []PluginPlan{}
	m := NewApplyReview("/etc/bigkis/system.toml", plans, false, true, false)
	mm := m.applyReviewModel

	if len(mm.plans) != 0 {
		t.Errorf("expected 0 plans, got %d", len(mm.plans))
	}
	// View should not crash
	mm.width = 100
	mm.height = 30
	v := mm.View()
	if !strings.Contains(v, "Apply Review") {
		t.Errorf("empty View() should still show header; got:\n%s", v)
	}
}

func TestApplyReview_WindowSizeMsg(t *testing.T) {
	m := newTestReview()

	model, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	mm := model.(*applyReviewModel)

	if mm.width != 120 {
		t.Errorf("width = %d, want 120", mm.width)
	}
	if mm.height != 40 {
		t.Errorf("height = %d, want 40", mm.height)
	}
	if mm.list.Width() <= 0 {
		t.Errorf("list width should be > 0 after resize")
	}
}

func TestApplyReview_CtrlCQuits(t *testing.T) {
	m := newTestReview()

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("expected Quit command after ctrl+c")
	}
	mm := model.(*applyReviewModel)
	if !mm.done {
		t.Error("expected done after ctrl+c")
	}
	if !mm.cancelled {
		t.Error("expected cancelled after ctrl+c")
	}
}

func TestApplyReview_Accessors(t *testing.T) {
	m := newTestReview()

	// Default state
	if m.Cancelled() {
		t.Error("expected Cancelled()=false initially")
	}
	if m.Confirmed() {
		t.Error("expected Confirmed()=false initially")
	}

	// Quit
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if !m.Cancelled() {
		t.Error("expected Cancelled()=true after q")
	}
}

func TestApplyReview_DoneReturnsEmptyView(t *testing.T) {
	m := newTestReview()
	m.done = true
	if v := m.View(); v != "" {
		t.Errorf("View() after done should be empty; got %q", v)
	}
}

func TestApplyReview_InitReturnsNil(t *testing.T) {
	m := newTestReview()
	cmd := m.Init()
	if cmd != nil {
		t.Errorf("Init() should return nil, got %v", cmd)
	}
}

func TestApplyReview_DoneUpdateNoops(t *testing.T) {
	m := newTestReview()
	m.done = true

	// Send a quit key — should be ignored because done is already true
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd != nil {
		t.Errorf("expected nil cmd when already done, got %v", cmd)
	}
	mm := model.(*applyReviewModel)
	if !mm.done {
		t.Error("expected done to remain true")
	}
}

func TestApplyReview_keyMatchQuit(t *testing.T) {
	m := newTestReview()

	if !key.Matches(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}, m.keymap.Quit) {
		t.Error("q should match Quit binding")
	}
	if !key.Matches(tea.KeyMsg{Type: tea.KeyCtrlC}, m.keymap.Quit) {
		t.Error("ctrl+c should match Quit binding")
	}
}

func TestApplyReview_keyMatchConfirm(t *testing.T) {
	m := newTestReview()

	if !key.Matches(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}, m.keymap.Confirm) {
		t.Error("y should match Confirm binding")
	}
}

func TestApplyReview_ViewShowsHelpBar(t *testing.T) {
	m := newTestReview()
	m.width = 100
	m.height = 30

	v := m.View()
	// The confirm bar is always visible (no interactive confirmation mode in
	// this read-only review), so the help bar is not rendered. Instead the
	// confirm bar shows key hints.
	if !strings.Contains(v, "y") {
		t.Errorf("View() should contain 'y' hint; got:\n%s", v)
	}
	if !strings.Contains(v, "q") {
		t.Errorf("View() should contain 'q' hint; got:\n%s", v)
	}
	if !strings.Contains(v, "proceed to apply changes?") {
		t.Errorf("View() should contain confirm prompt; got:\n%s", v)
	}
}

func TestPluginPlanItem_FilterValue(t *testing.T) {
	item := pluginPlanItem{
		plan: PluginPlan{Name: "pacman"},
	}
	if item.FilterValue() != "pacman" {
		t.Errorf("FilterValue() = %q, want %q", item.FilterValue(), "pacman")
	}
}

func TestPluginPlanItem_Description(t *testing.T) {
	item := pluginPlanItem{
		plan: PluginPlan{Name: "pacman"},
	}
	if item.Description() != "" {
		t.Errorf("Description() should be empty, got %q", item.Description())
	}
}

func TestApplyReview_DryRunSummary(t *testing.T) {
	plans := []PluginPlan{
		{Name: "pacman", InSync: false, Report: plugin.Report{Operations: []plugin.Operation{
			{Kind: plugin.OpAdd, Target: "vim"},
		}}},
	}
	m := NewApplyReview("/etc/bigkis/system.toml", plans, true, false, false)
	mm := m.applyReviewModel
	mm.width = 100
	mm.height = 30

	v := mm.View()
	if !strings.Contains(v, "dry-run") {
		t.Errorf("dry-run View() should contain 'dry-run'; got:\n%s", v)
	}
}

func TestApplyReview_WillUpgradeSummary(t *testing.T) {
	plans := []PluginPlan{
		{Name: "pacman", InSync: false, Report: plugin.Report{Operations: []plugin.Operation{
			{Kind: plugin.OpAdd, Target: "vim"},
		}}},
	}
	m := NewApplyReview("/etc/bigkis/system.toml", plans, false, true, false)
	mm := m.applyReviewModel
	mm.width = 100
	mm.height = 30

	v := mm.View()
	if !strings.Contains(v, "will upgrade") {
		t.Errorf("View() should contain 'will upgrade'; got:\n%s", v)
	}
}

func TestApplyReview_NoChangesSummary(t *testing.T) {
	plans := []PluginPlan{
		{Name: "pacman", InSync: true, Report: plugin.Report{}},
	}
	m := NewApplyReview("/etc/bigkis/system.toml", plans, false, false, false)
	mm := m.applyReviewModel
	mm.width = 100
	mm.height = 30

	v := mm.View()
	if !strings.Contains(v, "system matches declaration") {
		t.Errorf("no-changes View() should contain 'system matches declaration'; got:\n%s", v)
	}
}

func TestApplyReview_UpdateViewport_OutOfRange(t *testing.T) {
	m := newTestReview()
	// Index -1 and out-of-range should not panic
	m.updateViewport(-1)
	m.updateViewport(99)
	// Still navigable
	if m.list.Index() != 0 {
		t.Errorf("list index should be 0 after out-of-range calls, got %d", m.list.Index())
	}
}

// ──────────────────────────────────────────────
// Phase 5 selective tests
// ──────────────────────────────────────────────

func TestApplyReview_SelectiveToggle(t *testing.T) {
	m := newTestReviewSelective()
	m.width = 100
	m.height = 30
	m.focusRight = true

	// Initially all checked
	if !m.opItems[0].checked {
		t.Error("expected op 0 checked initially")
	}

	// Press space to toggle first op
	m.Update(tea.KeyMsg{Type: tea.KeySpace})
	if m.opItems[0].checked {
		t.Error("expected op 0 unchecked after space")
	}
}

func TestApplyReview_SelectiveToggleAll(t *testing.T) {
	m := newTestReviewSelective()
	m.width = 100
	m.height = 30
	m.focusRight = true

	// All initially checked
	for _, item := range m.opItems {
		if !item.checked {
			t.Fatal("expected all checked initially")
		}
	}

	// Press A to toggle all (uncheck all)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	for _, item := range m.opItems {
		if item.checked {
			t.Error("expected all unchecked after toggle all")
		}
	}
}

func TestApplyReview_SelectiveProceed(t *testing.T) {
	m := newTestReviewSelective()
	m.width = 100
	m.height = 30
	m.focusRight = true

	// Press enter to proceed with all checked
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := model.(*applyReviewModel)
	if cmd == nil {
		t.Fatal("expected Quit command after proceed")
	}
	if !mm.done {
		t.Error("expected done after proceed")
	}
	if !mm.confirmed {
		t.Error("expected confirmed after proceed")
	}
}

func TestApplyReview_SelectiveTabSwitchesFocus(t *testing.T) {
	m := newTestReviewSelective()
	m.width = 100
	m.height = 30

	if m.focusRight {
		t.Error("expected focusRight=false initially")
	}

	// Tab to focus right pane
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if !m.focusRight {
		t.Error("expected focusRight=true after tab")
	}

	// Tab again to focus left pane
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.focusRight {
		t.Error("expected focusRight=false after second tab")
	}
}

func TestApplyReview_SelectiveViewShowsCheckboxes(t *testing.T) {
	m := newTestReviewSelective()
	m.width = 100
	m.height = 30

	v := m.View()
	if !strings.Contains(v, "[x]") {
		t.Errorf("View() should show [x] checkboxes; got:\n%s", v)
	}
	if !strings.Contains(v, "[space]") {
		t.Errorf("View() should show [space] hint; got:\n%s", v)
	}
}

func TestApplyReview_NonSelectiveUsesViewport(t *testing.T) {
	m := newTestReview()
	m.width = 100
	m.height = 30

	// Should show viewport content (vim and nano from pacman)
	v := m.View()
	if !strings.Contains(v, "vim") {
		t.Errorf("View() should show vim in viewport; got:\n%s", v)
	}
}

func TestApplyReview_ShowsDependencyInstalledPackages(t *testing.T) {
	plans := []PluginPlan{
		{
			Name:                "pacman",
			InSync:              true,
			Report:              plugin.Report{},
			DependencyInstalled: []string{"qt6-svg"},
		},
	}
	m := NewApplyReview("/etc/bigkis/system.toml", plans, false, false, false).applyReviewModel
	m.width = 100
	m.height = 30

	v := m.View()
	if !strings.Contains(v, "already installed as dependency") {
		t.Errorf("View() should explain dependency-installed packages; got:\n%s", v)
	}
	if !strings.Contains(v, "qt6-svg") {
		t.Errorf("View() should show dependency-installed package name; got:\n%s", v)
	}
}

func TestApplyReview_SelectiveShowsDependencyInstalledPackagesAsInfo(t *testing.T) {
	plans := []PluginPlan{
		{
			Name:                "pacman",
			InSync:              true,
			Report:              plugin.Report{},
			DependencyInstalled: []string{"qt6-svg"},
		},
	}
	m := NewApplyReview("/etc/bigkis/system.toml", plans, false, false, true).applyReviewModel
	m.width = 100
	m.height = 30

	v := m.View()
	if !strings.Contains(v, "already installed as dependency") {
		t.Errorf("View() should explain dependency-installed packages; got:\n%s", v)
	}
	if !strings.Contains(v, "qt6-svg") {
		t.Errorf("View() should show dependency-installed package name; got:\n%s", v)
	}
	if strings.Contains(v, "[x]") {
		t.Errorf("dependency-installed packages should not be selectable actions; got:\n%s", v)
	}
}

// ──────────────────────────────────────────────
// Upgrade display tests
// ──────────────────────────────────────────────

func TestApplyReview_UpgradeCountInPluginList(t *testing.T) {
	plans := []PluginPlan{
		{
			Name:   "pacman",
			InSync: true,
			Report: plugin.Report{},
			Upgrades: plugin.UpgradeReport{Operations: []plugin.Operation{
				{Kind: plugin.OpUpdate, Target: "firefox", Detail: "100.0 -> 101.0"},
			}},
		},
	}
	m := NewApplyReview("/etc/bigkis/system.toml", plans, false, true, false)
	mm := m.applyReviewModel
	mm.width = 100
	mm.height = 30

	v := mm.View()
	if !strings.Contains(v, "1 upgrades") {
		t.Errorf("View() should contain '1 upgrades' in list item; got:\n%s", v)
	}
}

func TestApplyReview_UpgradeEntriesInViewport(t *testing.T) {
	plans := []PluginPlan{
		{
			Name:   "pacman",
			InSync: true,
			Report: plugin.Report{},
			Upgrades: plugin.UpgradeReport{Operations: []plugin.Operation{
				{Kind: plugin.OpUpdate, Target: "firefox", Detail: "100.0 -> 101.0"},
			}},
		},
	}
	m := NewApplyReview("/etc/bigkis/system.toml", plans, false, true, false)
	mm := m.applyReviewModel
	mm.width = 100
	mm.height = 30

	vp := mm.viewport.View()
	if !strings.Contains(vp, "↑ upgrades") {
		t.Errorf("viewport should contain '↑ upgrades'; got:\n%s", vp)
	}
	if !strings.Contains(vp, "firefox") {
		t.Errorf("viewport should contain upgrade package name; got:\n%s", vp)
	}
}

func TestApplyReview_UpgradesInSelectiveMode(t *testing.T) {
	plans := []PluginPlan{
		{
			Name:   "pacman",
			InSync: true,
			Report: plugin.Report{},
			Upgrades: plugin.UpgradeReport{Operations: []plugin.Operation{
				{Kind: plugin.OpUpdate, Target: "firefox", Detail: "100.0 -> 101.0"},
			}},
		},
	}
	m := NewApplyReview("/etc/bigkis/system.toml", plans, false, true, true)
	mm := m.applyReviewModel
	mm.width = 100
	mm.height = 30

	// Check that opItems contains the upgrade with "↑" prefix
	var foundTitle bool
	for _, item := range mm.opItems {
		title := item.Title()
		if strings.Contains(title, "↑") && strings.Contains(title, "firefox") {
			foundTitle = true
			break
		}
	}
	if !foundTitle {
		t.Error("opItems should contain upgrade with '↑' prefix and 'firefox' in Title()")
	}

	// Verify the rendered View also shows "↑" for upgrades
	v := mm.View()
	if !strings.Contains(v, "↑") {
		t.Errorf("selective View() should contain '↑' for upgrades; got:\n%s", v)
	}
}

func TestApplyReview_FilteredPlansSubset(t *testing.T) {
	m := newTestReviewSelective()
	m.width = 100
	m.height = 30
	m.focusRight = true

	// Toggle off vim (first op)
	m.Update(tea.KeyMsg{Type: tea.KeySpace})

	// Proceed
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	fps := m.FilteredPlans()
	if len(fps) != 2 {
		t.Fatalf("expected 2 filtered plans, got %d", len(fps))
	}

	// pacman should have only nano (vim was unchecked)
	if fps[0].Name != "pacman" {
		t.Fatalf("first plan should be pacman, got %s", fps[0].Name)
	}
	if len(fps[0].Report.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(fps[0].Report.Operations))
	}
	if fps[0].Report.Operations[0].Target != "nano" {
		t.Errorf("expected nano operation, got %s", fps[0].Report.Operations[0].Target)
	}

	// aur should still be in sync
	if !fps[1].InSync {
		t.Error("expected aur to be in sync")
	}
}
