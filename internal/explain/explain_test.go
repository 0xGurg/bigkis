package explain

import (
	"strings"
	"testing"

	"codeberg.org/gurg/bigkis/internal/config"
	"codeberg.org/gurg/bigkis/internal/state"
)

// These tests focus on the pure-logic aspects of explain: declared/managed
// detection and status derivation. They never call out to the system, so the
// installed-side stays empty.

func TestInspect_DeclaredOnly(t *testing.T) {
	cfg := &config.Config{Pacman: config.Pacman{Packages: []string{"git"}}}
	r := Inspect("git", cfg, &state.State{})
	if len(r.Declared) != 1 || r.Declared[0].Plugin != "pacman" {
		t.Errorf("expected declared in pacman, got %v", r.Declared)
	}
	if !strings.Contains(r.StatusLine, "drift") {
		// Declared but not installed -> drift.
		t.Errorf("status = %q, want drift", r.StatusLine)
	}
}

func TestInspect_IgnoredShortCircuits(t *testing.T) {
	cfg := &config.Config{Pacman: config.Pacman{Ignored: []string{"opendoas"}}}
	r := Inspect("opendoas", cfg, &state.State{})
	if len(r.Ignored) == 0 {
		t.Fatal("expected ignored entry")
	}
	if !strings.Contains(r.StatusLine, "ignored") {
		t.Errorf("status = %q, want ignored", r.StatusLine)
	}
}

func TestInspect_NodePackageOverride(t *testing.T) {
	cfg := &config.Config{
		Node: config.Node{
			Package: []config.NodePackage{{Name: "@vue/cli", Manager: "yarn"}},
		},
	}
	r := Inspect("@vue/cli", cfg, &state.State{})
	if len(r.Declared) == 0 {
		t.Fatal("expected declared from [[node.package]]")
	}
	if r.Declared[0].Plugin != "node" {
		t.Errorf("plugin = %q, want node", r.Declared[0].Plugin)
	}
}

func TestInspect_ManagedPreviouslyButNoLongerDeclared(t *testing.T) {
	st := &state.State{}
	if err := st.Set("pacman", []string{"git"}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	r := Inspect("git", cfg, st)
	if len(r.Managed) == 0 {
		t.Errorf("expected managed entry, got %v", r.Managed)
	}
	if !strings.Contains(r.StatusLine, "no longer declared") {
		t.Errorf("status = %q, want 'no longer declared'", r.StatusLine)
	}
}

func TestInspect_RenderIncludesAllSections(t *testing.T) {
	cfg := &config.Config{Pacman: config.Pacman{Packages: []string{"git"}}}
	r := Inspect("git", cfg, &state.State{})
	out := r.Render()
	for _, want := range []string{"package: git", "declared:", "installed:", "managed:", "status:"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q in:\n%s", want, out)
		}
	}
}

func TestDerive_AllBranches(t *testing.T) {
	cases := []struct {
		name     string
		r        Result
		contains string
	}{
		{"ignored", Result{Ignored: []string{"pacman"}}, "ignored"},
		{"in sync", Result{Declared: []Source{{Plugin: "pacman"}}, Installed: []Install{{Plugin: "pacman"}}}, "in sync"},
		{"drift declared", Result{Declared: []Source{{Plugin: "pacman"}}}, "declared but not installed"},
		{"drift removed", Result{Managed: []string{"pacman"}, Installed: []Install{{Plugin: "pacman"}}}, "no longer declared"},
		{"unmanaged", Result{Installed: []Install{{Plugin: "pacman"}}}, "unmanaged"},
		{"unknown", Result{}, "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := derive(c.r)
			if !strings.Contains(got, c.contains) {
				t.Errorf("derive = %q, want substring %q", got, c.contains)
			}
		})
	}
}
