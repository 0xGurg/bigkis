package lockfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeberg.org/gurg/bigkis/internal/config"
)

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
