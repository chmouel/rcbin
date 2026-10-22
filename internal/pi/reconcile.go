package pi

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"github.com/chmouel/rc/internal/runner"
)

var piVersionPattern = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// reconcile refreshes Pi packages from the canonical settings. It is best
// effort: only cancellation is returned as an error.
func (s *Syncer) reconcile(ctx context.Context) error {
	res, err := s.R.Run(ctx, runner.Spec{Name: "pi", Args: []string{"--version"}, Dir: s.AgentDir, Stdin: ""})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.Rep.Warnf("pi: cannot determine the installed version (%v); skipping package update", err)
		return nil
	}
	ok, err := versionAtLeast(res.Stdout+" "+res.Stderr, minPiMajor, minPiMinor, minPiPatch)
	switch {
	case err != nil:
		s.Rep.Warnf("pi: cannot parse the installed version from %q; skipping package update", trimOutput(res.Stdout))
		return nil //nolint:nilerr // best effort: only cancellation is an error
	case !ok:
		s.Rep.Warnf("pi: version %s is older than %d.%d.%d; skipping package update", trimOutput(res.Stdout), minPiMajor, minPiMinor, minPiPatch)
		return nil
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	spec := runner.Spec{
		Name:  "pi",
		Args:  []string{"update", "--extensions", "--no-approve"},
		Dir:   s.AgentDir,
		Extra: []string{"PI_CODING_AGENT_DIR=" + s.AgentDir},
		Stdin: "",
	}
	if _, err := s.R.Run(ctx, spec); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Pinned npm sources are intentionally skipped by this command; Pi's
		// startup resolver installs them from settings.json.
		s.Rep.Warnf("pi: package update failed, continuing: %v", err)
	}
	return nil
}

func versionAtLeast(output string, major, minor, patch int) (bool, error) {
	m := piVersionPattern.FindStringSubmatch(output)
	if m == nil {
		return false, fmt.Errorf("no semantic version in %q", trimOutput(output))
	}
	got := [3]int{}
	for i := 0; i < 3; i++ {
		v, err := strconv.Atoi(m[i+1])
		if err != nil {
			return false, err
		}
		got[i] = v
	}
	want := [3]int{major, minor, patch}
	for i := 0; i < 3; i++ {
		if got[i] != want[i] {
			return got[i] > want[i], nil
		}
	}
	return true, nil
}

func trimOutput(s string) string {
	out := []rune(s)
	const max = 80
	for i, r := range out {
		if r == '\n' || r == '\r' {
			out = out[:i]
			break
		}
	}
	if len(out) > max {
		out = append(out[:max], '…')
	}
	return string(out)
}
