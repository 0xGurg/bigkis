package components

import (
	"strings"
	"testing"
)

func TestNewHelpBar_noBindings(t *testing.T) {
	t.Parallel()
	bar := NewHelpBar()
	v := bar.View()
	if v != "" {
		t.Errorf("empty HelpBar should render empty string, got %q", v)
	}
}

func TestHelpBar_singleBinding(t *testing.T) {
	t.Parallel()
	bar := NewHelpBar(HelpBinding{Key: "q", Desc: "quit"})
	v := bar.View()
	if !strings.Contains(v, "[q]") || !strings.Contains(v, "quit") {
		t.Errorf("View() should show binding; got %q", v)
	}
}

func TestHelpBar_multipleBindings(t *testing.T) {
	t.Parallel()
	bar := NewHelpBar(
		HelpBinding{Key: "space", Desc: "toggle"},
		HelpBinding{Key: "enter", Desc: "confirm"},
		HelpBinding{Key: "q", Desc: "quit"},
	)
	v := bar.View()
	for _, want := range []string{"space", "toggle", "enter", "confirm", "q", "quit"} {
		if !strings.Contains(v, want) {
			t.Errorf("View() should contain %q; got %q", want, v)
		}
	}
}

func TestHelpBar_setBindings(t *testing.T) {
	t.Parallel()
	bar := NewHelpBar(HelpBinding{Key: "q", Desc: "quit"})
	bar.SetBindings(HelpBinding{Key: "a", Desc: "all"}, HelpBinding{Key: "n", Desc: "none"})
	v := bar.View()
	if !strings.Contains(v, "[a]") || !strings.Contains(v, "all") {
		t.Errorf("View() should show new bindings; got %q", v)
	}
	if strings.Contains(v, "quit") {
		t.Errorf("View() should not contain old binding 'quit'; got %q", v)
	}
}

func TestHelpBar_emptyAfterSetBindingsEmpty(t *testing.T) {
	t.Parallel()
	bar := NewHelpBar(HelpBinding{Key: "q", Desc: "quit"})
	bar.SetBindings() // clear
	if v := bar.View(); v != "" {
		t.Errorf("after clearing bindings, View() should be empty; got %q", v)
	}
}
