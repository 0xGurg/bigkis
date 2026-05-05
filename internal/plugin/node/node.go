// Package node manages globally-installed node packages, with one of npm,
// pnpm, or yarn (classic) as the underlying manager. Each declared package
// can override the default manager.
package node

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/georgepagarigan/bigkis/internal/config"
	"github.com/georgepagarigan/bigkis/internal/plan"
	"github.com/georgepagarigan/bigkis/internal/plugin"
	"github.com/georgepagarigan/bigkis/internal/runner"
	"github.com/georgepagarigan/bigkis/internal/state"
	"github.com/georgepagarigan/bigkis/internal/ui"
)

const (
	mgrNPM  = "npm"
	mgrPNPM = "pnpm"
	mgrYARN = "yarn"
)

// Node is the bigkis plugin for global node packages.
type Node struct {
	cached map[string]plan.Diff
}

// persisted maps manager → declared package names from the most recent run.
type persisted map[string][]string

func New() *Node { return &Node{} }

func (n *Node) Name() string { return config.PluginNode }

// Available returns nil if at least one manager is on PATH. Individual
// managers are checked again at Apply time per declared package.
func (n *Node) Available() error {
	if !runner.HasCommand(mgrNPM) && !runner.HasCommand(mgrPNPM) && !runner.HasCommand(mgrYARN) {
		return fmt.Errorf("none of npm, pnpm, yarn found on PATH")
	}
	return nil
}

func (n *Node) Plan(cfg *config.Config, st *state.State) (plugin.Report, error) {
	declaredByMgr := groupDeclared(cfg)
	r := runner.New(false)

	var prev persisted
	if _, err := st.Get(n.Name(), &prev); err != nil {
		return plugin.Report{}, err
	}

	managers := allManagers(declaredByMgr, prev)
	diffs := map[string]plan.Diff{}
	for _, m := range managers {
		actual, err := probeManager(r, m)
		if err != nil {
			return plugin.Report{}, fmt.Errorf("probe %s: %w", m, err)
		}
		diffs[m] = plan.Compute(declaredByMgr[m], actual, prev[m], nil)
	}
	n.cached = diffs

	rep := plugin.Report{Plugin: n.Name()}
	sort.Strings(managers)
	for _, m := range managers {
		d := diffs[m]
		for _, name := range d.Add {
			rep.Operations = append(rep.Operations, plugin.Operation{Kind: plugin.OpAdd, Target: name, Detail: "via " + m})
		}
		for _, name := range d.Remove {
			rep.Operations = append(rep.Operations, plugin.Operation{Kind: plugin.OpRemove, Target: name, Detail: "via " + m})
		}
	}
	return rep, nil
}

func (n *Node) Apply(cfg *config.Config, st *state.State, r *runner.Runner, u *ui.UI) error {
	if n.cached == nil {
		if _, err := n.Plan(cfg, st); err != nil {
			return err
		}
	}

	any := false
	managers := make([]string, 0, len(n.cached))
	for m := range n.cached {
		managers = append(managers, m)
	}
	sort.Strings(managers)

	for _, m := range managers {
		d := n.cached[m]
		if !d.HasChanges() {
			continue
		}
		if !runner.HasCommand(m) {
			return fmt.Errorf("%s required to manage %d package(s) but not on PATH", m, len(d.Add)+len(d.Remove))
		}
		any = true

		if len(d.Add) > 0 {
			u.Step("node: installing %d package(s) via %s", len(d.Add), m)
			if _, err := r.Run(runner.Spec{Name: m, Args: installArgs(m, d.Add)}); err != nil {
				return fmt.Errorf("%s install: %w", m, err)
			}
		}
		if len(d.Remove) > 0 {
			u.Step("node: removing %d package(s) via %s", len(d.Remove), m)
			if _, err := r.Run(runner.Spec{Name: m, Args: removeArgs(m, d.Remove)}); err != nil {
				return fmt.Errorf("%s remove: %w", m, err)
			}
		}
	}

	if !any {
		u.Step("node: nothing to do")
	}
	return nil
}

func (n *Node) PersistState(cfg *config.Config, st *state.State) error {
	declaredByMgr := groupDeclared(cfg)
	out := persisted{}
	for m, pkgs := range declaredByMgr {
		out[m] = pkgs
	}
	return st.Set(n.Name(), out)
}

// groupDeclared returns a map manager → declared package list, applying
// per-package overrides on top of settings.node_manager.
func groupDeclared(cfg *config.Config) map[string][]string {
	out := map[string][]string{}
	overrides := map[string]string{}
	for _, np := range cfg.Node.Package {
		mgr := np.Manager
		if mgr == "" {
			mgr = cfg.Settings.NodeManager
		}
		overrides[np.Name] = mgr
		out[mgr] = appendUnique(out[mgr], np.Name)
	}
	for _, name := range cfg.Node.Packages {
		if _, hasOverride := overrides[name]; hasOverride {
			continue
		}
		mgr := cfg.Settings.NodeManager
		out[mgr] = appendUnique(out[mgr], name)
	}
	return out
}

func appendUnique(list []string, x string) []string {
	for _, y := range list {
		if y == x {
			return list
		}
	}
	return append(list, x)
}

func allManagers(declared map[string][]string, prev persisted) []string {
	set := map[string]struct{}{}
	for m := range declared {
		set[m] = struct{}{}
	}
	for m := range prev {
		set[m] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

func installArgs(mgr string, pkgs []string) []string {
	switch mgr {
	case mgrNPM:
		return append([]string{"install", "-g"}, pkgs...)
	case mgrPNPM:
		return append([]string{"add", "-g"}, pkgs...)
	case mgrYARN:
		return append([]string{"global", "add"}, pkgs...)
	}
	return nil
}

func removeArgs(mgr string, pkgs []string) []string {
	switch mgr {
	case mgrNPM:
		return append([]string{"uninstall", "-g"}, pkgs...)
	case mgrPNPM:
		return append([]string{"remove", "-g"}, pkgs...)
	case mgrYARN:
		return append([]string{"global", "remove"}, pkgs...)
	}
	return nil
}

func probeManager(r *runner.Runner, mgr string) ([]string, error) {
	if !runner.HasCommand(mgr) {
		return nil, nil
	}
	switch mgr {
	case mgrNPM, mgrPNPM:
		return probeNPMLike(r, mgr)
	case mgrYARN:
		return probeYarn(r)
	}
	return nil, fmt.Errorf("unknown node manager %q", mgr)
}

// probeNPMLike parses the output of `<mgr> ls -g --depth=0 --json`. Both npm
// and pnpm support that flag set with compatible JSON.
func probeNPMLike(r *runner.Runner, mgr string) ([]string, error) {
	out, err := r.Capture(mgr, "ls", "-g", "--depth=0", "--json")
	if err != nil {
		// npm exits non-zero if any peer-dep complaint exists; we still try
		// to parse stdout if it's valid JSON.
		if out == "" {
			return nil, err
		}
	}
	type listOutput struct {
		Dependencies map[string]any `json:"dependencies"`
	}
	// pnpm emits a JSON array; npm emits a single object. Normalize by trying
	// array first.
	var arr []listOutput
	if err := json.Unmarshal([]byte(out), &arr); err == nil && len(arr) > 0 {
		var names []string
		for _, l := range arr {
			for k := range l.Dependencies {
				names = append(names, k)
			}
		}
		return names, nil
	}
	var single listOutput
	if err := json.Unmarshal([]byte(out), &single); err == nil {
		var names []string
		for k := range single.Dependencies {
			names = append(names, k)
		}
		return names, nil
	}
	return nil, fmt.Errorf("could not parse %s ls output", mgr)
}

// yarnInfoLine matches lines like:  info "typescript@5.4.0" has binaries: ...
var yarnInfoLine = regexp.MustCompile(`^info "(.+)@[^"]+" has binaries`)

func probeYarn(r *runner.Runner) ([]string, error) {
	out, err := r.Capture("yarn", "global", "list", "--depth=0")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		m := yarnInfoLine.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) == 2 {
			names = append(names, m[1])
		}
	}
	return names, nil
}
