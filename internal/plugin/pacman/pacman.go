// Package pacman manages native Arch Linux packages.
package pacman

import (
	"fmt"
	"strings"

	"github.com/georgepagarigan/bigkis/internal/config"
	"github.com/georgepagarigan/bigkis/internal/plan"
	"github.com/georgepagarigan/bigkis/internal/plugin"
	"github.com/georgepagarigan/bigkis/internal/runner"
	"github.com/georgepagarigan/bigkis/internal/state"
	"github.com/georgepagarigan/bigkis/internal/ui"
)

type Pacman struct {
	cachedDiff *plan.Diff
}

func New() *Pacman { return &Pacman{} }

func (p *Pacman) Name() string { return config.PluginPacman }

func (p *Pacman) Available() error {
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
	r := runner.New(false)
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

func (p *Pacman) Apply(cfg *config.Config, st *state.State, r *runner.Runner, u *ui.UI) error {
	if p.cachedDiff == nil {
		if _, err := p.Plan(cfg, st); err != nil {
			return err
		}
	}
	d := *p.cachedDiff
	if !d.HasChanges() {
		u.Step("pacman: nothing to do")
		return nil
	}

	if len(d.Add) > 0 {
		u.Step("pacman: installing %d package(s)", len(d.Add))
		args := append([]string{"-S", "--needed", "--noconfirm"}, d.Add...)
		if _, err := r.Run(runner.Spec{Name: "pacman", Args: args, Sudo: true}); err != nil {
			return fmt.Errorf("pacman -S: %w", err)
		}
	}

	if len(d.Remove) > 0 {
		u.Step("pacman: marking %d package(s) as deps", len(d.Remove))
		demoteArgs := append([]string{"-D", "--asdeps"}, d.Remove...)
		if _, err := r.Run(runner.Spec{Name: "pacman", Args: demoteArgs, Sudo: true}); err != nil {
			return fmt.Errorf("pacman -D --asdeps: %w", err)
		}
		u.Step("pacman: pruning orphans")
		if err := pruneOrphans(r); err != nil {
			return err
		}
	}
	return nil
}

func (p *Pacman) PersistState(cfg *config.Config, st *state.State) error {
	declared := dedupAndFilter(cfg.Pacman.Packages, cfg.Pacman.Ignored)
	return st.Set(p.Name(), declared)
}

// pruneOrphans removes packages that are installed only as dependencies of
// nothing. It loops because removing an orphan may orphan more.
func pruneOrphans(r *runner.Runner) error {
	for {
		out, err := r.Capture("pacman", "-Qdtq")
		if err != nil {
			// pacman -Qdtq exits 1 when there are no orphans; treat as done.
			if strings.Contains(err.Error(), "exit status 1") {
				return nil
			}
			return fmt.Errorf("query orphans: %w", err)
		}
		orphans := splitLines(out)
		if len(orphans) == 0 {
			return nil
		}
		args := append([]string{"-Rns", "--noconfirm"}, orphans...)
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
