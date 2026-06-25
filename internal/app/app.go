// Package app wires the cobra command tree to the internal subsystems and maps
// outcomes to process exit codes.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/chmouel/rc/internal/config"
	"github.com/chmouel/rc/internal/output"
	"github.com/chmouel/rc/internal/runner"
)

// Deps are the injectable dependencies of the command tree. Tests provide fakes;
// production uses the standard implementations.
type Deps struct {
	Runner runner.Runner
	Stdout io.Writer
	Stderr io.Writer
	// Version is reported by `rc --version`. Defaults to "dev" when empty.
	Version string
}

// globals holds the parsed global flags shared by every command.
type globals struct {
	configPath     string
	host           string
	verbose        bool
	noColor        bool
	nonInteractive bool
	dryRun         bool
}

// operationalError marks a failure that is operational or validation related
// rather than a CLI usage mistake. main maps it to exit code 1; anything else
// returned from cobra (flag and argument errors) maps to exit code 2.
type operationalError struct{ err error }

func (e *operationalError) Error() string { return e.err.Error() }
func (e *operationalError) Unwrap() error { return e.err }

// op wraps a non-nil error as operational.
func op(err error) error {
	if err == nil {
		return nil
	}
	var already *operationalError
	if errors.As(err, &already) {
		return err
	}
	return &operationalError{err}
}

// Execute runs the command tree and returns the process exit code.
func Execute(ctx context.Context, args []string, deps Deps) int {
	if deps.Runner == nil {
		deps.Runner = runner.New()
	}

	root := newRootCmd(deps)
	root.SetArgs(args)
	root.SetOut(deps.Stdout)
	root.SetErr(deps.Stderr)

	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}

	var opErr *operationalError
	if errors.As(err, &opErr) {
		fmt.Fprintf(deps.Stderr, "rc: %s\n", opErr.Error())
		return 1
	}
	// Flag parsing and argument validation errors are usage errors.
	fmt.Fprintf(deps.Stderr, "rc: %s\n", err.Error())
	return 2
}

// newReporter constructs the Reporter from the global flags. Color is enabled
// only when the user has not passed --no-color, NO_COLOR is unset, and stderr is
// a terminal, so piped or redirected output stays plain.
func newReporter(g *globals, deps Deps) *output.Reporter {
	color := output.ColorFor(deps.Stderr, g.noColor)
	return output.New(deps.Stdout, deps.Stderr, color, g.verbose)
}

// loadConfig loads the layered configuration honoring --config and --host.
func loadConfig(g *globals) (*config.Config, error) {
	return config.Load(config.Options{
		GlobalPath: g.configPath,
		Hostname:   g.host,
	})
}
