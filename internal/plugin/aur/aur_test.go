package aur

import (
	"reflect"
	"testing"
)

func TestSplitLines_TakesFirstField(t *testing.T) {
	// `pacman -Qm` (without -q) prints "name version"; we must take only
	// the package name.
	in := "fnm-bin 1.34.2-1\nvisual-studio-code-bin 1.85.0-1\n"
	got := splitLines(in)
	want := []string{"fnm-bin", "visual-studio-code-bin"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSplitLines_QuietForm(t *testing.T) {
	in := "fnm-bin\nvisual-studio-code-bin\n"
	got := splitLines(in)
	want := []string{"fnm-bin", "visual-studio-code-bin"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDedupAndFilter(t *testing.T) {
	got := dedupAndFilter(
		[]string{"fnm-bin", "yay", "fnm-bin"},
		[]string{"yay"},
	)
	want := []string{"fnm-bin"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
