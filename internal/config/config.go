// Package config defines the bigkis TOML schema and loader.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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

// Config is the full TOML document.
type Config struct {
	Settings Settings `toml:"settings"`
	Pacman   Pacman   `toml:"pacman"`
	AUR      AUR      `toml:"aur"`
	Flatpak  Flatpak  `toml:"flatpak"`
	Node     Node     `toml:"node"`

	// Path is the absolute path the config was loaded from. Populated by Load.
	Path string `toml:"-"`
}

// Settings holds top-level options shared by plugins.
type Settings struct {
	Enabled     []string `toml:"enabled"`
	AURHelper   string   `toml:"aur_helper"`
	NodeManager string   `toml:"node_manager"`
}

// Pacman declares native Arch packages.
type Pacman struct {
	Packages []string `toml:"packages"`
	Ignored  []string `toml:"ignored"`
}

// AUR declares foreign packages built from the AUR via a helper.
type AUR struct {
	Packages []string `toml:"packages"`
	Ignored  []string `toml:"ignored"`
}

// Flatpak declares system-wide and per-user flatpak applications.
type Flatpak struct {
	Packages     []string            `toml:"packages"`
	Ignored      []string            `toml:"ignored"`
	UserPackages map[string][]string `toml:"user_packages"`
}

// Node declares globally-installed node packages.
type Node struct {
	Packages []string      `toml:"packages"`
	Package  []NodePackage `toml:"package"`
}

// NodePackage is a per-package manager override declared as `[[node.package]]`.
type NodePackage struct {
	Name    string `toml:"name"`
	Manager string `toml:"manager"`
}

// Load resolves the config path, parses, and validates.
func Load(explicitPath string) (*Config, error) {
	path, err := resolvePath(explicitPath)
	if err != nil {
		return nil, err
	}
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
		return nil, fmt.Errorf("unknown keys in %s: %v", path, keys)
	}
	c.Path = path
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
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

// resolvePath returns the first existing path among:
//  1. explicit (the --config flag)
//  2. $BIGKIS_CONFIG
//  3. /etc/bigkis/system.toml
//  4. $XDG_CONFIG_HOME/bigkis/system.toml or ~/.config/bigkis/system.toml
func resolvePath(explicit string) (string, error) {
	candidates := []string{}
	if explicit != "" {
		candidates = append(candidates, explicit)
	}
	if env := os.Getenv("BIGKIS_CONFIG"); env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates, "/etc/bigkis/system.toml")
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
