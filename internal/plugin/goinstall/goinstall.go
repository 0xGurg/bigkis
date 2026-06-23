// Package goinstall manages Go packages installed via `go install`.
package goinstall

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/0xGurg/bigkis/internal/config"
	"github.com/0xGurg/bigkis/internal/plan"
	"github.com/0xGurg/bigkis/internal/plugin"
	"github.com/0xGurg/bigkis/internal/runner"
	"github.com/0xGurg/bigkis/internal/state"
	"github.com/0xGurg/bigkis/internal/ui"
)

type GoInstall struct {
	cachedDiff *plan.Diff
	runner     *runner.Runner
}

func New() *GoInstall { return &GoInstall{} }

func (g *GoInstall) SetRunner(r *runner.Runner) { g.runner = r }

func (g *GoInstall) Name() string { return config.PluginGoInstall }

func (g *GoInstall) Available(cfg *config.Config) error {
	if !runner.HasCommand("go") {
		return fmt.Errorf("go not found on PATH")
	}
	return nil
}

func (g *GoInstall) Plan(cfg *config.Config, st *state.State) (plugin.Report, error) {
	r := g.runner
	if r == nil {
		r = runner.New(false)
	}
	gobin, err := gobinDir(r)
	if err != nil {
		return plugin.Report{}, fmt.Errorf("probe goinstall: %w", err)
	}
	actual := probeGoInstall(gobin, cfg.GoInstall.Packages)

	var last []string
	if _, err := st.Get(g.Name(), &last); err != nil {
		return plugin.Report{}, err
	}
	d := plan.Compute(cfg.GoInstall.Packages, actual, last, cfg.GoInstall.Ignored)
	g.cachedDiff = &d

	rep := plugin.Report{Plugin: g.Name()}
	for _, name := range d.Add {
		rep.Operations = append(rep.Operations, plugin.Operation{Kind: plugin.OpAdd, Target: name})
	}
	for _, name := range d.Remove {
		rep.Operations = append(rep.Operations, plugin.Operation{Kind: plugin.OpRemove, Target: name})
	}
	return rep, nil
}

func (g *GoInstall) Apply(cfg *config.Config, st *state.State, report plugin.Report, r *runner.Runner, u *ui.UI) error {
	if g.cachedDiff == nil {
		return fmt.Errorf("goinstall: Apply called before Plan")
	}
	d := *g.cachedDiff
	if err := assertReportMatchesDiff(report, d); err != nil {
		return fmt.Errorf("goinstall: %w", err)
	}
	if !d.HasChanges() {
		u.Step("goinstall: nothing to do")
		return nil
	}

	gobin, err := gobinDir(r)
	if err != nil {
		return fmt.Errorf("goinstall: get GOBIN: %w", err)
	}

	if len(d.Remove) > 0 {
		u.Step("goinstall: removing %d package(s)", len(d.Remove))
		for _, pkg := range d.Remove {
			bin := binaryName(pkg)
			path := filepath.Join(gobin, bin)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove %s: %w", path, err)
			}
		}
	}
	if len(d.Add) > 0 {
		u.Step("goinstall: installing %d package(s)", len(d.Add))
		for _, pkg := range d.Add {
			args := []string{"install", pkg + "@latest"}
			if _, err := r.Run(runner.Spec{Name: "go", Args: args}); err != nil {
				return fmt.Errorf("go install %s: %w", pkg, err)
			}
		}
	}
	return nil
}

func (g *GoInstall) Upgrade(cfg *config.Config, st *state.State, r *runner.Runner, u *ui.UI) error {
	_ = st
	u.Step("goinstall: reinstalling declared packages to upgrade")
	for _, pkg := range cfg.GoInstall.Packages {
		args := []string{"install", pkg + "@latest"}
		if _, err := r.Run(runner.Spec{Name: "go", Args: args}); err != nil {
			continue
		}
	}
	return nil
}

func (g *GoInstall) PendingUpgrades(cfg *config.Config, r *runner.Runner) (plugin.UpgradeReport, error) {
	_ = cfg
	// Detecting upgrades for go-installed packages requires querying the module
	// proxy for each package, which is complex and unreliable across different
	// module paths. Best-effort: return empty report.
	return plugin.UpgradeReport{Plugin: g.Name()}, nil
}

func (g *GoInstall) PersistState(cfg *config.Config, st *state.State) error {
	declared := dedupAndIgnore(cfg.GoInstall.Packages, cfg.GoInstall.Ignored)
	return st.Set(g.Name(), declared)
}

// ── helpers ──

// gobinDir returns the GOBIN directory path. Falls back to GOPATH/bin.
func gobinDir(r *runner.Runner) (string, error) {
	out, err := r.Capture("go", "env", "GOBIN")
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if out != "" && out != "GOBIN=" {
		// go env may output "set GOBIN=C:\..." on Windows or just "/path".
		// Strip any "set " prefix and "KEY=" prefix for robustness.
		out = strings.TrimPrefix(out, "set ")
		if idx := strings.Index(out, "="); idx >= 0 {
			out = out[idx+1:]
		}
		out = strings.TrimSpace(out)
	}
	if out == "" {
		gopath, err := r.Capture("go", "env", "GOPATH")
		if err != nil {
			return "", err
		}
		gopath = strings.TrimSpace(gopath)
		gopath = strings.TrimPrefix(gopath, "set ")
		if idx := strings.Index(gopath, "="); idx >= 0 {
			gopath = gopath[idx+1:]
		}
		gopath = strings.TrimSpace(gopath)
		if gopath == "" {
			return "", fmt.Errorf("cannot determine GOBIN or GOPATH")
		}
		out = filepath.Join(gopath, "bin")
	}
	return out, nil
}

// binaryName returns the last path segment of a Go package path.
// e.g. "golang.org/x/tools/cmd/stringer" → "stringer"
func binaryName(pkg string) string {
	// Strip @version if present
	if idx := strings.Index(pkg, "@"); idx >= 0 {
		pkg = pkg[:idx]
	}
	return filepath.Base(pkg)
}

// probeGoInstall checks which declared packages are already installed by
// looking for binaries in GOBIN.
func probeGoInstall(gobin string, declared []string) []string {
	var installed []string
	for _, pkg := range declared {
		bin := binaryName(pkg)
		path := filepath.Join(gobin, bin)
		if _, err := os.Stat(path); err == nil {
			installed = append(installed, pkg)
		}
	}
	return installed
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
