// Package pacman manages native Arch Linux packages.
package pacman

import (
	"fmt"
	"strings"

	"codeberg.org/gurg/bigkis/internal/config"
	"codeberg.org/gurg/bigkis/internal/plan"
	"codeberg.org/gurg/bigkis/internal/plugin"
	"codeberg.org/gurg/bigkis/internal/runner"
	"codeberg.org/gurg/bigkis/internal/state"
	"codeberg.org/gurg/bigkis/internal/ui"
)

type Pacman struct {
	cachedDiff *plan.Diff
	// runner is consulted by Plan for probes; if nil, Plan creates a fresh
	// runner.New(false). Tests use SetRunner to inject a Fake.
	runner *runner.Runner
}

func New() *Pacman { return &Pacman{} }

// SetRunner injects a runner used by Plan for probes. Intended for tests.
func (p *Pacman) SetRunner(r *runner.Runner) { p.runner = r }

func (p *Pacman) Name() string { return config.PluginPacman }

func (p *Pacman) Available(cfg *config.Config) error {
	if !runner.HasCommand("pacman") {
		return fmt.Errorf("pacman not found on PATH")
	}
	return nil
}

// Probe returns the explicitly-installed native packages.
func (p *Pacman) Probe(r *runner.Runner) ([]string, error) {
	out, err := r.Capture("pacman", "-Qqen")
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

func (p *Pacman) Plan(cfg *config.Config, st *state.State) (plugin.Report, error) {
	r := p.runner
	if r == nil {
		r = runner.New(false)
	}
	actual, err := p.Probe(r)
	if err != nil {
		return plugin.Report{}, fmt.Errorf("probe pacman: %w", err)
	}
	var last []string
	if _, err := st.Get(p.Name(), &last); err != nil {
		return plugin.Report{}, err
	}
	d := plan.Compute(cfg.Pacman.Packages, actual, last, cfg.Pacman.Ignored)
	p.cachedDiff = &d

	rep := plugin.Report{Plugin: p.Name()}
	for _, name := range d.Add {
		rep.Operations = append(rep.Operations, plugin.Operation{Kind: plugin.OpAdd, Target: name})
	}
	for _, name := range d.Remove {
		rep.Operations = append(rep.Operations, plugin.Operation{Kind: plugin.OpRemove, Target: name})
	}
	return rep, nil
}

func (p *Pacman) Upgrade(cfg *config.Config, st *state.State, r *runner.Runner, u *ui.UI) error {
	_ = cfg
	_ = st
	u.Step("pacman: upgrading native packages (sync + update)")
	if _, err := r.Run(runner.Spec{Name: "pacman", Args: []string{"-Syu", "--noconfirm"}, Sudo: true}); err != nil {
		return fmt.Errorf("pacman -Syu: %w", err)
	}
	return nil
}

// PendingUpgrades probes which native packages have newer versions available
// using pacman -Qu. Output format: "package oldver -> newver". pacman -Qu
// exits 0 with empty output when no upgrades are pending; exits 1 when the
// package database hasn't been synced (treated as "no info available").
func (p *Pacman) PendingUpgrades(cfg *config.Config, r *runner.Runner) (plugin.UpgradeReport, error) {
	_ = cfg
	out, err := r.Capture("pacman", "-Qu")
	if err != nil {
		// pacman -Qu exits 1 when databases are stale (no -Sy run). Treat as
		// "no upgrade info available" rather than a hard error.
		if runner.IsExitCode(err, 1) && out == "" {
			return plugin.UpgradeReport{Plugin: p.Name()}, nil
		}
		return plugin.UpgradeReport{}, fmt.Errorf("pacman -Qu: %w", err)
	}
	rep := plugin.UpgradeReport{Plugin: p.Name()}
	for _, line := range splitLines(out) {
		name, detail := parseUpgradeLine(line)
		if name == "" {
			continue
		}
		rep.Operations = append(rep.Operations, plugin.Operation{
			Kind:   plugin.OpUpdate,
			Target: name,
			Detail: detail,
		})
	}
	return rep, nil
}

// parseUpgradeLine parses "package oldver -> newver" output from pacman -Qu.
func parseUpgradeLine(line string) (name string, detail string) {
	// Format: "neovim 0.9.5-1 -> 0.10.0-1"
	parts := strings.SplitN(line, " ", 2)
	if len(parts) < 2 {
		return line, ""
	}
	name = parts[0]
	// The rest is "oldver -> newver"
	verPart := strings.TrimSpace(parts[1])
	return name, verPart
}

func (p *Pacman) Apply(cfg *config.Config, st *state.State, report plugin.Report, r *runner.Runner, u *ui.UI) error {
	if p.cachedDiff == nil {
		return fmt.Errorf("pacman: Apply called before Plan")
	}
	d := *p.cachedDiff
	if err := assertReportMatchesDiff(report, d); err != nil {
		return fmt.Errorf("pacman: %w", err)
	}
	if !d.HasChanges() {
		u.Step("pacman: nothing to do")
		return nil
	}

	// Demote and prune before installing so that conflicting packages (e.g.
	// replacing "nvidia" with "nvidia-dkms") are gone before the new package
	// is installed. Installing first fails when the new package conflicts
	// with the one still on disk.
	if len(d.Remove) > 0 {
		// Capture pre-existing orphans so the prune below does not yank
		// packages that were already orphans before bigkis touched anything.
		preOrphans, err := captureOrphans(r)
		if err != nil {
			return fmt.Errorf("query existing orphans: %w", err)
		}

		u.Step("pacman: marking %d package(s) as deps", len(d.Remove))
		demoteArgs := append([]string{"-D", "--asdeps"}, d.Remove...)
		if _, err := r.Run(runner.Spec{Name: "pacman", Args: demoteArgs, Sudo: true}); err != nil {
			return fmt.Errorf("pacman -D --asdeps: %w", err)
		}

		mode := cfg.Settings.PruneOrphans
		if mode == "" {
			mode = config.PruneOrphansScoped
		}
		switch mode {
		case config.PruneOrphansNone:
		case config.PruneOrphansAll:
			u.Step("pacman: pruning orphans (all)")
			if err := pruneOrphans(r, nil); err != nil {
				return err
			}
		default: // scoped
			u.Step("pacman: pruning orphans (scoped to this apply)")
			if err := pruneOrphans(r, preOrphans); err != nil {
				return err
			}
		}
	}

	if len(d.Add) > 0 {
		u.Step("pacman: installing %d package(s)", len(d.Add))
		args := append([]string{"-S", "--needed", "--noconfirm"}, d.Add...)
		if _, err := r.Run(runner.Spec{Name: "pacman", Args: args, Sudo: true}); err != nil {
			return fmt.Errorf("pacman -S: %w", err)
		}
	}
	return nil
}

// assertReportMatchesDiff verifies the Report passed to Apply matches the
// Diff captured during Plan. The orchestrator passes the user-confirmed
// Report; we refuse to apply if it disagrees with the cached plan.
func assertReportMatchesDiff(report plugin.Report, d plan.Diff) error {
	declared := map[string]bool{}
	for _, op := range report.Operations {
		key := opKey(op.Kind, op.Target)
		declared[key] = true
	}
	cached := map[string]bool{}
	for _, name := range d.Add {
		cached[opKey(plugin.OpAdd, name)] = true
	}
	for _, name := range d.Remove {
		cached[opKey(plugin.OpRemove, name)] = true
	}
	for k := range declared {
		if !cached[k] {
			return fmt.Errorf("report op %q not in cached plan; rerun Plan", k)
		}
	}
	// Relaxed: cached plan may contain ops the user chose to skip (subset
	// report from selective-apply TUI). Only declared→cached is enforced.
	return nil
}

func opKey(kind plugin.OpKind, target string) string {
	prefix := "+"
	if kind == plugin.OpRemove {
		prefix = "-"
	}
	return prefix + target
}

func (p *Pacman) PersistState(cfg *config.Config, st *state.State) error {
	declared := dedupAndFilter(cfg.Pacman.Packages, cfg.Pacman.Ignored)
	return st.Set(p.Name(), declared)
}

// captureOrphans returns the current set of orphan packages on the system.
// pacman -Qdtq exits with status 1 when there are no orphans, which we treat
// as an empty set rather than an error.
func captureOrphans(r *runner.Runner) (map[string]struct{}, error) {
	out, err := r.Capture("pacman", "-Qdtq")
	if err != nil {
		if runner.IsExitCode(err, 1) {
			return map[string]struct{}{}, nil
		}
		return nil, err
	}
	set := map[string]struct{}{}
	for _, name := range splitLines(out) {
		set[name] = struct{}{}
	}
	return set, nil
}

// pruneOrphans removes packages that are now installed only as dependencies
// of nothing. When preExisting is non-nil, orphans whose names are in that
// set are left alone (they were orphans before this apply). It loops because
// removing an orphan may orphan more.
func pruneOrphans(r *runner.Runner, preExisting map[string]struct{}) error {
	for {
		current, err := captureOrphans(r)
		if err != nil {
			return fmt.Errorf("query orphans: %w", err)
		}
		var toRemove []string
		for name := range current {
			if preExisting != nil {
				if _, was := preExisting[name]; was {
					continue
				}
			}
			toRemove = append(toRemove, name)
		}
		if len(toRemove) == 0 {
			return nil
		}
		args := append([]string{"-Rns", "--noconfirm"}, toRemove...)
		if _, err := r.Run(runner.Spec{Name: "pacman", Args: args, Sudo: true}); err != nil {
			return fmt.Errorf("pacman -Rns orphans: %w", err)
		}
	}
}

func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func dedupAndFilter(items, ignored []string) []string {
	ig := map[string]struct{}{}
	for _, x := range ignored {
		ig[x] = struct{}{}
	}
	seen := map[string]struct{}{}
	var out []string
	for _, x := range items {
		if _, skip := ig[x]; skip {
			continue
		}
		if _, dup := seen[x]; dup {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	return out
}
