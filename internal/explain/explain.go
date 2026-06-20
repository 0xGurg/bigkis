// Package explain inspects whether a single package is declared, installed,
// and managed by bigkis and renders a human-readable summary.
package explain

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/0xGurg/bigkis/internal/config"
	"github.com/0xGurg/bigkis/internal/state"
)

// Result is the structured outcome of inspecting a package.
type Result struct {
	Package    string
	Declared   []Source
	Ignored    []string
	Installed  []Install
	Managed    []string
	StatusLine string
}

// Source describes a place in the config that declared the package.
type Source struct {
	Plugin string
	Where  string
}

// Install describes a place on the system where the package was found.
type Install struct {
	Plugin  string
	Detail  string
	Version string
}

// Inspect builds a Result describing the package across config, system, and
// state.
func Inspect(pkg string, cfg *config.Config, st *state.State) Result {
	r := Result{Package: pkg}

	if cfg != nil {
		// Declarations - we look at fully-expanded (post-group) lists.
		check := func(plugin string, packages, ignored []string) {
			for _, p := range packages {
				if p == pkg {
					r.Declared = append(r.Declared, Source{Plugin: plugin, Where: "[" + plugin + "].packages"})
				}
			}
			for _, p := range ignored {
				if p == pkg {
					r.Ignored = append(r.Ignored, plugin)
				}
			}
		}
		check("pacman", cfg.Pacman.Packages, cfg.Pacman.Ignored)
		check("aur", cfg.AUR.Packages, cfg.AUR.Ignored)
		check("flatpak", cfg.Flatpak.Packages, cfg.Flatpak.Ignored)
		check("node", cfg.Node.Packages, nil)
		for _, np := range cfg.Node.Package {
			if np.Name == pkg {
				where := "[[node.package]]"
				if np.Manager != "" {
					where += " (manager=" + np.Manager + ")"
				}
				r.Declared = append(r.Declared, Source{Plugin: "node", Where: where})
			}
		}
		// [flatpak.user_packages.<user>] entries: prior versions of explain
		// missed these entirely, so per-user flatpaks would render as
		// undeclared even though the flatpak plugin manages them.
		userKeys := make([]string, 0, len(cfg.Flatpak.UserPackages))
		for u := range cfg.Flatpak.UserPackages {
			userKeys = append(userKeys, u)
		}
		sort.Strings(userKeys)
		for _, user := range userKeys {
			for _, p := range cfg.Flatpak.UserPackages[user] {
				if p == pkg {
					r.Declared = append(r.Declared, Source{
						Plugin: "flatpak",
						Where:  "[flatpak.user_packages." + user + "]",
					})
				}
			}
		}
	}

	r.Installed = probeInstalls(pkg)

	if st != nil {
		r.Managed = managedBy(pkg, st)
	}

	r.StatusLine = derive(r)
	return r
}

func managedBy(pkg string, st *state.State) []string {
	var out []string
	check := func(plugin string, list []string) {
		for _, x := range list {
			if x == pkg {
				out = append(out, plugin)
				return
			}
		}
	}

	var pacman []string
	if found, _ := st.Get("pacman", &pacman); found {
		check("pacman", pacman)
	}
	var aur []string
	if found, _ := st.Get("aur", &aur); found {
		check("aur", aur)
	}
	type flatpakState struct {
		System []string            `json:"system"`
		Users  map[string][]string `json:"users"`
	}
	var fp flatpakState
	if found, _ := st.Get("flatpak", &fp); found {
		check("flatpak", fp.System)
		users := make([]string, 0, len(fp.Users))
		for u := range fp.Users {
			users = append(users, u)
		}
		sort.Strings(users)
		for _, u := range users {
			for _, x := range fp.Users[u] {
				if x == pkg {
					out = append(out, "flatpak (user "+u+")")
				}
			}
		}
	}
	type nodeState map[string][]string
	var ns nodeState
	if found, _ := st.Get("node", &ns); found {
		mgrs := make([]string, 0, len(ns))
		for m := range ns {
			mgrs = append(mgrs, m)
		}
		sort.Strings(mgrs)
		for _, m := range mgrs {
			for _, x := range ns[m] {
				if x == pkg {
					out = append(out, "node ("+m+")")
				}
			}
		}
	}
	return out
}

func probeInstalls(pkg string) []Install {
	var out []Install
	if hasCommand("pacman") {
		// Detect both native and foreign packages with version info.
		if v, ok := pacmanVersion(pkg); ok {
			detail := "native"
			if isForeign(pkg) {
				detail = "foreign (AUR or custom)"
			}
			out = append(out, Install{Plugin: "pacman", Detail: detail, Version: v})
		}
	}
	if hasCommand("flatpak") {
		if v, ok := flatpakVersion(pkg); ok {
			out = append(out, Install{Plugin: "flatpak", Detail: "system", Version: v})
		}
	}
	for _, mgr := range []string{"npm", "pnpm", "yarn"} {
		if !hasCommand(mgr) {
			continue
		}
		if v, ok := nodeVersion(mgr, pkg); ok {
			out = append(out, Install{Plugin: "node", Detail: mgr, Version: v})
		}
	}
	return out
}

func pacmanVersion(pkg string) (string, bool) {
	out, err := exec.Command("pacman", "-Qi", pkg).Output()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "Version") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1]), true
			}
		}
	}
	return "", true
}

func isForeign(pkg string) bool {
	out, err := exec.Command("pacman", "-Qm", pkg).Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}

func flatpakVersion(pkg string) (string, bool) {
	out, err := exec.Command("flatpak", "info", pkg).Output()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Version:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Version:")), true
		}
	}
	return "", true
}

func nodeVersion(mgr, pkg string) (string, bool) {
	out, err := exec.Command(mgr, "ls", "-g", "--depth=0", pkg).Output()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, pkg+"@") {
			i := strings.LastIndex(line, pkg+"@")
			return line[i+len(pkg)+1:], true
		}
	}
	return "", false
}

func hasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func derive(r Result) string {
	declared := len(r.Declared) > 0
	ignored := len(r.Ignored) > 0
	installed := len(r.Installed) > 0
	managed := len(r.Managed) > 0

	switch {
	case ignored:
		return "ignored (bigkis will not touch this package)"
	case declared && installed:
		// A package is "in sync" only when at least one declared plugin
		// matches one of the install probes. Otherwise the user has e.g.
		// declared the pacman version but the system has the flatpak app,
		// which is real drift the next apply needs to act on.
		if pluginsOverlap(r.Declared, r.Installed) {
			return "in sync"
		}
		return "drift: declared via " + joinDeclared(r.Declared) + " but installed via " + joinInstalled(r.Installed)
	case declared && !installed:
		return "drift: declared but not installed (next apply will install)"
	case !declared && managed:
		return "drift: managed previously, no longer declared (next apply will remove)"
	case !declared && installed && !managed:
		return "unmanaged (installed on the system but never declared by bigkis)"
	case !declared && !installed:
		return "unknown (not declared, not installed, not in state)"
	}
	return "unknown"
}

func pluginsOverlap(decl []Source, ins []Install) bool {
	have := map[string]bool{}
	for _, d := range decl {
		have[d.Plugin] = true
	}
	for _, i := range ins {
		if have[i.Plugin] {
			return true
		}
	}
	return false
}

func joinDeclared(ss []Source) string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		if seen[s.Plugin] {
			continue
		}
		seen[s.Plugin] = true
		out = append(out, s.Plugin)
	}
	return strings.Join(out, ",")
}

func joinInstalled(ins []Install) string {
	seen := map[string]bool{}
	var out []string
	for _, i := range ins {
		if seen[i.Plugin] {
			continue
		}
		seen[i.Plugin] = true
		out = append(out, i.Plugin)
	}
	return strings.Join(out, ",")
}

// Render produces a human-readable text rendering of the Result.
func (r Result) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "package: %s\n", r.Package)

	if len(r.Declared) == 0 {
		fmt.Fprintln(&b, "  declared:  no")
	} else {
		fmt.Fprint(&b, "  declared:  yes")
		for i, s := range r.Declared {
			if i == 0 {
				fmt.Fprintf(&b, " (%s)\n", s.Where)
			} else {
				fmt.Fprintf(&b, "             also %s\n", s.Where)
			}
		}
	}

	if len(r.Ignored) > 0 {
		fmt.Fprintf(&b, "  ignored:   yes (%s)\n", strings.Join(r.Ignored, ", "))
	}

	if len(r.Installed) == 0 {
		fmt.Fprintln(&b, "  installed: no")
	} else {
		for i, ins := range r.Installed {
			label := "installed:"
			if i > 0 {
				label = "          "
			}
			detail := ins.Plugin
			if ins.Detail != "" {
				detail += " " + ins.Detail
			}
			if ins.Version != "" {
				detail += " v" + ins.Version
			}
			fmt.Fprintf(&b, "  %s %s\n", label, detail)
		}
	}

	if len(r.Managed) == 0 {
		fmt.Fprintln(&b, "  managed:   no")
	} else {
		fmt.Fprintf(&b, "  managed:   yes (%s)\n", strings.Join(r.Managed, ", "))
	}

	fmt.Fprintf(&b, "  status:    %s\n", r.StatusLine)
	return b.String()
}
