package plan

import (
	"reflect"
	"testing"
)

func TestCompute_FirstRunNeverRemoves(t *testing.T) {
	// On a fresh system with no recorded state, even if "vim" is installed
	// and not declared, we must NOT remove it. Only additions are produced.
	d := Compute(
		[]string{"git", "neovim"}, // declared
		[]string{"vim", "git"},    // actual on system
		nil,                       // no prior state
		nil,                       // no ignored
	)
	if !reflect.DeepEqual(d.Add, []string{"neovim"}) {
		t.Errorf("Add = %v, want [neovim]", d.Add)
	}
	if len(d.Remove) != 0 {
		t.Errorf("Remove = %v, want empty (first run safety)", d.Remove)
	}
	if !reflect.DeepEqual(d.Keep, []string{"git"}) {
		t.Errorf("Keep = %v, want [git]", d.Keep)
	}
}

func TestCompute_RemovalsScopedToLastApplied(t *testing.T) {
	// User declared git+vim before. Now declares only git. vim is still
	// installed. Both git and a manually-installed `htop` are in actual.
	// We must remove vim (we previously declared it) but leave htop alone.
	d := Compute(
		[]string{"git"},
		[]string{"git", "vim", "htop"},
		[]string{"git", "vim"},
		nil,
	)
	if !reflect.DeepEqual(d.Remove, []string{"vim"}) {
		t.Errorf("Remove = %v, want [vim]", d.Remove)
	}
	if len(d.Add) != 0 {
		t.Errorf("Add = %v, want empty", d.Add)
	}
}

func TestCompute_IgnoredNeverAddedOrRemoved(t *testing.T) {
	d := Compute(
		[]string{"git", "yay"},
		[]string{"yay"},
		[]string{"git", "yay"},
		[]string{"yay"},
	)
	for _, name := range d.Add {
		if name == "yay" {
			t.Errorf("yay should not be added when ignored")
		}
	}
	for _, name := range d.Remove {
		if name == "yay" {
			t.Errorf("yay should not be removed when ignored")
		}
	}
}

func TestCompute_RemovalNotIfNoLongerInstalled(t *testing.T) {
	// Previously declared git, now removed from declaration, but the user
	// already uninstalled it manually. Don't try to remove it again.
	d := Compute(nil, nil, []string{"git"}, nil)
	if len(d.Remove) != 0 {
		t.Errorf("Remove = %v, want empty (already gone)", d.Remove)
	}
}

func TestCompute_EmptyLastAppliedIsNotFirstRun(t *testing.T) {
	// A non-nil but empty lastApplied means "we previously declared
	// nothing." It must still be treated as a known state (no removals
	// because no candidates).
	d := Compute(
		[]string{"git"},
		[]string{"vim", "git"},
		[]string{}, // non-nil empty
		nil,
	)
	if len(d.Remove) != 0 {
		t.Errorf("Remove = %v, want empty", d.Remove)
	}
	if !reflect.DeepEqual(d.Add, []string(nil)) && !reflect.DeepEqual(d.Add, []string{}) {
		// "git" is already installed → no add
		if len(d.Add) != 0 {
			t.Errorf("Add = %v, want empty", d.Add)
		}
	}
}

func TestCompute_DeterministicSort(t *testing.T) {
	d := Compute(
		[]string{"c", "a", "b"},
		nil,
		nil,
		nil,
	)
	if !reflect.DeepEqual(d.Add, []string{"a", "b", "c"}) {
		t.Errorf("Add not sorted: %v", d.Add)
	}
}

func TestCompute_DuplicateInputsAreDeduped(t *testing.T) {
	d := Compute(
		[]string{"git", "git", "neovim"},
		nil,
		nil,
		nil,
	)
	if len(d.Add) != 2 {
		t.Errorf("Add = %v, want 2 unique entries", d.Add)
	}
}

func TestCompute_EmptyStringsAreIgnored(t *testing.T) {
	d := Compute(
		[]string{"", "git", ""},
		[]string{""},
		nil,
		nil,
	)
	for _, x := range d.Add {
		if x == "" {
			t.Errorf("empty string slipped into Add")
		}
	}
	if len(d.Add) != 1 {
		t.Errorf("Add = %v, want [git]", d.Add)
	}
}

func TestDiff_HasChanges(t *testing.T) {
	cases := []struct {
		d    Diff
		want bool
	}{
		{Diff{}, false},
		{Diff{Add: []string{"x"}}, true},
		{Diff{Remove: []string{"x"}}, true},
		{Diff{Keep: []string{"x"}}, false},
	}
	for i, c := range cases {
		if got := c.d.HasChanges(); got != c.want {
			t.Errorf("case %d: HasChanges() = %v, want %v", i, got, c.want)
		}
	}
}
