package runner

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Call records a single invocation handled by Fake.
type Call struct {
	Name        string
	Args        []string
	Dir         string
	Interactive bool
}

// Stub describes a canned response keyed by a command-line prefix match.
type Stub struct {
	// Match is matched against "name arg1 arg2 ..." with strings.HasPrefix.
	Match  string
	Result Result
	Err    error
}

// Fake is a deterministic Runner for tests. It records calls and returns
// stubbed results. It is safe for concurrent use so race tests can exercise the
// worker pool.
type Fake struct {
	mu       sync.Mutex
	Stubs    []Stub
	Missing  map[string]bool // executables reported as absent by LookPath
	Calls    []Call
	Fallback Result // returned when no stub matches
}

// NewFake returns an empty fake runner.
func NewFake() *Fake {
	return &Fake{Missing: map[string]bool{}}
}

// AddStub registers a canned response for commands whose joined form starts
// with match.
func (f *Fake) AddStub(match string, res Result, err error) *Fake {
	f.Stubs = append(f.Stubs, Stub{Match: match, Result: res, Err: err})
	return f
}

// LookPath reports executables as present unless explicitly marked missing.
func (f *Fake) LookPath(name string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Missing[name] {
		return "", false
	}
	return "/fake/" + name, true
}

// Run records the call and returns the first matching stub.
func (f *Fake) Run(_ context.Context, spec Spec) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, Call{Name: spec.Name, Args: append([]string{}, spec.Args...), Dir: spec.Dir, Interactive: spec.Interactive})

	line := spec.Name
	if len(spec.Args) > 0 {
		line += " " + strings.Join(spec.Args, " ")
	}
	for _, s := range f.Stubs {
		if strings.HasPrefix(line, s.Match) {
			if s.Err != nil {
				return s.Result, s.Err
			}
			return s.Result, nil
		}
	}
	return f.Fallback, nil
}

// CallLines returns each recorded call as a joined command line.
func (f *Fake) CallLines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.Calls))
	for _, c := range f.Calls {
		line := c.Name
		if len(c.Args) > 0 {
			line += " " + strings.Join(c.Args, " ")
		}
		out = append(out, line)
	}
	return out
}

// CallRecords returns a snapshot of recorded invocations.
func (f *Fake) CallRecords() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Call, len(f.Calls))
	for i, c := range f.Calls {
		out[i] = Call{Name: c.Name, Args: append([]string{}, c.Args...), Dir: c.Dir, Interactive: c.Interactive}
	}
	return out
}

// FailWith returns a runner Error for the given spec, useful for stub setup.
func FailWith(spec Spec, code int, stderr string) error {
	res := Result{ExitCode: code, Stderr: stderr}
	return &Error{Spec: spec, Result: res, Err: fmt.Errorf("exit %d", code)}
}
