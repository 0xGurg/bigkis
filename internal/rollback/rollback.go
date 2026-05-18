// Package rollback writes filesystem-agnostic rollback scripts before each
// apply and runs them on demand. It is the universal alternative to btrfs/ZFS
// snapshots for users on ext4 or any non-CoW filesystem.
package rollback

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codeberg.org/gurg/bigkis/internal/config"
	"codeberg.org/gurg/bigkis/internal/plugin"
	"codeberg.org/gurg/bigkis/internal/state"
)

// MaxRetained controls how many rollback scripts are kept on disk. The oldest
// scripts beyond this limit are removed when a new one is written.
const MaxRetained = 5

// Op is one inverse operation that the rollback script will perform.
type Op struct {
	Plugin string
	Kind   plugin.OpKind // inverse: OpAdd here means "we removed this; rollback should re-install"
	Target string
	Detail string
	// AURHelper is only meaningful when Plugin is "aur".
	AURHelper string
	// FlatpakRemote is only meaningful when Plugin is "flatpak". Empty means
	// "flathub" so older Op values continue to work.
	FlatpakRemote string
}

// Script is a rollback script that has been written to disk.
type Script struct {
	ID   string
	Path string
}

// Dir returns the directory where rollback scripts live. An explicit
// XDG_STATE_HOME wins (so callers running with sudo -E and tests can isolate),
// then the system path when running as root, then a per-user fallback.
func Dir() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "bigkis", "rollbacks")
	}
	if os.Geteuid() == 0 {
		return "/var/lib/bigkis/rollbacks"
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "bigkis", "rollbacks")
	}
	return "/var/lib/bigkis/rollbacks"
}

// OpsForReport translates a plugin's planned report into the inverse
// operations that would undo it.
func OpsForReport(pluginName string, cfg *config.Config, r plugin.Report) []Op {
	var ops []Op
	for _, op := range r.Operations {
		// Inverse: an Add becomes a Remove and vice versa.
		var inverse plugin.OpKind
		if op.Kind == plugin.OpAdd {
			inverse = plugin.OpRemove
		} else {
			inverse = plugin.OpAdd
		}
		ops = append(ops, Op{
			Plugin:        pluginName,
			Kind:          inverse,
			Target:        op.Target,
			Detail:        op.Detail,
			AURHelper:     cfg.Settings.AURHelper,
			FlatpakRemote: cfg.Flatpak.Remote,
		})
	}
	return ops
}

// Write writes a rollback script for the given operations and returns its
// path. If ops is empty no script is written and the returned path is "".
//
// Filenames include nanosecond precision so two applies in the same second
// don't clobber each other; rollback IDs sort lexicographically in time
// order.
func Write(ops []Op) (string, error) {
	if len(ops) == 0 {
		return "", nil
	}
	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir rollback dir: %w", err)
	}
	id := newID(time.Now().UTC())
	path := filepath.Join(dir, "rollback-"+id+".sh")

	var b strings.Builder
	fmt.Fprintln(&b, "#!/bin/sh")
	fmt.Fprintf(&b, "# bigkis rollback script generated %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintln(&b, "# This script reverses the apply that immediately followed it.")
	fmt.Fprintln(&b, "# Run it manually if you want to undo that apply:")
	fmt.Fprintf(&b, "#   bigkis rollback --id %s\n", id)
	fmt.Fprintln(&b, "set -e")
	fmt.Fprintln(&b)

	// Group ops by plugin and kind so we emit fewer commands.
	type key struct {
		plugin string
		kind   plugin.OpKind
		detail string
		helper string
		remote string
	}
	groups := map[key][]string{}
	for _, op := range ops {
		k := key{
			plugin: op.Plugin,
			kind:   op.Kind,
			detail: op.Detail,
			helper: op.AURHelper,
			remote: op.FlatpakRemote,
		}
		groups[k] = append(groups[k], op.Target)
	}

	type emitFn func(emitArgs) string
	emit := func(plugin string, kind plugin.OpKind) emitFn {
		switch plugin {
		case "pacman":
			return pacmanCommand(kind)
		case "aur":
			return aurCommand(kind)
		case "flatpak":
			return flatpakCommand(kind)
		case "node":
			return nodeCommand(kind)
		}
		return func(emitArgs) string { return "" }
	}

	keys := make([]key, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].plugin != keys[j].plugin {
			return keys[i].plugin < keys[j].plugin
		}
		return keys[i].kind < keys[j].kind
	})

	for _, k := range keys {
		targets := groups[k]
		sort.Strings(targets)
		cmd := emit(k.plugin, k.kind)(emitArgs{
			targets: targets,
			detail:  k.detail,
			helper:  k.helper,
			remote:  k.remote,
		})
		if cmd != "" {
			fmt.Fprintln(&b, cmd)
		}
	}

	if err := state.AtomicWrite(path, []byte(b.String()), 0o755); err != nil {
		return "", fmt.Errorf("write rollback script: %w", err)
	}

	if err := pruneOldScripts(dir); err != nil {
		// Best-effort. Do not fail the apply because of pruning.
		_ = err
	}
	return path, nil
}

// newID returns the rollback identifier for a given UTC instant. We keep
// nanosecond precision so concurrent applies (or two manual runs in the same
// second) don't collide on the same file.
func newID(t time.Time) string {
	return t.UTC().Format("20060102T150405.000000000Z")
}

// List returns the rollback scripts in chronological order (oldest first).
func List() ([]Script, error) {
	dir := Dir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read rollback dir: %w", err)
	}
	var out []Script
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "rollback-") || !strings.HasSuffix(name, ".sh") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "rollback-"), ".sh")
		out = append(out, Script{ID: id, Path: filepath.Join(dir, name)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Run executes a rollback script with sh.
func Run(s Script) error {
	cmd := exec.Command("sh", s.Path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Read returns the contents of a rollback script.
func Read(s Script) (string, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return "", fmt.Errorf("read rollback script: %w", err)
	}
	return string(data), nil
}

// OpCount returns an approximate count of operations in a rollback script body.
// It counts non-empty, non-comment lines, excluding the shebang and "set -e".
func OpCount(body string) int {
	count := 0
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || line == "set -e" || line == "#!/bin/sh" {
			continue
		}
		count++
	}
	return count
}

func pruneOldScripts(dir string) error {
	scripts, err := List()
	if err != nil {
		return err
	}
	if len(scripts) <= MaxRetained {
		return nil
	}
	for _, s := range scripts[:len(scripts)-MaxRetained] {
		_ = os.Remove(s.Path)
	}
	return nil
}

// command emitters -----------------------------------------------------------

// emitArgs bundles the inputs each per-plugin command emitter needs. It's
// defined at package scope so the emitters and their tests can share the
// type without referencing Write's local types.
type emitArgs struct {
	targets []string
	detail  string
	helper  string
	remote  string
}

func pacmanCommand(kind plugin.OpKind) func(emitArgs) string {
	return func(a emitArgs) string {
		joined := strings.Join(shellQuoteAll(a.targets), " ")
		switch kind {
		case plugin.OpAdd:
			return "sudo pacman -S --needed --noconfirm " + joined
		case plugin.OpRemove:
			return "sudo pacman -Rns --noconfirm " + joined
		}
		return ""
	}
}

func aurCommand(kind plugin.OpKind) func(emitArgs) string {
	return func(a emitArgs) string {
		helper := a.helper
		if helper == "" {
			helper = "yay"
		}
		joined := strings.Join(shellQuoteAll(a.targets), " ")
		switch kind {
		case plugin.OpAdd:
			return helper + " -S --needed --noconfirm " + joined
		case plugin.OpRemove:
			return helper + " -Rns --noconfirm " + joined
		}
		return ""
	}
}

func flatpakCommand(kind plugin.OpKind) func(emitArgs) string {
	return func(a emitArgs) string {
		joined := strings.Join(shellQuoteAll(a.targets), " ")
		remote := a.remote
		if remote == "" {
			remote = "flathub"
		}
		systemFlag := "--system"
		var sudoPrefix string
		userPrefix := ""
		if strings.HasPrefix(a.detail, "user ") {
			user := strings.TrimPrefix(a.detail, "user ")
			systemFlag = "--user"
			userPrefix = "sudo -u " + shellQuote(user) + " "
		} else {
			sudoPrefix = "sudo "
		}
		switch kind {
		case plugin.OpAdd:
			if userPrefix != "" {
				return userPrefix + "flatpak install " + systemFlag + " --noninteractive --assumeyes " + shellQuote(remote) + " " + joined
			}
			return sudoPrefix + "flatpak install " + systemFlag + " --noninteractive --assumeyes " + shellQuote(remote) + " " + joined
		case plugin.OpRemove:
			if userPrefix != "" {
				return userPrefix + "flatpak uninstall " + systemFlag + " --noninteractive --assumeyes " + joined
			}
			return sudoPrefix + "flatpak uninstall " + systemFlag + " --noninteractive --assumeyes " + joined
		}
		return ""
	}
}

func nodeCommand(kind plugin.OpKind) func(emitArgs) string {
	return func(a emitArgs) string {
		mgr := strings.TrimPrefix(a.detail, "via ")
		if mgr == "" {
			mgr = "npm"
		}
		joined := strings.Join(shellQuoteAll(a.targets), " ")
		switch kind {
		case plugin.OpAdd:
			return mgr + " " + addArgs(mgr) + " " + joined
		case plugin.OpRemove:
			return mgr + " " + removeArgs(mgr) + " " + joined
		}
		return ""
	}
}

func addArgs(mgr string) string {
	switch mgr {
	case "pnpm":
		return "add -g"
	case "yarn":
		return "global add"
	}
	return "install -g"
}

func removeArgs(mgr string) string {
	switch mgr {
	case "pnpm":
		return "remove -g"
	case "yarn":
		return "global remove"
	}
	return "uninstall -g"
}

// shellQuote wraps a string in POSIX single-quotes so it survives `sh -c`
// regardless of the characters it contains. Single quotes inside the string
// terminate-quote, escape, and re-open: `it's` -> `'it'\”s'`. We use this
// for every argument we emit into the rollback script.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !needsQuoting(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellQuoteAll(items []string) []string {
	out := make([]string, len(items))
	for i, x := range items {
		out[i] = shellQuote(x)
	}
	return out
}

// needsQuoting returns true when the string is not a strictly-safe POSIX
// shell word and so must be quoted. Anything outside the conservative
// alphanumerics + "+-./@_:" set goes through the single-quote branch.
func needsQuoting(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.' || r == '/' || r == '@' || r == '+' || r == ':':
		default:
			return true
		}
	}
	return false
}
