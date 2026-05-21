package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v2"

	"codeberg.org/gurg/bigkis/internal/config"
	"codeberg.org/gurg/bigkis/internal/plugin"
	"codeberg.org/gurg/bigkis/internal/runner"
	"codeberg.org/gurg/bigkis/internal/state"
	"codeberg.org/gurg/bigkis/internal/ui"
)

// fakePlugin is a Plugin used to drive applyStages from tests. It records
// when Apply / PersistState / Upgrade are called and can fail on demand.
type fakePlugin struct {
	name          string
	applyErr      error
	stateValue    []string
	appliedHere   bool
	upgradeCalled int
	upgradeErr    error
	availableErr  error
	planReport    plugin.Report
	planErr       error
	persistErr    error
}

func (p *fakePlugin) Name() string { return p.name }
func (p *fakePlugin) Available(cfg *config.Config) error {
	return p.availableErr
}
func (p *fakePlugin) Upgrade(cfg *config.Config, st *state.State, r *runner.Runner, u *ui.UI) error {
	p.upgradeCalled++
	return p.upgradeErr
}
func (p *fakePlugin) Plan(cfg *config.Config, st *state.State) (plugin.Report, error) {
	if p.planErr != nil {
		return plugin.Report{}, p.planErr
	}
	if p.planReport.Plugin != "" || len(p.planReport.Operations) > 0 {
		return p.planReport, nil
	}
	return plugin.Report{Plugin: p.name}, nil
}
func (p *fakePlugin) Apply(cfg *config.Config, st *state.State, report plugin.Report, r *runner.Runner, u *ui.UI) error {
	p.appliedHere = true
	return p.applyErr
}
func (p *fakePlugin) PersistState(cfg *config.Config, st *state.State) error {
	if p.persistErr != nil {
		return p.persistErr
	}
	return st.Set(p.name, p.stateValue)
}
func (p *fakePlugin) PendingUpgrades(cfg *config.Config, r *runner.Runner) (plugin.UpgradeReport, error) {
	return plugin.UpgradeReport{Plugin: p.name}, nil
}

func TestApplyStages_CheckpointsAfterEachPlugin(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	cfg := &config.Config{}
	st := &state.State{}
	logUI := ui.New(io.Discard, &bytes.Buffer{}, false, true)

	stages := []stage{
		{Plugin: &fakePlugin{name: "pacman", stateValue: []string{"git"}}, Report: plugin.Report{Plugin: "pacman"}},
		{Plugin: &fakePlugin{name: "aur", stateValue: []string{"yay-bin"}}, Report: plugin.Report{Plugin: "aur"}},
	}
	applied, err := applyStages(stages, cfg, st, statePath, runner.NewFake().Runner, logUI)
	if err != nil {
		t.Fatalf("applyStages: %v", err)
	}
	if len(applied) != 2 {
		t.Fatalf("expected 2 applied stages, got %d", len(applied))
	}

	loaded, err := state.Load(statePath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var pac []string
	if _, err := loaded.Get("pacman", &pac); err != nil || len(pac) != 1 || pac[0] != "git" {
		t.Errorf("pacman state after success = %v, err=%v", pac, err)
	}
	var aur []string
	if _, err := loaded.Get("aur", &aur); err != nil || len(aur) != 1 || aur[0] != "yay-bin" {
		t.Errorf("aur state after success = %v, err=%v", aur, err)
	}
}

func TestApplyStages_MidLoopFailureKeepsSuccessfulCheckpoints(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	cfg := &config.Config{}
	st := &state.State{}
	logUI := ui.New(io.Discard, &bytes.Buffer{}, false, true)

	boom := errors.New("boom")
	stages := []stage{
		{Plugin: &fakePlugin{name: "pacman", stateValue: []string{"git"}}, Report: plugin.Report{Plugin: "pacman"}},
		{Plugin: &fakePlugin{name: "aur", applyErr: boom}, Report: plugin.Report{Plugin: "aur"}},
		{Plugin: &fakePlugin{name: "node", stateValue: []string{"typescript"}}, Report: plugin.Report{Plugin: "node"}},
	}
	applied, err := applyStages(stages, cfg, st, statePath, runner.NewFake().Runner, logUI)
	if err == nil {
		t.Fatal("expected error from second stage, got nil")
	}
	// With continue-on-failure, pacman and node both succeed; only aur fails.
	if len(applied) != 2 || applied[0].Plugin.Name() != "pacman" || applied[1].Plugin.Name() != "node" {
		t.Errorf("applied = %+v, want pacman and node", applied)
	}

	// state.json should reflect pacman and node (which succeeded), not
	// aur (which failed before PersistState).
	loaded, err := state.Load(statePath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var pac []string
	if _, err := loaded.Get("pacman", &pac); err != nil || len(pac) != 1 {
		t.Errorf("expected pacman checkpointed, got %v err=%v", pac, err)
	}
	var aur []string
	if found, _ := loaded.Get("aur", &aur); found {
		t.Errorf("aur should not be in state after failure, got %v", aur)
	}
	var node []string
	if _, err := loaded.Get("node", &node); err != nil || len(node) != 1 {
		t.Errorf("expected node checkpointed, got %v err=%v", node, err)
	}
}

func TestApplyStages_PersistStateFailureIsWrapped(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	cfg := &config.Config{}
	st := &state.State{}
	logUI := ui.New(io.Discard, &bytes.Buffer{}, false, true)
	boom := errors.New("persist boom")
	stages := []stage{
		{Plugin: &fakePlugin{name: "pacman", stateValue: []string{"git"}, persistErr: boom}, Report: plugin.Report{Plugin: "pacman"}},
	}

	applied, err := applyStages(stages, cfg, st, statePath, runner.NewFake().Runner, logUI)

	// Apply succeeded but PersistState failed — plugin is still counted as
	// applied since the system was changed.
	if len(applied) != 1 || applied[0].Plugin.Name() != "pacman" {
		t.Fatalf("applied = %+v, want pacman (apply succeeded)", applied)
	}
	if err == nil || !strings.Contains(err.Error(), "pacman persist: persist boom") {
		t.Fatalf("err = %v, want wrapped persist error", err)
	}
}

func TestPluginsForUpgrade_FiltersUnavailablePreservesOrder(t *testing.T) {
	p1 := &fakePlugin{name: "pacman"}
	p2 := &fakePlugin{name: "aur"}
	p3 := &fakePlugin{name: "flatpak"}
	all := []plugin.Plugin{p1, p2, p3}
	got := pluginsForUpgrade(all, []string{"aur"})
	if len(got) != 2 || got[0].Name() != "pacman" || got[1].Name() != "flatpak" {
		t.Fatalf("got %+v", pluginNames(got))
	}
	if len(pluginsForUpgrade(all, nil)) != 3 {
		t.Fatal("empty unavailable should return original slice")
	}
	if len(pluginsForUpgrade(all, []string{})) != 3 {
		t.Fatal("empty unavailable slice should return original")
	}
}

func pluginNames(ps []plugin.Plugin) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Name())
	}
	return out
}

func TestRunUpgrades_OmitsPluginsMarkedUnavailableDuringPlan(t *testing.T) {
	cfg := &config.Config{}
	st := &state.State{}
	logUI := ui.New(io.Discard, &bytes.Buffer{}, false, true)
	skip := errors.New("skipped at plan")
	bad := &fakePlugin{name: "pacman", availableErr: skip}
	good := &fakePlugin{name: "aur"}
	filtered := pluginsForUpgrade([]plugin.Plugin{bad, good}, []string{"pacman"})
	if err := runUpgrades(filtered, cfg, st, runner.NewFake().Runner, logUI); err != nil {
		t.Fatal(err)
	}
	if bad.upgradeCalled != 0 {
		t.Errorf("pacman Upgrade ran %d times, want 0", bad.upgradeCalled)
	}
	if good.upgradeCalled != 1 {
		t.Errorf("aur Upgrade ran %d times, want 1", good.upgradeCalled)
	}
}

func TestRunUpgrades_SkipsCurrentlyUnavailablePlugin(t *testing.T) {
	var out bytes.Buffer
	logUI := ui.New(&out, &bytes.Buffer{}, false, true)
	p := &fakePlugin{name: "pacman", availableErr: errors.New("missing pacman")}

	if err := runUpgrades([]plugin.Plugin{p}, &config.Config{}, &state.State{}, runner.NewFake().Runner, logUI); err != nil {
		t.Fatalf("runUpgrades: %v", err)
	}
	if p.upgradeCalled != 0 {
		t.Fatalf("upgradeCalled = %d, want 0", p.upgradeCalled)
	}
	if !strings.Contains(out.String(), "pacman: unavailable") {
		t.Fatalf("warning missing from %q", out.String())
	}
}

func TestApplyConfirmPrompt_Wordings(t *testing.T) {
	if g := applyConfirmPrompt(true, true); g == "" || !strings.Contains(g, "upgrades") {
		t.Errorf("both: %q", g)
	}
	if g := applyConfirmPrompt(true, false); strings.Contains(g, "upgrades") {
		t.Errorf("no upgrade flag: %q", g)
	}
	if g := applyConfirmPrompt(false, true); !strings.Contains(g, "upgrades") || strings.Contains(g, "install/remove") {
		t.Errorf("upgrades only: %q", g)
	}
	if g := applyConfirmPrompt(false, false); g != "proceed?" {
		t.Errorf("nothing to do prompt: %q", g)
	}
}

func TestSplitCSV_TrimsSplitsAndDropsEmpty(t *testing.T) {
	got := splitCSV([]string{"pacman, aur", "", "flatpak", " node ,, "})
	want := []string{"pacman", "aur", "flatpak", "node"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("splitCSV = %v, want %v", got, want)
	}
}

func TestSelectPlugins_AppliesOnlySkipAndWarnings(t *testing.T) {
	reg := plugin.NewRegistry()
	reg.Register(&fakePlugin{name: "pacman"})
	reg.Register(&fakePlugin{name: "aur"})
	reg.Register(&fakePlugin{name: "flatpak"})
	var out bytes.Buffer
	logUI := ui.New(&out, &bytes.Buffer{}, false, true)

	got := selectPlugins(
		[]string{"pacman", "aur", "flatpak"},
		[]string{"aur", "typo"},
		[]string{"flatpak", "skip-typo"},
		reg,
		logUI,
	)

	if names := strings.Join(pluginNames(got), ","); names != "aur" {
		t.Fatalf("selected = %s, want aur", names)
	}
	log := out.String()
	for _, want := range []string{
		`--only "typo"`,
		`--skip "skip-typo"`,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("warning %q missing from %q", want, log)
		}
	}

	out.Reset()
	if got := selectPlugins([]string{"missing"}, nil, nil, reg, logUI); len(got) != 0 {
		t.Fatalf("unknown plugin selected: %v", pluginNames(got))
	}
	if !strings.Contains(out.String(), `plugin "missing" is enabled`) {
		t.Fatalf("unknown plugin warning missing from %q", out.String())
	}
}

func TestPrintReport_SortsAndFormatsOperations(t *testing.T) {
	var out bytes.Buffer
	logUI := ui.New(&out, &bytes.Buffer{}, false, true)
	report := plugin.Report{Plugin: "test", Operations: []plugin.Operation{
		{Kind: plugin.OpRemove, Target: "zeta"},
		{Kind: plugin.OpAdd, Target: "beta", Detail: "system"},
		{Kind: plugin.OpAdd, Target: "alpha"},
	}}

	printReport(logUI, report)

	got := out.String()
	want := "  + alpha\n  + beta (system)\n  - zeta\n"
	if got != want {
		t.Fatalf("report output = %q, want %q", got, want)
	}
}

func TestRunUpgrades_WrapsUpgradeError(t *testing.T) {
	cfg := &config.Config{}
	st := &state.State{}
	logUI := ui.New(io.Discard, &bytes.Buffer{}, false, true)
	boom := errors.New("boom")
	p := &fakePlugin{name: "pacman", upgradeErr: boom}

	err := runUpgrades([]plugin.Plugin{p}, cfg, st, runner.NewFake().Runner, logUI)

	if err == nil || !strings.Contains(err.Error(), "pacman") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want upgrade error mentioning pacman and boom", err)
	}
}

func TestPlanAll_RecordsReportsAndUnavailablePlugins(t *testing.T) {
	cfg := &config.Config{}
	st := &state.State{}
	var out bytes.Buffer
	logUI := ui.New(&out, &bytes.Buffer{}, false, true)
	good := &fakePlugin{
		name: "pacman",
		planReport: plugin.Report{
			Plugin:     "pacman",
			Operations: []plugin.Operation{{Kind: plugin.OpAdd, Target: "git"}},
		},
	}
	bad := &fakePlugin{name: "aur", availableErr: errors.New("missing helper")}

	res, err := planAll(cfg, st, []plugin.Plugin{good, bad}, logUI)

	if err != nil {
		t.Fatalf("planAll: %v", err)
	}
	if len(res.stages) != 1 || res.stages[0].Plugin.Name() != "pacman" {
		t.Fatalf("stages = %+v", res.stages)
	}
	if !res.overall {
		t.Fatal("overall should report drift from good plugin operation")
	}
	if strings.Join(res.unavailable, ",") != "aur" {
		t.Fatalf("unavailable = %v", res.unavailable)
	}
	if !strings.Contains(out.String(), "aur: unavailable") {
		t.Fatalf("warning missing from %q", out.String())
	}
}

func TestPlanAll_WrapsPlanError(t *testing.T) {
	logUI := ui.New(io.Discard, &bytes.Buffer{}, false, true)
	p := &fakePlugin{name: "pacman", planErr: errors.New("plan boom")}

	_, err := planAll(&config.Config{}, &state.State{}, []plugin.Plugin{p}, logUI)

	if err == nil || !strings.Contains(err.Error(), "pacman plan: plan boom") {
		t.Fatalf("err = %v, want wrapped plan error", err)
	}
}

func TestPersistInSync_WrapsPersistError(t *testing.T) {
	logUI := ui.New(io.Discard, &bytes.Buffer{}, false, true)
	p := &fakePlugin{name: "pacman", persistErr: errors.New("persist boom")}

	err := persistInSync([]plugin.Plugin{p}, &config.Config{}, &state.State{}, filepath.Join(t.TempDir(), "state.json"), logUI)

	if err == nil || !strings.Contains(err.Error(), "pacman persist state: persist boom") {
		t.Fatalf("err = %v, want wrapped persist error", err)
	}
}

func runTestApp(args []string) error {
	return runTestAppWithCommands(args, applyCommand())
}

func runTestAppWithCommands(args []string, commands ...*cli.Command) error {
	app := &cli.App{
		Name: "bigkis",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "config", EnvVars: []string{"BIGKIS_CONFIG"}},
		},
		Commands: commands,
	}
	all := append([]string{"bigkis"}, args...)
	return app.Run(all)
}

func suppressCLIExit(t *testing.T) {
	t.Helper()
	prev := cli.OsExiter
	cli.OsExiter = func(int) {}
	t.Cleanup(func() { cli.OsExiter = prev })
}

func TestRunApply_RunsUpgradesByDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "xdg-state"))
	cfgPath := filepath.Join(dir, "system.toml")
	if err := os.WriteFile(cfgPath, []byte("[settings]\nenabled = [\"pacman\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp := &fakePlugin{name: config.PluginPacman}
	prev := registryHook
	registryHook = func() *plugin.Registry {
		r := plugin.NewRegistry()
		r.Register(fp)
		return r
	}
	t.Cleanup(func() { registryHook = prev })
	if err := runTestApp([]string{"--config", cfgPath, "apply", "--yes"}); err != nil {
		t.Fatal(err)
	}
	if fp.upgradeCalled != 1 {
		t.Fatalf("expected 1 upgrade, got %d", fp.upgradeCalled)
	}
}

func TestRunStatus_ExitOnDriftReturnsDriftCode(t *testing.T) {
	suppressCLIExit(t)
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "xdg-state"))
	cfgPath := filepath.Join(dir, "system.toml")
	if err := os.WriteFile(cfgPath, []byte("[settings]\nenabled = [\"pacman\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp := &fakePlugin{
		name: config.PluginPacman,
		planReport: plugin.Report{
			Plugin:     config.PluginPacman,
			Operations: []plugin.Operation{{Kind: plugin.OpAdd, Target: "git"}},
		},
	}
	prev := registryHook
	registryHook = func() *plugin.Registry {
		r := plugin.NewRegistry()
		r.Register(fp)
		return r
	}
	t.Cleanup(func() { registryHook = prev })

	err := runTestAppWithCommands([]string{"--config", cfgPath, "status", "--exit-on-drift"}, statusCommand())

	if err == nil {
		t.Fatal("expected drift exit")
	}
	exit, ok := err.(cli.ExitCoder)
	if !ok || exit.ExitCode() != ExitDrift {
		t.Fatalf("err = %v, want drift exit code %d", err, ExitDrift)
	}
}

func TestRunStatus_UnavailablePluginDoesNotReportDrift(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "xdg-state"))
	cfgPath := filepath.Join(dir, "system.toml")
	if err := os.WriteFile(cfgPath, []byte("[settings]\nenabled = [\"pacman\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp := &fakePlugin{name: config.PluginPacman, availableErr: errors.New("missing pacman")}
	prev := registryHook
	registryHook = func() *plugin.Registry {
		r := plugin.NewRegistry()
		r.Register(fp)
		return r
	}
	t.Cleanup(func() { registryHook = prev })

	if err := runTestAppWithCommands([]string{"--config", cfgPath, "status", "--exit-on-drift"}, statusCommand()); err != nil {
		t.Fatalf("status should not exit drift when plugin is unavailable: %v", err)
	}
}

func TestRunApply_NoUpgradeFlagSkipsUpgrade(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "xdg-state"))
	cfgPath := filepath.Join(dir, "system.toml")
	if err := os.WriteFile(cfgPath, []byte("[settings]\nenabled = [\"pacman\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp := &fakePlugin{name: config.PluginPacman}
	prev := registryHook
	registryHook = func() *plugin.Registry {
		r := plugin.NewRegistry()
		r.Register(fp)
		return r
	}
	t.Cleanup(func() { registryHook = prev })
	if err := runTestApp([]string{"--config", cfgPath, "apply", "--yes", "--no-upgrade"}); err != nil {
		t.Fatal(err)
	}
	if fp.upgradeCalled != 0 {
		t.Fatalf("expected no upgrade, got %d", fp.upgradeCalled)
	}
}

// TestPersistInSync_RecordsOwnershipForFirstRunClean exercises the new
// post-apply (and post-no-op-apply) state writeout for plugins that had no
// changes. Without this, a clean first run leaves lastApplied empty so the
// first-run-safety in plan.Compute inhibits removals indefinitely.
func TestPersistInSync_RecordsOwnershipForFirstRunClean(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	cfg := &config.Config{}
	st := &state.State{}
	logUI := ui.New(io.Discard, &bytes.Buffer{}, false, true)

	plugins := []plugin.Plugin{
		&fakePlugin{name: "pacman", stateValue: []string{"git", "neovim"}},
		&fakePlugin{name: "node", stateValue: []string{"typescript"}},
	}
	if err := persistInSync(plugins, cfg, st, statePath, logUI); err != nil {
		t.Fatalf("persistInSync: %v", err)
	}
	for _, p := range plugins {
		if (p.(*fakePlugin)).appliedHere {
			t.Errorf("%s.Apply should not have been called by persistInSync", p.Name())
		}
	}

	loaded, err := state.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var pac []string
	if _, err := loaded.Get("pacman", &pac); err != nil || len(pac) != 2 {
		t.Errorf("pacman = %v, err=%v", pac, err)
	}
	var node []string
	if _, err := loaded.Get("node", &node); err != nil || len(node) != 1 {
		t.Errorf("node = %v, err=%v", node, err)
	}
}
