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
	return plugin.Report{Plugin: p.name}, nil
}
func (p *fakePlugin) Apply(cfg *config.Config, st *state.State, report plugin.Report, r *runner.Runner, u *ui.UI) error {
	p.appliedHere = true
	return p.applyErr
}
func (p *fakePlugin) PersistState(cfg *config.Config, st *state.State) error {
	return st.Set(p.name, p.stateValue)
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
	if len(applied) != 1 || applied[0].Plugin.Name() != "pacman" {
		t.Errorf("applied = %+v, want only pacman", applied)
	}

	// state.json should reflect ONLY the pacman stage (which succeeded), not
	// node (which never ran) and not aur (which failed before PersistState).
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
	if found, _ := loaded.Get("node", &node); found {
		t.Errorf("node should not be in state (never ran), got %v", node)
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
}

func runTestApp(args []string) error {
	app := &cli.App{
		Name: "bigkis",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "config", EnvVars: []string{"BIGKIS_CONFIG"}},
		},
		Commands: []*cli.Command{applyCommand()},
	}
	all := append([]string{"bigkis"}, args...)
	return app.Run(all)
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
