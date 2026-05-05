package pacman

import (
	"reflect"
	"testing"
)

func TestSplitLines(t *testing.T) {
	in := "git\nneovim\n\nwget\n"
	got := splitLines(in)
	want := []string{"git", "neovim", "wget"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSplitLines_Empty(t *testing.T) {
	if got := splitLines(""); len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestSplitLines_TrimsWhitespace(t *testing.T) {
	in := "  git  \n  \nneovim\t\n"
	got := splitLines(in)
	if !reflect.DeepEqual(got, []string{"git", "neovim"}) {
		t.Errorf("got %v", got)
	}
}

func TestDedupAndFilter(t *testing.T) {
	got := dedupAndFilter(
		[]string{"git", "yay", "git", "neovim", "yay"},
		[]string{"yay"},
	)
	want := []string{"git", "neovim"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDedupAndFilter_OrderPreserved(t *testing.T) {
	got := dedupAndFilter([]string{"c", "a", "b", "a"}, nil)
	want := []string{"c", "a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order not preserved: %v", got)
	}
}
