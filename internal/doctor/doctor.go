// Package doctor implements the bigkis doctor preflight checks. It looks at
// the host (commands on PATH, root context, writable state/rollback dirs)
// and the loaded config (declared plugins, AUR helper, node managers, the
// flatpak remote) and reports any combination that would make the next
// apply unhappy.
//
// Each check is small and independent so we can render them as either a
// human-friendly checklist or a stable JSON shape for tooling.
package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"codeberg.org/gurg/bigkis/internal/config"
)

// Status is the outcome of a single check.
type Status string

const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// Check is one diagnostic entry in the doctor report.
type Check struct {
	Name    string `json:"name"`
	Status  Status `json:"status"`
	Message string `json:"message,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

// Report is the structured doctor output. OK is true iff no check is at
// StatusFail (warns are tolerated; the operator decides).
type Report struct {
	ConfigPath string  `json:"configPath,omitempty"`
	Checks     []Check `json:"checks"`
	OK         bool    `json:"ok"`
}

// Env captures the host knobs doctor reads. Pulling them out as fields
// makes the checks fully deterministic in tests.
type Env struct {
	Geteuid       func() int
	Getenv        func(string) string
	LookPath      func(string) (string, error)
	StatePath     string
	RollbackDir   string
	FlatpakRemote func(name string) (bool, error) // returns (exists, err)
}

// DefaultEnv returns the production wiring: real OS calls and a flatpak
// remote probe via `flatpak remotes`.
func DefaultEnv(statePath, rollbackDir string) Env {
	return Env{
		Geteuid:       os.Geteuid,
		Getenv:        os.Getenv,
		LookPath:      exec.LookPath,
		StatePath:     statePath,
		RollbackDir:   rollbackDir,
		FlatpakRemote: realFlatpakRemote,
	}
}

// Run executes the full battery of checks and returns the aggregated report.
// cfg may be nil when config loading itself failed; in that case we still
// report host-level checks (which are independent of cfg) plus a fail line
// for the config itself, so doctor produces useful output instead of bailing.
func Run(cfg *config.Config, configErr error, env Env) Report {
	r := Report{OK: true}

	if cfg != nil {
		r.ConfigPath = cfg.Path
	}
	r.Checks = append(r.Checks, configCheck(cfg, configErr))

	r.Checks = append(r.Checks, processCheck(env))

	r.Checks = append(r.Checks, commandCheck(env, "pacman", StatusFail,
		"required by the pacman plugin to query and install native packages",
		"install pacman, or remove pacman from settings.enabled"))
	r.Checks = append(r.Checks, commandCheck(env, "flatpak", StatusWarn,
		"required by the flatpak plugin",
		"install flatpak, or remove flatpak from settings.enabled"))

	if cfg != nil {
		r.Checks = append(r.Checks, applyUpgradeNote())
		r.Checks = append(r.Checks, aurHelperCheck(cfg, env))
		r.Checks = append(r.Checks, aurUserCheck(cfg, env))
		r.Checks = append(r.Checks, nodeManagerChecks(cfg, env)...)
		r.Checks = append(r.Checks, flatpakRemoteCheck(cfg, env))
	}

	r.Checks = append(r.Checks, writableDirCheck(env, "state path", env.StatePath))
	r.Checks = append(r.Checks, writableDirCheck(env, "rollback dir", env.RollbackDir))

	for _, c := range r.Checks {
		if c.Status == StatusFail {
			r.OK = false
			break
		}
	}
	return r
}

func applyUpgradeNote() Check {
	return Check{
		Name:    "apply:upgrade",
		Status:  StatusOK,
		Message: "apply runs system upgrades for enabled plugins before install/remove (pass --no-upgrade to skip)",
	}
}

func configCheck(cfg *config.Config, err error) Check {
	if err != nil {
		return Check{Name: "config", Status: StatusFail, Message: err.Error(),
			Hint: "fix the config or pass --config to point at a working file"}
	}
	if cfg == nil {
		return Check{Name: "config", Status: StatusFail, Message: "no config loaded"}
	}
	msg := "loaded " + cfg.Path
	if len(cfg.SourcePaths) > 1 {
		msg += fmt.Sprintf(" (+%d include(s))", len(cfg.SourcePaths)-1)
	}
	return Check{Name: "config", Status: StatusOK, Message: msg}
}

// processCheck reports the privilege context bigkis is running in and warns
// when running as root without SUDO_USER (AUR helper has nowhere to drop to).
func processCheck(env Env) Check {
	uid := env.Geteuid()
	if uid != 0 {
		return Check{Name: "process", Status: StatusOK,
			Message: fmt.Sprintf("running as uid %d (apply will need sudo for system-wide changes)", uid)}
	}
	if env.Getenv("SUDO_USER") == "" {
		return Check{Name: "process", Status: StatusWarn,
			Message: "running as root with no SUDO_USER set",
			Hint:    "invoke bigkis as `sudo bigkis ...` from a regular account so the AUR helper has a non-root user to run as"}
	}
	if env.Getenv("SUDO_USER") == "root" {
		return Check{Name: "process", Status: StatusWarn,
			Message: "running as root with SUDO_USER=root",
			Hint:    "the AUR helper cannot run as root; re-invoke from a non-root account"}
	}
	return Check{Name: "process", Status: StatusOK,
		Message: fmt.Sprintf("running as root via sudo from %s", env.Getenv("SUDO_USER"))}
}

func commandCheck(env Env, name string, missingStatus Status, role, hint string) Check {
	if _, err := env.LookPath(name); err != nil {
		return Check{Name: "command:" + name, Status: missingStatus,
			Message: name + " not on PATH (" + role + ")",
			Hint:    hint}
	}
	return Check{Name: "command:" + name, Status: StatusOK, Message: name + " present"}
}

func aurHelperCheck(cfg *config.Config, env Env) Check {
	helper := cfg.Settings.AURHelper
	if helper == "" {
		return Check{Name: "aur:helper", Status: StatusFail,
			Message: "settings.aur_helper is empty"}
	}
	if _, err := env.LookPath(helper); err != nil {
		if !cfg.IsEnabled(config.PluginAUR) {
			return Check{Name: "aur:helper", Status: StatusOK,
				Message: helper + " not on PATH but aur plugin disabled"}
		}
		return Check{Name: "aur:helper", Status: StatusFail,
			Message: fmt.Sprintf("aur helper %q not on PATH", helper),
			Hint:    "install " + helper + ", change settings.aur_helper, or drop aur from settings.enabled"}
	}
	return Check{Name: "aur:helper", Status: StatusOK, Message: helper + " present"}
}

// aurUserCheck duplicates the resolveHelperUser logic from the aur plugin
// at a level doctor can describe in its own words. We keep them separate so
// doctor doesn't pull in the plugin's runner.
func aurUserCheck(cfg *config.Config, env Env) Check {
	if !cfg.IsEnabled(config.PluginAUR) {
		return Check{Name: "aur:user", Status: StatusOK, Message: "aur plugin not enabled; skipped"}
	}
	if env.Geteuid() != 0 {
		return Check{Name: "aur:user", Status: StatusOK,
			Message: "aur helper will run as the current user"}
	}
	user := env.Getenv("SUDO_USER")
	if user == "" {
		return Check{Name: "aur:user", Status: StatusFail,
			Message: "running as root with no SUDO_USER",
			Hint:    "AUR helpers refuse to operate as root; invoke bigkis via sudo from a regular user"}
	}
	if user == "root" {
		return Check{Name: "aur:user", Status: StatusFail,
			Message: "SUDO_USER=root is not a safe target for the AUR helper"}
	}
	return Check{Name: "aur:user", Status: StatusOK,
		Message: "aur helper will run as " + user}
}

func nodeManagerChecks(cfg *config.Config, env Env) []Check {
	if !cfg.IsEnabled(config.PluginNode) {
		return []Check{{Name: "node:manager", Status: StatusOK, Message: "node plugin not enabled; skipped"}}
	}
	managers := referencedNodeManagers(cfg)
	if len(managers) == 0 {
		return []Check{{Name: "node:manager", Status: StatusOK, Message: "no node packages declared"}}
	}
	var out []Check
	for _, m := range managers {
		if _, err := env.LookPath(m); err != nil {
			out = append(out, Check{
				Name:    "node:manager:" + m,
				Status:  StatusFail,
				Message: m + " not on PATH but referenced by declared node packages",
				Hint:    "install " + m + " or move those packages to a different manager",
			})
		} else {
			out = append(out, Check{
				Name:    "node:manager:" + m,
				Status:  StatusOK,
				Message: m + " present",
			})
		}
	}
	return out
}

func referencedNodeManagers(cfg *config.Config) []string {
	set := map[string]struct{}{}
	if len(cfg.Node.Packages) > 0 && cfg.Settings.NodeManager != "" {
		set[cfg.Settings.NodeManager] = struct{}{}
	}
	for _, np := range cfg.Node.Package {
		mgr := np.Manager
		if mgr == "" {
			mgr = cfg.Settings.NodeManager
		}
		if mgr != "" {
			set[mgr] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

func flatpakRemoteCheck(cfg *config.Config, env Env) Check {
	if !cfg.IsEnabled(config.PluginFlatpak) {
		return Check{Name: "flatpak:remote", Status: StatusOK, Message: "flatpak plugin not enabled; skipped"}
	}
	remote := cfg.Flatpak.Remote
	if remote == "" {
		remote = "flathub"
	}
	if _, err := env.LookPath("flatpak"); err != nil {
		return Check{Name: "flatpak:remote", Status: StatusWarn,
			Message: "flatpak not on PATH; cannot verify remote " + remote}
	}
	if env.FlatpakRemote == nil {
		return Check{Name: "flatpak:remote", Status: StatusOK,
			Message: "remote " + remote + " (verification skipped)"}
	}
	exists, err := env.FlatpakRemote(remote)
	if err != nil {
		return Check{Name: "flatpak:remote", Status: StatusWarn,
			Message: "could not list flatpak remotes: " + err.Error()}
	}
	if !exists {
		return Check{Name: "flatpak:remote", Status: StatusFail,
			Message: "flatpak remote " + remote + " is not configured",
			Hint:    "run `flatpak remote-add --if-not-exists " + remote + " <url>`"}
	}
	return Check{Name: "flatpak:remote", Status: StatusOK, Message: "remote " + remote + " is configured"}
}

func writableDirCheck(env Env, label, target string) Check {
	if target == "" {
		return Check{Name: label, Status: StatusWarn, Message: label + " path is empty"}
	}
	dir := target
	if filepath.Ext(target) != "" || strings.HasSuffix(target, ".json") {
		dir = filepath.Dir(target)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Check{Name: label, Status: StatusFail,
			Message: "cannot create " + dir + ": " + err.Error()}
	}
	probe, err := os.CreateTemp(dir, ".bigkis-doctor-*")
	if err != nil {
		return Check{Name: label, Status: StatusFail,
			Message: "cannot write to " + dir + ": " + err.Error(),
			Hint:    "fix permissions or run bigkis as the user that owns the directory"}
	}
	probeName := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probeName)
	return Check{Name: label, Status: StatusOK, Message: dir + " is writable"}
}

func realFlatpakRemote(name string) (bool, error) {
	out, err := exec.Command("flatpak", "remotes", "--columns=name").Output()
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "Name" {
			continue
		}
		if line == name {
			return true, nil
		}
	}
	return false, nil
}

// Render returns a human-readable rendering of the report.
func (r Report) Render() string {
	var b strings.Builder
	if r.ConfigPath != "" {
		fmt.Fprintf(&b, "config: %s\n", r.ConfigPath)
	}
	for _, c := range r.Checks {
		marker := "ok"
		switch c.Status {
		case StatusWarn:
			marker = "warn"
		case StatusFail:
			marker = "fail"
		}
		fmt.Fprintf(&b, "  [%s] %s: %s\n", marker, c.Name, c.Message)
		if c.Hint != "" {
			fmt.Fprintf(&b, "         hint: %s\n", c.Hint)
		}
	}
	if r.OK {
		fmt.Fprintln(&b, "summary: no failing checks")
	} else {
		fmt.Fprintln(&b, "summary: at least one check failed; fix above before running apply")
	}
	return b.String()
}
