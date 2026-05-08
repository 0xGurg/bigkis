package doctor

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"codeberg.org/gurg/bigkis/internal/config"
)

// stubEnv builds a deterministic Env for the table tests below. Every real
// host knob is replaced with an in-memory stub so doctor's behavior under
// different system layouts is fully reproducible from the test alone.
func stubEnv(t *testing.T, opts struct {
	uid        int
	envv       map[string]string
	commands   map[string]bool
	statePath  string
	rollback   string
	flatpakRem map[string]bool
	flatpakErr error
}) Env {
	t.Helper()
	if opts.statePath == "" {
		opts.statePath = filepath.Join(t.TempDir(), "state.json")
	}
	if opts.rollback == "" {
		opts.rollback = filepath.Join(t.TempDir(), "rollbacks")
	}
	return Env{
		Geteuid: func() int { return opts.uid },
		Getenv:  func(k string) string { return opts.envv[k] },
		LookPath: func(name string) (string, error) {
			if opts.commands[name] {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("not found: " + name)
		},
		StatePath:   opts.statePath,
		RollbackDir: opts.rollback,
		FlatpakRemote: func(name string) (bool, error) {
			if opts.flatpakErr != nil {
				return false, opts.flatpakErr
			}
			return opts.flatpakRem[name], nil
		},
	}
}

func findCheck(r Report, name string) (Check, bool) {
	for _, c := range r.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

func TestRun_HappyPath(t *testing.T) {
	env := stubEnv(t, struct {
		uid        int
		envv       map[string]string
		commands   map[string]bool
		statePath  string
		rollback   string
		flatpakRem map[string]bool
		flatpakErr error
	}{
		uid:        1000,
		envv:       map[string]string{},
		commands:   map[string]bool{"pacman": true, "flatpak": true, "yay": true, "npm": true},
		flatpakRem: map[string]bool{"flathub": true},
	})
	cfg := &config.Config{
		Path: "/etc/bigkis/system.toml",
		Settings: config.Settings{
			Enabled:      []string{"pacman", "aur", "flatpak", "node"},
			AURHelper:    "yay",
			NodeManager:  "npm",
			PruneOrphans: "scoped",
		},
		Flatpak: config.Flatpak{Remote: "flathub"},
		Node:    config.Node{Packages: []string{"typescript"}},
	}
	r := Run(cfg, nil, env)
	if !r.OK {
		t.Fatalf("expected OK report, got %+v", r)
	}
}

func TestRun_FailsWhenAURHelperMissing(t *testing.T) {
	env := stubEnv(t, struct {
		uid        int
		envv       map[string]string
		commands   map[string]bool
		statePath  string
		rollback   string
		flatpakRem map[string]bool
		flatpakErr error
	}{
		uid:      1000,
		envv:     map[string]string{},
		commands: map[string]bool{"pacman": true, "flatpak": true},
	})
	cfg := &config.Config{
		Settings: config.Settings{
			Enabled:   []string{"pacman", "aur"},
			AURHelper: "yay",
		},
	}
	r := Run(cfg, nil, env)
	c, ok := findCheck(r, "aur:helper")
	if !ok {
		t.Fatal("missing aur:helper check")
	}
	if c.Status != StatusFail {
		t.Errorf("expected fail, got %s", c.Status)
	}
	if r.OK {
		t.Errorf("OK should be false when aur helper is missing")
	}
}

func TestRun_FailsAsRootWithoutSudoUser(t *testing.T) {
	env := stubEnv(t, struct {
		uid        int
		envv       map[string]string
		commands   map[string]bool
		statePath  string
		rollback   string
		flatpakRem map[string]bool
		flatpakErr error
	}{
		uid:      0,
		envv:     map[string]string{}, // no SUDO_USER
		commands: map[string]bool{"pacman": true, "yay": true, "flatpak": true},
	})
	cfg := &config.Config{
		Settings: config.Settings{
			Enabled:   []string{"pacman", "aur"},
			AURHelper: "yay",
		},
	}
	r := Run(cfg, nil, env)
	c, ok := findCheck(r, "aur:user")
	if !ok {
		t.Fatal("missing aur:user check")
	}
	if c.Status != StatusFail {
		t.Errorf("expected aur:user fail, got %s (%s)", c.Status, c.Message)
	}
}

func TestRun_FlatpakRemoteMissing(t *testing.T) {
	env := stubEnv(t, struct {
		uid        int
		envv       map[string]string
		commands   map[string]bool
		statePath  string
		rollback   string
		flatpakRem map[string]bool
		flatpakErr error
	}{
		uid:        1000,
		commands:   map[string]bool{"flatpak": true, "pacman": true},
		flatpakRem: map[string]bool{"flathub": true},
	})
	cfg := &config.Config{
		Settings: config.Settings{Enabled: []string{"flatpak"}},
		Flatpak:  config.Flatpak{Remote: "fedora"},
	}
	r := Run(cfg, nil, env)
	c, ok := findCheck(r, "flatpak:remote")
	if !ok {
		t.Fatal("missing flatpak:remote check")
	}
	if c.Status != StatusFail {
		t.Errorf("expected fail when remote missing, got %+v", c)
	}
}

func TestRun_NodeManagerMissing(t *testing.T) {
	env := stubEnv(t, struct {
		uid        int
		envv       map[string]string
		commands   map[string]bool
		statePath  string
		rollback   string
		flatpakRem map[string]bool
		flatpakErr error
	}{
		uid:      1000,
		commands: map[string]bool{"pacman": true, "flatpak": true, "npm": true},
	})
	cfg := &config.Config{
		Settings: config.Settings{Enabled: []string{"node"}, NodeManager: "pnpm"},
		Node:     config.Node{Packages: []string{"typescript"}},
	}
	r := Run(cfg, nil, env)
	c, ok := findCheck(r, "node:manager:pnpm")
	if !ok {
		t.Fatal("missing node:manager:pnpm check")
	}
	if c.Status != StatusFail {
		t.Errorf("expected fail for missing pnpm, got %+v", c)
	}
}

func TestRun_ConfigError(t *testing.T) {
	env := stubEnv(t, struct {
		uid        int
		envv       map[string]string
		commands   map[string]bool
		statePath  string
		rollback   string
		flatpakRem map[string]bool
		flatpakErr error
	}{
		uid:      1000,
		commands: map[string]bool{"pacman": true},
	})
	r := Run(nil, errors.New("parse error: bad toml"), env)
	c, _ := findCheck(r, "config")
	if c.Status != StatusFail {
		t.Errorf("expected config fail, got %+v", c)
	}
	if r.OK {
		t.Error("OK must be false when config errored")
	}
}

func TestRender_IncludesHints(t *testing.T) {
	r := Report{
		Checks: []Check{{Name: "x", Status: StatusFail, Message: "broken", Hint: "fix it"}},
		OK:     false,
	}
	out := r.Render()
	if !strings.Contains(out, "[fail] x:") || !strings.Contains(out, "hint: fix it") {
		t.Errorf("render missing fields:\n%s", out)
	}
}
