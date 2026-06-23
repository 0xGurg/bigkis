// Package cargo manages Rust packages installed via `cargo install`.
package cargo

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/0xGurg/bigkis/internal/config"
	"github.com/0xGurg/bigkis/internal/plan"
	"github.com/0xGurg/bigkis/internal/plugin"
	"github.com/0xGurg/bigkis/internal/runner"
	"github.com/0xGurg/bigkis/internal/state"
	"github.com/0xGurg/bigkis/internal/ui"
)

// cargoListLine matches "package v1.0.0:" lines from `cargo install --list`.
var cargoListLine = regexp.MustCompile(`^(\S+) v`)

type Cargo struct {
	cachedDiff *plan.Diff
	runner     *runner.Runner
}

func New() *Cargo { return &Cargo{} }

func (c *Cargo) SetRunner(r *runner.Runner) { c.runner = r }

func (c *Cargo) Name() string { return config.PluginCargo }

func (c *Cargo) Available(cfg *config.Config) error {
	if !runner.HasCommand("cargo") {
		return fmt.Errorf("cargo not found on PATH")
	}
	return nil
}

func (c *Cargo) Plan(cfg *config.Config, st *state.State) (plugin.Report, error) {
	r := c.runner
	if r == nil {
		r = runner.New(false)
	}
	actual, err := probeCargo(r)
	if err != nil {
		return plugin.Report{}, fmt.Errorf("probe cargo: %w", err)
	}
	var last []string
	if _, err := st.Get(c.Name(), &last); err != nil {
		return plugin.Report{}, err
	}
	d := plan.Compute(cfg.Cargo.Packages, actual, last, cfg.Cargo.Ignored)
	c.cachedDiff = &d

	rep := plugin.Report{Plugin: c.Name()}
	for _, name := range d.Add {
		rep.Operations = append(rep.Operations, plugin.Operation{Kind: plugin.OpAdd, Target: name})
	}
	for _, name := range d.Remove {
		rep.Operations = append(rep.Operations, plugin.Operation{Kind: plugin.OpRemove, Target: name})
	}
	return rep, nil
}

func (c *Cargo) Apply(cfg *config.Config, st *state.State, report plugin.Report, r *runner.Runner, u *ui.UI) error {
	if c.cachedDiff == nil {
		return fmt.Errorf("cargo: Apply called before Plan")
	}
	d := *c.cachedDiff
	if err := assertReportMatchesDiff(report, d); err != nil {
		return fmt.Errorf("cargo: %w", err)
	}
	if !d.HasChanges() {
		u.Step("cargo: nothing to do")
		return nil
	}

	if len(d.Remove) > 0 {
		u.Step("cargo: uninstalling %d package(s)", len(d.Remove))
		for _, pkg := range d.Remove {
			args := []string{"uninstall", pkg}
			if _, err := r.Run(runner.Spec{Name: "cargo", Args: args}); err != nil {
				return fmt.Errorf("cargo uninstall %s: %w", pkg, err)
			}
		}
	}
	if len(d.Add) > 0 {
		u.Step("cargo: installing %d package(s)", len(d.Add))
		for _, pkg := range d.Add {
			args := []string{"install", pkg}
			if _, err := r.Run(runner.Spec{Name: "cargo", Args: args}); err != nil {
				return fmt.Errorf("cargo install %s: %w", pkg, err)
			}
		}
	}
	return nil
}

func (c *Cargo) Upgrade(cfg *config.Config, st *state.State, r *runner.Runner, u *ui.UI) error {
	_ = st
	u.Step("cargo: reinstalling declared packages to upgrade")
	for _, pkg := range cfg.Cargo.Packages {
		// cargo install re-installs/upgrades if already present.
		args := []string{"install", pkg}
		if _, err := r.Run(runner.Spec{Name: "cargo", Args: args}); err != nil {
			// Best-effort: skip packages that fail to upgrade.
			continue
		}
	}
	return nil
}

func (c *Cargo) PendingUpgrades(cfg *config.Config, r *runner.Runner) (plugin.UpgradeReport, error) {
	_ = cfg
	if !runner.HasCommand("cargo") {
		return plugin.UpgradeReport{Plugin: c.Name()}, nil
	}

	// Probe currently installed versions.
	out, err := r.Capture("cargo", "install", "--list")
	if err != nil {
		return plugin.UpgradeReport{Plugin: c.Name()}, nil
	}

	type installedPkg struct {
		name    string
		version string
	}
	var pkgs []installedPkg
	for _, line := range splitLines(out) {
		m := regexp.MustCompile(`^(\S+) v(\S+):`).FindStringSubmatch(line)
		if len(m) == 3 {
			pkgs = append(pkgs, installedPkg{name: m[1], version: m[2]})
		}
	}

	var ops []plugin.Operation
	for _, pkg := range pkgs {
		// Query latest version via `cargo search`.
		searchOut, err := r.Capture("cargo", "search", pkg.name, "--limit", "1")
		if err != nil {
			continue
		}
		// cargo search output: "name = \"version\"    # description"
		rm := regexp.MustCompile(fmt.Sprintf(`^%s\s*=\s*"([^"]+)"`, regexp.QuoteMeta(pkg.name))).FindStringSubmatch(searchOut)
		if len(rm) < 2 {
			continue
		}
		latest := rm[1]
		if latest != pkg.version {
			ops = append(ops, plugin.Operation{
				Kind:   plugin.OpUpdate,
				Target: pkg.name,
				Detail: pkg.version + " → " + latest,
			})
		}
	}
	return plugin.UpgradeReport{Plugin: c.Name(), Operations: ops}, nil
}

func (c *Cargo) PersistState(cfg *config.Config, st *state.State) error {
	declared := dedupAndIgnore(cfg.Cargo.Packages, cfg.Cargo.Ignored)
	return st.Set(c.Name(), declared)
}

// ── helpers ──

func probeCargo(r *runner.Runner) ([]string, error) {
	out, err := r.Capture("cargo", "install", "--list")
	if err != nil {
		return nil, err
	}
	var pkgs []string
	for _, line := range splitLines(out) {
		m := cargoListLine.FindStringSubmatch(line)
		if len(m) == 2 {
			pkgs = append(pkgs, m[1])
		}
	}
	return pkgs, nil
}

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
	return nil
}

func opKey(kind plugin.OpKind, target string) string {
	if kind == plugin.OpRemove {
		return "-" + target
	}
	return "+" + target
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

func dedupAndIgnore(items, ignored []string) []string {
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
