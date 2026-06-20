package lockfile

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/0xGurg/bigkis/internal/config"
)

func stubCommands(t *testing.T, available map[string]bool, outputs map[string]string) {
	t.Helper()
	prevLookPath := lookPath
	prevCommandOutput := commandOutput
	lookPath = func(name string) (string, error) {
		if available[name] {
			return "/usr/bin/" + name, nil
		}
		return "", fmt.Errorf("%s not found", name)
	}
	commandOutput = func(name string, args ...string) ([]byte, error) {
		key := name + " " + strings.Join(args, " ")
		out, ok := outputs[key]
		if !ok {
			return nil, fmt.Errorf("unexpected command: %s", key)
		}
		return []byte(out), nil
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
		commandOutput = prevCommandOutput
	})
}

func TestDefaultPath_NextToConfig(t *testing.T) {
	got := DefaultPath("/etc/bigkis/system.toml")
	want := "/etc/bigkis/bigkis.lock"
	if got != want {
		t.Errorf("DefaultPath = %q, want %q", got, want)
	}
}

func TestDefaultPath_EmptyConfigPath(t *testing.T) {
	got := DefaultPath("")
	if got != "bigkis.lock" {
		t.Errorf("DefaultPath = %q, want bigkis.lock", got)
	}
}

func TestQuote_BareKeysUnchanged(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"git", "git"},
		{"linux-firmware", "linux-firmware"},
		{"base_devel", "base_devel"},
		{"foo123", "foo123"},
		{"@vue/cli", `"@vue/cli"`},
		{"org.mozilla.firefox", `"org.mozilla.firefox"`},
	}
	for _, c := range cases {
		got := quote(c.in)
		if got != c.want {
			t.Errorf("quote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestWrite_EmptyConfigStillWritesHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bigkis.lock")
	// A minimal config: no plugins enabled means we only write the header.
	cfg := &config.Config{}
	if err := Write(path, cfg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if !strings.Contains(got, "schema_version = 1\n") {
		t.Fatalf("schema version missing from %q", got)
	}
	if !strings.Contains(got, "generated_at") {
		t.Fatalf("generated_at missing from %q", got)
	}
}

func TestWrite_RejectsEmptyPath(t *testing.T) {
	if err := Write("", &config.Config{}); err == nil {
		t.Fatal("Write with empty path succeeded")
	}
}

func TestParseFlatpakInfo_VersionAndCommit(t *testing.T) {
	out := `
Name: org.mozilla.firefox
Version: 124.0
Commit: deadbeefcafe
`
	commit, version := parseFlatpakInfo(out)
	if version != "124.0" {
		t.Errorf("version = %q", version)
	}
	if commit != "deadbeefcafe" {
		t.Errorf("commit = %q", commit)
	}
}

func TestParseFlatpakInfo_TrimsAndAllowsMissingFields(t *testing.T) {
	commit, version := parseFlatpakInfo(" Version:  1.2.3 \nName: x\n")
	if version != "1.2.3" {
		t.Fatalf("version = %q", version)
	}
	if commit != "" {
		t.Fatalf("commit = %q, want empty", commit)
	}
}

func TestWrite_EmitsEnabledPluginSectionsFromCommandOutput(t *testing.T) {
	stubCommands(t,
		map[string]bool{"pacman": true, "flatpak": true, "sudo": true},
		map[string]string{
			"pacman -Qi git":                                    "Name    : git\nVersion : 2.45.0-1\n",
			"pacman -Qi yay-bin":                                "Name    : yay-bin\nVersion : 12.3.5-1\n",
			"flatpak info org.mozilla.firefox":                  "Version: 124.0\nCommit: deadbeef\n",
			"sudo -u alice flatpak --user info com.example.App": "Version: 1.0\nCommit: cafe\n",
		},
	)
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "bigkis.lock")
	cfg := &config.Config{
		Settings: config.Settings{Enabled: []string{
			config.PluginPacman,
			config.PluginAUR,
			config.PluginFlatpak,
		}},
		Pacman: config.Pacman{Packages: []string{"git"}},
		AUR:    config.AUR{Packages: []string{"yay-bin"}},
		Flatpak: config.Flatpak{
			Packages: []string{"org.mozilla.firefox"},
			UserPackages: map[string][]string{
				"alice": {"com.example.App"},
			},
		},
	}

	if err := Write(path, cfg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	for _, want := range []string{
		"[pacman.git]\nversion = \"2.45.0-1\"",
		"[aur.yay-bin]\nversion = \"12.3.5-1\"",
		"[flatpak.\"org.mozilla.firefox\"]\nversion = \"124.0\"\ncommit  = \"deadbeef\"",
		"[flatpak.user.alice.\"com.example.App\"]\nversion = \"1.0\"\ncommit  = \"cafe\"",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("lockfile missing %q:\n%s", want, got)
		}
	}
}

func TestWrite_SortsPackagesAndSkipsMissingVersions(t *testing.T) {
	stubCommands(t,
		map[string]bool{"pacman": true},
		map[string]string{
			"pacman -Qi zed": "Version : 1.0\n",
			"pacman -Qi git": "Version : 2.0\n",
			"pacman -Qi bad": "Name : bad\n",
		},
	)
	dir := t.TempDir()
	path := filepath.Join(dir, "bigkis.lock")
	cfg := &config.Config{
		Settings: config.Settings{Enabled: []string{config.PluginPacman}},
		Pacman:   config.Pacman{Packages: []string{"zed", "bad", "git"}},
	}

	if err := Write(path, cfg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if strings.Contains(got, "[pacman.bad]") {
		t.Fatalf("package without version should be skipped:\n%s", got)
	}
	order := []string{"[pacman.git]", "[pacman.zed]"}
	positions := []int{strings.Index(got, order[0]), strings.Index(got, order[1])}
	if positions[0] < 0 || positions[1] < 0 || positions[0] > positions[1] {
		t.Fatalf("packages not sorted as %v:\n%s", order, got)
	}
}

func TestWrite_DisabledPluginsDoNotProbeCommands(t *testing.T) {
	calls := []string{}
	stubCommands(t,
		map[string]bool{"pacman": true, "flatpak": true},
		map[string]string{},
	)
	commandOutput = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil, fmt.Errorf("unexpected command")
	}
	dir := t.TempDir()
	cfg := &config.Config{
		Settings: config.Settings{Enabled: []string{config.PluginNode}},
		Pacman:   config.Pacman{Packages: []string{"git"}},
		Flatpak:  config.Flatpak{Packages: []string{"org.mozilla.firefox"}},
	}

	if err := Write(filepath.Join(dir, "bigkis.lock"), cfg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{}) {
		t.Fatalf("disabled plugins should not call commands, got %v", calls)
	}
}

// TestWrite_RepresentsUserPackages exercises the flatpak.user_packages path.
// flatpak isn't on the test host, so per-user info lookups will fail, but we
// still expect the section header so managed user packages aren't invisible
// in the lockfile.
func TestWrite_RepresentsUserPackages(t *testing.T) {
	if !hasCommand("flatpak") {
		t.Skip("flatpak not on PATH; the user-packages emitter early-returns")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "bigkis.lock")
	cfg := &config.Config{
		Settings: config.Settings{Enabled: []string{config.PluginFlatpak}},
		Flatpak: config.Flatpak{
			UserPackages: map[string][]string{
				"alice": {"com.example.App"},
			},
		},
	}
	if err := Write(path, cfg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "[flatpak.user.alice.") {
		t.Errorf("user section missing:\n%s", body)
	}
}
