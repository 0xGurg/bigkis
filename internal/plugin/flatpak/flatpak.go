// Package flatpak manages flatpak applications, system-wide and per-user.
package flatpak

import (
	"fmt"
	"sort"
	"strings"

	"codeberg.org/gurg/bigkis/internal/config"
	"codeberg.org/gurg/bigkis/internal/plan"
	"codeberg.org/gurg/bigkis/internal/plugin"
	"codeberg.org/gurg/bigkis/internal/runner"
	"codeberg.org/gurg/bigkis/internal/state"
	"codeberg.org/gurg/bigkis/internal/ui"
)

type Flatpak struct {
	cached *cachedPlan
}

type cachedPlan struct {
	system plan.Diff
	users  map[string]plan.Diff
}

// persisted is what we save into the state file under the "flatpak" key.
type persisted struct {
	System []string            `json:"system"`
	Users  map[string][]string `json:"users"`
}

func New() *Flatpak { return &Flatpak{} }

func (f *Flatpak) Name() string { return config.PluginFlatpak }

func (f *Flatpak) Available() error {
	if !runner.HasCommand("flatpak") {
		return fmt.Errorf("flatpak not found on PATH")
	}
	return nil
}

func (f *Flatpak) probeSystem(r *runner.Runner) ([]string, error) {
	out, err := r.Capture("flatpak", "list", "--app", "--system", "--columns=application")
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

func (f *Flatpak) probeUser(r *runner.Runner, username string) ([]string, error) {
	// We can't easily probe another user's flatpak installs without sudo to
	// that user. Instead, we rely on `sudo -u <user> flatpak list --user`.
	cmd := fmt.Sprintf("sudo -u %s flatpak list --app --user --columns=application", username)
	out, err := r.Capture("sh", "-c", cmd)
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

func (f *Flatpak) Plan(cfg *config.Config, st *state.State) (plugin.Report, error) {
	r := runner.New(false)

	systemActual, err := f.probeSystem(r)
	if err != nil {
		return plugin.Report{}, fmt.Errorf("probe flatpak system: %w", err)
	}

	var last persisted
	if _, err := st.Get(f.Name(), &last); err != nil {
		return plugin.Report{}, err
	}

	systemDiff := plan.Compute(cfg.Flatpak.Packages, systemActual, last.System, cfg.Flatpak.Ignored)

	userDiffs := map[string]plan.Diff{}
	for username, declared := range cfg.Flatpak.UserPackages {
		actual, err := f.probeUser(r, username)
		if err != nil {
			return plugin.Report{}, fmt.Errorf("probe flatpak user %s: %w", username, err)
		}
		var lastForUser []string
		if last.Users != nil {
			lastForUser = last.Users[username]
		}
		userDiffs[username] = plan.Compute(declared, actual, lastForUser, cfg.Flatpak.Ignored)
	}
	// Also handle users dropped from config: their previously-declared apps
	// should be removed.
	for username, prev := range last.Users {
		if _, stillDeclared := cfg.Flatpak.UserPackages[username]; stillDeclared {
			continue
		}
		actual, err := f.probeUser(r, username)
		if err != nil {
			return plugin.Report{}, fmt.Errorf("probe flatpak user %s: %w", username, err)
		}
		userDiffs[username] = plan.Compute(nil, actual, prev, cfg.Flatpak.Ignored)
	}

	f.cached = &cachedPlan{system: systemDiff, users: userDiffs}

	rep := plugin.Report{Plugin: f.Name()}
	for _, name := range systemDiff.Add {
		rep.Operations = append(rep.Operations, plugin.Operation{Kind: plugin.OpAdd, Target: name, Detail: "system"})
	}
	for _, name := range systemDiff.Remove {
		rep.Operations = append(rep.Operations, plugin.Operation{Kind: plugin.OpRemove, Target: name, Detail: "system"})
	}
	// Stable iteration order so reports are deterministic.
	users := make([]string, 0, len(userDiffs))
	for u := range userDiffs {
		users = append(users, u)
	}
	sort.Strings(users)
	for _, username := range users {
		ud := userDiffs[username]
		for _, name := range ud.Add {
			rep.Operations = append(rep.Operations, plugin.Operation{Kind: plugin.OpAdd, Target: name, Detail: "user " + username})
		}
		for _, name := range ud.Remove {
			rep.Operations = append(rep.Operations, plugin.Operation{Kind: plugin.OpRemove, Target: name, Detail: "user " + username})
		}
	}
	return rep, nil
}

func (f *Flatpak) Apply(cfg *config.Config, st *state.State, r *runner.Runner, u *ui.UI) error {
	if f.cached == nil {
		if _, err := f.Plan(cfg, st); err != nil {
			return err
		}
	}

	if f.cached.system.HasChanges() {
		if len(f.cached.system.Add) > 0 {
			u.Step("flatpak: installing %d system app(s)", len(f.cached.system.Add))
			args := append([]string{"install", "--system", "--noninteractive", "--assumeyes", "flathub"}, f.cached.system.Add...)
			if _, err := r.Run(runner.Spec{Name: "flatpak", Args: args, Sudo: true}); err != nil {
				return fmt.Errorf("flatpak install system: %w", err)
			}
		}
		if len(f.cached.system.Remove) > 0 {
			u.Step("flatpak: removing %d system app(s)", len(f.cached.system.Remove))
			args := append([]string{"uninstall", "--system", "--noninteractive", "--assumeyes"}, f.cached.system.Remove...)
			if _, err := r.Run(runner.Spec{Name: "flatpak", Args: args, Sudo: true}); err != nil {
				return fmt.Errorf("flatpak uninstall system: %w", err)
			}
		}
	}

	users := make([]string, 0, len(f.cached.users))
	for u := range f.cached.users {
		users = append(users, u)
	}
	sort.Strings(users)
	for _, username := range users {
		d := f.cached.users[username]
		if !d.HasChanges() {
			continue
		}
		if len(d.Add) > 0 {
			u.Step("flatpak: installing %d app(s) for user %s", len(d.Add), username)
			args := append([]string{"install", "--user", "--noninteractive", "--assumeyes", "flathub"}, d.Add...)
			if _, err := r.Run(runner.Spec{Name: "flatpak", Args: args, User: username}); err != nil {
				return fmt.Errorf("flatpak install user %s: %w", username, err)
			}
		}
		if len(d.Remove) > 0 {
			u.Step("flatpak: removing %d app(s) for user %s", len(d.Remove), username)
			args := append([]string{"uninstall", "--user", "--noninteractive", "--assumeyes"}, d.Remove...)
			if _, err := r.Run(runner.Spec{Name: "flatpak", Args: args, User: username}); err != nil {
				return fmt.Errorf("flatpak uninstall user %s: %w", username, err)
			}
		}
	}

	if !f.cached.system.HasChanges() && !anyUserChanges(f.cached.users) {
		u.Step("flatpak: nothing to do")
	}
	return nil
}

func (f *Flatpak) PersistState(cfg *config.Config, st *state.State) error {
	users := map[string][]string{}
	for username, pkgs := range cfg.Flatpak.UserPackages {
		users[username] = dedupAndFilter(pkgs, cfg.Flatpak.Ignored)
	}
	return st.Set(f.Name(), persisted{
		System: dedupAndFilter(cfg.Flatpak.Packages, cfg.Flatpak.Ignored),
		Users:  users,
	})
}

func anyUserChanges(diffs map[string]plan.Diff) bool {
	for _, d := range diffs {
		if d.HasChanges() {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Application ID") {
			continue
		}
		out = append(out, line)
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
