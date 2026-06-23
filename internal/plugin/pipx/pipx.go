// Package pipx manages Python packages installed via `pipx install`.
package pipx

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/0xGurg/bigkis/internal/config"
	"github.com/0xGurg/bigkis/internal/plan"
	"github.com/0xGurg/bigkis/internal/plugin"
	"github.com/0xGurg/bigkis/internal/runner"
	"github.com/0xGurg/bigkis/internal/state"
	"github.com/0xGurg/bigkis/internal/ui"
)

type Pipx struct {
	cachedDiff *plan.Diff
	runner     *runner.Runner
}

func New() *Pipx { return &Pipx{} }

func (p *Pipx) SetRunner(r *runner.Runner) { p.runner = r }

func (p *Pipx) Name() string { return config.PluginPipx }

func (p *Pipx) Available(cfg *config.Config) error {
	if !runner.HasCommand("pipx") {
		return fmt.Errorf("pipx not found on PATH")
	}
	return nil
}

func (p *Pipx) Plan(cfg *config.Config, st *state.State) (plugin.Report, error) {
	r := p.runner
	if r == nil {
		r = runner.New(false)
	}
	actual, err := probePipx(r)
	if err != nil {
		return plugin.Report{}, fmt.Errorf("probe pipx: %w", err)
	}
	var last []string
	if _, err := st.Get(p.Name(), &last); err != nil {
		return plugin.Report{}, err
	}
	d := plan.Compute(cfg.Pipx.Packages, actual, last, cfg.Pipx.Ignored)
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

func (p *Pipx) Apply(cfg *config.Config, st *state.State, report plugin.Report, r *runner.Runner, u *ui.UI) error {
	if p.cachedDiff == nil {
		return fmt.Errorf("pipx: Apply called before Plan")
	}
	d := *p.cachedDiff
	if err := assertReportMatchesDiff(report, d); err != nil {
		return fmt.Errorf("pipx: %w", err)
	}
	if !d.HasChanges() {
		u.Step("pipx: nothing to do")
		return nil
	}

	if len(d.Remove) > 0 {
		u.Step("pipx: uninstalling %d package(s)", len(d.Remove))
		for _, pkg := range d.Remove {
			if _, err := r.Run(runner.Spec{Name: "pipx", Args: []string{"uninstall", pkg}}); err != nil {
				return fmt.Errorf("pipx uninstall %s: %w", pkg, err)
			}
		}
	}
	if len(d.Add) > 0 {
		u.Step("pipx: installing %d package(s)", len(d.Add))
		for _, pkg := range d.Add {
			if _, err := r.Run(runner.Spec{Name: "pipx", Args: []string{"install", pkg}}); err != nil {
				return fmt.Errorf("pipx install %s: %w", pkg, err)
			}
		}
	}
	return nil
}

func (p *Pipx) Upgrade(cfg *config.Config, st *state.State, r *runner.Runner, u *ui.UI) error {
	_ = cfg
	_ = st
	u.Step("pipx: upgrading all packages")
	if _, err := r.Run(runner.Spec{Name: "pipx", Args: []string{"upgrade-all"}}); err != nil {
		return fmt.Errorf("pipx upgrade-all: %w", err)
	}
	return nil
}

func (p *Pipx) PendingUpgrades(cfg *config.Config, r *runner.Runner) (plugin.UpgradeReport, error) {
	_ = cfg
	// pipx doesn't have a built-in outdated check that works well across
	// versions. Best-effort: return empty report.
	return plugin.UpgradeReport{Plugin: p.Name()}, nil
}

func (p *Pipx) PersistState(cfg *config.Config, st *state.State) error {
	declared := dedupAndIgnore(cfg.Pipx.Packages, cfg.Pipx.Ignored)
	return st.Set(p.Name(), declared)
}

// ── helpers ──

// pipxListOutput matches `pipx list --json` structure.
type pipxListOutput struct {
	Venvs map[string]struct{} `json:"venvs"`
}

func probePipx(r *runner.Runner) ([]string, error) {
	out, err := r.Capture("pipx", "list", "--json")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	var lo pipxListOutput
	if err := json.Unmarshal([]byte(out), &lo); err != nil {
		return nil, fmt.Errorf("parse pipx list: %w", err)
	}
	var pkgs []string
	for name := range lo.Venvs {
		pkgs = append(pkgs, name)
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
