package runner

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestIsExitCode_FakeMatchesAndDoesNotMatch(t *testing.T) {
	err := NewExitError(1, "exit status 1")
	if !IsExitCode(err, 1) {
		t.Error("RC=1 should match")
	}
	if IsExitCode(err, 2) {
		t.Error("RC=1 should not match 2")
	}
	if !IsExitCode(err, 0, 1, 2) {
		t.Error("RC=1 should match within {0,1,2}")
	}
}

func TestIsExitCode_NilAndUnrelated(t *testing.T) {
	if IsExitCode(nil, 1) {
		t.Error("nil err must not match")
	}
	if IsExitCode(errors.New("boom"), 1) {
		t.Error("plain error must not match")
	}
}

func TestIsExitCode_WrappedFakeStillMatches(t *testing.T) {
	wrapped := fmt.Errorf("probe: %w", NewExitError(7, ""))
	if !IsExitCode(wrapped, 7) {
		t.Error("wrapped fake exit error should still match via errors.As")
	}
}

func TestFakeRunner_RecordsCallsAndScripts(t *testing.T) {
	f := NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		return "stdout-here", "", 0, nil
	}
	out, err := f.Capture("pacman", "-Qqen")
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if out != "stdout-here" {
		t.Errorf("got %q", out)
	}
	if len(f.Calls) != 1 || f.Calls[0].Name != "pacman" || !reflect.DeepEqual(f.Calls[0].Args, []string{"-Qqen"}) {
		t.Errorf("calls = %+v", f.Calls)
	}
}

func TestFakeRunner_DryRunStillRecordsViaHookOnRun(t *testing.T) {
	f := NewFake()
	if _, err := f.Run(Spec{Name: "pacman", Args: []string{"-S", "git"}, Sudo: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(f.Calls) != 1 {
		t.Fatalf("expected 1 call, got %+v", f.Calls)
	}
	if !f.Calls[0].Sudo {
		t.Error("Sudo bit should be preserved")
	}
}

func TestRun_DryRunPrintsFormattedCommand(t *testing.T) {
	var out bytes.Buffer
	r := &Runner{DryRun: true, Stdout: &out}

	if _, err := r.Run(Spec{Name: "pacman", Args: []string{"-S", "git"}, Sudo: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "[dry-run] sudo -E pacman -S git") {
		t.Fatalf("dry-run output = %q", got)
	}
}

func TestRun_HookStreamsOutputAndWrapsError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r := &Runner{Stdout: &stdout, Stderr: &stderr}
	r.Hook = func(spec Spec) (string, string, int, error) {
		return "out", "err", 9, errors.New("boom")
	}

	res, err := r.Run(Spec{Name: "tool"})

	if err == nil || !strings.Contains(err.Error(), "tool: boom") {
		t.Fatalf("err = %v, want wrapped hook error", err)
	}
	if res.ExitCode != 9 || res.Stdout != "out" || res.Stderr != "err" {
		t.Fatalf("result = %+v", res)
	}
	if stdout.String() != "out" || stderr.String() != "err" {
		t.Fatalf("streamed stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestCapture_HookErrorIncludesStderr(t *testing.T) {
	r := New(false)
	r.Hook = func(spec Spec) (string, string, int, error) {
		return "partial", "bad stderr\n", 2, errors.New("failed")
	}

	out, err := r.Capture("tool", "arg")

	if out != "partial" {
		t.Fatalf("out = %q", out)
	}
	if err == nil || !strings.Contains(err.Error(), "tool arg: failed: bad stderr") {
		t.Fatalf("err = %v", err)
	}
}

func TestHasCommandUsesLookPath(t *testing.T) {
	prev := LookPath
	t.Cleanup(func() { LookPath = prev })
	LookPath = func(name string) (string, error) {
		if name == "present" {
			return "/bin/present", nil
		}
		return "", errors.New("missing")
	}

	if !HasCommand("present") {
		t.Fatal("present command not found")
	}
	if HasCommand("missing") {
		t.Fatal("missing command found")
	}
}

// TestRun_NoPanicOnExecStartFailure exercises the nil-ProcessState guard:
// when the command can't even start (missing binary), Run must surface the
// error with a synthetic exit code instead of panicking on the nil deref.
func TestRun_NoPanicOnExecStartFailure(t *testing.T) {
	r := New(false)
	res, err := r.Run(Spec{Name: "this-binary-definitely-does-not-exist-bigkis-test"})
	if err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
	if res.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1 for start failure", res.ExitCode)
	}
}
