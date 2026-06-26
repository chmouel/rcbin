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

type fileSelector interface {
	SelectFiles(title string, files []changedFile, rep *output.Reporter) ([]changedFile, bool, error)
}

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

type changedFile struct {
	Status  string
	Path    string
	OldPath string
}

func (f changedFile) displayPath() string {
	path := sanitizePath(f.Path)
	if f.OldPath == "" {
		return path
	}
	return sanitizePath(f.OldPath) + " -> " + path
}

func sanitizePath(path string) string {
	path = strings.ReplaceAll(path, "\n", `\n`)
	return strings.ReplaceAll(path, "\r", `\r`)
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

func (a *Adapter) changedFiles(ctx context.Context, dir string) ([]changedFile, error) {
	res, err := a.R.Run(ctx, runner.Spec{
		Name: "git",
		Args: []string{"-C", dir, "status", "--porcelain=v1", "-z", "--untracked-files=normal"},
		Dir:  dir,
	})
	if err != nil {
		return nil, err
	}
	return parseChangedFilesZ(res.Stdout), nil
}

func parseChangedFilesZ(out string) []changedFile {
	if out == "" {
		return nil
	}
	parts := strings.Split(out, "\x00")
	files := make([]changedFile, 0, len(parts))
	for i := 0; i < len(parts); i++ {
		entry := parts[i]
		if entry == "" || len(entry) < 4 || entry[2] != ' ' {
			continue
		}
		status := entry[:2]
		path := entry[3:]
		file := changedFile{Status: status, Path: path}
		if isRenameOrCopy(status) && i+1 < len(parts) {
			file.OldPath = parts[i+1]
			i++
		}
		files = append(files, file)
	}
	return files
}

func isRenameOrCopy(status string) bool {
	return strings.ContainsAny(status, "RC")
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

func (a *Adapter) showChanges(ctx context.Context, dir string, mode changeDisplayMode) error {
	if mode == changeDisplayFullDiff {
		return a.showDiff(ctx, dir)
	}
	return a.showStatus(ctx, dir)
}

func (a *Adapter) showStatus(ctx context.Context, dir string) error {
	files, err := a.changedFiles(ctx, dir)
	if err != nil {
		return err
	}
	for _, file := range files {
		a.Rep.Println(a.compactStatusLine(file))
	}
	return nil
}

func (a *Adapter) showDiff(ctx context.Context, dir string) error {
	diff, err := a.combinedDiff(ctx, dir)
	if err != nil {
		return err
	}
	printed := false
	if strings.TrimSpace(diff) != "" {
		if err := a.delta(ctx, diff); err != nil {
			return err
		}
		printed = true
	}

	files, err := a.changedFiles(ctx, dir)
	if err != nil {
		return err
	}
	printedUntracked := false
	for _, file := range files {
		if file.Status != "??" {
			continue
		}
		if printed && !printedUntracked {
			a.Rep.Println("")
		}
		a.Rep.Println(a.compactStatusLine(file))
		printed = true
		printedUntracked = true
	}
	return nil
}

func (a *Adapter) combinedDiff(ctx context.Context, dir string) (string, error) {
	var chunks []string
	for _, args := range [][]string{
		{"--cached"},
		nil,
	} {
		out, err := a.gitDiff(ctx, dir, args...)
		if err != nil {
			return "", err
		}
		out = strings.TrimRight(out, "\n")
		if strings.TrimSpace(out) == "" {
			continue
		}
		chunks = append(chunks, out)
	}
	if len(chunks) == 0 {
		return "", nil
	}
	return strings.Join(chunks, "\n") + "\n", nil
}

func (a *Adapter) delta(ctx context.Context, diff string) error {
	if _, ok := a.R.LookPath("delta"); !ok {
		return fmt.Errorf("delta not found for diff display")
	}
	_, err := a.R.Run(ctx, runner.Spec{Name: "delta", Args: []string{"--paging=auto"}, Stdin: diff, Interactive: true})
	return err
}

func (a *Adapter) gitDiff(ctx context.Context, dir string, args ...string) (string, error) {
	gitArgs := append([]string{"-C", dir, "diff", "--no-ext-diff"}, args...)
	res, err := a.R.Run(ctx, runner.Spec{Name: "git", Args: gitArgs, Dir: dir})
	return res.Stdout, err
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

func (a *Adapter) compactStatusLine(file changedFile) string {
	label := compactStatus(file.Status)
	path := file.displayPath()
	if !a.Rep.Color() || label == "" {
		if label == "" {
			return path
		}
		return label + compactStatusSeparator(label) + path
	}
	plain := label + compactStatusSeparator(label) + path
	if label == "??" {
		return a.Rep.Dim(plain)
	}
	if len(file.Status) < 2 {
		return plain
	}
	x, y := file.Status[0:1], file.Status[1:2]
	if x != " " && y != " " {
		return a.Rep.Good(x) + a.Rep.Bad(y) + " " + path
	}
	if x != " " {
		return a.Rep.Good(x) + "  " + path
	}
	if y != " " {
		return a.Rep.Bad(y) + "  " + path
	}
	return plain
}

func compactStatus(status string) string {
	if len(status) < 2 {
		return strings.TrimSpace(status)
	}
	x, y := status[0:1], status[1:2]
	if x == "?" && y == "?" {
		return "??"
	}
	if x != " " && y != " " {
		return x + y
	}
	if x != " " {
		return x
	}
	if y != " " {
		return y
	}
	return strings.TrimSpace(status)
}

func compactStatusSeparator(label string) string {
	if len(label) == 1 {
		return "  "
	}
	return " "
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

// StdinPrompter reads single-key choices from a terminal.
type StdinPrompter struct {
	In  io.Reader
	Out io.Writer
}

// NewStdinPrompter returns a prompter reading from stdin and echoing to stderr.
func NewStdinPrompter() *StdinPrompter {
	return &StdinPrompter{In: os.Stdin, Out: os.Stderr}
}

type selectKey int

const (
	selectKeyNone selectKey = iota
	selectKeyUp
	selectKeyDown
	selectKeyToggle
	selectKeyToggleAll
	selectKeyAccept
	selectKeyCancel
)

type selectAction int

const (
	selectActionNone selectAction = iota
	selectActionAccept
	selectActionCancel
)

type fileSelectState struct {
	cursor   int
	offset   int
	selected []bool
}

func newFileSelectState(count int) fileSelectState {
	return fileSelectState{selected: make([]bool, count)}
}

func (s *fileSelectState) apply(key selectKey, maxVisible int) selectAction {
	switch key {
	case selectKeyUp:
		if s.cursor > 0 {
			s.cursor--
		}
	case selectKeyDown:
		if s.cursor < len(s.selected)-1 {
			s.cursor++
		}
	case selectKeyToggle:
		if len(s.selected) > 0 {
			s.selected[s.cursor] = !s.selected[s.cursor]
		}
	case selectKeyToggleAll:
		selectAll := false
		for _, selected := range s.selected {
			if !selected {
				selectAll = true
				break
			}
		}
		for i := range s.selected {
			s.selected[i] = selectAll
		}
	case selectKeyAccept:
		return selectActionAccept
	case selectKeyCancel:
		return selectActionCancel
	}
	s.adjustOffset(maxVisible)
	return selectActionNone
}

func (s *fileSelectState) adjustOffset(maxVisible int) {
	if maxVisible < 1 {
		maxVisible = 1
	}
	if s.cursor < s.offset {
		s.offset = s.cursor
	}
	if s.cursor >= s.offset+maxVisible {
		s.offset = s.cursor - maxVisible + 1
	}
	if s.offset < 0 {
		s.offset = 0
	}
}

func (s *fileSelectState) selectedCount() int {
	var count int
	for _, selected := range s.selected {
		if selected {
			count++
		}
	}
	return count
}

func (s *fileSelectState) selectedFiles(files []changedFile) []changedFile {
	selected := make([]changedFile, 0, s.selectedCount())
	for i, file := range files {
		if i < len(s.selected) && s.selected[i] {
			selected = append(selected, file)
		}
	}
	return selected
}

// SelectFiles presents a terminal multi-select menu. It returns ok=false when
// the user cancels or accepts with no files selected.
func (p *StdinPrompter) SelectFiles(title string, files []changedFile, rep *output.Reporter) ([]changedFile, bool, error) {
	if len(files) == 0 {
		return nil, false, nil
	}
	if f, ok := p.In.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return p.rawSelectFiles(f, title, files, rep)
	}
	return p.lineSelectFiles(title, files, rep)
}

func (p *StdinPrompter) rawSelectFiles(f *os.File, title string, files []changedFile, rep *output.Reporter) ([]changedFile, bool, error) {
	fd := int(f.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return p.lineSelectFiles(title, files, rep)
	}

	restored := false
	restore := func() {
		if restored {
			return
		}
		_ = term.Restore(fd, oldState)
		fmt.Fprint(p.Out, "\x1b[?25h")
		restored = true
	}
	defer restore()

	fmt.Fprint(p.Out, "\x1b[?25l")
	reader := bufio.NewReader(f)
	state := newFileSelectState(len(files))
	maxVisible := maxSelectorVisible(fd, len(files))
	rendered := 0
	for {
		rendered = renderFileSelector(p.Out, rep, title, files, &state, maxVisible, rendered)
		key, err := readSelectKey(reader)
		if err != nil {
			return nil, false, err
		}
		switch state.apply(key, maxVisible) {
		case selectActionAccept:
			restore()
			fmt.Fprintln(p.Out)
			selected := state.selectedFiles(files)
			return selected, len(selected) > 0, nil
		case selectActionCancel:
			restore()
			fmt.Fprintln(p.Out)
			return nil, false, nil
		}
	}
}

func (p *StdinPrompter) lineSelectFiles(title string, files []changedFile, _ *output.Reporter) ([]changedFile, bool, error) {
	fmt.Fprintln(p.Out, title)
	for i, file := range files {
		fmt.Fprintf(p.Out, "%d) %s %s\n", i+1, file.Status, file.displayPath())
	}
	fmt.Fprint(p.Out, "Select files (numbers, a=all, q=cancel): ")
	reader := bufio.NewReader(p.In)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return nil, false, nil
	}
	selected, ok := parseLineSelection(line, len(files))
	if !ok {
		return nil, false, nil
	}
	state := fileSelectState{selected: selected}
	return state.selectedFiles(files), state.selectedCount() > 0, nil
}

func parseLineSelection(line string, count int) ([]bool, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.EqualFold(line, "q") {
		return nil, false
	}
	selected := make([]bool, count)
	if strings.EqualFold(line, "a") {
		for i := range selected {
			selected[i] = true
		}
		return selected, true
	}
	tokens := strings.FieldsFunc(line, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	for _, token := range tokens {
		n, err := strconv.Atoi(token)
		if err != nil || n < 1 || n > count {
			return nil, false
		}
		selected[n-1] = true
	}
	return selected, true
}

func maxSelectorVisible(fd, count int) int {
	_, height, err := term.GetSize(fd)
	if err == nil && height > 6 {
		return min(count, max(1, height-5))
	}
	return min(count, 12)
}

func readSelectKey(r *bufio.Reader) (selectKey, error) {
	b, err := r.ReadByte()
	if err != nil {
		return selectKeyCancel, err
	}
	switch b {
	case 3, 4, 'q', 'Q':
		return selectKeyCancel, nil
	case '\r', '\n':
		return selectKeyAccept, nil
	case ' ':
		return selectKeyToggle, nil
	case 'a', 'A':
		return selectKeyToggleAll, nil
	case 'j', 'J':
		return selectKeyDown, nil
	case 'k', 'K':
		return selectKeyUp, nil
	case 0x1b:
		next, _ := r.ReadByte()
		if next != '[' {
			return selectKeyNone, nil
		}
		arrow, _ := r.ReadByte()
		switch arrow {
		case 'A':
			return selectKeyUp, nil
		case 'B':
			return selectKeyDown, nil
		}
	}
	return selectKeyNone, nil
}

func renderFileSelector(w io.Writer, rep *output.Reporter, title string, files []changedFile, state *fileSelectState, maxVisible, previousLines int) int {
	if previousLines > 0 {
		fmt.Fprintf(w, "\x1b[%dA", previousLines)
	}
	state.adjustOffset(maxVisible)
	end := min(len(files), state.offset+maxVisible)
	lines := []string{
		fmt.Sprintf("%s %s", rep.Arrow(), rep.Bold(title)),
		rep.Dim("j/k or arrows move  space toggles  a toggles all  enter commits  q cancels"),
	}
	for i := state.offset; i < end; i++ {
		cursor := " "
		if i == state.cursor {
			cursor = rep.Accent(">")
		}
		checkbox := "[ ]"
		if state.selected[i] {
			checkbox = rep.Good("[x]")
		}
		lines = append(lines, fmt.Sprintf("%s %s %s %s", cursor, checkbox, selectorStatus(rep, files[i].Status), files[i].displayPath()))
	}
	summary := fmt.Sprintf("%d/%d selected", state.selectedCount(), len(files))
	if len(files) > maxVisible {
		summary += fmt.Sprintf("  showing %d-%d/%d", state.offset+1, end, len(files))
	}
	lines = append(lines, rep.Dim(summary))

	for _, line := range lines {
		fmt.Fprintf(w, "\r\x1b[2K%s\n", line)
	}
	return len(lines)
}

func selectorStatus(rep *output.Reporter, status string) string {
	if !rep.Color() || len(status) < 2 {
		return status
	}
	x, y := status[0:1], status[1:2]
	if x == "?" && y == "?" {
		return rep.Dim(status)
	}
	if x != " " {
		x = rep.Good(x)
	}
	if y != " " {
		y = rep.Bad(y)
	}
	return x + y
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
