package tui

import (
	"os"
	"testing"
)

// Since ShouldUse requires real TTYs for both stdout and stdin, we cannot
// easily test the TTY check in isolation without a pty. Instead we verify
// the logical conditions (json, quiet, env vars) and the nil-file behavior
// of isTerminal. The full TTY path is exercised by integration tests that
// pipe / redirect output.

func TestShouldUse_jsonFlag(t *testing.T) {
	t.Parallel()
	if got := ShouldUse(os.Stdout, os.Stdin, true, false); got {
		t.Error("ShouldUse = true with --json; want false")
	}
}

func TestShouldUse_quietFlag(t *testing.T) {
	t.Parallel()
	if got := ShouldUse(os.Stdout, os.Stdin, false, true); got {
		t.Error("ShouldUse = true with --quiet; want false")
	}
}

func TestShouldUse_noColorEnv(t *testing.T) {
	// Note: No t.Parallel() — t.Setenv requires sequential execution.
	t.Setenv("NO_COLOR", "1")
	if got := ShouldUse(os.Stdout, os.Stdin, false, false); got {
		t.Error("ShouldUse = true with NO_COLOR=1; want false")
	}
}

func TestShouldUse_bigkisNoTuiEnv(t *testing.T) {
	// Note: No t.Parallel() — t.Setenv requires sequential execution.
	t.Setenv("BIGKIS_NO_TUI", "1")
	if got := ShouldUse(os.Stdout, os.Stdin, false, false); got {
		t.Error("ShouldUse = true with BIGKIS_NO_TUI=1; want false")
	}
}

// isTerminal tests

func TestIsTerminal_nilFile(t *testing.T) {
	t.Parallel()
	if isTerminal(nil) {
		t.Error("isTerminal(nil) = true; want false")
	}
}

func TestIsTerminal_diskFile(t *testing.T) {
	t.Parallel()
	f, err := os.CreateTemp("", "tui-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	if isTerminal(f) {
		t.Error("isTerminal(disk file) = true; want false")
	}
}

func TestShouldUse_PipeSuppressesTUI(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	if v := ShouldUse(w, os.Stdin, false, false); v {
		t.Error("ShouldUse should return false when stdout is a pipe")
	}
}

func TestIsTerminal_devNull(t *testing.T) {
	t.Parallel()
	f, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if isTerminal(f) {
		t.Error("isTerminal(/dev/null) = true; want false")
	}
}
