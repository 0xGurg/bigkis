package aur

import (
	"bytes"
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

func silentUI() *ui.UI { return ui.New(io.Discard, &bytes.Buffer{}, false, true) }

// stubLookPath replaces runner.LookPath so HasCommand returns true (or per-name
// behavior) without consulting the real PATH.
func stubLookPath(t *testing.T, available map[string]bool) {
	t.Helper()
	prev := runner.LookPath
	runner.LookPath = func(name string) (string, error) {
		if available == nil || available[name] {
			return "/usr/bin/" + name, nil
		}
		return "", &lookErr{name}
	}
	t.Cleanup(func() { runner.LookPath = prev })
}

type lookErr struct{ name string }

func (e *lookErr) Error() string {
	return "exec: \"" + e.name + "\": executable file not found in $PATH"
}

func TestAvailable_RejectsMissingHelper(t *testing.T) {
	// pacman is present but yay is not.
	stubLookPath(t, map[string]bool{"pacman": true, "yay": false})
	a := New()
	cfg := &config.Config{Settings: config.Settings{AURHelper: "yay"}}
	if err := a.Available(cfg); err == nil || !strings.Contains(err.Error(), "yay") {
		t.Errorf("expected helper-missing error, got %v", err)
	}
}

func TestAvailable_AcceptsHelperOnPath(t *testing.T) {
	stubLookPath(t, map[string]bool{"pacman": true, "yay": true})
	a := New()
	cfg := &config.Config{Settings: config.Settings{AURHelper: "yay"}}
	if err := a.Available(cfg); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestProbe_NoForeignPackagesIsExitOne(t *testing.T) {
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		// Simulate pacman -Qqm exit 1 with empty stdout (no foreign packages).
		return "", "", 1, runner.NewExitError(1, "exit status 1")
	}
	a := New()
	got, err := a.Probe(f.Runner)
	if err != nil {
		t.Errorf("RC=1 should be 'no foreign packages', got error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty list, got %v", got)
	}
}

func TestApply_RejectsCallBeforePlan(t *testing.T) {
	a := New()
	err := a.Apply(&config.Config{}, &state.State{}, plugin.Report{}, runner.NewFake().Runner, silentUI())
	if err == nil || !strings.Contains(err.Error(), "before Plan") {
		t.Errorf("expected before-Plan error, got %v", err)
	}
}

func TestApply_UsesConfiguredHelper(t *testing.T) {
	stubLookPath(t, map[string]bool{"pacman": true, "paru": true})
	planF := runner.NewFake()
	planF.Respond = func(name string, args []string) (string, string, int, error) {
		// Empty foreign-package list, so all declared packages are adds.
		return "", "", 1, runner.NewExitError(1, "exit status 1")
	}
	a := New()
	a.SetRunner(planF.Runner)
	cfg := &config.Config{
		Settings: config.Settings{AURHelper: "paru"},
		AUR:      config.AUR{Packages: []string{"yay-bin"}},
	}
	report, err := a.Plan(cfg, &state.State{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	applyF := runner.NewFake()
	if err := a.Apply(cfg, &state.State{}, report, applyF.Runner, silentUI()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(applyF.Calls) != 1 {
		t.Fatalf("expected 1 call, got %+v", applyF.Calls)
	}
	if applyF.Calls[0].Name != "paru" || applyF.Calls[0].Sudo {
		t.Errorf("expected unsudoed paru, got %+v", applyF.Calls[0])
	}
}
