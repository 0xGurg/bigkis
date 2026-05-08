package rollback

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codeberg.org/gurg/bigkis/internal/config"
	"codeberg.org/gurg/bigkis/internal/plugin"
)

func TestOpsForReport_InvertsOperations(t *testing.T) {
	cfg := &config.Config{Settings: config.Settings{AURHelper: "yay"}}
	r := plugin.Report{
		Plugin: "pacman",
		Operations: []plugin.Operation{
			{Kind: plugin.OpAdd, Target: "neovim"},
			{Kind: plugin.OpRemove, Target: "vim"},
		},
	}
	ops := OpsForReport("pacman", cfg, r)
	if len(ops) != 2 {
		t.Fatalf("len(ops) = %d, want 2", len(ops))
	}
	// Add becomes Remove; Remove becomes Add (rollback inverts).
	if ops[0].Kind != plugin.OpRemove || ops[0].Target != "neovim" {
		t.Errorf("op[0] = %+v, want Remove neovim", ops[0])
	}
	if ops[1].Kind != plugin.OpAdd || ops[1].Target != "vim" {
		t.Errorf("op[1] = %+v, want Add vim", ops[1])
	}
}

func TestWrite_GeneratesPacmanCommands(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp)

	ops := []Op{
		{Plugin: "pacman", Kind: plugin.OpRemove, Target: "neovim"},
		{Plugin: "pacman", Kind: plugin.OpAdd, Target: "vim"},
	}
	path, err := Write(ops)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty rollback path")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if !strings.Contains(got, "pacman -S --needed --noconfirm") {
		t.Errorf("missing reinstall line:\n%s", got)
	}
	if !strings.Contains(got, "pacman -Rns --noconfirm") {
		t.Errorf("missing removal line:\n%s", got)
	}
	if !strings.Contains(got, "vim") || !strings.Contains(got, "neovim") {
		t.Errorf("missing package targets:\n%s", got)
	}
	if !strings.HasPrefix(got, "#!/bin/sh") {
		t.Errorf("missing shebang:\n%s", got)
	}
}

func TestWrite_EmptyOpsIsNoop(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp)

	path, err := Write(nil)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if path != "" {
		t.Errorf("expected empty path for empty ops, got %q", path)
	}
}

func TestWrite_PrunesOldScripts(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp)

	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-seed many old scripts.
	for i := 0; i < MaxRetained+3; i++ {
		name := filepath.Join(dir, "rollback-2020010"+string(rune('1'+i))+"T000000Z.sh")
		if err := os.WriteFile(name, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Write triggers pruning.
	if _, err := Write([]Op{{Plugin: "pacman", Kind: plugin.OpRemove, Target: "x"}}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	scripts, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) > MaxRetained {
		t.Errorf("retained %d scripts, want <= %d", len(scripts), MaxRetained)
	}
}

func TestList_MissingDirReturnsEmpty(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp)

	scripts, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(scripts) != 0 {
		t.Errorf("expected no scripts, got %v", scripts)
	}
}

func TestPacmanCommand(t *testing.T) {
	cmd := pacmanCommand(plugin.OpAdd)(emitArgs{targets: []string{"git", "neovim"}})
	if !strings.Contains(cmd, "pacman -S --needed --noconfirm") {
		t.Errorf("got %q", cmd)
	}
	cmd = pacmanCommand(plugin.OpRemove)(emitArgs{targets: []string{"git"}})
	if !strings.Contains(cmd, "pacman -Rns --noconfirm") {
		t.Errorf("got %q", cmd)
	}
}

func TestNodeCommand_PerManager(t *testing.T) {
	cases := []struct {
		mgr string
		add string
		rem string
	}{
		{"npm", "install -g", "uninstall -g"},
		{"pnpm", "add -g", "remove -g"},
		{"yarn", "global add", "global remove"},
	}
	for _, c := range cases {
		add := nodeCommand(plugin.OpAdd)(emitArgs{
			targets: []string{"typescript"},
			detail:  "via " + c.mgr,
		})
		if !strings.Contains(add, c.mgr+" "+c.add) {
			t.Errorf("%s add = %q", c.mgr, add)
		}
		rem := nodeCommand(plugin.OpRemove)(emitArgs{
			targets: []string{"typescript"},
			detail:  "via " + c.mgr,
		})
		if !strings.Contains(rem, c.mgr+" "+c.rem) {
			t.Errorf("%s remove = %q", c.mgr, rem)
		}
	}
}

func TestFlatpakCommand_UserVsSystem(t *testing.T) {
	sys := flatpakCommand(plugin.OpAdd)(emitArgs{
		targets: []string{"org.mozilla.firefox"},
		detail:  "system",
	})
	if !strings.Contains(sys, "sudo flatpak install --system") {
		t.Errorf("system add = %q", sys)
	}
	usr := flatpakCommand(plugin.OpAdd)(emitArgs{
		targets: []string{"com.valvesoftware.Steam"},
		detail:  "user alice",
	})
	if !strings.Contains(usr, "sudo -u alice flatpak install --user") {
		t.Errorf("user add = %q", usr)
	}
}

// TestFlatpakCommand_HonorsCustomRemote guards the regression where the
// rollback script always re-installed from "flathub" even when the apply ran
// against a different remote.
func TestFlatpakCommand_HonorsCustomRemote(t *testing.T) {
	cmd := flatpakCommand(plugin.OpAdd)(emitArgs{
		targets: []string{"org.mozilla.firefox"},
		detail:  "system",
		remote:  "fedora",
	})
	if !strings.Contains(cmd, " fedora org.mozilla.firefox") {
		t.Errorf("custom remote not honored: %q", cmd)
	}
	if strings.Contains(cmd, "flathub") {
		t.Errorf("rollback should not reference flathub when remote=fedora: %q", cmd)
	}
}

// TestFlatpakCommand_DefaultsToFlathub checks the unset-remote fallback.
func TestFlatpakCommand_DefaultsToFlathub(t *testing.T) {
	cmd := flatpakCommand(plugin.OpAdd)(emitArgs{
		targets: []string{"org.mozilla.firefox"},
		detail:  "system",
	})
	if !strings.Contains(cmd, " flathub org.mozilla.firefox") {
		t.Errorf("expected flathub default: %q", cmd)
	}
}

func TestAURCommand_UsesHelper(t *testing.T) {
	cmd := aurCommand(plugin.OpAdd)(emitArgs{
		targets: []string{"yay"},
		helper:  "paru",
	})
	if !strings.HasPrefix(cmd, "paru -S") {
		t.Errorf("aur add = %q, want paru prefix", cmd)
	}
}

// TestShellQuote_RoundTripsTrickyTargets makes sure POSIX single-quote
// escaping covers the awkward cases that used to ride through %q.
func TestShellQuote_RoundTripsTrickyTargets(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"git", "git"},
		{"@vue/cli", "@vue/cli"},
		{"name with space", "'name with space'"},
		{"weird;rm -rf /", `'weird;rm -rf /'`},
		{"with'quote", `'with'\''quote'`},
		{"", "''"},
	}
	for _, c := range cases {
		if got := shellQuote(c.in); got != c.want {
			t.Errorf("shellQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestNewID_Unique guards against same-second collisions on rapid applies.
// The format includes nanoseconds so two consecutive calls produce distinct
// IDs as long as the clock advances at all between them.
func TestNewID_Unique(t *testing.T) {
	t1 := time.Date(2026, 5, 7, 12, 0, 0, 1, time.UTC)
	t2 := time.Date(2026, 5, 7, 12, 0, 0, 2, time.UTC)
	if newID(t1) == newID(t2) {
		t.Errorf("expected distinct IDs for 1ns-apart times")
	}
}
