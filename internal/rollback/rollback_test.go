package rollback

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	cmd := pacmanCommand(plugin.OpAdd)([]string{"git", "neovim"}, "", "")
	if !strings.Contains(cmd, "pacman -S --needed --noconfirm") {
		t.Errorf("got %q", cmd)
	}
	cmd = pacmanCommand(plugin.OpRemove)([]string{"git"}, "", "")
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
		add := nodeCommand(plugin.OpAdd)([]string{"typescript"}, "via "+c.mgr, "")
		if !strings.Contains(add, c.mgr+" "+c.add) {
			t.Errorf("%s add = %q", c.mgr, add)
		}
		rem := nodeCommand(plugin.OpRemove)([]string{"typescript"}, "via "+c.mgr, "")
		if !strings.Contains(rem, c.mgr+" "+c.rem) {
			t.Errorf("%s remove = %q", c.mgr, rem)
		}
	}
}

func TestFlatpakCommand_UserVsSystem(t *testing.T) {
	sys := flatpakCommand(plugin.OpAdd)([]string{"org.mozilla.firefox"}, "system", "")
	if !strings.Contains(sys, "sudo flatpak install --system") {
		t.Errorf("system add = %q", sys)
	}
	usr := flatpakCommand(plugin.OpAdd)([]string{"com.valvesoftware.Steam"}, "user alice", "")
	if !strings.Contains(usr, "sudo -u alice flatpak install --user") {
		t.Errorf("user add = %q", usr)
	}
}

func TestAURCommand_UsesHelper(t *testing.T) {
	cmd := aurCommand(plugin.OpAdd)([]string{"yay"}, "", "paru")
	if !strings.HasPrefix(cmd, "paru -S") {
		t.Errorf("aur add = %q, want paru prefix", cmd)
	}
}
