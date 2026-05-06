// Package aur manages foreign packages built from the AUR by wrapping an
// installed helper such as yay or paru.
package aur

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

type AUR struct {
	helper     string
	cachedDiff *plan.Diff
	// runner is consulted by Plan for probes; if nil, Plan creates a fresh
	// runner.New(false). Tests use SetRunner to inject a Fake.
	runner *runner.Runner
}

func New() *AUR { return &AUR{} }

// SetRunner injects a runner used by Plan for probes. Intended for tests.
func (a *AUR) SetRunner(r *runner.Runner) { a.runner = r }

func (a *AUR) Name() string { return config.PluginAUR }

// Available checks that the tools needed for the AUR plugin to function are
// on PATH. We need both pacman (to query foreign packages) and the user's
// configured AUR helper. The helper is checked here instead of inside Apply
// so status / dry-run surface a missing helper before the user is prompted.
func (a *AUR) Available(cfg *config.Config) error {
	if !runner.HasCommand("pacman") {
		return fmt.Errorf("pacman not found on PATH (required to query foreign packages)")
	}
	helper := cfg.Settings.AURHelper
	if helper == "" {
		return fmt.Errorf("settings.aur_helper is not set")
	}
	if !runner.HasCommand(helper) {
		return fmt.Errorf("aur helper %q not found on PATH; install it or change settings.aur_helper", helper)
	}
	return nil
}

// Probe returns the foreign packages installed on the system. "Foreign" means
// installed but not present in any sync repository, which is what AUR
// installs are.
func (a *AUR) Probe(r *runner.Runner) ([]string, error) {
	out, err := r.Capture("pacman", "-Qqm")
	if err != nil {
		// pacman -Qqm exits 1 when there are no foreign packages; that's fine.
		if runner.IsExitCode(err, 1) {
			return nil, nil
		}
		return nil, err
	}
	return splitLines(out), nil
}

func (a *AUR) Plan(cfg *config.Config, st *state.State) (plugin.Report, error) {
	a.helper = cfg.Settings.AURHelper
	r := a.runner
	if r == nil {
		r = runner.New(false)
	}
	actual, err := a.Probe(r)
	if err != nil {
		return plugin.Report{}, fmt.Errorf("probe aur: %w", err)
	}
	var last []string
	if _, err := st.Get(a.Name(), &last); err != nil {
		return plugin.Report{}, err
	}
	d := plan.Compute(cfg.AUR.Packages, actual, last, cfg.AUR.Ignored)
	a.cachedDiff = &d

	rep := plugin.Report{Plugin: a.Name()}
	for _, name := range d.Add {
		rep.Operations = append(rep.Operations, plugin.Operation{
			Kind: plugin.OpAdd, Target: name, Detail: "via " + a.helper,
		})
	}
	for _, name := range d.Remove {
		rep.Operations = append(rep.Operations, plugin.Operation{
			Kind: plugin.OpRemove, Target: name, Detail: "via " + a.helper,
		})
	}
	return rep, nil
}

func (a *AUR) Apply(cfg *config.Config, st *state.State, report plugin.Report, r *runner.Runner, u *ui.UI) error {
	if a.cachedDiff == nil {
		return fmt.Errorf("aur: Apply called before Plan")
	}
	d := *a.cachedDiff
	if err := assertReportMatchesDiff(report, d); err != nil {
		return fmt.Errorf("aur: %w", err)
	}
	if !d.HasChanges() {
		u.Step("aur: nothing to do")
		return nil
	}

	if a.helper == "" {
		a.helper = cfg.Settings.AURHelper
	}

	// AUR helpers must be invoked as a non-root user. We rely on the user
	// running bigkis to be a sudoer; the helper itself elevates as needed.
	if len(d.Add) > 0 {
		u.Step("aur: installing %d package(s) via %s", len(d.Add), a.helper)
		args := append([]string{"-S", "--needed", "--noconfirm"}, d.Add...)
		if _, err := r.Run(runner.Spec{Name: a.helper, Args: args}); err != nil {
			return fmt.Errorf("%s -S: %w", a.helper, err)
		}
	}

	if len(d.Remove) > 0 {
		u.Step("aur: removing %d package(s) via %s", len(d.Remove), a.helper)
		args := append([]string{"-Rns", "--noconfirm"}, d.Remove...)
		if _, err := r.Run(runner.Spec{Name: a.helper, Args: args}); err != nil {
			return fmt.Errorf("%s -Rns: %w", a.helper, err)
		}
	}
	return nil
}

// assertReportMatchesDiff verifies the Report passed to Apply matches the
// Diff captured during Plan, so we never apply a different set of changes
// than what the user confirmed.
func assertReportMatchesDiff(report plugin.Report, d plan.Diff) error {
	declared := map[string]bool{}
	for _, op := range report.Operations {
		declared[opKey(op.Kind, op.Target)] = true
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
	for k := range cached {
		if !declared[k] {
			return fmt.Errorf("cached plan op %q not in report; rerun Plan", k)
		}
	}
	return nil
}

func opKey(kind plugin.OpKind, target string) string {
	prefix := "+"
	if kind == plugin.OpRemove {
		prefix = "-"
	}
	return prefix + target
}

func (a *AUR) PersistState(cfg *config.Config, st *state.State) error {
	declared := dedupAndFilter(cfg.AUR.Packages, cfg.AUR.Ignored)
	return st.Set(a.Name(), declared)
}

func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		// pacman -Qqm prints "name version" with the -m flag alone; with -q
		// it prints just names. We tolerate either by taking the first field.
		fields := strings.Fields(line)
		if len(fields) > 0 {
			out = append(out, fields[0])
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
