package commitui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// Prompter asks the user for a single-key choice.
type Prompter interface {
	// Choice presents prompt and returns the chosen lowercase key. An empty
	// response yields def.
	Choice(prompt string, def byte) (byte, error)
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
