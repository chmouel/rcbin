// Package output renders human-readable logs, result lines, and machine output.
// Logs and diagnostics are written to stderr; machine output (such as Waybar
// JSON) and result lines are written to stdout so callers can parse stdout
// cleanly. Human-facing styling (ANSI color + Nerd Font icons) is applied only
// when color is enabled; machine output on stdout always stays plain.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// Level classifies a log message.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// ANSI style codes.
const (
	reset   = "\033[0m"
	bold    = "\033[1m"
	dim     = "\033[2m"
	red     = "\033[31m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	magenta = "\033[35m"
	cyan    = "\033[36m"
)

// Nerd Font icons (require a patched font). Used on the human-facing surface
// only. Without a Nerd Font these render as a missing-glyph box; rc assumes a
// Nerd Font terminal and falls back to plain text only when color is disabled.
const (
	iconOK     = "\uf00c" //
	iconFail   = "\uf00d" //
	iconWarn   = "\uf071" //
	iconInfo   = "\uf05a" //
	iconSkip   = "\uf056" //
	iconDebug  = "\uf188" //
	iconBranch = "\ue0a0" //
	iconArrow  = "\u276f" // ❯
)

// ColorFor reports whether ANSI styling should be used for w. It honors an
// explicit disable (e.g. --no-color), the NO_COLOR convention, and requires w
// to be a terminal so piped or redirected output stays plain.
func ColorFor(w io.Writer, disabled bool) bool {
	if disabled {
		return false
	}
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// Reporter renders logs and results with optional color.
type Reporter struct {
	out     io.Writer
	err     io.Writer
	color   bool
	verbose bool
}

// New returns a Reporter writing results to out and logs to err.
func New(out, err io.Writer, color, verbose bool) *Reporter {
	return &Reporter{out: out, err: err, color: color, verbose: verbose}
}

func (r *Reporter) paint(code, s string) string {
	if !r.color || code == "" || s == "" {
		return s
	}
	return code + s + reset
}

// Bold returns s in bold when color is enabled.
func (r *Reporter) Bold(s string) string { return r.paint(bold, s) }

// Dim returns s dimmed when color is enabled.
func (r *Reporter) Dim(s string) string { return r.paint(dim, s) }

// Accent returns s in the accent color (bold cyan) when color is enabled.
func (r *Reporter) Accent(s string) string { return r.paint(bold+cyan, s) }

// Key highlights a hotkey fragment (bold magenta), e.g. the bracketed letter in
// a menu entry.
func (r *Reporter) Key(s string) string { return r.paint(bold+magenta, s) }

// Good returns s in green (e.g. a staged/added status column).
func (r *Reporter) Good(s string) string { return r.paint(green, s) }

// Bad returns s in red (e.g. an unstaged/removed status column).
func (r *Reporter) Bad(s string) string { return r.paint(red, s) }

// Caution returns s in yellow (e.g. a warning count).
func (r *Reporter) Caution(s string) string { return r.paint(yellow, s) }

// Color reports whether styling is enabled, so callers can choose plain
// alternatives (icons, prompts) when it is not.
func (r *Reporter) Color() bool { return r.color }

// Rule prints a section header to stderr: a Nerd Font branch icon and a bold
// accent title flanked by colored rules.
func (r *Reporter) Rule(title string) {
	fmt.Fprintln(r.err, r.RuleString(title))
}

// RuleString builds the section-header string used by Rule.
func (r *Reporter) RuleString(title string) string {
	const width = 52
	visible := "── " + iconBranch + " " + title + " "
	pad := width - utf8.RuneCountInString(visible)
	if pad < 3 {
		pad = 3
	}
	tail := strings.Repeat("─", pad)
	return r.paint(cyan, "── ") + r.paint(cyan, iconBranch) + " " +
		r.paint(bold+cyan, title) + " " + r.paint(cyan, tail)
}

// Debugf logs at debug level (stderr) when verbose is enabled.
func (r *Reporter) Debugf(format string, a ...any) {
	if !r.verbose {
		return
	}
	fmt.Fprintf(r.err, "%s %s\n", r.paint(blue, iconDebug), fmt.Sprintf(format, a...))
}

// Infof logs at info level to stderr.
func (r *Reporter) Infof(format string, a ...any) {
	fmt.Fprintf(r.err, "%s %s\n", r.paint(cyan, iconInfo), fmt.Sprintf(format, a...))
}

// Warnf logs at warn level to stderr.
func (r *Reporter) Warnf(format string, a ...any) {
	fmt.Fprintf(r.err, "%s %s\n", r.paint(yellow, iconWarn), fmt.Sprintf(format, a...))
}

// Errorf logs at error level to stderr.
func (r *Reporter) Errorf(format string, a ...any) {
	fmt.Fprintf(r.err, "%s %s\n", r.paint(bold+red, iconFail), fmt.Sprintf(format, a...))
}

// Successf prints a success result line to stderr.
func (r *Reporter) Successf(format string, a ...any) {
	fmt.Fprintln(r.err, r.SuccessLine(format, a...))
}

// SuccessLine returns a styled success result line without printing it, so
// callers that buffer ordered output can store it.
func (r *Reporter) SuccessLine(format string, a ...any) string {
	return fmt.Sprintf("%s %s", r.paint(green, iconOK), fmt.Sprintf(format, a...))
}

// Failf prints a failure result line to stderr.
func (r *Reporter) Failf(format string, a ...any) {
	fmt.Fprintf(r.err, "%s %s\n", r.paint(bold+red, iconFail), fmt.Sprintf(format, a...))
}

// Skipf prints a dimmed skip result line to stderr.
func (r *Reporter) Skipf(format string, a ...any) {
	fmt.Fprintf(r.err, "%s %s\n", r.paint(dim, iconSkip), r.paint(dim, fmt.Sprintf(format, a...)))
}

// Printf writes human-facing plain text to stderr.
func (r *Reporter) Printf(format string, a ...any) {
	fmt.Fprintf(r.err, format, a...)
}

// Println writes a human-facing line to stderr.
func (r *Reporter) Println(s string) {
	fmt.Fprintln(r.err, s)
}

// Arrow returns the prompt arrow icon styled in the accent color.
func (r *Reporter) Arrow() string { return r.paint(bold+cyan, iconArrow) }

// Out returns the result writer (stdout).
func (r *Reporter) Out() io.Writer { return r.out }

// Err returns the log writer (stderr).
func (r *Reporter) Err() io.Writer { return r.err }

// Verbose reports whether verbose logging is enabled.
func (r *Reporter) Verbose() bool { return r.verbose }

// WaybarPayload is the JSON object consumed by Waybar's custom module.
type WaybarPayload struct {
	Text    string `json:"text"`
	Tooltip string `json:"tooltip"`
	Class   string `json:"class"`
}

// Waybar renders the dirty-repository count as a Waybar JSON object. An empty
// list reports a count of zero with an empty tooltip.
func Waybar(dirty []string) (string, error) {
	payload := WaybarPayload{
		Text:    fmt.Sprintf("%d", len(dirty)),
		Tooltip: strings.Join(dirty, "\n"),
		Class:   "git-changes",
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
