package pacman

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"codeberg.org/gurg/bigkis/internal/config"
	"codeberg.org/gurg/bigkis/internal/plugin"
	"codeberg.org/gurg/bigkis/internal/runner"
	"codeberg.org/gurg/bigkis/internal/state"
	"codeberg.org/gurg/bigkis/internal/ui"
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

// silentUI returns a UI that discards output. Tests use this so they can
// assert on argv without log noise; warnings still go to /dev/null.
func silentUI() *ui.UI { return ui.New(io.Discard, &bytes.Buffer{}, false, true) }

// stubLookPath replaces runner.LookPath for the duration of t so HasCommand
// returns true for any binary name without consulting the real PATH.
func stubLookPath(t *testing.T) {
	t.Helper()
	prev := runner.LookPath
	runner.LookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	t.Cleanup(func() { runner.LookPath = prev })
}

func TestPlan_RemovalRequiresLastApplied(t *testing.T) {
	stubLookPath(t)
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		if name == "pacman" && len(args) == 1 && args[0] == "-Qqen" {
			// Both packages are installed but bigkis has no prior state, so
			// no removals should be planned (first-run safety).
			return "git\nneovim\n", "", 0, nil
		}
		return "", "", 0, nil
	}

	p := New()
	p.SetRunner(f.Runner)
	cfg := &config.Config{Pacman: config.Pacman{Packages: []string{"git"}}}
	st := &state.State{}
	report, err := p.Plan(cfg, st)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(report.Operations) != 0 {
		t.Errorf("first-run with no lastApplied should not propose removals, got %+v", report.Operations)
	}
}

func TestPlan_AddAndRemoveAfterPriorState(t *testing.T) {
	stubLookPath(t)
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		if name == "pacman" && len(args) == 1 && args[0] == "-Qqen" {
			return "neovim\n", "", 0, nil
		}
		return "", "", 0, nil
	}
	p := New()
	p.SetRunner(f.Runner)

	cfg := &config.Config{Pacman: config.Pacman{Packages: []string{"git"}}}
	st := &state.State{}
	if err := st.Set("pacman", []string{"neovim"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	report, err := p.Plan(cfg, st)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	gotKinds := map[string]string{}
	for _, op := range report.Operations {
		k := "add"
		if op.Kind == plugin.OpRemove {
			k = "remove"
		}
		gotKinds[op.Target] = k
	}
	if gotKinds["git"] != "add" || gotKinds["neovim"] != "remove" {
		t.Errorf("unexpected ops: %+v", report.Operations)
	}
}

func TestApply_RejectsCallBeforePlan(t *testing.T) {
	p := New()
	err := p.Apply(&config.Config{}, &state.State{}, plugin.Report{}, runner.NewFake().Runner, silentUI())
	if err == nil || !strings.Contains(err.Error(), "before Plan") {
		t.Errorf("expected before-Plan error, got %v", err)
	}
}

func TestApply_AddIssuesPacmanS(t *testing.T) {
	stubLookPath(t)
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		if name == "pacman" && len(args) == 1 && args[0] == "-Qqen" {
			return "", "", 0, nil
		}
		return "", "", 0, nil
	}
	p := New()
	p.SetRunner(f.Runner)
	cfg := &config.Config{
		Pacman:   config.Pacman{Packages: []string{"git"}},
		Settings: config.Settings{PruneOrphans: config.PruneOrphansScoped},
	}
	report, err := p.Plan(cfg, &state.State{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Apply uses a separate runner so the probe calls and apply calls don't
	// share Calls; that mirrors how main.go wires things.
	applyR := runner.NewFake()
	if err := p.Apply(cfg, &state.State{}, report, applyR.Runner, silentUI()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(applyR.Calls) != 1 {
		t.Fatalf("expected 1 call (pacman -S), got %d: %+v", len(applyR.Calls), applyR.Calls)
	}
	got := applyR.Calls[0]
	if got.Name != "pacman" || !got.Sudo {
		t.Errorf("expected sudo pacman, got %+v", got)
	}
	wantArgs := []string{"-S", "--needed", "--noconfirm", "git"}
	if !reflect.DeepEqual(got.Args, wantArgs) {
		t.Errorf("argv = %v, want %v", got.Args, wantArgs)
	}
}

func TestApply_RemoveDemotesAndScopedPrunes(t *testing.T) {
	stubLookPath(t)
	planF := runner.NewFake()
	planF.Respond = func(name string, args []string) (string, string, int, error) {
		if name == "pacman" && len(args) == 1 && args[0] == "-Qqen" {
			return "git\nneovim\n", "", 0, nil
		}
		return "", "", 0, nil
	}
	p := New()
	p.SetRunner(planF.Runner)

	cfg := &config.Config{
		Pacman:   config.Pacman{Packages: []string{"git"}},
		Settings: config.Settings{PruneOrphans: config.PruneOrphansScoped},
	}
	st := &state.State{}
	if err := st.Set("pacman", []string{"git", "neovim"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	report, err := p.Plan(cfg, st)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	applyF := runner.NewFake()
	// Sequence: -Qdtq (preExisting), -D --asdeps, -Qdtq, -Rns ...
	calls := 0
	applyF.Respond = func(name string, args []string) (string, string, int, error) {
		if name == "pacman" && len(args) == 1 && args[0] == "-Qdtq" {
			calls++
			switch calls {
			case 1:
				// Pre-existing orphan: leftover-orphan was already an orphan
				// before this apply, scoped prune must NOT remove it.
				return "leftover-orphan\n", "", 0, nil
			case 2:
				// After demote: previous + neovim now orphaned.
				return "leftover-orphan\nneovim\n", "", 0, nil
			default:
				// Loop termination: only the pre-existing orphan remains.
				return "leftover-orphan\n", "", 0, nil
			}
		}
		return "", "", 0, nil
	}

	if err := p.Apply(cfg, st, report, applyF.Runner, silentUI()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Find the -Rns call and confirm it ONLY contains neovim, not the
	// pre-existing orphan.
	var rns *runner.FakeCall
	for i, c := range applyF.Calls {
		if len(c.Args) > 0 && c.Args[0] == "-Rns" {
			rns = &applyF.Calls[i]
			break
		}
	}
	if rns == nil {
		t.Fatalf("expected pacman -Rns call, got %+v", applyF.Calls)
	}
	wantArgs := []string{"-Rns", "--noconfirm", "neovim"}
	if !reflect.DeepEqual(rns.Args, wantArgs) {
		t.Errorf("scoped prune leaked: argv=%v, want %v", rns.Args, wantArgs)
	}
}

func TestPruneOrphans_NoOrphansExitOneIsNotAnError(t *testing.T) {
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		// Mimic pacman -Qdtq returning exit 1 with empty output.
		return "", "", 1, runner.NewExitError(1, "exit status 1")
	}
	if err := pruneOrphans(f.Runner, nil); err != nil {
		t.Errorf("RC=1 should be treated as 'no orphans', got error: %v", err)
	}
}

func TestPruneOrphans_OtherExitCodePropagates(t *testing.T) {
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		return "", "database lock", 2, runner.NewExitError(2, "exit status 2")
	}
	err := pruneOrphans(f.Runner, nil)
	if err == nil {
		t.Fatal("expected error for RC=2")
	}
	if errors.Is(err, nil) {
		t.Fatal("expected wrapped error")
	}
}

func TestUpgrade_RunsSyu(t *testing.T) {
	stubLookPath(t)
	p := New()
	f := runner.NewFake()
	if err := p.Upgrade(&config.Config{}, &state.State{}, f.Runner, silentUI()); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if len(f.Calls) != 1 {
		t.Fatalf("expected 1 call, got %+v", f.Calls)
	}
	c := f.Calls[0]
	if c.Name != "pacman" || !c.Sudo || c.User != "" {
		t.Errorf("unexpected call: %+v", c)
	}
	want := []string{"-Syu", "--noconfirm"}
	if !reflect.DeepEqual(c.Args, want) {
		t.Errorf("argv = %v, want %v", c.Args, want)
	}
}
