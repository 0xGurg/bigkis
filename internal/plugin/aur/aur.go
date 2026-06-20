// Package aur manages foreign packages built from the AUR by wrapping an
// installed helper such as yay or paru.
package aur

import (
	"fmt"
	"os"
	"strings"

	"github.com/0xGurg/bigkis/internal/config"
	"github.com/0xGurg/bigkis/internal/plan"
	"github.com/0xGurg/bigkis/internal/plugin"
	"github.com/0xGurg/bigkis/internal/runner"
	"github.com/0xGurg/bigkis/internal/state"
	"github.com/0xGurg/bigkis/internal/ui"
)

// geteuid returns the effective UID of the current process. It's a var so
// tests can stub it without spinning up real privileged subprocesses.
var geteuid = os.Geteuid

// getenv mirrors os.Getenv via a var so tests can stub SUDO_USER lookups.
var getenv = os.Getenv

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
//
// AUR helpers refuse to operate as root, so we also verify the apply will
// have a non-root user to drop to: either we're running unprivileged, or we
// were invoked under sudo and SUDO_USER is set to a non-root account.
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
	if _, err := resolveHelperUser(); err != nil {
		return err
	}
	return nil
}

// resolveHelperUser returns the username the AUR helper should run as. When
// bigkis is invoked under sudo we drop to $SUDO_USER; an unprivileged
// invocation runs the helper as the current user (returning ""). Returning
// an error means the helper has no safe user to run as (root with no
// SUDO_USER, or SUDO_USER=root).
func resolveHelperUser() (string, error) {
	if geteuid() != 0 {
		return "", nil
	}
	user := getenv("SUDO_USER")
	if user == "" {
		return "", fmt.Errorf("aur: refusing to run helper as root; re-invoke bigkis via sudo from a regular user account so $SUDO_USER is set")
	}
	if user == "root" {
		return "", fmt.Errorf("aur: SUDO_USER=root is not a safe target for the AUR helper")
	}
	return user, nil
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

func (a *AUR) Upgrade(cfg *config.Config, st *state.State, r *runner.Runner, u *ui.UI) error {
	_ = st
	helper := cfg.Settings.AURHelper
	if helper == "" {
		return fmt.Errorf("aur: settings.aur_helper is not set")
	}
	helperUser, err := resolveHelperUser()
	if err != nil {
		return err
	}
	u.Step("aur: upgrading AUR packages via %s", helper)
	if _, err := r.Run(runner.Spec{Name: helper, Args: []string{"-Sua", "--noconfirm"}, User: helperUser}); err != nil {
		return fmt.Errorf("%s -Sua: %w", helper, err)
	}
	return nil
}

// PendingUpgrades probes which foreign packages have newer versions available
// using <helper> -Qua. Output format: "package oldver -> newver". The helper
// exits with code 1 and empty stdout when no upgrades are available, which we
// treat as "no upgrades" rather than an error.
func (a *AUR) PendingUpgrades(cfg *config.Config, r *runner.Runner) (plugin.UpgradeReport, error) {
	helper := cfg.Settings.AURHelper
	if helper == "" {
		return plugin.UpgradeReport{Plugin: a.Name()}, nil
	}
	helperUser, err := resolveHelperUser()
	if err != nil {
		return plugin.UpgradeReport{}, err
	}
	res, err := r.Run(runner.Spec{Name: helper, Args: []string{"-Qua"}, User: helperUser, CaptureOutput: true})
	if err != nil {
		// The helper exits 1 with empty output when nothing to upgrade.
		if runner.IsExitCode(err, 1) && res.Stdout == "" {
			return plugin.UpgradeReport{Plugin: a.Name()}, nil
		}
		return plugin.UpgradeReport{}, fmt.Errorf("%s -Qua: %w", helper, err)
	}
	rep := plugin.UpgradeReport{Plugin: a.Name()}
	for _, line := range strings.Split(res.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
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

// parseUpgradeLine parses "package oldver -> newver" output from pacman -Qu
// or an AUR helper's -Qua.
func parseUpgradeLine(line string) (name string, detail string) {
	// Format: "neovim 0.9.5-1 -> 0.10.0-1"
	parts := strings.SplitN(line, " ", 2)
	if len(parts) < 2 {
		return line, ""
	}
	name = parts[0]
	verPart := strings.TrimSpace(parts[1])
	return name, verPart
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

	// AUR helpers must be invoked as a non-root user. When bigkis itself is
	// running under sudo, drop privileges to $SUDO_USER; the helper escalates
	// for the package install steps via its own pkexec/sudo path. When
	// running as a regular user we leave User empty (= current user).
	helperUser, err := resolveHelperUser()
	if err != nil {
		return err
	}
	// Remove before installing so that conflicting packages (e.g. replacing
	// "quickshell" with "quickshell-git") are gone before the new package is
	// installed. Installing first fails when the new package conflicts with
	// the one still on disk.
	if len(d.Remove) > 0 {
		u.Step("aur: removing %d package(s) via %s", len(d.Remove), a.helper)
		args := append([]string{"-Rns", "--noconfirm"}, d.Remove...)
		if _, err := r.Run(runner.Spec{Name: a.helper, Args: args, User: helperUser}); err != nil {
			return fmt.Errorf("%s -Rns: %w", a.helper, err)
		}
	}

	if len(d.Add) > 0 {
		u.Step("aur: installing %d package(s) via %s", len(d.Add), a.helper)
		args := append([]string{"-S", "--needed", "--noconfirm"}, d.Add...)
		res, err := r.Run(runner.Spec{Name: a.helper, Args: args, User: helperUser, CaptureOutput: true})
		if err != nil {
			// --noconfirm answers the conflict prompt with the default (N),
			// so "X and Y are in conflict. Remove Y? [y/N]" is answered "no"
			// and the install fails. Detect this, remove the conflicting
			// package with pacman, and retry.
			conflicts := parseConflictError(res.Stderr, d.Add)
			if len(conflicts) == 0 {
				return fmt.Errorf("%s -S: %w", a.helper, err)
			}
			u.Step("aur: removing conflicting package(s): %s", strings.Join(conflicts, " "))
			rmArgs := append([]string{"-Rns", "--noconfirm"}, conflicts...)
			if _, rmErr := r.Run(runner.Spec{Name: "pacman", Args: rmArgs, Sudo: true}); rmErr != nil {
				return fmt.Errorf("%s -S: %w (also failed to remove conflicting packages: %v)", a.helper, err, rmErr)
			}
			u.Step("aur: retrying install of %d package(s)", len(d.Add))
			retryArgs := append([]string{"-S", "--needed", "--noconfirm"}, d.Add...)
			if _, err := r.Run(runner.Spec{Name: a.helper, Args: retryArgs, User: helperUser}); err != nil {
				return fmt.Errorf("%s -S (retry): %w", a.helper, err)
			}
			return nil
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

// parseConflictError extracts conflicting package names from pacman's error
// output. When pacman fails with "unresolvable package conflicts", the stderr
// contains lines like:
//
//	quickshell-git and quickshell are in conflict
//
// We return the package names that are NOT in the adding set (i.e. the
// already-installed packages that need to be removed).
func parseConflictError(stderr string, adding []string) []string {
	addingSet := map[string]bool{}
	for _, p := range adding {
		addingSet[p] = true
	}

	var toRemove []string
	seen := map[string]bool{}
	for _, line := range strings.Split(stderr, "\n") {
		if !strings.Contains(line, "are in conflict") {
			continue
		}
		andIdx := strings.Index(line, " and ")
		if andIdx < 0 {
			continue
		}
		pkg1 := trimPacmanErrorPrefix(strings.TrimSpace(line[:andIdx]))
		rest := line[andIdx+5:] // after " and "
		endIdx := strings.Index(rest, " are in conflict")
		if endIdx < 0 {
			continue
		}
		pkg2 := strings.TrimSpace(rest[:endIdx])
		for _, pkg := range []string{pkg1, pkg2} {
			if pkg == "" || seen[pkg] || addingSet[pkg] {
				continue
			}
			seen[pkg] = true
			toRemove = append(toRemove, pkg)
		}
	}
	return toRemove
}

// trimPacmanErrorPrefix strips leading ":: " or "error: " prefixes that
// pacman puts before package names in conflict messages.
func trimPacmanErrorPrefix(s string) string {
	for _, prefix := range []string{":: ", "error: "} {
		if strings.HasPrefix(s, prefix) {
			return strings.TrimSpace(s[len(prefix):])
		}
	}
	return s
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
