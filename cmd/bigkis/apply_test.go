package main

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"codeberg.org/gurg/bigkis/internal/config"
	"codeberg.org/gurg/bigkis/internal/plugin"
	"codeberg.org/gurg/bigkis/internal/runner"
	"codeberg.org/gurg/bigkis/internal/state"
	"codeberg.org/gurg/bigkis/internal/ui"
)

// fakePlugin is a Plugin used to drive applyStages from tests. It records
// when Apply / PersistState are called and can fail on demand.
type fakePlugin struct {
	name        string
	applyErr    error
	stateValue  []string
	appliedHere bool
}

func (p *fakePlugin) Name() string                       { return p.name }
func (p *fakePlugin) Available(cfg *config.Config) error { return nil }
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
	if err := applyStages(stages, cfg, st, statePath, runner.NewFake().Runner, logUI); err != nil {
		t.Fatalf("applyStages: %v", err)
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
	if err := applyStages(stages, cfg, st, statePath, runner.NewFake().Runner, logUI); err == nil {
		t.Fatal("expected error from second stage, got nil")
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
