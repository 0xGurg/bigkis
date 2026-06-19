package importer

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// ──────────────────────────────────────────────
// Helper
// ──────────────────────────────────────────────

func keyRune(r rune) tea.Msg {
	return tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{r}})
}

func keyPress(s string) tea.Msg {
	return tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune(s)})
}

func tabMsg() tea.Msg {
	return tea.KeyMsg{Type: tea.KeyTab}
}

func spaceMsg() tea.Msg {
	return tea.KeyMsg{Type: tea.KeySpace}
}

func ctrlCMsg() tea.Msg {
	return tea.KeyMsg{Type: tea.KeyCtrlC}
}

// newTestPicker creates a model with fixed test data so tests don't depend on
// system state.
func newTestPicker(only, pacman, aur, flatpak []string, node []NodePackage) *importPickerModel {
	tabs := only
	if len(tabs) == 0 {
		tabs = []string{"pacman", "aur", "flatpak", "node"}
	}
	return newImportPickerWithData(tabs, Options{}, pacman, aur, flatpak, node, nil)
}

// ──────────────────────────────────────────────
// Tests
// ──────────────────────────────────────────────

func TestImportPicker_QuitCancels(t *testing.T) {
	m := newTestPicker(nil, []string{"pkg1"}, nil, nil, nil)

	// Simulate pressing 'q'
	result, cmd := m.Update(keyPress("q"))
	if cmd == nil {
		t.Error("expected a command (tea.Quit), got nil")
	}

	picker, ok := result.(*importPickerModel)
	if !ok {
		t.Fatalf("expected *importPickerModel, got %T", result)
	}

	if !picker.done {
		t.Error("expected done after pressing 'q'")
	}
	if !picker.cancelled {
		t.Error("expected cancelled after pressing 'q'")
	}
}

func TestImportPicker_CtrlCQuits(t *testing.T) {
	m := newTestPicker(nil, []string{"pkg1"}, nil, nil, nil)

	result, _ := m.Update(ctrlCMsg())
	picker, ok := result.(*importPickerModel)
	if !ok {
		t.Fatalf("expected *importPickerModel, got %T", result)
	}

	if !picker.done {
		t.Error("expected done after ctrl+c")
	}
	if !picker.cancelled {
		t.Error("expected cancelled after ctrl+c")
	}
}

func TestImportPicker_SpaceToggles(t *testing.T) {
	m := newTestPicker(nil, []string{"pkg1", "pkg2"}, nil, nil, nil)

	// Initially both are checked
	if !m.selectedPacman["pkg1"] {
		t.Error("expected pkg1 to be initially checked")
	}
	if !m.selectedPacman["pkg2"] {
		t.Error("expected pkg2 to be initially checked")
	}

	// First item (pkg1) should be at index 0 by default. Press space.
	m.Update(spaceMsg())

	if m.selectedPacman["pkg1"] {
		t.Error("expected pkg1 to be unchecked after pressing space")
	}
	if !m.selectedPacman["pkg2"] {
		t.Error("expected pkg2 to remain checked")
	}

	// Press space again to re-check
	m.Update(spaceMsg())

	if !m.selectedPacman["pkg1"] {
		t.Error("expected pkg1 to be checked again after second space")
	}
}

func TestImportPicker_SelectAll(t *testing.T) {
	m := newTestPicker(nil, []string{"pkg1", "pkg2", "pkg3"}, nil, nil, nil)

	// Uncheck all first by pressing 'n'
	m.Update(keyPress("n"))

	for _, p := range []string{"pkg1", "pkg2", "pkg3"} {
		if m.selectedPacman[p] {
			t.Errorf("expected %s to be unchecked after 'n'", p)
		}
	}

	// Now press 'a' to select all
	m.Update(keyPress("a"))

	for _, p := range []string{"pkg1", "pkg2", "pkg3"} {
		if !m.selectedPacman[p] {
			t.Errorf("expected %s to be checked after 'a'", p)
		}
	}
}

func TestImportPicker_SelectNone(t *testing.T) {
	m := newTestPicker(nil, []string{"pkg1", "pkg2", "pkg3"}, nil, nil, nil)

	// Press 'n' to uncheck all
	m.Update(keyPress("n"))

	for _, p := range []string{"pkg1", "pkg2", "pkg3"} {
		if m.selectedPacman[p] {
			t.Errorf("expected %s to be unchecked after 'n'", p)
		}
	}
}

func TestImportPicker_TabCycles(t *testing.T) {
	m := newTestPicker(nil,
		[]string{"pac1"},
		[]string{"aur1"},
		[]string{"flat1"},
		[]NodePackage{{Name: "node1", Manager: "npm"}},
	)

	if m.tabs[m.active] != "pacman" {
		t.Errorf("expected first tab to be pacman, got %s", m.tabs[m.active])
	}

	// Press tab
	m.Update(tabMsg())
	if m.tabs[m.active] != "aur" {
		t.Errorf("expected tab to be aur after one tab, got %s", m.tabs[m.active])
	}

	// Press tab again
	m.Update(tabMsg())
	if m.tabs[m.active] != "flatpak" {
		t.Errorf("expected tab to be flatpak after two tabs, got %s", m.tabs[m.active])
	}

	// Press tab again
	m.Update(tabMsg())
	if m.tabs[m.active] != "node" {
		t.Errorf("expected tab to be node after three tabs, got %s", m.tabs[m.active])
	}

	// Press tab again — should wrap to pacman
	m.Update(tabMsg())
	if m.tabs[m.active] != "pacman" {
		t.Errorf("expected tab to wrap to pacman after four tabs, got %s", m.tabs[m.active])
	}
}

func TestImportPicker_TabCyclesOnly(t *testing.T) {
	// With --only=[flatpak, node], only those two tabs should exist
	m := newTestPicker([]string{"flatpak", "node"},
		nil, nil,
		[]string{"flat1"},
		[]NodePackage{{Name: "node1", Manager: "npm"}},
	)

	if len(m.tabs) != 2 {
		t.Errorf("expected 2 tabs, got %d: %v", len(m.tabs), m.tabs)
	}
	if m.tabs[m.active] != "flatpak" {
		t.Errorf("expected first tab to be flatpak, got %s", m.tabs[m.active])
	}

	m.Update(tabMsg())
	if m.tabs[m.active] != "node" {
		t.Errorf("expected tab to be node, got %s", m.tabs[m.active])
	}

	m.Update(tabMsg())
	if m.tabs[m.active] != "flatpak" {
		t.Errorf("expected tab to wrap to flatpak, got %s", m.tabs[m.active])
	}
}

func TestImportPicker_WriteQuits(t *testing.T) {
	m := newTestPicker(nil,
		[]string{"pac1", "pac2"},
		[]string{"aur1"},
		nil,
		[]NodePackage{{Name: "node1", Manager: "npm"}},
	)

	// Uncheck pac2 before writing
	m.selectedPacman["pac2"] = false

	// Press 'o' to write & quit
	result, cmd := m.Update(keyPress("o"))
	if cmd == nil {
		t.Error("expected a command (tea.Quit), got nil")
	}

	picker, ok := result.(*importPickerModel)
	if !ok {
		t.Fatalf("expected *importPickerModel, got %T", result)
	}
	if !picker.done {
		t.Error("expected done after pressing 'o'")
	}
	if picker.cancelled {
		t.Error("expected cancelled to be false after pressing 'o'")
	}

	// Check selection
	sel := picker.selection
	if len(sel.Pacman) != 1 || sel.Pacman[0] != "pac1" {
		t.Errorf("expected 1 pacman package (pac1), got %v", sel.Pacman)
	}
	if len(sel.AUR) != 1 || sel.AUR[0] != "aur1" {
		t.Errorf("expected 1 AUR package (aur1), got %v", sel.AUR)
	}
	if len(sel.Flatpak) != 0 {
		t.Errorf("expected 0 flatpak packages, got %v", sel.Flatpak)
	}
	if len(sel.Node) != 1 || sel.Node[0].Name != "node1" {
		t.Errorf("expected 1 node package (node1), got %v", sel.Node)
	}
}

func TestImportPicker_ViewShowsTabs(t *testing.T) {
	m := newTestPicker(nil, []string{"pkg1"}, nil, nil, nil)
	view := m.View()

	if !strings.Contains(view, "pacman") {
		t.Errorf("view should contain pacman tab:\n%s", view)
	}
}

func TestImportPicker_FilterToggle(t *testing.T) {
	m := newTestPicker(nil, []string{"alpha", "beta", "gamma"}, nil, nil, nil)

	// Press '/' to enter filter mode
	_, _ = m.Update(keyPress("/"))
	if !m.filtering {
		t.Error("expected filtering to be true after pressing '/'")
	}

	// Press 'esc' to exit filter mode
	escMsg := tea.KeyMsg{Type: tea.KeyEsc}
	_, _ = m.Update(escMsg)
	if m.filtering {
		t.Error("expected filtering to be false after pressing 'esc'")
	}
}

func TestImportPicker_ImplementsModel(t *testing.T) {
	// Compile-time check that the model satisfies tea.Model.
	var _ tea.Model = (*importPickerModel)(nil)
}

func TestImportPicker_NodeToggle(t *testing.T) {
	m := newTestPicker([]string{"node"}, nil, nil, nil, []NodePackage{
		{Name: "pkg1", Manager: "npm"},
		{Name: "pkg2", Manager: "pnpm"},
	})

	// Both should be initially checked
	if !m.selectedNode["pkg1\x00npm"] {
		t.Error("expected pkg1/npm to be checked")
	}
	if !m.selectedNode["pkg2\x00pnpm"] {
		t.Error("expected pkg2/pnpm to be checked")
	}

	// Toggle with space on first node item
	m.Update(spaceMsg())

	if m.selectedNode["pkg1\x00npm"] {
		t.Error("expected pkg1/npm to be unchecked after space")
	}
	if !m.selectedNode["pkg2\x00pnpm"] {
		t.Error("expected pkg2/pnpm to remain checked")
	}
}

func TestImportPicker_FilterHidesItems(t *testing.T) {
	m := newTestPicker(nil, []string{"alpha", "beta", "gamma"}, nil, nil, nil)

	// Enter filter mode
	m.Update(keyPress("/"))

	// Type "alp"
	for _, r := range []rune{'a', 'l', 'p'} {
		m.filterInput, _ = m.filterInput.Update(keyRune(r))
	}

	// Rebuild the list (filter is applied in rebuildActiveList)
	m.rebuildActiveList()

	// Only "alpha" should be visible
	lst := m.activeListPtr()
	if len(lst.VisibleItems()) != 1 {
		t.Errorf("expected 1 visible item after filtering 'alp', got %d", len(lst.VisibleItems()))
	}
}

func TestImportPicker_WindowResize(t *testing.T) {
	m := newTestPicker(nil, []string{"pkg1", "pkg2", "pkg3"}, nil, nil, nil)

	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	if m.width != 120 {
		t.Errorf("expected width 120, got %d", m.width)
	}
	if m.height != 40 {
		t.Errorf("expected height 40, got %d", m.height)
	}

	// Verify lists were resized
	if m.pacmanList.Width() < 40 {
		t.Error("expected list to have been resized")
	}
}

func TestImportPicker_EmptyTabShowsNoPackages(t *testing.T) {
	m := newTestPicker([]string{"pacman"}, nil, nil, nil, nil)
	view := m.View()
	if !strings.Contains(view, "(no packages)") {
		t.Errorf("expected '(no packages)' in view for empty tab, got: %s", view)
	}
}

func TestImportPicker_BuildSelectionSortOrder(t *testing.T) {
	// Test string package sorting (pacman)
	m := newTestPicker([]string{"pacman"}, []string{"zzz", "aaa", "mmm"}, nil, nil, nil)
	m.Update(keyRune('o')) // Write — triggers buildSelection
	sel := m.Selection()
	if len(sel.Pacman) != 3 {
		t.Fatalf("expected 3 pacman packages, got %d", len(sel.Pacman))
	}
	if sel.Pacman[0] != "aaa" || sel.Pacman[1] != "mmm" || sel.Pacman[2] != "zzz" {
		t.Errorf("expected sorted [aaa mmm zzz], got %v", sel.Pacman)
	}

	// Test Node sorting (Manager first, then Name)
	m2 := newTestPicker([]string{"node"}, nil, nil, nil, []NodePackage{
		{Name: "beta", Manager: "npm"},
		{Name: "alpha", Manager: "pnpm"},
		{Name: "gamma", Manager: "npm"},
	})
	m2.Update(keyRune('o'))
	sel2 := m2.Selection()
	if len(sel2.Node) != 3 {
		t.Fatalf("expected 3 node packages, got %d", len(sel2.Node))
	}
	if sel2.Node[0].Manager != "npm" || sel2.Node[0].Name != "beta" {
		t.Errorf("expected npm/beta first, got %s/%s", sel2.Node[0].Manager, sel2.Node[0].Name)
	}
	if sel2.Node[1].Manager != "npm" || sel2.Node[1].Name != "gamma" {
		t.Errorf("expected npm/gamma second, got %s/%s", sel2.Node[1].Manager, sel2.Node[1].Name)
	}
	if sel2.Node[2].Manager != "pnpm" || sel2.Node[2].Name != "alpha" {
		t.Errorf("expected pnpm/alpha third, got %s/%s", sel2.Node[2].Manager, sel2.Node[2].Name)
	}
}
