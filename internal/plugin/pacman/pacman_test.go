package pacman

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/0xGurg/bigkis/internal/config"
	"github.com/0xGurg/bigkis/internal/plugin"
	"github.com/0xGurg/bigkis/internal/runner"
	"github.com/0xGurg/bigkis/internal/state"
	"github.com/0xGurg/bigkis/internal/ui"
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
		if name == "pacman" && len(args) == 1 {
			switch args[0] {
			case "-Qqen":
				return "neovim\n", "", 0, nil
			case "-Qq":
				return "neovim\n", "", 0, nil
			}
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
		if name == "pacman" && len(args) == 1 {
			switch args[0] {
			case "-Qqen", "-Qq":
				return "", "", 0, nil
			}
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

func TestApply_AcceptsSubsetReport(t *testing.T) {
	stubLookPath(t)
	planF := runner.NewFake()
	planF.Respond = func(name string, args []string) (string, string, int, error) {
		if name == "pacman" && len(args) == 1 {
			switch args[0] {
			case "-Qqen", "-Qq":
				return "", "", 0, nil
			}
		}
		return "", "", 0, nil
	}
	p := New()
	p.SetRunner(planF.Runner)
	cfg := &config.Config{
		Pacman:   config.Pacman{Packages: []string{"git", "neovim"}},
		Settings: config.Settings{PruneOrphans: config.PruneOrphansNone},
	}
	report, err := p.Plan(cfg, &state.State{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(report.Operations) < 2 {
		t.Fatalf("expected at least 2 ops, got %d", len(report.Operations))
	}
	// Trim to a subset (keep only the first op, discard the rest).
	report.Operations = report.Operations[:1]

	applyF := runner.NewFake()
	if err := p.Apply(cfg, &state.State{}, report, applyF.Runner, silentUI()); err != nil {
		t.Fatalf("Apply with subset report should succeed, got: %v", err)
	}
}

func TestApply_DemotesBeforeInstalling(t *testing.T) {
	// When replacing "nvidia" with "nvidia-dkms", the demote+prune must
	// happen before the install so the conflicting package is gone.
	stubLookPath(t)
	planF := runner.NewFake()
	planF.Respond = func(name string, args []string) (string, string, int, error) {
		if name == "pacman" && len(args) == 1 {
			switch args[0] {
			case "-Qqen":
				return "nvidia\n", "", 0, nil
			case "-Qq":
				return "nvidia\n", "", 0, nil
			}
		}
		return "", "", 0, nil
	}
	p := New()
	p.SetRunner(planF.Runner)

	cfg := &config.Config{
		Pacman:   config.Pacman{Packages: []string{"nvidia-dkms"}},
		Settings: config.Settings{PruneOrphans: config.PruneOrphansScoped},
	}
	st := &state.State{}
	if err := st.Set("pacman", []string{"nvidia"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	report, err := p.Plan(cfg, st)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	applyF := runner.NewFake()
	calls := 0
	applyF.Respond = func(name string, args []string) (string, string, int, error) {
		if name == "pacman" && len(args) == 1 && args[0] == "-Qdtq" {
			calls++
			switch calls {
			case 1:
				return "nvidia\n", "", 0, nil // pre-existing orphan
			default:
				return "", "", 1, runner.NewExitError(1, "exit status 1")
			}
		}
		return "", "", 0, nil
	}

	if err := p.Apply(cfg, st, report, applyF.Runner, silentUI()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Find the -D --asdeps and -S calls and verify order.
	var demoteIdx, installIdx int = -1, -1
	for i, c := range applyF.Calls {
		if len(c.Args) > 0 && c.Args[0] == "-D" {
			demoteIdx = i
		}
		if len(c.Args) > 0 && c.Args[0] == "-S" {
			installIdx = i
		}
	}
	if demoteIdx == -1 {
		t.Fatal("expected pacman -D --asdeps call")
	}
	if installIdx == -1 {
		t.Fatal("expected pacman -S call")
	}
	if demoteIdx > installIdx {
		t.Errorf("demote (index %d) should come before install (index %d)", demoteIdx, installIdx)
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

func TestPlan_TracksDependencyOnlyPackagesWithoutOperation(t *testing.T) {
	stubLookPath(t)
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		if name == "pacman" && len(args) == 1 {
			switch args[0] {
			case "-Qqen":
				return "", "", 0, nil
			case "-Qq":
				return "qt6-svg\n", "", 0, nil
			}
		}
		return "", "", 0, nil
	}

	p := New()
	p.SetRunner(f.Runner)
	cfg := &config.Config{Pacman: config.Pacman{Packages: []string{"qt6-svg"}}}
	report, err := p.Plan(cfg, &state.State{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(report.Operations) != 0 {
		t.Fatalf("expected no executable ops, got %+v", report.Operations)
	}
	if len(p.cachedDiff.Add) != 0 {
		t.Fatalf("expected no install adds, got %v", p.cachedDiff.Add)
	}
	if !reflect.DeepEqual(p.DependencyInstalled(), []string{"qt6-svg"}) {
		t.Fatalf("DependencyInstalled = %v", p.DependencyInstalled())
	}
}

func TestApply_DoesNotPromoteDependencyOnlyPackages(t *testing.T) {
	stubLookPath(t)
	planF := runner.NewFake()
	planF.Respond = func(name string, args []string) (string, string, int, error) {
		if name == "pacman" && len(args) == 1 {
			switch args[0] {
			case "-Qqen":
				return "", "", 0, nil
			case "-Qq":
				return "qt6-svg\n", "", 0, nil
			}
		}
		return "", "", 0, nil
	}

	p := New()
	p.SetRunner(planF.Runner)
	cfg := &config.Config{
		Pacman:   config.Pacman{Packages: []string{"qt6-svg"}},
		Settings: config.Settings{PruneOrphans: config.PruneOrphansNone},
	}
	report, err := p.Plan(cfg, &state.State{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	applyF := runner.NewFake()
	if err := p.Apply(cfg, &state.State{}, report, applyF.Runner, silentUI()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(applyF.Calls) != 0 {
		t.Fatalf("expected no pacman calls, got %+v", applyF.Calls)
	}
}

func TestPendingUpgrades_ParsesOutput(t *testing.T) {
	stubLookPath(t)
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		if name == "pacman" && len(args) >= 1 && args[0] == "-Qu" {
			return "neovim 0.9.5-1 -> 0.10.0-1\ngit 2.44.0-1 -> 2.45.0-1\n", "", 0, nil
		}
		return "", "", 0, nil
	}
	p := New()
	rep, err := p.PendingUpgrades(&config.Config{}, f.Runner)
	if err != nil {
		t.Fatalf("PendingUpgrades: %v", err)
	}
	if rep.Plugin != "pacman" {
		t.Errorf("Plugin = %q, want pacman", rep.Plugin)
	}
	if len(rep.Operations) != 2 {
		t.Fatalf("got %d ops, want 2", len(rep.Operations))
	}
	if rep.Operations[0].Kind != plugin.OpUpdate {
		t.Error("op should be OpUpdate")
	}
	if rep.Operations[0].Target != "neovim" {
		t.Errorf("Target = %q", rep.Operations[0].Target)
	}
	if !strings.Contains(rep.Operations[0].Detail, "0.9.5-1 -> 0.10.0-1") {
		t.Error("missing version detail")
	}
	if rep.Operations[1].Target != "git" {
		t.Errorf("Target = %q", rep.Operations[1].Target)
	}
	if !rep.HasUpgrades() {
		t.Error("HasUpgrades should be true")
	}
}

func TestPendingUpgrades_StaleDbReturnsEmptyReport(t *testing.T) {
	stubLookPath(t)
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		if name == "pacman" && len(args) >= 1 && args[0] == "-Qu" {
			// Exit 1 with empty stdout = stale DB
			return "", "", 1, runner.NewExitError(1, "exit status 1")
		}
		return "", "", 0, nil
	}
	p := New()
	rep, err := p.PendingUpgrades(&config.Config{}, f.Runner)
	if err != nil {
		t.Fatalf("stale DB should not be an error: %v", err)
	}
	if len(rep.Operations) != 0 {
		t.Errorf("expected 0 ops for stale DB, got %d", len(rep.Operations))
	}
	if rep.HasUpgrades() {
		t.Error("HasUpgrades should be false for stale DB")
	}
}

func TestPendingUpgrades_NoUpgradesReturnsEmptyReport(t *testing.T) {
	stubLookPath(t)
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		if name == "pacman" && len(args) >= 1 && args[0] == "-Qu" {
			return "", "", 0, nil
		}
		return "", "", 0, nil
	}
	p := New()
	rep, err := p.PendingUpgrades(&config.Config{}, f.Runner)
	if err != nil {
		t.Fatalf("PendingUpgrades: %v", err)
	}
	if len(rep.Operations) != 0 {
		t.Errorf("expected 0 ops, got %d", len(rep.Operations))
	}
	if rep.HasUpgrades() {
		t.Error("HasUpgrades should be false")
	}
}

func TestPendingUpgrades_CommandFailsReturnsError(t *testing.T) {
	stubLookPath(t)
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		if name == "pacman" && len(args) >= 1 && args[0] == "-Qu" {
			return "some error output", "", 127, runner.NewExitError(127, "command failed")
		}
		return "", "", 0, nil
	}
	p := New()
	_, err := p.PendingUpgrades(&config.Config{}, f.Runner)
	if err == nil {
		t.Fatal("expected error for exit 127")
	}
}
