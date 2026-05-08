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

// ---------- includes ----------

func writeTOMLAt(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestLoad_IncludeMergesPackagesAndScalars(t *testing.T) {
	dir := t.TempDir()
	writeTOMLAt(t, dir, "shared.toml", `
[settings]
aur_helper = "paru"

[pacman]
packages = ["base", "git"]
`)
	main := writeTOMLAt(t, dir, "system.toml", `
[settings]
include = ["shared.toml"]

[pacman]
packages = ["neovim"]
`)
	cfg, err := Load(main)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"base", "git", "neovim"}
	if got := cfg.Pacman.Packages; !sameSet(got, want) {
		t.Errorf("packages = %v, want union %v", got, want)
	}
	// Top-level should win for scalars when set, but it isn't set here, so
	// the include's value carries through.
	if cfg.Settings.AURHelper != "paru" {
		t.Errorf("aur_helper = %q, want paru (from include)", cfg.Settings.AURHelper)
	}
	if len(cfg.SourcePaths) != 2 {
		t.Errorf("SourcePaths = %v, want 2 entries", cfg.SourcePaths)
	}
}

func TestLoad_TopLevelWinsForScalars(t *testing.T) {
	dir := t.TempDir()
	writeTOMLAt(t, dir, "shared.toml", `
[settings]
aur_helper   = "yay"
node_manager = "npm"
`)
	main := writeTOMLAt(t, dir, "system.toml", `
[settings]
include      = ["shared.toml"]
aur_helper   = "paru"
node_manager = "pnpm"
`)
	cfg, err := Load(main)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Settings.AURHelper != "paru" {
		t.Errorf("aur_helper = %q, want paru (top-level wins)", cfg.Settings.AURHelper)
	}
	if cfg.Settings.NodeManager != "pnpm" {
		t.Errorf("node_manager = %q, want pnpm (top-level wins)", cfg.Settings.NodeManager)
	}
}

func TestLoad_IncludeCycleErrors(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.toml")
	b := filepath.Join(dir, "b.toml")
	if err := os.WriteFile(a, []byte(`[settings]
include = ["b.toml"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte(`[settings]
include = ["a.toml"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(a)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestLoad_IncludeRelativePathsResolveAgainstParent(t *testing.T) {
	root := t.TempDir()
	subDir := filepath.Join(root, "modules")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTOMLAt(t, subDir, "shell.toml", `[pacman]
packages = ["fish"]
`)
	main := writeTOMLAt(t, root, "system.toml", `
[settings]
include = ["modules/shell.toml"]

[pacman]
packages = ["base"]
`)
	cfg, err := Load(main)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !sameSet(cfg.Pacman.Packages, []string{"base", "fish"}) {
		t.Errorf("packages = %v, want [base fish]", cfg.Pacman.Packages)
	}
}

// ---------- host overlays ----------

func TestLoad_HostOverlayApplies(t *testing.T) {
	t.Setenv("HOSTNAME", "ignored-by-os.Hostname") // not actually used; keep test deterministic via overlay name
	hostname, _ := os.Hostname()
	if hostname == "" {
		t.Skip("no hostname available; skipping")
	}
	path := writeTOML(t, `
[pacman]
packages = ["base"]

[hosts.`+hostname+`.pacman]
packages = ["brightnessctl"]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !sameSet(cfg.Pacman.Packages, []string{"base", "brightnessctl"}) {
		t.Errorf("packages = %v, want host overlay merged", cfg.Pacman.Packages)
	}
}

// TestLoad_HostOverlayAppliesPruneOrphans guards against the regression where
// applyHostOverlay merged aur_helper / node_manager but silently dropped
// settings.prune_orphans.
func TestLoad_HostOverlayAppliesPruneOrphans(t *testing.T) {
	hostname, _ := os.Hostname()
	if hostname == "" {
		t.Skip("no hostname available; skipping")
	}
	path := writeTOML(t, `
[settings]
prune_orphans = "scoped"

[hosts.`+hostname+`.settings]
prune_orphans = "none"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Settings.PruneOrphans != PruneOrphansNone {
		t.Errorf("prune_orphans = %q, want host overlay 'none'", cfg.Settings.PruneOrphans)
	}
}

// TestResolvePath_ExplicitMissingErrors guards against fallthrough to the
// default search path when a missing --config is supplied.
func TestResolvePath_ExplicitMissingErrors(t *testing.T) {
	t.Setenv("BIGKIS_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	missing := filepath.Join(t.TempDir(), "definitely-missing.toml")
	_, err := Load(missing)
	if err == nil {
		t.Fatal("expected error when explicit --config is missing")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error should mention the missing path; got %v", err)
	}
}

func TestLoad_HostOverlayIgnoredOnOtherHosts(t *testing.T) {
	path := writeTOML(t, `
[pacman]
packages = ["base"]

[hosts.never-this-hostname-xyz.pacman]
packages = ["should-not-appear"]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, p := range cfg.Pacman.Packages {
		if p == "should-not-appear" {
			t.Fatalf("host overlay leaked into non-matching host: %v", cfg.Pacman.Packages)
		}
	}
}

// ---------- groups ----------

func TestLoad_GroupsExpandIntoPackages(t *testing.T) {
	path := writeTOML(t, `
[groups]
dev_cli = ["git", "neovim"]
desktop = ["firefox"]

[pacman]
groups   = ["dev_cli", "desktop"]
packages = ["base"]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"base", "git", "neovim", "firefox"}
	if !sameSet(cfg.Pacman.Packages, want) {
		t.Errorf("packages = %v, want union with groups", cfg.Pacman.Packages)
	}
	if len(cfg.Pacman.Groups) != 0 {
		t.Errorf("Groups should be cleared after expansion: %v", cfg.Pacman.Groups)
	}
}

func TestLoad_UndefinedGroupErrors(t *testing.T) {
	path := writeTOML(t, `
[pacman]
groups = ["never_defined"]
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "undefined group") {
		t.Fatalf("expected undefined-group error, got %v", err)
	}
}

// ---------- helpers ----------

func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	g := map[string]bool{}
	for _, x := range got {
		g[x] = true
	}
	for _, x := range want {
		if !g[x] {
			return false
		}
	}
	return true
}
