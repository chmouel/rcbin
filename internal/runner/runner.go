// Package runner executes external commands with explicit control over the
// working directory, environment, and stdio handling. Git and YADM remain
// external processes so the tool respects existing SSH agents, credential
// helpers, hooks, signing, aliases, and user configuration.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
)

// Spec describes a single external command invocation.
type Spec struct {
	// Name is the executable to run. It is resolved through PATH.
	Name string
	// Args are the arguments passed to the executable.
	Args []string
	// Dir is the working directory. Empty means the current directory.
	Dir string
	// Env, when non-nil, replaces the process environment entirely. When nil
	// the parent environment is inherited.
	Env []string
	// Extra appends KEY=VALUE entries on top of the inherited environment.
	Extra []string
	// Stdin, when set, is fed to the process standard input.
	Stdin string
	// Interactive connects the child to the parent terminal (stdin, stdout,
	// stderr). Captured output is unavailable in this mode.
	Interactive bool
}

// Result holds the outcome of a captured command.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Runner executes commands. Production code uses Exec; tests use a fake.
type Runner interface {
	// Run executes the command and captures stdout/stderr unless the spec is
	// interactive. A non-zero exit yields a non-nil error alongside the
	// populated Result.
	Run(ctx context.Context, spec Spec) (Result, error)
	// LookPath reports whether an executable can be found in PATH.
	LookPath(name string) (string, bool)
}

// Exec is the production Runner backed by os/exec.
type Exec struct{}

// New returns the production runner.
func New() *Exec { return &Exec{} }

// interactiveDepth counts interactive children that currently own the
// controlling terminal.
var interactiveDepth atomic.Int32

// InteractiveActive reports whether an interactive child currently owns the
// terminal. The top-level signal handler consults this so keyboard signals
// (Ctrl-C) are left to the foreground child instead of cancelling rc's work.
func InteractiveActive() bool { return interactiveDepth.Load() > 0 }

// LookPath resolves an executable through PATH.
func (e *Exec) LookPath(name string) (string, bool) {
	p, err := exec.LookPath(name)
	if err != nil {
		return "", false
	}
	return p, true
}

// Run executes the command described by spec.
func (e *Exec) Run(ctx context.Context, spec Spec) (Result, error) {
	env := spec.Env
	if env == nil {
		env = os.Environ()
	}
	if len(spec.Extra) > 0 {
		env = append(append([]string{}, env...), spec.Extra...)
	}

	if spec.Interactive {
		return e.runInteractive(spec, env)
	}

	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = env

	// Place captured children in their own process group so cancellation can
	// signal the whole group, matching the SIGINT/SIGTERM propagation
	// requirement.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative PID targets the process group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}

	var outBuf, errBuf bytes.Buffer
	if spec.Stdin != "" {
		cmd.Stdin = strings.NewReader(spec.Stdin)
	}
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	res := Result{
		Stdout: outBuf.String(),
		Stderr: errBuf.String(),
	}

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
		}
		return res, &Error{Spec: spec, Result: res, Err: err}
	}
	return res, nil
}

// runInteractive runs a child that owns the controlling terminal (lazygit,
// Emacs, a direct git commit, ...). The child is deliberately left in rc's
// process group — the terminal's foreground group — so it can read and write
// the TTY and receives keyboard signals such as Ctrl-C directly. It is not
// bound to the context, so a Ctrl-C the user typed into the child never tears
// the child down from underneath them. While it runs, InteractiveActive reports
// true so the top-level signal handler defers terminal signals to the child.
func (e *Exec) runInteractive(spec Spec, env []string) (Result, error) {
	interactiveDepth.Add(1)
	defer interactiveDepth.Add(-1)

	cmd := exec.Command(spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		res := Result{}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
		}
		return res, &Error{Spec: spec, Result: res, Err: err}
	}
	return Result{}, nil
}

// Error wraps a command failure with the subsystem-relevant context.
type Error struct {
	Spec   Spec
	Result Result
	Err    error
}

func (e *Error) Error() string {
	cmd := e.Spec.Name
	if len(e.Spec.Args) > 0 {
		cmd += " " + strings.Join(e.Spec.Args, " ")
	}
	msg := fmt.Sprintf("command failed: %s (exit %d)", cmd, e.Result.ExitCode)
	if s := strings.TrimSpace(e.Result.Stderr); s != "" {
		msg += ": " + s
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }
