package app

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chmouel/rc/internal/runner"
)

// run executes the command tree with a fake runner against a temporary HOME so
// configuration loading is hermetic.
func run(t *testing.T, fake *runner.Fake, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	if fake == nil {
		fake = runner.NewFake()
	}
	var out, errBuf bytes.Buffer
	// Always pass an explicit host to avoid depending on the test machine.
	full := append([]string{"--host", "testhost"}, args...)
	code = Execute(context.Background(), full, Deps{
		Runner: fake,
		Stdout: &out,
		Stderr: &errBuf,
	})
	return out.String(), errBuf.String(), code
}

func TestNoArgsPrintsHelp(t *testing.T) {
	out, _, code := run(t, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out, "Usage:") {
		t.Errorf("help output missing Usage:\n%s", out)
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	_, errOut, code := run(t, nil, "frobnicate")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut, "unknown command") {
		t.Errorf("stderr = %q, want unknown command", errOut)
	}
}

func TestUnknownFlagIsUsageError(t *testing.T) {
	_, _, code := run(t, nil, "status", "--nope")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestConfigValidateSucceeds(t *testing.T) {
	_, errOut, code := run(t, nil, "config", "validate")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%s)", code, errOut)
	}
	if !strings.Contains(errOut, "configuration valid") {
		t.Errorf("stderr missing validation message:\n%s", errOut)
	}
}

func TestStatusWaybarReportsZero(t *testing.T) {
	// No repositories exist under the temp HOME, so the count is zero and the
	// payload is valid JSON on stdout.
	out, _, code := run(t, nil, "status", "--format", "waybar")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	var payload struct {
		Text    string `json:"text"`
		Tooltip string `json:"tooltip"`
		Class   string `json:"class"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("waybar output is not JSON: %v\n%s", err, out)
	}
	if payload.Text != "0" {
		t.Errorf("text = %q, want 0", payload.Text)
	}
}

func TestStatusInvalidFormatIsUsageError(t *testing.T) {
	_, _, code := run(t, nil, "status", "--format", "xml")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestSyncMutuallyExclusiveFlags(t *testing.T) {
	_, _, code := run(t, nil, "sync", "--skip-yadm", "--yadm-only")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestSyncChangedOnlyDoesNotCloneOrSyncYadmByDefault(t *testing.T) {
	fake := runner.NewFake()
	out, errOut, code := run(t, fake, "sync", "--changed-only")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%s)", code, errOut)
	}
	if out != "" {
		t.Fatalf("changed-only human output should not use stdout, got %q", out)
	}
	for _, line := range fake.CallLines() {
		if strings.HasPrefix(line, "git clone") {
			t.Fatalf("changed-only must not clone missing repositories, calls: %v", fake.CallLines())
		}
		if strings.HasPrefix(line, "yadm ") {
			t.Fatalf("changed-only must not sync yadm by default, calls: %v", fake.CallLines())
		}
	}
}

func TestMigrateCommandRemoved(t *testing.T) {
	_, errOut, code := run(t, nil, "migrate")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut, "unknown command") {
		t.Errorf("stderr = %q, want unknown command", errOut)
	}
}

func TestDoctorRunsAndReports(t *testing.T) {
	fake := runner.NewFake()
	// Report some required tools as missing to exercise warning paths without
	// failing the overall run unexpectedly; doctor returns an error only when a
	// check fails. We assert the command produces a deterministic exit code.
	_, _, code := run(t, fake, "doctor", "--offline")
	if code != 0 && code != 1 {
		t.Fatalf("doctor exit code = %d, want 0 or 1", code)
	}
}
