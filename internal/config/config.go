// Package config defines the bigkis TOML schema and loader.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"
)

// Plugin names used in [settings].enabled and on-disk state keys.
const (
	PluginPacman  = "pacman"
	PluginAUR     = "aur"
	PluginFlatpak = "flatpak"
	PluginNode    = "node"
)

var validPlugins = map[string]bool{
	PluginPacman:  true,
	PluginAUR:     true,
	PluginFlatpak: true,
	PluginNode:    true,
}

var validAURHelpers = map[string]bool{"yay": true, "paru": true}
var validNodeManagers = map[string]bool{"npm": true, "pnpm": true, "yarn": true}

// Pacman orphan-prune modes. "scoped" (default) prunes only orphans that did
// not exist before this apply; "all" prunes every orphan on the system after
// demotion (the original 0.x behavior); "none" disables pruning entirely so
// removed packages stay on disk as deps until the user prunes manually.
const (
	PruneOrphansScoped = "scoped"
	PruneOrphansAll    = "all"
	PruneOrphansNone   = "none"
)

var validPruneModes = map[string]bool{
	PruneOrphansScoped: true,
	PruneOrphansAll:    true,
	PruneOrphansNone:   true,
}

// Config is the full TOML document, after include merging, host overlay
// application, and group expansion.
type Config struct {
	Settings Settings               `toml:"settings"`
	Pacman   Pacman                 `toml:"pacman"`
	AUR      AUR                    `toml:"aur"`
	Flatpak  Flatpak                `toml:"flatpak"`
	Node     Node                   `toml:"node"`
	Groups   map[string][]string    `toml:"groups"`
	Hosts    map[string]HostOverlay `toml:"hosts"`

	// Path is the absolute path of the top-level config that was loaded.
	Path string `toml:"-"`
	// SourcePaths is every file that contributed to the merged config, in
	// the order they were merged: SourcePaths[0] is always Path.
	SourcePaths []string `toml:"-"`
}

// Settings holds top-level options shared by plugins.
type Settings struct {
	Enabled     []string `toml:"enabled"`
	AURHelper   string   `toml:"aur_helper"`
	NodeManager string   `toml:"node_manager"`
	// PruneOrphans controls how the pacman plugin prunes orphans after a
	// removal: "scoped" (default), "all", or "none". See PruneOrphans*
	// constants above.
	PruneOrphans string `toml:"prune_orphans"`
	// Include is a list of additional TOML files to merge in. Paths are
	// resolved relative to the directory of the file that declared them.
	// Includes are merged first; the file that includes them wins for
	// scalars, while lists are concatenated and de-duplicated.
	Include []string `toml:"include"`
}

// Pacman declares native Arch packages.
type Pacman struct {
	Packages []string `toml:"packages"`
	Ignored  []string `toml:"ignored"`
	Groups   []string `toml:"groups"`
}

// AUR declares foreign packages built from the AUR via a helper.
type AUR struct {
	Packages []string `toml:"packages"`
	Ignored  []string `toml:"ignored"`
	Groups   []string `toml:"groups"`
}

// Flatpak declares system-wide and per-user flatpak applications.
type Flatpak struct {
	Packages     []string            `toml:"packages"`
	Ignored      []string            `toml:"ignored"`
	UserPackages map[string][]string `toml:"user_packages"`
	Groups       []string            `toml:"groups"`
	// Remote is the flatpak remote name used for installs (default "flathub").
	Remote string `toml:"remote"`
}

// Node declares globally-installed node packages.
type Node struct {
	Packages []string      `toml:"packages"`
	Package  []NodePackage `toml:"package"`
	Groups   []string      `toml:"groups"`
}

// NodePackage is a per-package manager override declared as `[[node.package]]`.
type NodePackage struct {
	Name    string `toml:"name"`
	Manager string `toml:"manager"`
}

// HostOverlay is a section under `[hosts.<hostname>]` that, when the current
// machine's hostname matches, is overlaid on top of the merged configuration.
type HostOverlay struct {
	Settings Settings `toml:"settings"`
	Pacman   Pacman   `toml:"pacman"`
	AUR      AUR      `toml:"aur"`
	Flatpak  Flatpak  `toml:"flatpak"`
	Node     Node     `toml:"node"`
}

// Load resolves the config path, parses the file plus any includes, applies
// the matching host overlay, expands groups, fills in defaults, and validates.
func Load(explicitPath string) (*Config, error) {
	path, err := resolvePath(explicitPath)
	if err != nil {
		return nil, err
	}

	visited := map[string]bool{}
	merged, sources, err := loadAndMerge(path, visited)
	if err != nil {
		return nil, err
	}

	merged.Path = path
	merged.SourcePaths = sources

	hostname, _ := os.Hostname()
	merged.applyHostOverlay(hostname)

	if err := merged.expandGroups(); err != nil {
		return nil, err
	}

	merged.applyDefaults()
	if err := merged.Validate(); err != nil {
		return nil, err
	}
	return merged, nil
}

// loadAndMerge reads the file at path and recursively merges in any files it
// declares via [settings].include. Later files overlay earlier ones; the file
// being processed always wins over its own includes for scalars.
func loadAndMerge(path string, visited map[string]bool) (*Config, []string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, fmt.Errorf("abs %s: %w", path, err)
	}
	if visited[abs] {
		return nil, nil, fmt.Errorf("include cycle detected at %s", abs)
	}
	visited[abs] = true
	defer delete(visited, abs)

	file, err := decodeFile(abs)
	if err != nil {
		return nil, nil, err
	}

	merged := &Config{}
	sources := []string{abs}
	for _, inc := range file.Settings.Include {
		incPath := inc
		if !filepath.IsAbs(incPath) {
			incPath = filepath.Join(filepath.Dir(abs), incPath)
		}
		sub, subSources, err := loadAndMerge(incPath, visited)
		if err != nil {
			return nil, nil, err
		}
		applyOverlay(merged, sub)
		sources = append(sources, subSources...)
	}
	applyOverlay(merged, file)
	return merged, sources, nil
}

func decodeFile(path string) (*Config, error) {
	var c Config
	meta, err := toml.DecodeFile(path, &c)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		var keys []string
		for _, k := range undecoded {
			keys = append(keys, k.String())
		}
		sort.Strings(keys)
		return nil, fmt.Errorf("unknown keys in %s: %v", path, keys)
	}
	// Catch typos in a single file: duplicate enabled entries are almost
	// always a mistake. Across-file merging silently de-dupes.
	seen := map[string]bool{}
	for _, p := range c.Settings.Enabled {
		if seen[p] {
			return nil, fmt.Errorf("settings.enabled in %s: duplicate plugin %q", path, p)
		}
		seen[p] = true
	}
	return &c, nil
}

// applyOverlay merges overlay into base. Lists are concatenated and de-duped;
// scalars are overlaid only when the overlay sets a non-zero value (so the
// outermost file wins because it is always applied last). Maps merge key by
// key, with overlay winning for shared keys.
func applyOverlay(base, overlay *Config) {
	// Settings
	base.Settings.Enabled = mergeStrings(base.Settings.Enabled, overlay.Settings.Enabled)
	if overlay.Settings.AURHelper != "" {
		base.Settings.AURHelper = overlay.Settings.AURHelper
	}
	if overlay.Settings.NodeManager != "" {
		base.Settings.NodeManager = overlay.Settings.NodeManager
	}
	if overlay.Settings.PruneOrphans != "" {
		base.Settings.PruneOrphans = overlay.Settings.PruneOrphans
	}
	// Include is intentionally not merged into the result; it has already
	// been processed by loadAndMerge.

	mergePacman(&base.Pacman, &overlay.Pacman)
	mergeAUR(&base.AUR, &overlay.AUR)
	mergeFlatpak(&base.Flatpak, &overlay.Flatpak)
	mergeNode(&base.Node, &overlay.Node)

	if len(overlay.Groups) > 0 {
		if base.Groups == nil {
			base.Groups = map[string][]string{}
		}
		for k, v := range overlay.Groups {
			base.Groups[k] = mergeStrings(base.Groups[k], v)
		}
	}

	if len(overlay.Hosts) > 0 {
		if base.Hosts == nil {
			base.Hosts = map[string]HostOverlay{}
		}
		for k, v := range overlay.Hosts {
			merged := base.Hosts[k]
			mergePacman(&merged.Pacman, &v.Pacman)
			mergeAUR(&merged.AUR, &v.AUR)
			mergeFlatpak(&merged.Flatpak, &v.Flatpak)
			mergeNode(&merged.Node, &v.Node)
			merged.Settings.Enabled = mergeStrings(merged.Settings.Enabled, v.Settings.Enabled)
			if v.Settings.AURHelper != "" {
				merged.Settings.AURHelper = v.Settings.AURHelper
			}
			if v.Settings.NodeManager != "" {
				merged.Settings.NodeManager = v.Settings.NodeManager
			}
			if v.Settings.PruneOrphans != "" {
				merged.Settings.PruneOrphans = v.Settings.PruneOrphans
			}
			base.Hosts[k] = merged
		}
	}
}

func mergePacman(b, o *Pacman) {
	b.Packages = mergeStrings(b.Packages, o.Packages)
	b.Ignored = mergeStrings(b.Ignored, o.Ignored)
	b.Groups = mergeStrings(b.Groups, o.Groups)
}

func mergeAUR(b, o *AUR) {
	b.Packages = mergeStrings(b.Packages, o.Packages)
	b.Ignored = mergeStrings(b.Ignored, o.Ignored)
	b.Groups = mergeStrings(b.Groups, o.Groups)
}

func mergeFlatpak(b, o *Flatpak) {
	b.Packages = mergeStrings(b.Packages, o.Packages)
	b.Ignored = mergeStrings(b.Ignored, o.Ignored)
	b.Groups = mergeStrings(b.Groups, o.Groups)
	if o.Remote != "" {
		b.Remote = o.Remote
	}
	if len(o.UserPackages) > 0 {
		if b.UserPackages == nil {
			b.UserPackages = map[string][]string{}
		}
		for k, v := range o.UserPackages {
			b.UserPackages[k] = mergeStrings(b.UserPackages[k], v)
		}
	}
}

func mergeNode(b, o *Node) {
	b.Packages = mergeStrings(b.Packages, o.Packages)
	b.Groups = mergeStrings(b.Groups, o.Groups)
	// [[node.package]] entries: dedup by name; later overrides win.
	overrides := map[string]NodePackage{}
	order := []string{}
	for _, np := range b.Package {
		if _, dup := overrides[np.Name]; !dup {
			order = append(order, np.Name)
		}
		overrides[np.Name] = np
	}
	for _, np := range o.Package {
		if _, dup := overrides[np.Name]; !dup {
			order = append(order, np.Name)
		}
		overrides[np.Name] = np
	}
	b.Package = b.Package[:0]
	for _, name := range order {
		b.Package = append(b.Package, overrides[name])
	}
}

func mergeStrings(a, b []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(a)+len(b))
	for _, x := range a {
		if x == "" {
			continue
		}
		if _, dup := seen[x]; dup {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	for _, x := range b {
		if x == "" {
			continue
		}
		if _, dup := seen[x]; dup {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// applyHostOverlay overlays [hosts.<hostname>] on top of the merged config
// when hostname matches. The host overlay always wins for scalars and
// extends list fields.
func (c *Config) applyHostOverlay(hostname string) {
	if hostname == "" {
		return
	}
	overlay, ok := c.Hosts[hostname]
	if !ok {
		return
	}
	c.Settings.Enabled = mergeStrings(c.Settings.Enabled, overlay.Settings.Enabled)
	if overlay.Settings.AURHelper != "" {
		c.Settings.AURHelper = overlay.Settings.AURHelper
	}
	if overlay.Settings.NodeManager != "" {
		c.Settings.NodeManager = overlay.Settings.NodeManager
	}
	if overlay.Settings.PruneOrphans != "" {
		c.Settings.PruneOrphans = overlay.Settings.PruneOrphans
	}
	mergePacman(&c.Pacman, &overlay.Pacman)
	mergeAUR(&c.AUR, &overlay.AUR)
	mergeFlatpak(&c.Flatpak, &overlay.Flatpak)
	mergeNode(&c.Node, &overlay.Node)
}

// expandGroups inlines named groups from [groups] into each plugin's
// `packages` list. After this runs, plugins do not need to know about groups.
// Undefined group names produce a hard error.
func (c *Config) expandGroups() error {
	expand := func(refs []string, dest *[]string) error {
		for _, name := range refs {
			pkgs, ok := c.Groups[name]
			if !ok {
				return fmt.Errorf("undefined group %q", name)
			}
			*dest = mergeStrings(*dest, pkgs)
		}
		return nil
	}
	if err := expand(c.Pacman.Groups, &c.Pacman.Packages); err != nil {
		return fmt.Errorf("pacman.groups: %w", err)
	}
	if err := expand(c.AUR.Groups, &c.AUR.Packages); err != nil {
		return fmt.Errorf("aur.groups: %w", err)
	}
	if err := expand(c.Flatpak.Groups, &c.Flatpak.Packages); err != nil {
		return fmt.Errorf("flatpak.groups: %w", err)
	}
	if err := expand(c.Node.Groups, &c.Node.Packages); err != nil {
		return fmt.Errorf("node.groups: %w", err)
	}
	// After expansion plugins should not see the group references.
	c.Pacman.Groups = nil
	c.AUR.Groups = nil
	c.Flatpak.Groups = nil
	c.Node.Groups = nil
	return nil
}

func (c *Config) applyDefaults() {
	if len(c.Settings.Enabled) == 0 {
		c.Settings.Enabled = []string{PluginPacman, PluginAUR, PluginFlatpak, PluginNode}
	}
	if c.Settings.AURHelper == "" {
		c.Settings.AURHelper = "yay"
	}
	if c.Settings.NodeManager == "" {
		c.Settings.NodeManager = "npm"
	}
	if c.Settings.PruneOrphans == "" {
		c.Settings.PruneOrphans = PruneOrphansScoped
	}
	if c.Flatpak.Remote == "" {
		c.Flatpak.Remote = "flathub"
	}
	if c.Flatpak.UserPackages == nil {
		c.Flatpak.UserPackages = map[string][]string{}
	}
}

// Validate checks the config for unknown plugin names and bad enums.
func (c *Config) Validate() error {
	seen := map[string]bool{}
	for _, p := range c.Settings.Enabled {
		if !validPlugins[p] {
			return fmt.Errorf("settings.enabled: unknown plugin %q (valid: pacman, aur, flatpak, node)", p)
		}
		if seen[p] {
			return fmt.Errorf("settings.enabled: duplicate plugin %q", p)
		}
		seen[p] = true
	}
	if !validAURHelpers[c.Settings.AURHelper] {
		return fmt.Errorf("settings.aur_helper: %q invalid (valid: yay, paru)", c.Settings.AURHelper)
	}
	if !validNodeManagers[c.Settings.NodeManager] {
		return fmt.Errorf("settings.node_manager: %q invalid (valid: npm, pnpm, yarn)", c.Settings.NodeManager)
	}
	if !validPruneModes[c.Settings.PruneOrphans] {
		return fmt.Errorf("settings.prune_orphans: %q invalid (valid: scoped, all, none)", c.Settings.PruneOrphans)
	}
	for i, np := range c.Node.Package {
		if np.Name == "" {
			return fmt.Errorf("node.package[%d]: name is required", i)
		}
		if np.Manager == "" {
			continue // falls back to settings.node_manager
		}
		if !validNodeManagers[np.Manager] {
			return fmt.Errorf("node.package[%d].manager: %q invalid (valid: npm, pnpm, yarn)", i, np.Manager)
		}
	}
	return nil
}

// IsEnabled returns true if the named plugin is in settings.enabled.
func (c *Config) IsEnabled(name string) bool {
	for _, p := range c.Settings.Enabled {
		if p == name {
			return true
		}
	}
	return false
}

// resolvePath resolves a config path. An explicit --config (or
// $BIGKIS_CONFIG) overrides the search path entirely: if it points at a
// missing file, that's a hard error rather than a silent fallthrough to
// /etc/bigkis/system.toml. Without an explicit path, we walk the default
// search path and return the first match.
//
// Search path (no explicit override):
//  1. /etc/bigkis/system.toml
//  2. $XDG_CONFIG_HOME/bigkis/system.toml
//  3. ~/.config/bigkis/system.toml
func resolvePath(explicit string) (string, error) {
	if explicit == "" {
		explicit = os.Getenv("BIGKIS_CONFIG")
	}
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", fmt.Errorf("config not found: %s", explicit)
			}
			return "", fmt.Errorf("stat %s: %w", explicit, err)
		}
		return filepath.Abs(explicit)
	}

	candidates := []string{"/etc/bigkis/system.toml"}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		candidates = append(candidates, filepath.Join(xdg, "bigkis", "system.toml"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".config", "bigkis", "system.toml"))
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			abs, err := filepath.Abs(p)
			if err != nil {
				return "", err
			}
			return abs, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat %s: %w", p, err)
		}
	}
	return "", fmt.Errorf("no config found; tried: %v", candidates)
}
