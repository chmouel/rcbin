package commitui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/chmouel/rc/internal/output"
)

type fileSelector interface {
	SelectFiles(title string, files []changedFile, rep *output.Reporter) ([]changedFile, bool, error)
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
