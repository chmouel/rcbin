// Command rc is a workstation orchestrator: it synchronizes YADM and Git
// repositories, manages symlinks, runs backups and updates, and reports
// diagnostics.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/chmouel/rc/internal/app"
	"github.com/chmouel/rc/internal/runner"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run())
}

// run wires signal handling and returns the process exit code. It is separate
// from main so deferred cleanup runs before os.Exit.
func run() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Cancel the context on SIGINT/SIGTERM so in-flight work stops and child
	// process groups receive the signal. While an interactive child (lazygit,
	// Emacs, a direct commit) owns the terminal, a typed Ctrl-C is meant for
	// that child, so rc ignores SIGINT and lets the child handle it instead of
	// tearing down its own work.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		for sig := range sigCh {
			if sig == os.Interrupt && runner.InteractiveActive() {
				continue
			}
			cancel()
		}
	}()

	return app.Execute(ctx, os.Args[1:], app.Deps{
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Version: version,
	})
}
