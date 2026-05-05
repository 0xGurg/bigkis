package lockfile

import (
	"path/filepath"
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
