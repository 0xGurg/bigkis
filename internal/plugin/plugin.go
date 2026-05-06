// Package plugin defines the interface every bigkis plugin must implement.
package plugin

import (
	"codeberg.org/gurg/bigkis/internal/config"
	"codeberg.org/gurg/bigkis/internal/runner"
	"codeberg.org/gurg/bigkis/internal/state"
	"codeberg.org/gurg/bigkis/internal/ui"
)

// Plugin is the contract every package source implements. The orchestrator
// calls Available, Plan, then Apply, then PersistState in order.
type Plugin interface {
	// Name is the stable plugin identifier (also used as the state key).
	Name() string

	// Available returns nil if the plugin can run on this system (e.g. its
	// underlying tools are installed). A non-nil error is treated as "skip
	// with warning". This is also where plugins should fail fast on missing
	// helpers (like an AUR helper) so status/dry-run report the problem
	// before the user is asked to confirm. The plugin receives cfg so
	// tool-name overrides (e.g. settings.aur_helper) can be respected.
	Available(cfg *config.Config) error

	// Plan reads the declaration and the current system, computes the work to
	// do, and returns a human-readable Report. It must not change the system.
	// Plugins are expected to cache whatever they need to run a corresponding
	// Apply; the caller will pass the same Report to Apply so the executed
	// work matches what the user confirmed.
	Plan(cfg *config.Config, st *state.State) (Report, error)

	// Apply executes exactly the operations described by report. report must
	// be the value returned by the most recent Plan call on this plugin.
	// Plugins must not consult the live system inside Apply; if their cached
	// plan is missing or no longer matches report, Apply returns an error
	// rather than silently re-deriving work.
	Apply(cfg *config.Config, st *state.State, report Report, r *runner.Runner, u *ui.UI) error

	// PersistState writes the now-current declared set into st under Name().
	// Called only after a successful Apply.
	PersistState(cfg *config.Config, st *state.State) error
}

// Report is what a Plan call returns. Plugins build human-readable output by
// describing each operation; the orchestrator handles printing.
type Report struct {
	Plugin     string
	Operations []Operation
}

// Operation describes a single add/remove the plugin will perform.
type Operation struct {
	Kind   OpKind
	Target string
	// Detail is an optional human-readable qualifier, e.g. "as user georgep" or
	// "via pnpm".
	Detail string
}

type OpKind int

const (
	OpAdd OpKind = iota
	OpRemove
)

func (r Report) HasChanges() bool { return len(r.Operations) > 0 }

// Registry is a simple ordered set of plugins keyed by name.
type Registry struct {
	plugins map[string]Plugin
}

func NewRegistry() *Registry { return &Registry{plugins: map[string]Plugin{}} }

func (r *Registry) Register(p Plugin) { r.plugins[p.Name()] = p }

func (r *Registry) Get(name string) (Plugin, bool) {
	p, ok := r.plugins[name]
	return p, ok
}
