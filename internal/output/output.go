// Package output renders human-readable logs, result lines, and machine output.
// Logs and diagnostics are written to stderr; machine output (such as Waybar
// JSON) and result lines are written to stdout so callers can parse stdout
// cleanly.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Level classifies a log message.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// ANSI color codes.
const (
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	blue   = "\033[34m"
	reset  = "\033[0m"
)

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
	if !r.color {
		return s
	}
	return code + s + reset
}

// Debugf logs at debug level (stderr) when verbose is enabled.
func (r *Reporter) Debugf(format string, a ...any) {
	if !r.verbose {
		return
	}
	fmt.Fprintf(r.err, "%s %s\n", r.paint(blue, "DEBUG:"), fmt.Sprintf(format, a...))
}

// Infof logs at info level to stderr.
func (r *Reporter) Infof(format string, a ...any) {
	fmt.Fprintf(r.err, "%s %s\n", r.paint(green, "INFO:"), fmt.Sprintf(format, a...))
}

// Warnf logs at warn level to stderr.
func (r *Reporter) Warnf(format string, a ...any) {
	fmt.Fprintf(r.err, "%s %s\n", r.paint(yellow, "WARN:"), fmt.Sprintf(format, a...))
}

// Errorf logs at error level to stderr.
func (r *Reporter) Errorf(format string, a ...any) {
	fmt.Fprintf(r.err, "%s %s\n", r.paint(red, "ERROR:"), fmt.Sprintf(format, a...))
}

// Successf prints a green checkmark result line to stderr.
func (r *Reporter) Successf(format string, a ...any) {
	fmt.Fprintf(r.err, "%s %s\n", r.paint(green, "✓"), fmt.Sprintf(format, a...))
}

// Failf prints a red cross result line to stderr.
func (r *Reporter) Failf(format string, a ...any) {
	fmt.Fprintf(r.err, "%s %s\n", r.paint(red, "✗"), fmt.Sprintf(format, a...))
}

// Skipf prints a dimmed skip result line to stderr.
func (r *Reporter) Skipf(format string, a ...any) {
	fmt.Fprintf(r.err, "%s %s\n", r.paint(yellow, "–"), fmt.Sprintf(format, a...))
}

// Printf writes human-facing plain text to stderr.
func (r *Reporter) Printf(format string, a ...any) {
	fmt.Fprintf(r.err, format, a...)
}

// Println writes a human-facing line to stderr.
func (r *Reporter) Println(s string) {
	fmt.Fprintln(r.err, s)
}

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
