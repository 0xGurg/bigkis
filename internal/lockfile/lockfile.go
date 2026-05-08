// Package lockfile writes a reproducibility lockfile after a successful apply.
//
// The lockfile records the versions/refs of installed packages so another
// machine can be reasoned about. Lockfile enforcement is informational by
// default; future versions may enforce on apply.
package lockfile

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codeberg.org/gurg/bigkis/internal/config"
	"codeberg.org/gurg/bigkis/internal/state"
)

const SchemaVersion = 1

// DefaultPath returns the path used when --lock is not given. It places
// bigkis.lock next to the top-level config file.
func DefaultPath(configPath string) string {
	if configPath == "" {
		return "bigkis.lock"
	}
	return filepath.Join(filepath.Dir(configPath), "bigkis.lock")
}

// Write inspects the current system for declared packages and writes a TOML
// lockfile to path.
func Write(path string, cfg *config.Config) error {
	if path == "" {
		return fmt.Errorf("lockfile path is empty")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "schema_version = %d\n", SchemaVersion)
	fmt.Fprintf(&b, "generated_at   = %q\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintln(&b)

	if cfg.IsEnabled(config.PluginPacman) {
		writePacman(&b, cfg.Pacman.Packages)
	}
	if cfg.IsEnabled(config.PluginAUR) {
		writeAUR(&b, cfg.AUR.Packages)
	}
	if cfg.IsEnabled(config.PluginFlatpak) {
		writeFlatpak(&b, cfg.Flatpak.Packages)
		writeFlatpakUsers(&b, cfg.Flatpak.UserPackages)
	}
	// node intentionally omitted from v1: per-manager versions and global
	// install paths vary too much to make a useful lock for now.

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir lockfile dir: %w", err)
	}
	return state.AtomicWrite(path, []byte(b.String()), 0o644)
}

func writePacman(b *strings.Builder, packages []string) {
	if !hasCommand("pacman") {
		return
	}
	pkgs := append([]string(nil), packages...)
	sort.Strings(pkgs)
	for _, p := range pkgs {
		v, ok := pacmanVersion(p)
		if !ok {
			continue
		}
		fmt.Fprintf(b, "[pacman.%s]\n", quote(p))
		fmt.Fprintf(b, "version = %q\n\n", v)
	}
}

func writeAUR(b *strings.Builder, packages []string) {
	if !hasCommand("pacman") {
		return
	}
	pkgs := append([]string(nil), packages...)
	sort.Strings(pkgs)
	for _, p := range pkgs {
		v, ok := pacmanVersion(p)
		if !ok {
			continue
		}
		fmt.Fprintf(b, "[aur.%s]\n", quote(p))
		fmt.Fprintf(b, "version = %q\n\n", v)
	}
}

func writeFlatpak(b *strings.Builder, packages []string) {
	if !hasCommand("flatpak") {
		return
	}
	pkgs := append([]string(nil), packages...)
	sort.Strings(pkgs)
	for _, p := range pkgs {
		commit, version := flatpakInfo(p)
		if commit == "" && version == "" {
			continue
		}
		fmt.Fprintf(b, "[flatpak.%s]\n", quote(p))
		if version != "" {
			fmt.Fprintf(b, "version = %q\n", version)
		}
		if commit != "" {
			fmt.Fprintf(b, "commit  = %q\n", commit)
		}
		fmt.Fprintln(b)
	}
}

// writeFlatpakUsers emits a [flatpak.user.<user>.<pkg>] section per declared
// per-user flatpak so the lockfile reflects everything the flatpak plugin
// manages, not just system-wide installs. Versions are best-effort: when the
// process can't query a user install (running as root without sudo to that
// user) we still emit the package as a key with an empty body so it's
// represented.
func writeFlatpakUsers(b *strings.Builder, users map[string][]string) {
	if !hasCommand("flatpak") {
		return
	}
	if len(users) == 0 {
		return
	}
	usernames := make([]string, 0, len(users))
	for u := range users {
		usernames = append(usernames, u)
	}
	sort.Strings(usernames)
	for _, user := range usernames {
		pkgs := append([]string(nil), users[user]...)
		sort.Strings(pkgs)
		for _, p := range pkgs {
			commit, version := flatpakUserInfo(user, p)
			fmt.Fprintf(b, "[flatpak.user.%s.%s]\n", quote(user), quote(p))
			if version != "" {
				fmt.Fprintf(b, "version = %q\n", version)
			}
			if commit != "" {
				fmt.Fprintf(b, "commit  = %q\n", commit)
			}
			fmt.Fprintln(b)
		}
	}
}

// flatpakUserInfo asks for `flatpak info` of a per-user install. We try the
// `sudo -u <user> flatpak --user info <pkg>` form so we get the right
// install regardless of who's running bigkis. If sudo or the user lookup
// fails, we fall back to plain `flatpak info` (which only sees system).
func flatpakUserInfo(user, pkg string) (commit, version string) {
	if hasCommand("sudo") {
		out, err := exec.Command("sudo", "-u", user, "flatpak", "--user", "info", pkg).Output()
		if err == nil {
			return parseFlatpakInfo(string(out))
		}
	}
	return flatpakInfo(pkg)
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
	return "", false
}

func flatpakInfo(pkg string) (commit, version string) {
	out, err := exec.Command("flatpak", "info", pkg).Output()
	if err != nil {
		return "", ""
	}
	return parseFlatpakInfo(string(out))
}

func parseFlatpakInfo(out string) (commit, version string) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Version:"):
			version = strings.TrimSpace(strings.TrimPrefix(line, "Version:"))
		case strings.HasPrefix(line, "Commit:"):
			commit = strings.TrimSpace(strings.TrimPrefix(line, "Commit:"))
		}
	}
	return
}

func hasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// quote produces a TOML-safe key. Names with special characters are wrapped
// in double quotes; bare keys are emitted as-is.
func quote(name string) string {
	for _, r := range name {
		if !(r == '_' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return fmt.Sprintf("%q", name)
		}
	}
	return name
}
