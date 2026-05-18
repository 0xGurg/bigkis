package rollback

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"codeberg.org/gurg/bigkis/internal/tui/components"
	rollbackdata "codeberg.org/gurg/bigkis/internal/rollback"
)

// newTestBrowser creates a model with pre-populated scripts for testing.
// Unlike NewRollbackBrowser, it does not read from the filesystem.
func newTestBrowser(scripts []rollbackdata.Script) *rollbackBrowserModel {
	reversed := make([]rollbackdata.Script, len(scripts))
	for i, s := range scripts {
		reversed[len(scripts)-1-i] = s
	}
	scriptItems := make([]scriptItem, len(reversed))
	items := make([]list.Item, len(reversed))
	for i, s := range reversed {
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

	vp := viewport.New(50, 20)

	m := &rollbackBrowserModel{
		keymap:   defaultBrowserKeyMap,
		scripts:  reversed,
		items:    scriptItems,
		list:     l,
		viewport: vp,
		previews: make(map[string]string),
		confirm:  components.NewConfirmBar("execute this rollback script?", false),
	}
	return m
}

func TestRollbackBrowser_QuitReturnsCancelled(t *testing.T) {
	m := newTestBrowser([]rollbackdata.Script{
		{ID: "20260517T120000Z", Path: "/tmp/r1.sh"},
	})

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("expected Quit command")
	}
	mm := model.(*rollbackBrowserModel)
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

func TestRollbackBrowser_CtrlCQuits(t *testing.T) {
	m := newTestBrowser([]rollbackdata.Script{
		{ID: "20260517T120000Z", Path: "/tmp/r1.sh"},
	})

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("expected Quit command")
	}
	mm := model.(*rollbackBrowserModel)
	if !mm.done {
		t.Error("expected done after ctrl+c")
	}
	if !mm.cancelled {
		t.Error("expected cancelled after ctrl+c")
	}
}

func TestRollbackBrowser_EmptyList(t *testing.T) {
	m := newTestBrowser(nil) // no scripts

	if len(m.scripts) != 0 {
		t.Errorf("expected 0 scripts, got %d", len(m.scripts))
	}
	if len(m.items) != 0 {
		t.Errorf("expected 0 items, got %d", len(m.items))
	}
	if m.list.Index() != 0 {
		t.Errorf("empty list index should be 0, got %d", m.list.Index())
	}
}

func TestRollbackBrowser_ViewShowsHeader(t *testing.T) {
	m := newTestBrowser([]rollbackdata.Script{
		{ID: "20260517T120000Z", Path: "/tmp/r1.sh"},
		{ID: "20260517T110000Z", Path: "/tmp/r2.sh"},
	})
	m.width = 100
	m.height = 30

	v := m.View()
	if !strings.Contains(v, "Rollback Browser") {
		t.Errorf("View() should contain header 'Rollback Browser'; got:\n%s", v)
	}
}

func TestRollbackBrowser_SelectEntersConfirmMode(t *testing.T) {
	m := newTestBrowser([]rollbackdata.Script{
		{ID: "20260517T120000Z", Path: "/tmp/r1.sh"},
	})

	// Select via enter
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := model.(*rollbackBrowserModel)

	if cmd != nil {
		t.Errorf("expected no cmd after enter, got %v", cmd)
	}
	if !mm.confirming {
		t.Error("expected confirming=true after enter")
	}
	if !mm.items[0].active {
		t.Error("expected items[0].active=true after enter")
	}
}

func TestRollbackBrowser_EscCancelsConfirmMode(t *testing.T) {
	m := newTestBrowser([]rollbackdata.Script{
		{ID: "20260517T120000Z", Path: "/tmp/r1.sh"},
	})

	// Enter confirmation mode
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// Esc should cancel
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm := model.(*rollbackBrowserModel)

	if cmd != nil {
		t.Errorf("expected no cmd after esc, got %v", cmd)
	}
	if mm.confirming {
		t.Error("expected confirming=false after esc")
	}
}

func TestRollbackBrowser_ConfirmYExecutes(t *testing.T) {
	m := newTestBrowser([]rollbackdata.Script{
		{ID: "20260517T120000Z", Path: "/tmp/r1.sh"},
	})

	// Enter confirmation mode
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// Press 'y' to confirm
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	mm := model.(*rollbackBrowserModel)

	if cmd == nil {
		t.Fatal("expected Quit command after confirming")
	}
	if !mm.confirmed {
		t.Error("expected confirmed=true after y")
	}
	if !mm.done {
		t.Error("expected done=true after y")
	}
	if mm.cancelled {
		t.Error("expected cancelled=false after y")
	}
}

func TestRollbackBrowser_ConfirmNRejects(t *testing.T) {
	m := newTestBrowser([]rollbackdata.Script{
		{ID: "20260517T120000Z", Path: "/tmp/r1.sh"},
	})

	// Enter confirmation mode
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// Press 'n' to reject
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	mm := model.(*rollbackBrowserModel)

	if cmd != nil {
		t.Errorf("expected no cmd after n, got %v", cmd)
	}
	if mm.confirming {
		t.Error("expected confirming=false after n")
	}
	if mm.confirmed {
		t.Error("expected confirmed=false after n")
	}
}

func TestRollbackBrowser_ListNewestFirst(t *testing.T) {
	scripts := []rollbackdata.Script{
		{ID: "a", Path: "/tmp/a.sh"},
		{ID: "b", Path: "/tmp/b.sh"},
		{ID: "c", Path: "/tmp/c.sh"},
	}
	m := newTestBrowser(scripts)

	// Newest first means reversed: c, b, a
	if len(m.scripts) != 3 {
		t.Fatalf("expected 3 scripts, got %d", len(m.scripts))
	}
	if m.scripts[0].ID != "c" {
		t.Errorf("scripts[0].ID = %q, want %q", m.scripts[0].ID, "c")
	}
	if m.scripts[1].ID != "b" {
		t.Errorf("scripts[1].ID = %q, want %q", m.scripts[1].ID, "b")
	}
	if m.scripts[2].ID != "a" {
		t.Errorf("scripts[2].ID = %q, want %q", m.scripts[2].ID, "a")
	}
}

func TestRollbackBrowser_ScriptItemFilterValue(t *testing.T) {
	item := scriptItem{script: rollbackdata.Script{ID: "20260517T120000Z"}}
	if item.FilterValue() != "20260517T120000Z" {
		t.Errorf("FilterValue = %q, want ID", item.FilterValue())
	}
}

func TestRollbackBrowser_ScriptItemTitleActive(t *testing.T) {
	item := scriptItem{script: rollbackdata.Script{ID: "test-id"}, opCount: 3, active: true}
	title := item.Title()
	if !strings.HasPrefix(title, ">") {
		t.Errorf("active item Title should start with '>'; got %q", title)
	}
	if !strings.Contains(title, "test-id") {
		t.Errorf("title should contain ID; got %q", title)
	}
}

func TestRollbackBrowser_ScriptItemTitleInactive(t *testing.T) {
	item := scriptItem{script: rollbackdata.Script{ID: "test-id"}, opCount: 3, active: false}
	title := item.Title()
	if strings.HasPrefix(title, ">") {
		t.Errorf("inactive item Title should not start with '>'; got %q", title)
	}
}

func TestRollbackBrowser_ScriptItemDescription(t *testing.T) {
	item := scriptItem{script: rollbackdata.Script{ID: "x", Path: "/some/path.sh"}}
	if item.Description() != "/some/path.sh" {
		t.Errorf("Description = %q, want path", item.Description())
	}
}

func TestRollbackBrowser_Accessors(t *testing.T) {
	m := newTestBrowser([]rollbackdata.Script{
		{ID: "test-id", Path: "/tmp/test.sh"},
	})

	// Default state: no run target, cancelled, not confirmed
	if m.Cancelled() {
		t.Error("expected Cancelled()=false initially")
	}
	if m.Confirmed() {
		t.Error("expected Confirmed()=false initially")
	}
	if m.Err() != nil {
		t.Errorf("expected Err()=nil initially, got %v", m.Err())
	}

	// Select and confirm
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	if !m.Confirmed() {
		t.Error("expected Confirmed()=true after y")
	}
	if m.RunTarget().ID != "test-id" {
		t.Errorf("RunTarget().ID = %q, want %q", m.RunTarget().ID, "test-id")
	}
	if m.RunTarget().Path != "/tmp/test.sh" {
		t.Errorf("RunTarget().Path = %q, want %q", m.RunTarget().Path, "/tmp/test.sh")
	}
}

func TestRollbackBrowser_ViewShowsHelpBarInNormalMode(t *testing.T) {
	m := newTestBrowser([]rollbackdata.Script{
		{ID: "20260517T120000Z", Path: "/tmp/r1.sh"},
	})
	m.width = 100
	m.height = 30

	v := m.View()
	if !strings.Contains(v, "↑↓") {
		t.Errorf("View() should contain navigation hint; got:\n%s", v)
	}
	if !strings.Contains(v, "enter") {
		t.Errorf("View() should contain enter hint; got:\n%s", v)
	}
	if !strings.Contains(v, "q") {
		t.Errorf("View() should contain quit hint; got:\n%s", v)
	}
}

func TestRollbackBrowser_ViewShowsConfirmInConfirmMode(t *testing.T) {
	m := newTestBrowser([]rollbackdata.Script{
		{ID: "20260517T120000Z", Path: "/tmp/r1.sh"},
	})
	m.width = 100
	m.height = 30

	// Enter confirmation mode
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	v := m.View()
	if !strings.Contains(v, "execute this rollback script?") {
		t.Errorf("View() in confirm mode should show prompt; got:\n%s", v)
	}
}

func TestRollbackBrowser_DoneReturnsEmptyView(t *testing.T) {
	m := newTestBrowser([]rollbackdata.Script{
		{ID: "20260517T120000Z", Path: "/tmp/r1.sh"},
	})
	m.done = true
	if v := m.View(); v != "" {
		t.Errorf("View() after done should be empty; got %q", v)
	}
}

func TestRollbackBrowser_WindowSizeResizesPanes(t *testing.T) {
	m := newTestBrowser([]rollbackdata.Script{
		{ID: "20260517T120000Z", Path: "/tmp/r1.sh"},
	})

	model, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	mm := model.(*rollbackBrowserModel)

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

// TestRollbackBrowser_InitReturnsNil verifies Init() is a no-op.
func TestRollbackBrowser_InitReturnsNil(t *testing.T) {
	m := newTestBrowser(nil)
	cmd := m.Init()
	if cmd != nil {
		t.Errorf("Init() should return nil, got %v", cmd)
	}
}

// TestRollbackBrowser_DoneUpdateNoops checks that once done, further updates
// are ignored.
func TestRollbackBrowser_DoneUpdateNoops(t *testing.T) {
	m := newTestBrowser([]rollbackdata.Script{
		{ID: "20260517T120000Z", Path: "/tmp/r1.sh"},
	})
	m.done = true

	// Send a quit key — should be ignored because done is already true
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd != nil {
		t.Errorf("expected nil cmd when already done, got %v", cmd)
	}
	mm := model.(*rollbackBrowserModel)
	if !mm.done {
		t.Error("expected done to remain true")
	}
}

// TestRollbackBrowser_keyMatchQuit checks that both q and ctrl+c match
// the Quit key binding.
func TestRollbackBrowser_keyMatchQuit(t *testing.T) {
	m := newTestBrowser(nil)

	if !key.Matches(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}, m.keymap.Quit) {
		t.Error("q should match Quit binding")
	}
	if !key.Matches(tea.KeyMsg{Type: tea.KeyCtrlC}, m.keymap.Quit) {
		t.Error("ctrl+c should match Quit binding")
	}
}

// TestRollbackBrowser_keyMatchSelect checks that enter matches Select.
func TestRollbackBrowser_keyMatchSelect(t *testing.T) {
	m := newTestBrowser(nil)

	if !key.Matches(tea.KeyMsg{Type: tea.KeyEnter}, m.keymap.Select) {
		t.Error("enter should match Select binding")
	}
}

// TestRollbackBrowser_keyMatchBack checks that esc matches Back.
func TestRollbackBrowser_keyMatchBack(t *testing.T) {
	m := newTestBrowser(nil)

	if !key.Matches(tea.KeyMsg{Type: tea.KeyEsc}, m.keymap.Back) {
		t.Error("esc should match Back binding")
	}
}
