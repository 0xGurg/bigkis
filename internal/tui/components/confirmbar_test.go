package components

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNewConfirmBar_defaultPrompt(t *testing.T) {
	t.Parallel()
	bar := NewConfirmBar("", false)
	if bar.Prompt != "" {
		t.Errorf("expected empty prompt, got %q", bar.Prompt)
	}

	v := bar.View()
	if !strings.Contains(v, "proceed?") {
		t.Errorf("View() without prompt should show default 'proceed?'")
	}
	if !strings.Contains(v, "y") || !strings.Contains(v, "N") || !strings.Contains(v, "q") {
		t.Errorf("View() should show y/N/q keys")
	}
}

func TestNewConfirmBar_customPrompt(t *testing.T) {
	t.Parallel()
	bar := NewConfirmBar("install packages?", false)
	v := bar.View()
	if !strings.Contains(v, "install packages?") {
		t.Errorf("View() should show custom prompt; got %q", v)
	}
}

func TestConfirmBar_initialState(t *testing.T) {
	t.Parallel()
	bar := NewConfirmBar("test", false)
	if bar.Result() != ConfirmNone {
		t.Errorf("expected ConfirmNone, got %d", bar.Result())
	}
}

func TestConfirmBar_keyY(t *testing.T) {
	t.Parallel()
	bar := NewConfirmBar("test", false)
	result := bar.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if result != ConfirmYes {
		t.Errorf("key 'y': expected ConfirmYes, got %d", result)
	}
}

func TestConfirmBar_keyUpperY(t *testing.T) {
	t.Parallel()
	bar := NewConfirmBar("test", false)
	result := bar.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
	if result != ConfirmYes {
		t.Errorf("key 'Y': expected ConfirmYes, got %d", result)
	}
}

func TestConfirmBar_keyN(t *testing.T) {
	t.Parallel()
	bar := NewConfirmBar("test", false)
	result := bar.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if result != ConfirmNo {
		t.Errorf("key 'n': expected ConfirmNo, got %d", result)
	}
}

func TestConfirmBar_keyUpperN(t *testing.T) {
	t.Parallel()
	bar := NewConfirmBar("test", false)
	result := bar.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	if result != ConfirmNo {
		t.Errorf("key 'N': expected ConfirmNo, got %d", result)
	}
}

func TestConfirmBar_keyQ(t *testing.T) {
	t.Parallel()
	bar := NewConfirmBar("test", false)
	result := bar.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if result != ConfirmQuit {
		t.Errorf("key 'q': expected ConfirmQuit, got %d", result)
	}
}

func TestConfirmBar_keyCtrlC(t *testing.T) {
	t.Parallel()
	bar := NewConfirmBar("test", false)
	result := bar.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if result != ConfirmQuit {
		t.Errorf("ctrl+c: expected ConfirmQuit, got %d", result)
	}
}

func TestConfirmBar_unknownKeyReturnsNone(t *testing.T) {
	t.Parallel()
	bar := NewConfirmBar("test", false)
	result := bar.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if result != ConfirmNone {
		t.Errorf("unknown key: expected ConfirmNone, got %d", result)
	}
}

func TestConfirmBar_onceDecidedIgnoresFurtherKeys(t *testing.T) {
	t.Parallel()
	bar := NewConfirmBar("test", false)
	bar.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	// now try pressing 'y'
	result := bar.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if result != ConfirmNo {
		t.Errorf("after 'n', key 'y': expected still ConfirmNo, got %d", result)
	}
}

func TestConfirmBar_reset(t *testing.T) {
	t.Parallel()
	bar := NewConfirmBar("test", false)
	bar.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if bar.Result() != ConfirmYes {
		t.Fatal("setup failed: expected ConfirmYes")
	}
	bar.Reset()
	if bar.Result() != ConfirmNone {
		t.Errorf("after Reset(): expected ConfirmNone, got %d", bar.Result())
	}
	// should be usable again
	bar.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if bar.Result() != ConfirmNo {
		t.Errorf("after Reset()+press 'n': expected ConfirmNo, got %d", bar.Result())
	}
}

func TestConfirmBar_viewEmptyAfterDecision(t *testing.T) {
	t.Parallel()
	bar := NewConfirmBar("test", false)
	bar.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if v := bar.View(); v != "" {
		t.Errorf("View() after decision should be empty, got %q", v)
	}
}

// assumeYes

func TestConfirmBar_assumeYes(t *testing.T) {
	t.Parallel()
	bar := NewConfirmBar("test", true)
	if bar.Result() != ConfirmYes {
		t.Errorf("assumeYes: expected ConfirmYes, got %d", bar.Result())
	}
	if v := bar.View(); v != "" {
		t.Errorf("assumeYes: View() should be empty, got %q", v)
	}
}

func TestConfirmBar_assumeYesIgnoresInput(t *testing.T) {
	t.Parallel()
	bar := NewConfirmBar("test", true)
	// pressing 'q' should have no effect
	result := bar.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if result != ConfirmYes {
		t.Errorf("assumeYes + 'q': expected ConfirmYes, got %d", result)
	}
}
