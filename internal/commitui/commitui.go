// Package commitui provides interactive adapters for repositories and YADM that
// have local changes: lazygit, Emacs/Magit, aicommit, a direct signed commit,
// or skip. In non-interactive mode no UI is launched.
package commitui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/chmouel/rc/internal/config"
	"github.com/chmouel/rc/internal/output"
	"github.com/chmouel/rc/internal/repo"
	"github.com/chmouel/rc/internal/runner"
)

// Prompter asks the user for a single-key choice.
type Prompter interface {
	// Choice presents prompt and returns the chosen lowercase key. An empty
	// response yields def.
	Choice(prompt string, def byte) (byte, error)
}

// Adapter implements repo.DirtyHandler and yadm.Interactive.
type Adapter struct {
	R     runner.Runner
	Rep   *output.Reporter
	Tools config.ToolsConfig
	Pr    Prompter
}

var _ repo.DirtyHandler = (*Adapter)(nil)

// Handle processes a dirty repository interactively.
func (a *Adapter) Handle(ctx context.Context, target config.RepoTarget, name string) (bool, error) {
	dir := target.Path
	before := repo.Head(ctx, a.R, dir)

	a.Rep.Rule(name)

	// Loop the action menu so [b]ack from the continue prompt re-displays it.
menu:
	for {
		a.showStatus(ctx, dir)

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
		case 'm':
			if err := a.emacs(ctx, dir, "magit-status"); err != nil {
				return false, err
			}
		case 'a':
			if err := a.run(ctx, dir, a.aicommit(), "-a"); err != nil {
				return false, err
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

func (a *Adapter) showStatus(ctx context.Context, dir string) {
	res, _ := a.R.Run(ctx, runner.Spec{Name: "git", Args: []string{"-C", dir, "status", "--short"}, Dir: dir})
	// Trim only trailing newlines: git's short format encodes the staged column
	// as the first character, which may be a space (e.g. " M path"), so trimming
	// leading whitespace would mangle the first line.
	out := strings.Trim(res.Stdout, "\n")
	if strings.TrimSpace(out) == "" {
		return
	}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		a.Rep.Println(a.colorStatusLine(line))
	}
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

// colorStatusLine colorizes a `git status --short` line: the staged column
// (index) in green, the worktree column in red, and untracked entries dimmed.
func (a *Adapter) colorStatusLine(line string) string {
	if !a.Rep.Color() || len(line) < 3 {
		return line
	}
	x, y, rest := line[0:1], line[1:2], line[3:]
	if x == "?" && y == "?" {
		return a.Rep.Dim("?? " + rest)
	}
	if x != " " {
		x = a.Rep.Good(x)
	}
	if y != " " {
		y = a.Rep.Bad(y)
	}
	return x + y + " " + rest
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

func (a *Adapter) run(ctx context.Context, dir, name string, args ...string) error {
	_, err := a.R.Run(ctx, runner.Spec{Name: name, Args: args, Dir: dir})
	return err
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

// StdinPrompter reads single-key choices from a terminal.
type StdinPrompter struct {
	In  io.Reader
	Out io.Writer
}

// NewStdinPrompter returns a prompter reading from stdin and echoing to stderr.
func NewStdinPrompter() *StdinPrompter {
	return &StdinPrompter{In: os.Stdin, Out: os.Stderr}
}

// Choice prints the prompt and reads a single keypress without requiring Enter
// when stdin is a terminal. Ctrl-C and Ctrl-D map to 'q' (quit), Enter selects
// def, and any other key is returned lowercased. When stdin is not a terminal
// (pipes, tests) it falls back to reading a full line.
func (p *StdinPrompter) Choice(prompt string, def byte) (byte, error) {
	fmt.Fprint(p.Out, prompt)

	if f, ok := p.In.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return p.rawChoice(f, def)
	}
	return p.lineChoice(def)
}

// rawChoice reads one keypress in raw mode so the menu reacts to a single key.
// Raw mode disables terminal signal generation, so Ctrl-C arrives as byte 0x03
// and is handled here rather than as a SIGINT delivered to rc.
func (p *StdinPrompter) rawChoice(f *os.File, def byte) (byte, error) {
	fd := int(f.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return p.lineChoice(def)
	}

	var buf [1]byte
	n, _ := f.Read(buf[:])
	// Restore before echoing so newlines are processed normally again. Only the
	// byte count matters here: a trailing read error with no byte means EOF, so
	// fall back to the default choice.
	_ = term.Restore(fd, oldState)

	if n == 0 {
		fmt.Fprintln(p.Out)
		return def, nil
	}

	switch b := buf[0]; b {
	case 3, 4: // Ctrl-C, Ctrl-D
		fmt.Fprintln(p.Out, "^C")
		return 'q', nil
	case '\r', '\n':
		fmt.Fprintln(p.Out)
		return def, nil
	default:
		fmt.Fprintf(p.Out, "%c\n", b)
		return asciiLower(b), nil
	}
}

// lineChoice reads a full line, returning the first lowercased character or def
// when the line is empty.
func (p *StdinPrompter) lineChoice(def byte) (byte, error) {
	reader := bufio.NewReader(p.In)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return def, nil
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return asciiLower(line[0]), nil
}

// asciiLower lowercases a single ASCII byte.
func asciiLower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}
