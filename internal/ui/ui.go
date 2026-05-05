// Package ui provides terminal output helpers and confirmation prompts.
package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiBlue   = "\x1b[34m"
	ansiCyan   = "\x1b[36m"
	ansiGrey   = "\x1b[90m"
)

// UI prints status messages. Color is disabled automatically when stdout is not
// a terminal or when NO_COLOR is set.
type UI struct {
	out   io.Writer
	in    io.Reader
	color bool
	yes   bool
}

func New(out io.Writer, in io.Reader, color, assumeYes bool) *UI {
	return &UI{out: out, in: in, color: color, yes: assumeYes}
}

// Default returns a UI bound to stdout/stdin with sensible defaults.
func Default(assumeYes bool) *UI {
	return New(os.Stdout, os.Stdin, isColorTerminal(), assumeYes)
}

// SetAssumeYes toggles the auto-yes behavior at runtime so callers (e.g. the
// CLI) can apply a --yes flag without losing the UI's other settings.
func (u *UI) SetAssumeYes(yes bool) { u.yes = yes }

func isColorTerminal() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("FORCE_COLOR") != "" {
		return true
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func (u *UI) paint(code, s string) string {
	if !u.color {
		return s
	}
	return code + s + ansiReset
}

// Info prints an informational line prefixed with "::".
func (u *UI) Info(format string, args ...any) {
	fmt.Fprintf(u.out, "%s %s\n", u.paint(ansiBlue+ansiBold, "::"), fmt.Sprintf(format, args...))
}

// Step prints a sub-step indented under an Info line.
func (u *UI) Step(format string, args ...any) {
	fmt.Fprintf(u.out, "  %s %s\n", u.paint(ansiCyan, "->"), fmt.Sprintf(format, args...))
}

// Add highlights an addition in green.
func (u *UI) Add(format string, args ...any) {
	fmt.Fprintf(u.out, "  %s %s\n", u.paint(ansiGreen, "+"), fmt.Sprintf(format, args...))
}

// Remove highlights a removal in red.
func (u *UI) Remove(format string, args ...any) {
	fmt.Fprintf(u.out, "  %s %s\n", u.paint(ansiRed, "-"), fmt.Sprintf(format, args...))
}

// Warn prints a yellow warning.
func (u *UI) Warn(format string, args ...any) {
	fmt.Fprintf(u.out, "%s %s\n", u.paint(ansiYellow+ansiBold, "warning:"), fmt.Sprintf(format, args...))
}

// Errorf prints a red error.
func (u *UI) Errorf(format string, args ...any) {
	fmt.Fprintf(u.out, "%s %s\n", u.paint(ansiRed+ansiBold, "error:"), fmt.Sprintf(format, args...))
}

// Dim prints a dimmed informational message.
func (u *UI) Dim(format string, args ...any) {
	fmt.Fprintf(u.out, "%s\n", u.paint(ansiGrey, fmt.Sprintf(format, args...)))
}

// Confirm asks the user for yes/no confirmation. Returns true if user agrees.
// When assumeYes was set on construction, returns true without prompting.
func (u *UI) Confirm(prompt string) bool {
	if u.yes {
		return true
	}
	fmt.Fprintf(u.out, "%s [y/N] ", prompt)
	r := bufio.NewReader(u.in)
	line, err := r.ReadString('\n')
	if err != nil {
		return false
	}
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}
