package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTOML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "system.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	return path
}

func TestLoad_FullConfig(t *testing.T) {
	path := writeTOML(t, `
[settings]
enabled = ["pacman", "aur", "flatpak", "node"]
aur_helper = "paru"
node_manager = "pnpm"

[pacman]
packages = ["git", "neovim"]
ignored  = ["opendoas"]

[aur]
packages = ["fnm-bin"]
ignored  = ["yay"]

[flatpak]
packages = ["org.mozilla.firefox"]
[flatpak.user_packages]
georgep = ["com.valvesoftware.Steam"]

[node]
packages = ["typescript"]
[[node.package]]
name = "@vue/cli"
manager = "yarn"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Settings.AURHelper != "paru" {
		t.Errorf("aur_helper = %q, want paru", cfg.Settings.AURHelper)
	}
	if cfg.Settings.NodeManager != "pnpm" {
		t.Errorf("node_manager = %q, want pnpm", cfg.Settings.NodeManager)
	}
	if len(cfg.Pacman.Packages) != 2 {
		t.Errorf("pacman.packages = %v", cfg.Pacman.Packages)
	}
	if cfg.Flatpak.UserPackages["georgep"][0] != "com.valvesoftware.Steam" {
		t.Errorf("flatpak.user_packages parsing failed: %v", cfg.Flatpak.UserPackages)
	}
	if len(cfg.Node.Package) != 1 || cfg.Node.Package[0].Name != "@vue/cli" {
		t.Errorf("node.package parsing failed: %v", cfg.Node.Package)
	}
}

func TestLoad_EmptyConfigAppliesDefaults(t *testing.T) {
	path := writeTOML(t, ``)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	wantEnabled := []string{PluginPacman, PluginAUR, PluginFlatpak, PluginNode}
	if len(cfg.Settings.Enabled) != len(wantEnabled) {
		t.Errorf("Enabled = %v, want %v", cfg.Settings.Enabled, wantEnabled)
	}
	if cfg.Settings.AURHelper != "yay" {
		t.Errorf("default aur_helper = %q, want yay", cfg.Settings.AURHelper)
	}
	if cfg.Settings.NodeManager != "npm" {
		t.Errorf("default node_manager = %q, want npm", cfg.Settings.NodeManager)
	}
}

func TestLoad_RejectsUnknownPlugin(t *testing.T) {
	path := writeTOML(t, `
[settings]
enabled = ["pacman", "snap"]
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unknown plugin, got nil")
	}
	if !strings.Contains(err.Error(), "unknown plugin") {
		t.Errorf("error = %v, want 'unknown plugin'", err)
	}
}

func TestLoad_RejectsDuplicatePlugin(t *testing.T) {
	path := writeTOML(t, `
[settings]
enabled = ["pacman", "pacman"]
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestLoad_RejectsBadAURHelper(t *testing.T) {
	path := writeTOML(t, `
[settings]
aur_helper = "trizen"
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "aur_helper") {
		t.Fatalf("expected aur_helper error, got %v", err)
	}
}

func TestLoad_RejectsBadNodeManager(t *testing.T) {
	path := writeTOML(t, `
[settings]
node_manager = "bun"
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "node_manager") {
		t.Fatalf("expected node_manager error, got %v", err)
	}
}

func TestLoad_RejectsBadNodePackageOverride(t *testing.T) {
	path := writeTOML(t, `
[[node.package]]
name = "typescript"
manager = "bun"
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "node.package") {
		t.Fatalf("expected node.package error, got %v", err)
	}
}

func TestLoad_RejectsNamelessNodePackageOverride(t *testing.T) {
	path := writeTOML(t, `
[[node.package]]
manager = "yarn"
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("expected name-required error, got %v", err)
	}
}

func TestLoad_RejectsUnknownTomlKeys(t *testing.T) {
	path := writeTOML(t, `
[settings]
totally_unknown_field = true
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "unknown keys") {
		t.Fatalf("expected unknown-keys error, got %v", err)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	// Use a clean isolated env so the search path doesn't accidentally find
	// /etc/bigkis or ~/.config/bigkis.
	t.Setenv("BIGKIS_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.toml"))
	if err == nil {
		t.Fatal("expected error for missing config")
	}
}

func TestConfig_IsEnabled(t *testing.T) {
	c := &Config{Settings: Settings{Enabled: []string{"pacman", "node"}}}
	if !c.IsEnabled("pacman") {
		t.Error("pacman should be enabled")
	}
	if c.IsEnabled("flatpak") {
		t.Error("flatpak should not be enabled")
	}
}
