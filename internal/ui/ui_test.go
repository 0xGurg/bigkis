package ui

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestOutputMethodsPlain(t *testing.T) {
	var out bytes.Buffer
	u := New(&out, strings.NewReader(""), false, false)

	u.Info("hello %s", "world")
	u.Step("doing %d", 1)
	u.Add("install %s", "pkg")
	u.Remove("remove %s", "old")
	u.Warn("careful")
	u.Errorf("broken")
	u.Dim("quiet detail")

	want := strings.Join([]string{
		":: hello world",
		"  -> doing 1",
		"  + install pkg",
		"  - remove old",
		"warning: careful",
		"error: broken",
		"quiet detail",
		"",
	}, "\n")
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestQuietSuppressesNonEssentialOutput(t *testing.T) {
	var out bytes.Buffer
	u := New(&out, strings.NewReader(""), false, false)
	u.SetQuiet(true)

	u.Info("info")
	u.Step("step")
	u.Add("add")
	u.Remove("remove")
	u.Dim("dim")
	u.Warn("warn")
	u.Errorf("error")

	got := out.String()
	if strings.Contains(got, "info") || strings.Contains(got, "step") ||
		strings.Contains(got, "add") || strings.Contains(got, "remove") ||
		strings.Contains(got, "dim") {
		t.Fatalf("quiet output included suppressed line: %q", got)
	}
	if !strings.Contains(got, "warning: warn") || !strings.Contains(got, "error: error") {
		t.Fatalf("quiet output missed warnings/errors: %q", got)
	}
}

func TestColorWrapsMarkers(t *testing.T) {
	var out bytes.Buffer
	u := New(&out, strings.NewReader(""), true, false)

	u.Add("pkg")

	got := out.String()
	if !strings.Contains(got, ansiGreen+"+"+ansiReset) {
		t.Fatalf("colored add marker missing from %q", got)
	}
}

func TestConfirm(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "yes short", in: "y\n", want: true},
		{name: "yes word", in: "YES\n", want: true},
		{name: "no default", in: "\n", want: false},
		{name: "other text", in: "sure\n", want: false},
		{name: "eof", in: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			u := New(&out, strings.NewReader(tt.in), false, false)

			got := u.Confirm("continue?")

			if got != tt.want {
				t.Fatalf("Confirm = %v, want %v", got, tt.want)
			}
			if !strings.Contains(out.String(), "continue? [y/N] ") {
				t.Fatalf("prompt missing from %q", out.String())
			}
		})
	}
}

func TestConfirmAssumeYesDoesNotPrompt(t *testing.T) {
	var out bytes.Buffer
	u := New(&out, strings.NewReader(""), false, true)

	if !u.Confirm("continue?") {
		t.Fatal("Confirm with assume yes = false, want true")
	}
	if out.Len() != 0 {
		t.Fatalf("assume yes should not prompt, got %q", out.String())
	}
}

func TestSetAssumeYes(t *testing.T) {
	var out bytes.Buffer
	u := New(&out, strings.NewReader(""), false, false)

	u.SetAssumeYes(true)

	if !u.Confirm("continue?") {
		t.Fatal("Confirm after SetAssumeYes = false, want true")
	}
}

func TestDefaultReturnsUsableUI(t *testing.T) {
	if Default(true) == nil {
		t.Fatal("Default returned nil")
	}
}

func TestIsColorTTYEnvironmentOverrides(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("FORCE_COLOR", "")
	if IsColorTTY(os.Stdout) {
		t.Fatal("NO_COLOR should disable color")
	}

	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "1")
	if !IsColorTTY(nil) {
		t.Fatal("FORCE_COLOR should enable color even without a file")
	}

	t.Setenv("FORCE_COLOR", "")
	if IsColorTTY(nil) {
		t.Fatal("nil file should not be treated as color tty")
	}
}
