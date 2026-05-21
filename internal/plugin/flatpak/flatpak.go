// Package flatpak manages flatpak applications, system-wide and per-user.
package flatpak

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"codeberg.org/gurg/bigkis/internal/config"
	"codeberg.org/gurg/bigkis/internal/plan"
	"codeberg.org/gurg/bigkis/internal/plugin"
	"codeberg.org/gurg/bigkis/internal/runner"
	"codeberg.org/gurg/bigkis/internal/state"
	"codeberg.org/gurg/bigkis/internal/ui"
)

// safeUsername restricts the values bigkis will pass through to `sudo -u`.
// Real Linux usernames are a much wider character set in theory, but bigkis
// users name themselves with letters/digits/dashes/underscores in practice;
// rejecting anything else avoids passing surprising input through to sudo.
var safeUsername = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)

type Flatpak struct {
	cached *cachedPlan
	// runner is consulted by Plan for probes; if nil, Plan creates a fresh
	// runner.New(false). Tests use SetRunner to inject a Fake.
	runner *runner.Runner
}

// SetRunner injects a runner used by Plan for probes. Intended for tests.
func (f *Flatpak) SetRunner(r *runner.Runner) { f.runner = r }

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

func (f *Flatpak) Available(cfg *config.Config) error {
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
	// that user. Use argv form (no shell) and validate the username so a
	// surprising value cannot get expanded by sudo or sh.
	if !safeUsername.MatchString(username) {
		return nil, fmt.Errorf("flatpak.user_packages: refusing to probe user %q (must match %s)", username, safeUsername)
	}
	out, err := r.Capture("sudo", "-u", username, "flatpak", "list", "--app", "--user", "--columns=application")
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

func (f *Flatpak) Plan(cfg *config.Config, st *state.State) (plugin.Report, error) {
	r := f.runner
	if r == nil {
		r = runner.New(false)
	}

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

func (f *Flatpak) Upgrade(cfg *config.Config, st *state.State, r *runner.Runner, u *ui.UI) error {
	_ = st
	u.Step("flatpak: upgrading system installations")
	if _, err := r.Run(runner.Spec{
		Name: "flatpak",
		Args: []string{"update", "--system", "--noninteractive", "--assumeyes"},
		Sudo: true,
	}); err != nil {
		return fmt.Errorf("flatpak update --system: %w", err)
	}
	usernames := make([]string, 0, len(cfg.Flatpak.UserPackages))
	for name := range cfg.Flatpak.UserPackages {
		usernames = append(usernames, name)
	}
	sort.Strings(usernames)
	for _, username := range usernames {
		if !safeUsername.MatchString(username) {
			return fmt.Errorf("flatpak.user_packages: refusing to update for user %q (must match %s)", username, safeUsername)
		}
		u.Step("flatpak: upgrading user %s installations", username)
		if _, err := r.Run(runner.Spec{
			Name: "flatpak",
			Args: []string{"update", "--user", "--noninteractive", "--assumeyes"},
			User: username,
		}); err != nil {
			return fmt.Errorf("flatpak update --user %s: %w", username, err)
		}
	}
	return nil
}

func (f *Flatpak) PendingUpgrades(cfg *config.Config, r *runner.Runner) (plugin.UpgradeReport, error) {
	if !runner.HasCommand("flatpak") {
		return plugin.UpgradeReport{Plugin: f.Name()}, nil
	}

	var ops []plugin.Operation

	// System upgrades
	out, err := r.Capture("flatpak", "remote-ls", "--updates", "--system", "--app")
	if err != nil {
		return plugin.UpgradeReport{}, fmt.Errorf("flatpak remote-ls --system --updates: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Application ID") {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) < 3 {
			continue
		}
		ops = append(ops, plugin.Operation{
			Kind:   plugin.OpUpdate,
			Target: cols[0],
			Detail: "→ " + cols[2] + " (system)",
		})
	}

	// Per-user upgrades
	usernames := make([]string, 0, len(cfg.Flatpak.UserPackages))
	for name := range cfg.Flatpak.UserPackages {
		usernames = append(usernames, name)
	}
	sort.Strings(usernames)
	for _, username := range usernames {
		if !safeUsername.MatchString(username) {
			continue
		}
		out, err := r.Capture("sudo", "-u", username, "flatpak", "remote-ls", "--updates", "--user", "--app")
		if err != nil {
			// Best-effort: user may not have flatpak configured.
			continue
		}
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "Application ID") {
				continue
			}
			cols := strings.Split(line, "\t")
			if len(cols) < 3 {
				continue
			}
			ops = append(ops, plugin.Operation{
				Kind:   plugin.OpUpdate,
				Target: cols[0],
				Detail: "→ " + cols[2] + " (user " + username + ")",
			})
		}
	}

	return plugin.UpgradeReport{Plugin: f.Name(), Operations: ops}, nil
}

func (f *Flatpak) Apply(cfg *config.Config, st *state.State, report plugin.Report, r *runner.Runner, u *ui.UI) error {
	if f.cached == nil {
		return fmt.Errorf("flatpak: Apply called before Plan")
	}
	if err := assertReportMatchesCached(report, f.cached); err != nil {
		return fmt.Errorf("flatpak: %w", err)
	}

	remote := cfg.Flatpak.Remote
	if remote == "" {
		remote = "flathub"
	}

	if f.cached.system.HasChanges() {
		// Remove before installing so that conflicting apps are gone before
		// the replacement is installed.
		if len(f.cached.system.Remove) > 0 {
			u.Step("flatpak: removing %d system app(s)", len(f.cached.system.Remove))
			args := append([]string{"uninstall", "--system", "--noninteractive", "--assumeyes"}, f.cached.system.Remove...)
			if _, err := r.Run(runner.Spec{Name: "flatpak", Args: args, Sudo: true}); err != nil {
				return fmt.Errorf("flatpak uninstall system: %w", err)
			}
		}
		if len(f.cached.system.Add) > 0 {
			u.Step("flatpak: installing %d system app(s) from %s", len(f.cached.system.Add), remote)
			args := append([]string{"install", "--system", "--noninteractive", "--assumeyes", remote}, f.cached.system.Add...)
			if _, err := r.Run(runner.Spec{Name: "flatpak", Args: args, Sudo: true}); err != nil {
				return fmt.Errorf("flatpak install system: %w", err)
			}
		}
	}

	users := make([]string, 0, len(f.cached.users))
	for u := range f.cached.users {
		users = append(users, u)
	}
	sort.Strings(users)
	for _, username := range users {
		if !safeUsername.MatchString(username) {
			return fmt.Errorf("flatpak.user_packages: refusing to install for user %q (must match %s)", username, safeUsername)
		}
		d := f.cached.users[username]
		if !d.HasChanges() {
			continue
		}
		if len(d.Remove) > 0 {
			u.Step("flatpak: removing %d app(s) for user %s", len(d.Remove), username)
			args := append([]string{"uninstall", "--user", "--noninteractive", "--assumeyes"}, d.Remove...)
			if _, err := r.Run(runner.Spec{Name: "flatpak", Args: args, User: username}); err != nil {
				return fmt.Errorf("flatpak uninstall user %s: %w", username, err)
			}
		}
		if len(d.Add) > 0 {
			u.Step("flatpak: installing %d app(s) for user %s from %s", len(d.Add), username, remote)
			args := append([]string{"install", "--user", "--noninteractive", "--assumeyes", remote}, d.Add...)
			if _, err := r.Run(runner.Spec{Name: "flatpak", Args: args, User: username}); err != nil {
				return fmt.Errorf("flatpak install user %s: %w", username, err)
			}
		}
	}

	if !f.cached.system.HasChanges() && !anyUserChanges(f.cached.users) {
		u.Step("flatpak: nothing to do")
	}
	return nil
}

// assertReportMatchesCached verifies the operations in report match the
// cached system+user diffs computed in Plan. We refuse to apply mismatched
// reports rather than silently re-deriving from the live system.
func assertReportMatchesCached(report plugin.Report, cached *cachedPlan) error {
	type key struct {
		kind   plugin.OpKind
		target string
		detail string
	}
	declared := map[key]bool{}
	for _, op := range report.Operations {
		declared[key{op.Kind, op.Target, op.Detail}] = true
	}
	expected := map[key]bool{}
	for _, name := range cached.system.Add {
		expected[key{plugin.OpAdd, name, "system"}] = true
	}
	for _, name := range cached.system.Remove {
		expected[key{plugin.OpRemove, name, "system"}] = true
	}
	for username, d := range cached.users {
		for _, name := range d.Add {
			expected[key{plugin.OpAdd, name, "user " + username}] = true
		}
		for _, name := range d.Remove {
			expected[key{plugin.OpRemove, name, "user " + username}] = true
		}
	}
	for k := range declared {
		if !expected[k] {
			return fmt.Errorf("report op %+v not in cached plan; rerun Plan", k)
		}
	}
	// Relaxed: cached plan may contain ops the user chose to skip (subset
	// report from selective-apply TUI). Only declared→expected is enforced.
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
