// Package runner executes external commands with optional dry-run, sudo
// elevation, and per-user privilege drop.
package runner

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"
)

// Runner executes external commands.
type Runner struct {
	DryRun bool
	Stdout io.Writer
	Stderr io.Writer
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
	res := Result{
		ExitCode: cmd.ProcessState.ExitCode(),
		Stdout:   outBuf.String(),
		Stderr:   errBuf.String(),
	}
	if err != nil {
		return res, fmt.Errorf("%s: %w", spec.Name, err)
	}
	return res, nil
}

// Capture is a convenience that runs the command and returns its stdout. It
// runs the command even in dry-run mode (queries are read-only).
func (r *Runner) Capture(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// HasCommand returns true if the given binary is on PATH.
func HasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
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
