// Package importer scans the current system and emits a starter system.toml.
//
// It is intentionally independent of the plugin packages: it shells out to
// pacman/flatpak/npm/pnpm/yarn directly so it can run on a system that does
// not yet have a bigkis config or state file.
package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Options controls what gets imported.
type Options struct {
	// Only restricts the import to a subset of plugin names. Empty means "all".
	Only []string
	// AURHelper sets the [settings].aur_helper value in the output.
	AURHelper string
	// NodeManager sets the [settings].node_manager value in the output.
	NodeManager string
}

// NodePackage represents a globally installed Node.js package with its manager.
type NodePackage struct {
	Name    string
	Manager string // "npm", "pnpm", or "yarn"
}

// Selection holds the user's filtered package choices from the interactive TUI.
type Selection struct {
	Pacman  []string
	AUR     []string
	Flatpak []string
	Node    []NodePackage
}

// errFlatpakNotInstalled is a sentinel error used when flatpak is not on PATH.
var errFlatpakNotInstalled = errors.New("flatpak not installed")

// ──────────────────────────────────────────────
// Public scan functions
// ──────────────────────────────────────────────

// ScanPacman returns all explicitly installed native packages via pacman -Qqen.
func ScanPacman() ([]string, error) {
	return captureLines("pacman", "-Qqen")
}

// ScanAUR returns all foreign (AUR) packages via pacman -Qqm.
// When no foreign packages exist pacman exits 1; this is treated as empty.
func ScanAUR() ([]string, error) {
	pkgs, err := captureLines("pacman", "-Qqm")
	if err != nil {
		if isExitOne(err) {
			return nil, nil
		}
		return nil, err
	}
	return pkgs, nil
}

// ScanFlatpak returns all system flatpak applications.
// Returns nil, nil when flatpak is not installed.
func ScanFlatpak() ([]string, error) {
	if !hasCommand("flatpak") {
		return nil, errFlatpakNotInstalled
	}
	pkgs, err := captureLines("flatpak", "list", "--app", "--system", "--columns=application")
	if err != nil {
		return nil, err
	}
	return stripFlatpakHeader(pkgs), nil
}

// ScanNode returns all globally installed Node.js packages across npm, pnpm,
// and yarn, annotated with their originating manager.
func ScanNode() ([]NodePackage, error) {
	managers := []string{"npm", "pnpm", "yarn"}
	var entries []NodePackage
	var errs []string
	for _, mgr := range managers {
		if !hasCommand(mgr) {
			continue
		}
		pkgs, err := probeNodeManager(mgr)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", mgr, err))
			continue
		}
		for _, p := range pkgs {
			entries = append(entries, NodePackage{Name: p, Manager: mgr})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Manager != entries[j].Manager {
			return entries[i].Manager < entries[j].Manager
		}
		return entries[i].Name < entries[j].Name
	})
	if len(errs) > 0 {
		return entries, fmt.Errorf("node probe issues: %s", strings.Join(errs, "; "))
	}
	return entries, nil
}

// ──────────────────────────────────────────────
// Run / RunSelected
// ──────────────────────────────────────────────

// Run writes a TOML config representing the current system to out.
func Run(out io.Writer, opts Options) error {
	pacmanPkgs, pacmanErr := ScanPacman()
	aurPkgs, aurErr := ScanAUR()
	flatpakPkgs, flatpakErr := ScanFlatpak()
	nodePkgs, nodeErr := ScanNode()
	return runAll(out, opts,
		pacmanPkgs, pacmanErr,
		aurPkgs, aurErr,
		flatpakPkgs, flatpakErr,
		nodePkgs, nodeErr,
	)
}

// RunSelected writes a TOML config using the pre-filtered selection.
// It does not shell out for scanning. The output format is identical to Run.
func RunSelected(out io.Writer, opts Options, sel Selection) error {
	return runAll(out, opts,
		sel.Pacman, nil,
		sel.AUR, nil,
		sel.Flatpak, nil,
		sel.Node, nil,
	)
}

// runAll writes the complete TOML output using pre-scanned data.
func runAll(out io.Writer, opts Options,
	pacmanPkgs []string, pacmanErr error,
	aurPkgs []string, aurErr error,
	flatpakPkgs []string, flatpakErr error,
	nodePkgs []NodePackage, nodeErr error,
) error {
	want := buildSelector(opts.Only)

	aurHelper := opts.AURHelper
	if aurHelper == "" {
		aurHelper = "yay"
	}
	nodeManager := opts.NodeManager
	if nodeManager == "" {
		nodeManager = "npm"
	}

	fmt.Fprintf(out, header, time.Now().UTC().Format("2006-01-02"))
	fmt.Fprintln(out, "[settings]")
	fmt.Fprintf(out, "enabled      = %s\n", enabledList(opts.Only))
	fmt.Fprintf(out, "aur_helper   = %q\n", aurHelper)
	fmt.Fprintf(out, "node_manager = %q\n", nodeManager)
	fmt.Fprintln(out)

	if want("pacman") {
		writePacmanSection(out, pacmanPkgs, pacmanErr)
	}
	if want("aur") {
		writeAURSection(out, aurPkgs, aurErr)
	}
	if want("flatpak") {
		writeFlatpakSection(out, flatpakPkgs, flatpakErr)
	}
	if want("node") {
		writeNodeSection(out, nodeManager, nodePkgs, nodeErr)
	}
	return nil
}

// enabledList returns the TOML array literal for settings.enabled. When the
// caller passed --only we honour that subset so the generated config doesn't
// silently re-enable plugins they didn't import (which would invite removals
// for sections we never populated).
func enabledList(only []string) string {
	all := []string{"pacman", "aur", "flatpak", "node"}
	chosen := all
	if len(only) > 0 {
		valid := map[string]bool{}
		for _, n := range all {
			valid[n] = true
		}
		seen := map[string]bool{}
		chosen = chosen[:0]
		for _, n := range only {
			if !valid[n] || seen[n] {
				continue
			}
			seen[n] = true
			chosen = append(chosen, n)
		}
	}
	parts := make([]string, len(chosen))
	for i, n := range chosen {
		parts[i] = fmt.Sprintf("%q", n)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

const header = `# Generated by ` + "`bigkis import`" + ` on %s.
#
# IMPORTANT: review and curate this file before applying. The importer simply
# dumps what is currently installed on this machine; you almost certainly
# want to remove transient or experimental packages, organize related
# packages into [groups], and add per-user flatpak entries that the importer
# cannot infer.
#
# After curating:
#   bigkis check  -c system.toml
#   bigkis status -c system.toml
#   sudo bigkis apply --dry-run -c system.toml
#   sudo bigkis apply -c system.toml

`

// ──────────────────────────────────────────────
// Section writers (accept pre-scanned data)
// ──────────────────────────────────────────────

func writePacmanSection(out io.Writer, pkgs []string, scanErr error) {
	fmt.Fprintln(out, "[pacman]")
	if scanErr != nil {
		fmt.Fprintf(out, "# pacman probe failed: %s\n", scanErr)
		fmt.Fprintln(out, "packages = []")
		fmt.Fprintln(out, "ignored  = []")
		fmt.Fprintln(out)
		return
	}
	writeStringList(out, "packages", pkgs)
	fmt.Fprintln(out, "ignored  = []")
	fmt.Fprintln(out)
}

func writeAURSection(out io.Writer, pkgs []string, scanErr error) {
	fmt.Fprintln(out, "[aur]")
	if scanErr != nil {
		fmt.Fprintf(out, "# aur probe failed: %s\n", scanErr)
		fmt.Fprintln(out, "packages = []")
		fmt.Fprintln(out, "ignored  = []")
		fmt.Fprintln(out)
		return
	}
	writeStringList(out, "packages", pkgs)
	fmt.Fprintln(out, "ignored  = []")
	fmt.Fprintln(out)
}

func writeFlatpakSection(out io.Writer, pkgs []string, scanErr error) {
	fmt.Fprintln(out, "[flatpak]")
	if errors.Is(scanErr, errFlatpakNotInstalled) {
		fmt.Fprintln(out, "# flatpak not installed; skipping system probe")
		fmt.Fprintln(out, "packages = []")
		fmt.Fprintln(out, "ignored  = []")
		fmt.Fprintln(out, "[flatpak.user_packages]")
		fmt.Fprintln(out, `# Add per-user flatpaks here, e.g. alice = ["com.valvesoftware.Steam"]`)
		fmt.Fprintln(out)
		return
	}
	if scanErr != nil {
		fmt.Fprintf(out, "# flatpak probe failed: %s\n", scanErr)
		pkgs = nil
	}
	writeStringList(out, "packages", pkgs)
	fmt.Fprintln(out, "ignored  = []")
	fmt.Fprintln(out, "[flatpak.user_packages]")
	fmt.Fprintln(out, "# Per-user flatpaks are NOT auto-imported. Add them manually:")
	fmt.Fprintln(out, `# alice = ["com.valvesoftware.Steam"]`)
	fmt.Fprintln(out)
}

func writeNodeSection(out io.Writer, defaultMgr string, pkgs []NodePackage, scanErr error) {
	fmt.Fprintln(out, "[node]")
	if scanErr != nil {
		fmt.Fprintf(out, "# %s\n", scanErr)
	}
	var defaults []string
	var overrides []NodePackage
	for _, p := range pkgs {
		if p.Manager == defaultMgr {
			defaults = append(defaults, p.Name)
		} else {
			overrides = append(overrides, p)
		}
	}
	writeStringList(out, "packages", defaults)
	for _, o := range overrides {
		fmt.Fprintln(out, "[[node.package]]")
		fmt.Fprintf(out, "name    = %q\n", o.Name)
		fmt.Fprintf(out, "manager = %q\n", o.Manager)
	}
}

// ──────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────

func buildSelector(only []string) func(string) bool {
	if len(only) == 0 {
		return func(string) bool { return true }
	}
	set := map[string]struct{}{}
	for _, n := range only {
		set[n] = struct{}{}
	}
	return func(name string) bool {
		_, ok := set[name]
		return ok
	}
}

func captureLines(name string, args ...string) ([]string, error) {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return nil, err
	}
	return splitLines(string(out)), nil
}

func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// `pacman -Qm` prints "name version"; take only the package name.
		fields := strings.Fields(line)
		out = append(out, fields[0])
	}
	return out
}

func stripFlatpakHeader(in []string) []string {
	var out []string
	for _, line := range in {
		if strings.HasPrefix(line, "Application") {
			continue
		}
		out = append(out, line)
	}
	return out
}

func hasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func isExitOne(err error) bool {
	if err == nil {
		return false
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode() == 1
	}
	return false
}

func writeStringList(out io.Writer, key string, items []string) {
	uniq := dedup(items)
	sort.Strings(uniq)
	if len(uniq) == 0 {
		fmt.Fprintf(out, "%s = []\n", key)
		return
	}
	fmt.Fprintf(out, "%s = [\n", key)
	for _, x := range uniq {
		fmt.Fprintf(out, "  %q,\n", x)
	}
	fmt.Fprintln(out, "]")
}

func dedup(items []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, x := range items {
		if x == "" {
			continue
		}
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	return out
}

// node probes mirror what the node plugin does, kept in sync intentionally.

func probeNodeManager(mgr string) ([]string, error) {
	switch mgr {
	case "npm", "pnpm":
		return probeNPMLike(mgr)
	case "yarn":
		return probeYarn()
	}
	return nil, fmt.Errorf("unknown node manager %q", mgr)
}

func probeNPMLike(mgr string) ([]string, error) {
	// npm and pnpm sometimes exit non-zero (peer-dep complaints) while still
	// emitting valid JSON on stdout. Try parsing first; only surface the run
	// error if we have nothing usable. This mirrors the node plugin's probe.
	out, runErr := exec.Command(mgr, "ls", "-g", "--depth=0", "--json").Output()
	if len(out) == 0 {
		if runErr != nil {
			return nil, fmt.Errorf("%s ls -g: %w", mgr, runErr)
		}
		return nil, nil
	}
	type listOutput struct {
		Dependencies map[string]any `json:"dependencies"`
	}
	var arr []listOutput
	if err := json.Unmarshal(out, &arr); err == nil && len(arr) > 0 {
		var names []string
		for _, l := range arr {
			for k := range l.Dependencies {
				names = append(names, k)
			}
		}
		return names, nil
	}
	var single listOutput
	if err := json.Unmarshal(out, &single); err == nil {
		var names []string
		for k := range single.Dependencies {
			names = append(names, k)
		}
		return names, nil
	}
	if runErr != nil {
		return nil, fmt.Errorf("parse %s ls output: %w", mgr, runErr)
	}
	return nil, fmt.Errorf("could not parse %s ls output", mgr)
}

var yarnInfoLine = regexp.MustCompile(`^info "(.+)@[^"]+" has binaries`)

func probeYarn() ([]string, error) {
	out, err := exec.Command("yarn", "global", "list", "--depth=0").Output()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		m := yarnInfoLine.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) == 2 {
			names = append(names, m[1])
		}
	}
	return names, nil
}
