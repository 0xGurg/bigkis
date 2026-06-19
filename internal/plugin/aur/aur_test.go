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
	stubProcessUser(t, 1000, map[string]string{})
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
	stubProcessUser(t, 1000, map[string]string{})
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
	// Single call: paru -S (install succeeds, no conflicts).
	if len(applyF.Calls) != 1 {
		t.Fatalf("expected 1 call, got %+v", applyF.Calls)
	}
	if applyF.Calls[0].Name != "paru" || applyF.Calls[0].Sudo {
		t.Errorf("expected unsudoed paru, got %+v", applyF.Calls[0])
	}
	if applyF.Calls[0].Args[0] != "-S" {
		t.Errorf("expected paru -S install, got args: %v", applyF.Calls[0].Args)
	}
}

// stubProcessUser replaces the geteuid/getenv hooks so tests can simulate
// running as root with a SUDO_USER, as root without one, or as a regular
// user. It also restores the originals when the test ends.
func stubProcessUser(t *testing.T, euid int, env map[string]string) {
	t.Helper()
	origUID := geteuid
	origEnv := getenv
	geteuid = func() int { return euid }
	getenv = func(k string) string { return env[k] }
	t.Cleanup(func() {
		geteuid = origUID
		getenv = origEnv
	})
}

func TestAvailable_RejectsRootWithoutSudoUser(t *testing.T) {
	stubLookPath(t, map[string]bool{"pacman": true, "yay": true})
	stubProcessUser(t, 0, map[string]string{})
	a := New()
	cfg := &config.Config{Settings: config.Settings{AURHelper: "yay"}}
	err := a.Available(cfg)
	if err == nil || !strings.Contains(err.Error(), "SUDO_USER") {
		t.Errorf("expected SUDO_USER error, got %v", err)
	}
}

func TestAvailable_RejectsRootWithSudoUserRoot(t *testing.T) {
	stubLookPath(t, map[string]bool{"pacman": true, "yay": true})
	stubProcessUser(t, 0, map[string]string{"SUDO_USER": "root"})
	a := New()
	cfg := &config.Config{Settings: config.Settings{AURHelper: "yay"}}
	if err := a.Available(cfg); err == nil || !strings.Contains(err.Error(), "SUDO_USER=root") {
		t.Errorf("expected SUDO_USER=root error, got %v", err)
	}
}

func TestAvailable_AcceptsSudoFromRegularUser(t *testing.T) {
	stubLookPath(t, map[string]bool{"pacman": true, "yay": true})
	stubProcessUser(t, 0, map[string]string{"SUDO_USER": "alice"})
	a := New()
	cfg := &config.Config{Settings: config.Settings{AURHelper: "yay"}}
	if err := a.Available(cfg); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestApply_DropsToSudoUserWhenInvokedAsRoot(t *testing.T) {
	stubLookPath(t, map[string]bool{"pacman": true, "yay": true})
	stubProcessUser(t, 0, map[string]string{"SUDO_USER": "alice"})
	planF := runner.NewFake()
	planF.Respond = func(name string, args []string) (string, string, int, error) {
		return "", "", 1, runner.NewExitError(1, "exit status 1")
	}
	a := New()
	a.SetRunner(planF.Runner)
	cfg := &config.Config{
		Settings: config.Settings{AURHelper: "yay"},
		AUR:      config.AUR{Packages: []string{"fnm-bin"}},
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
	if got := applyF.Calls[0].User; got != "alice" {
		t.Errorf("expected helper to run as alice, got User=%q (call=%+v)", got, applyF.Calls[0])
	}
}

func TestApply_KeepsCurrentUserWhenInvokedUnprivileged(t *testing.T) {
	stubLookPath(t, map[string]bool{"pacman": true, "yay": true})
	stubProcessUser(t, 1000, map[string]string{})
	planF := runner.NewFake()
	planF.Respond = func(name string, args []string) (string, string, int, error) {
		return "", "", 1, runner.NewExitError(1, "exit status 1")
	}
	a := New()
	a.SetRunner(planF.Runner)
	cfg := &config.Config{
		Settings: config.Settings{AURHelper: "yay"},
		AUR:      config.AUR{Packages: []string{"fnm-bin"}},
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
	if got := applyF.Calls[0].User; got != "" {
		t.Errorf("expected helper to run as current user (User=\"\"), got %q", got)
	}
}

func TestApply_AcceptsSubsetReport(t *testing.T) {
	stubLookPath(t, map[string]bool{"pacman": true, "paru": true})
	stubProcessUser(t, 1000, map[string]string{})
	planF := runner.NewFake()
	planF.Respond = func(name string, args []string) (string, string, int, error) {
		// No foreign packages installed, so all declared become adds.
		return "", "", 1, runner.NewExitError(1, "exit status 1")
	}
	a := New()
	a.SetRunner(planF.Runner)
	cfg := &config.Config{
		Settings: config.Settings{AURHelper: "paru"},
		AUR:      config.AUR{Packages: []string{"yay-bin", "fnm-bin"}},
	}
	report, err := a.Plan(cfg, &state.State{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(report.Operations) < 2 {
		t.Fatalf("expected at least 2 ops, got %d", len(report.Operations))
	}
	// Trim to a subset (keep only the first op).
	report.Operations = report.Operations[:1]

	applyF := runner.NewFake()
	if err := a.Apply(cfg, &state.State{}, report, applyF.Runner, silentUI()); err != nil {
		t.Fatalf("Apply with subset report should succeed, got: %v", err)
	}
}

func TestApply_RemovesBeforeInstalling(t *testing.T) {
	// When replacing "quickshell" with "quickshell-git", the removal must
	// happen before the install so the conflicting package is gone when the
	// new one is installed.
	stubLookPath(t, map[string]bool{"pacman": true, "yay": true})
	stubProcessUser(t, 1000, map[string]string{})

	planF := runner.NewFake()
	planF.Respond = func(name string, args []string) (string, string, int, error) {
		// quickshell is currently installed as a foreign package.
		return "quickshell\n", "", 0, nil
	}
	a := New()
	a.SetRunner(planF.Runner)

	cfg := &config.Config{
		Settings: config.Settings{AURHelper: "yay"},
		AUR:      config.AUR{Packages: []string{"quickshell-git"}},
	}
	// State records that bigkis previously declared quickshell.
	st := &state.State{}
	if err := st.Set("aur", []string{"quickshell"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	report, err := a.Plan(cfg, st)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	applyF := runner.NewFake()
	if err := a.Apply(cfg, st, report, applyF.Runner, silentUI()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Calls: 1) remove quickshell (-Rns), 2) install quickshell-git (-S).
	var removeIdx, installIdx int = -1, -1
	for i, c := range applyF.Calls {
		if c.Name == "yay" && len(c.Args) > 0 && c.Args[0] == "-Rns" {
			removeIdx = i
		}
		if c.Name == "yay" && len(c.Args) > 0 && c.Args[0] == "-S" {
			installIdx = i
		}
	}
	if removeIdx < 0 {
		t.Fatal("expected a removal call (-Rns)")
	}
	if installIdx < 0 {
		t.Fatal("expected an install call (-S)")
	}
	if removeIdx >= installIdx {
		t.Fatalf("removal (call %d) must come before install (call %d)", removeIdx, installIdx)
	}
	if applyF.Calls[removeIdx].Args[len(applyF.Calls[removeIdx].Args)-1] != "quickshell" {
		t.Errorf("expected removal of quickshell, got args: %v", applyF.Calls[removeIdx].Args)
	}
	if applyF.Calls[installIdx].Args[len(applyF.Calls[installIdx].Args)-1] != "quickshell-git" {
		t.Errorf("expected install of quickshell-git, got args: %v", applyF.Calls[installIdx].Args)
	}
}

func TestUpgrade_RunsSuaAsSudoUser(t *testing.T) {
	stubLookPath(t, map[string]bool{"pacman": true, "yay": true})
	stubProcessUser(t, 0, map[string]string{"SUDO_USER": "alice"})
	a := New()
	f := runner.NewFake()
	cfg := &config.Config{Settings: config.Settings{AURHelper: "yay"}}
	if err := a.Upgrade(cfg, &state.State{}, f.Runner, silentUI()); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if len(f.Calls) != 1 {
		t.Fatalf("calls: %+v", f.Calls)
	}
	c := f.Calls[0]
	if c.Name != "yay" || c.Sudo {
		t.Errorf("unexpected call: %+v", c)
	}
	if c.User != "alice" {
		t.Errorf("User = %q, want alice", c.User)
	}
	want := []string{"-Sua", "--noconfirm"}
	if !reflect.DeepEqual(c.Args, want) {
		t.Errorf("args = %v", c.Args)
	}
}

func TestUpgrade_AsCurrentUserWhenUnprivileged(t *testing.T) {
	stubLookPath(t, map[string]bool{"pacman": true, "yay": true})
	stubProcessUser(t, 1000, map[string]string{})
	a := New()
	f := runner.NewFake()
	cfg := &config.Config{Settings: config.Settings{AURHelper: "yay"}}
	if err := a.Upgrade(cfg, &state.State{}, f.Runner, silentUI()); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if len(f.Calls) != 1 {
		t.Fatalf("calls: %+v", f.Calls)
	}
	if f.Calls[0].User != "" {
		t.Errorf("expected empty User, got %q", f.Calls[0].User)
	}
}

func TestApply_RemovesUnmanagedConflictingPackage(t *testing.T) {
	// "quickshell" is installed but NOT managed by bigkis (not in state).
	// "quickshell-git" is in the config. First install attempt fails with a
	// conflict, bigkis removes quickshell via pacman, then retries install.
	stubLookPath(t, map[string]bool{"pacman": true, "yay": true})
	stubProcessUser(t, 1000, map[string]string{})

	planF := runner.NewFake()
	planF.Respond = func(name string, args []string) (string, string, int, error) {
		// No foreign packages installed (quickshell is native, not foreign).
		return "", "", 1, runner.NewExitError(1, "exit status 1")
	}
	a := New()
	a.SetRunner(planF.Runner)

	cfg := &config.Config{
		Settings: config.Settings{AURHelper: "yay"},
		AUR:      config.AUR{Packages: []string{"quickshell-git"}},
	}
	report, err := a.Plan(cfg, &state.State{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	applyF := runner.NewFake()
	applyF.Respond = func(name string, args []string) (string, string, int, error) {
		if name == "yay" && len(args) > 0 && args[0] == "-S" {
			if countCallsWithArgs(applyF.Calls, "yay", "-S") == 1 {
				stderr := ":: quickshell-git and quickshell are in conflict\nerror: unresolvable package conflicts detected\n"
				return "", stderr, 1, runner.NewExitError(1, "exit status 1")
			}
			return "", "", 0, nil
		}
		if name == "pacman" && len(args) > 0 && args[0] == "-Rns" {
			return "", "", 0, nil
		}
		return "", "", 0, nil
	}
	if err := a.Apply(cfg, &state.State{}, report, applyF.Runner, silentUI()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Should have: 1) yay -S (fails), 2) pacman -Rns quickshell,
	// 3) yay -S (retry succeeds).
	var conflictRemoveIdx, installIdx int = -1, -1
	installCalls := 0
	for i, c := range applyF.Calls {
		if c.Name == "pacman" && len(c.Args) > 0 && c.Args[0] == "-Rns" {
			conflictRemoveIdx = i
		}
		if c.Name == "yay" && len(c.Args) > 0 && c.Args[0] == "-S" {
			installIdx = i
			installCalls++
		}
	}
	if conflictRemoveIdx < 0 {
		t.Fatal("expected a conflict removal call (pacman -Rns)")
	}
	if installIdx < 0 {
		t.Fatal("expected an install call (-S)")
	}
	if installCalls != 2 {
		t.Fatalf("expected 2 install attempts, got %d (calls=%+v)", installCalls, applyF.Calls)
	}
	if conflictRemoveIdx >= installIdx {
		t.Fatalf("conflict removal (call %d) must come before install (call %d)", conflictRemoveIdx, installIdx)
	}
	lastArg := applyF.Calls[conflictRemoveIdx].Args[len(applyF.Calls[conflictRemoveIdx].Args)-1]
	if lastArg != "quickshell" {
		t.Errorf("expected conflict removal of quickshell, got last arg %q", lastArg)
	}
}

func TestParseConflictError(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		adding []string
		want   []string
	}{
		{
			name:   "no conflicts",
			stderr: "error: failed to prepare transaction\n",
			adding: []string{"quickshell-git"},
			want:   nil,
		},
		{
			name:   "extract other side of conflict",
			stderr: ":: quickshell-git and quickshell are in conflict\n",
			adding: []string{"quickshell-git"},
			want:   []string{"quickshell"},
		},
		{
			name:   "strip error prefix",
			stderr: "error: quickshell-git and quickshell are in conflict\n",
			adding: []string{"quickshell-git"},
			want:   []string{"quickshell"},
		},
		{
			name:   "ignore adding package names",
			stderr: ":: quickshell-git and quickshell are in conflict\n:: foo and bar are in conflict\n",
			adding: []string{"quickshell-git", "foo"},
			want:   []string{"quickshell", "bar"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseConflictError(tt.stderr, tt.adding)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTrimPacmanErrorPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{":: quickshell-git", "quickshell-git"},
		{"error: quickshell", "quickshell"},
		{"quickshell", "quickshell"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := trimPacmanErrorPrefix(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func countCallsWithArgs(calls []runner.FakeCall, name, firstArg string) int {
	n := 0
	for _, c := range calls {
		if c.Name == name && len(c.Args) > 0 && c.Args[0] == firstArg {
			n++
		}
	}
	return n
}

func TestPendingUpgrades_ParsesOutput(t *testing.T) {
	stubLookPath(t, map[string]bool{"pacman": true, "yay": true})
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		if name == "yay" && len(args) >= 1 && args[0] == "-Qua" {
			return "neovim 0.9.5-1 -> 0.10.0-1\ngit 2.44.0-1 -> 2.45.0-1\n", "", 0, nil
		}
		return "", "", 0, nil
	}
	a := New()
	cfg := &config.Config{Settings: config.Settings{AURHelper: "yay"}}
	rep, err := a.PendingUpgrades(cfg, f.Runner)
	if err != nil {
		t.Fatalf("PendingUpgrades: %v", err)
	}
	if rep.Plugin != "aur" {
		t.Errorf("Plugin = %q, want aur", rep.Plugin)
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
	if !rep.HasUpgrades() {
		t.Error("HasUpgrades should be true")
	}
}

func TestPendingUpgrades_NoHelperConfigured(t *testing.T) {
	stubLookPath(t, nil)
	f := runner.NewFake()
	a := New()
	cfg := &config.Config{Settings: config.Settings{AURHelper: ""}}
	rep, err := a.PendingUpgrades(cfg, f.Runner)
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

func TestPendingUpgrades_NoUpgradesReturnsEmpty(t *testing.T) {
	stubLookPath(t, map[string]bool{"pacman": true, "yay": true})
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		if name == "yay" && len(args) >= 1 && args[0] == "-Qua" {
			return "", "", 1, runner.NewExitError(1, "no upgrades")
		}
		return "", "", 0, nil
	}
	a := New()
	cfg := &config.Config{Settings: config.Settings{AURHelper: "yay"}}
	rep, err := a.PendingUpgrades(cfg, f.Runner)
	if err != nil {
		t.Fatalf("exit 1 + empty stdout should not be error: %v", err)
	}
	if len(rep.Operations) != 0 {
		t.Errorf("expected 0 ops, got %d", len(rep.Operations))
	}
}

func TestPendingUpgrades_CommandFailsReturnsError(t *testing.T) {
	stubLookPath(t, map[string]bool{"pacman": true, "yay": true})
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		if name == "yay" && len(args) >= 1 && args[0] == "-Qua" {
			return "some error", "", 127, runner.NewExitError(127, "command failed")
		}
		return "", "", 0, nil
	}
	a := New()
	cfg := &config.Config{Settings: config.Settings{AURHelper: "yay"}}
	_, err := a.PendingUpgrades(cfg, f.Runner)
	if err == nil {
		t.Fatal("expected error for exit 127")
	}
}
