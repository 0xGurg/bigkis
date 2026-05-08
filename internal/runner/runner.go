// Package runner executes external commands with optional dry-run, sudo
// elevation, and per-user privilege drop.
package runner

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"
)

// Hook is an optional command-execution hook used by tests. When a Runner
// has Hook set, Run and Capture call this instead of executing real OS
// processes. Tests can use it to assert on the argv produced by plugins and
// to script command output and exit codes.
type Hook func(spec Spec) (stdout, stderr string, exitCode int, err error)

// Runner executes external commands.
type Runner struct {
	DryRun bool
	Stdout io.Writer
	Stderr io.Writer
	Hook   Hook
}

func New(dryRun bool) *Runner {
	return &Runner{DryRun: dryRun, Stdout: os.Stdout, Stderr: os.Stderr}
}

// Spec describes one command invocation.
type Spec struct {
	Name string
	Args []string
	// User runs the command as this user (drops privileges via setuid/setgid).
	// Empty means "current user".
	User string
	// Sudo prepends `sudo` (with -E to preserve env). Mutually exclusive with User.
	Sudo bool
	// Env overrides specific environment variables.
	Env map[string]string
	// Stdin is optional input piped to the command.
	Stdin io.Reader
	// CaptureOutput, when true, also returns combined output to the caller in
	// addition to streaming it.
	CaptureOutput bool
}

// Result holds the outcome of a Run.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Run executes the spec.
func (r *Runner) Run(spec Spec) (Result, error) {
	if r.DryRun {
		fmt.Fprintf(r.Stdout, "    [dry-run] %s\n", formatCmd(spec))
		return Result{}, nil
	}

	if r.Hook != nil {
		out, errOut, code, err := r.Hook(spec)
		if r.Stdout != nil && out != "" {
			fmt.Fprint(r.Stdout, out)
		}
		if r.Stderr != nil && errOut != "" {
			fmt.Fprint(r.Stderr, errOut)
		}
		res := Result{ExitCode: code, Stdout: out, Stderr: errOut}
		if err != nil {
			return res, fmt.Errorf("%s: %w", spec.Name, err)
		}
		return res, nil
	}

	name := spec.Name
	args := spec.Args
	if spec.Sudo {
		name = "sudo"
		args = append([]string{"-E", spec.Name}, spec.Args...)
	}

	cmd := exec.Command(name, args...)

	env := os.Environ()
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	if spec.User != "" {
		u, err := user.Lookup(spec.User)
		if err != nil {
			return Result{}, fmt.Errorf("lookup user %q: %w", spec.User, err)
		}
		uid, err := strconv.ParseUint(u.Uid, 10, 32)
		if err != nil {
			return Result{}, fmt.Errorf("parse uid: %w", err)
		}
		gid, err := strconv.ParseUint(u.Gid, 10, 32)
		if err != nil {
			return Result{}, fmt.Errorf("parse gid: %w", err)
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)},
		}
		cmd.Env = append(cmd.Env, "HOME="+u.HomeDir, "USER="+u.Username, "LOGNAME="+u.Username)
	}

	cmd.Stdin = spec.Stdin

	var outBuf, errBuf bytes.Buffer
	if spec.CaptureOutput {
		cmd.Stdout = io.MultiWriter(r.Stdout, &outBuf)
		cmd.Stderr = io.MultiWriter(r.Stderr, &errBuf)
	} else {
		cmd.Stdout = r.Stdout
		cmd.Stderr = r.Stderr
	}

	err := cmd.Run()
	// cmd.ProcessState is nil when the command failed before exec (missing
	// binary, exec permission denied, fork error). Guard the nil deref so we
	// surface the underlying error instead of panicking.
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	res := Result{
		ExitCode: exitCode,
		Stdout:   outBuf.String(),
		Stderr:   errBuf.String(),
	}
	if err != nil {
		return res, fmt.Errorf("%s: %w", spec.Name, err)
	}
	return res, nil
}

// Capture is a convenience that runs the command and returns its stdout. It
// runs the command even in dry-run mode (queries are read-only). On a
// non-zero exit it returns the captured stdout *and* the wrapped error so
// callers that know the tool exits non-zero on legitimate empty results
// (pacman -Qdtq, pacman -Qqm) can still use stdout if they want.
func (r *Runner) Capture(name string, args ...string) (string, error) {
	if r.Hook != nil {
		spec := Spec{Name: name, Args: append([]string(nil), args...)}
		out, errOut, _, err := r.Hook(spec)
		if err != nil {
			return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(errOut))
		}
		return out, nil
	}
	cmd := exec.Command(name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// LookPath is the function HasCommand uses to test binary availability.
// Tests can replace it (and restore it afterwards) to bypass the real
// filesystem when checking command presence.
var LookPath = exec.LookPath

// HasCommand returns true if the given binary is on PATH.
func HasCommand(name string) bool {
	_, err := LookPath(name)
	return err == nil
}

// IsExitCode reports whether err is (or wraps) a command exit error whose
// exit code matches any of codes. Used by callers that treat specific
// non-zero exits as expected (e.g. "no orphans" / "no foreign packages").
func IsExitCode(err error, codes ...int) bool {
	if err == nil {
		return false
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		got := exitErr.ExitCode()
		for _, c := range codes {
			if got == c {
				return true
			}
		}
		return false
	}
	// Hook-based errors (tests) bubble up as fakeExitError; honor that too.
	var fake *fakeExitError
	if errors.As(err, &fake) {
		got := fake.code
		for _, c := range codes {
			if got == c {
				return true
			}
		}
	}
	return false
}

// fakeExitError lets tests using Hook return an error that IsExitCode can
// recognize. Construct via NewExitError.
type fakeExitError struct {
	code int
	msg  string
}

func (e *fakeExitError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return fmt.Sprintf("exit status %d", e.code)
}

// NewExitError builds an error that IsExitCode recognizes as having the
// given exit code. Useful for hooked / fake runners in tests.
func NewExitError(code int, msg string) error {
	return &fakeExitError{code: code, msg: msg}
}

func formatCmd(s Spec) string {
	parts := []string{}
	if s.Sudo {
		parts = append(parts, "sudo", "-E")
	}
	if s.User != "" {
		parts = append(parts, "(as "+s.User+")")
	}
	parts = append(parts, s.Name)
	parts = append(parts, s.Args...)
	return strings.Join(parts, " ")
}
