// Package commitui provides interactive adapters for repositories and YADM that
// have local changes: lazygit, Emacs/Magit, aicommit, a direct signed commit,
// or skip. In non-interactive mode no UI is launched.
package commitui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chmouel/rc/internal/config"
	"github.com/chmouel/rc/internal/output"
	"github.com/chmouel/rc/internal/repo"
	"github.com/chmouel/rc/internal/runner"
)

type changeDisplayMode int

const (
	changeDisplayCompact changeDisplayMode = iota
	changeDisplayFullDiff
)

func (m changeDisplayMode) toggle() changeDisplayMode {
	if m == changeDisplayFullDiff {
		return changeDisplayCompact
	}
	return changeDisplayFullDiff
}

// Adapter implements repo.DirtyHandler and yadm.Interactive.
type Adapter struct {
	R        runner.Runner
	Rep      *output.Reporter
	Tools    config.ToolsConfig
	Pr       Prompter
	Selector fileSelector
}

var _ repo.DirtyHandler = (*Adapter)(nil)

// Handle processes a dirty repository interactively.
func (a *Adapter) Handle(ctx context.Context, target config.RepoTarget, name string) (bool, error) {
	dir := target.Path
	before := repo.Head(ctx, a.R, dir)

	a.Rep.Rule(name)

	// Loop the action menu so [b]ack from the continue prompt re-displays it.
	displayMode := changeDisplayCompact
menu:
	for {
		if err := a.showChanges(ctx, dir, displayMode); err != nil {
			return false, err
		}

		choice, err := a.Pr.Choice(a.menuPrompt(name), 'l')
		if err != nil {
			return false, err
		}

		switch choice {
		case 'q':
			return false, repo.ErrAbort
		case 's':
			a.Rep.Infof("skipping %s", name)
			return false, nil
		case 'd':
			displayMode = displayMode.toggle()
			continue menu
		case 'm':
			if err := a.emacs(ctx, dir, "magit-status"); err != nil {
				return false, err
			}
		case 'a':
			done, err := a.aiCommit(ctx, dir, name)
			if err != nil {
				return repo.Head(ctx, a.R, dir) != before, err
			}
			if !done {
				continue menu
			}
		case 'c':
			if err := a.runInteractive(ctx, dir, "git", "commit", "-s", "-a"); err != nil {
				return false, err
			}
		default: // 'l' and anything else
			if err := a.runInteractive(ctx, dir, a.lazygit()); err != nil {
				return false, err
			}
		}

		// Confirm before syncing, mirroring the original rc which prompts
		// "Would you like to continue?" after the commit tool and quits on "n".
		// [b]ack returns to the action menu for another pass.
		cont, err := a.Pr.Choice(a.continuePrompt(), 'y')
		if err != nil {
			return false, err
		}
		switch cont {
		case 'n', 'q':
			return repo.Head(ctx, a.R, dir) != before, repo.ErrAbort
		case 'b':
			continue menu
		}
		break menu
	}
	// quit lazygit without committing everything), a rebase pull cannot run, so
	// the pull is best-effort there and only warns instead of aborting the run,
	// mirroring the legacy `git pull -q || true`. A pull failure on a clean tree
	// is a genuine error and is reported.
	changed := func() bool { return repo.Head(ctx, a.R, dir) != before }

	if _, err := a.R.Run(ctx, runner.Spec{Name: "git", Args: []string{"-C", dir, "pull", "--quiet"}, Dir: dir}); err != nil {
		if !repo.HasChanges(ctx, a.R, dir) {
			return changed(), fmt.Errorf("pull failed: %w", err)
		}
		a.Rep.Warnf("%s: uncommitted changes remain, skipping pull", name)
	}
	if _, err := a.R.Run(ctx, runner.Spec{Name: "git", Args: []string{"-C", dir, "push"}, Dir: dir}); err != nil {
		return changed(), fmt.Errorf("push failed: %w", err)
	}

	return changed(), nil
}

// YadmDirty launches lazygit against the YADM state repository.
func (a *Adapter) YadmDirty(ctx context.Context, stateDir string) error {
	home, _ := os.UserHomeDir()
	gitDir := filepath.Join(stateDir, "repo.git")
	return a.runInteractive(ctx, "", a.lazygit(), "-w", home, "-g", gitDir)
}

func (a *Adapter) aiCommit(ctx context.Context, dir, name string) (bool, error) {
	files, err := a.changedFiles(ctx, dir)
	if err != nil {
		return false, err
	}
	if len(files) <= 1 {
		return true, a.runInteractive(ctx, dir, a.aicommit(), "-a")
	}

	selector := a.fileSelector()
	if selector == nil {
		return true, a.runInteractive(ctx, dir, a.aicommit(), "-a")
	}

	selected, ok, err := selector.SelectFiles(name+" AI commit files", files, a.Rep)
	if err != nil {
		return false, err
	}
	if !ok || len(selected) == 0 {
		return false, nil
	}

	for _, file := range selected {
		if err := a.runInteractive(ctx, dir, a.aicommit(), file.Path); err != nil {
			return true, err
		}
	}
	return true, nil
}

func (a *Adapter) fileSelector() fileSelector {
	if a.Selector != nil {
		return a.Selector
	}
	if selector, ok := a.Pr.(fileSelector); ok {
		return selector
	}
	return nil
}

// menuPrompt renders the action menu with the hotkey letters highlighted and
// the surrounding labels dimmed. The default action (lazygit) is accented.
func (a *Adapter) menuPrompt(name string) string {
	item := func(key, label string) string {
		return a.Rep.Key("["+key+"]") + a.Rep.Dim(label)
	}
	entries := strings.Join([]string{
		item("m", "agit"),
		a.Rep.Accent("[l]azygit"),
		item("d", "iff"),
		item("s", "kip"),
		item("a", "i"),
		item("c", "ommit"),
		item("q", "uit"),
	}, " ")
	return fmt.Sprintf("%s %s  %s ", a.Rep.Arrow(), a.Rep.Bold(name), entries)
}

// continuePrompt renders the post-action confirmation with highlighted keys.
func (a *Adapter) continuePrompt() string {
	entries := strings.Join([]string{
		a.Rep.Accent("[Y]es"),
		a.Rep.Key("[n]") + a.Rep.Dim("o"),
		a.Rep.Key("[b]") + a.Rep.Dim("ack"),
	}, " ")
	return fmt.Sprintf("%s %s %s ", a.Rep.Arrow(), a.Rep.Dim("continue?"), entries)
}

// emacs opens fn (e.g. magit-status) for dir. With a reachable Emacs server it
// creates a frame on the current terminal (emacsclient -t) so the UI appears
// where rc runs rather than in a pre-existing GUI frame; otherwise it starts a
// terminal Emacs. default-directory is bound to dir so magit acts on this repo.
func (a *Adapter) emacs(ctx context.Context, dir, fn string) error {
	dd := dir
	if dd != "" && !strings.HasSuffix(dd, "/") {
		dd += "/"
	}
	expr := fmt.Sprintf("(let ((default-directory %s)) (%s))", strconv.Quote(dd), fn)

	if _, ok := a.R.LookPath("emacsclient"); ok {
		if _, err := a.R.Run(ctx, runner.Spec{Name: "pgrep", Args: []string{"-i", "emacs"}}); err == nil {
			return a.runInteractive(ctx, dir, "emacsclient", "-t", "-e", expr)
		}
	}
	return a.runInteractive(ctx, dir, "emacs", "-nw", "--eval", expr)
}

func (a *Adapter) runInteractive(ctx context.Context, dir, name string, args ...string) error {
	_, err := a.R.Run(ctx, runner.Spec{Name: name, Args: args, Dir: dir, Interactive: true})
	return err
}

func (a *Adapter) lazygit() string {
	if a.Tools.Lazygit != "" {
		return a.Tools.Lazygit
	}
	return "lazygit"
}

func (a *Adapter) aicommit() string {
	if a.Tools.Aicommit != "" {
		return a.Tools.Aicommit
	}
	return "aicommit"
}
