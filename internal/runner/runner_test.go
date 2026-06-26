package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func selfPGID(t *testing.T) int {
	t.Helper()
	pgid, err := syscall.Getpgid(os.Getpid())
	if err != nil {
		t.Fatalf("getpgid: %v", err)
	}
	return pgid
}

func readPGID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pgid file: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse pgid %q: %v", data, err)
	}
	return n
}

// TestInteractiveSharesForegroundGroup verifies that an interactive child stays
// in rc's process group so it owns the controlling terminal. Placing it in a
// separate group (the previous behavior) made TUIs like lazygit block on
// SIGTTIN/SIGTTOU.
func TestInteractiveSharesForegroundGroup(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	out := filepath.Join(t.TempDir(), "pgid")
	_, err := New().Run(context.Background(), Spec{
		Name:        "sh",
		Args:        []string{"-c", "ps -o pgid= -p $$ | tr -d ' ' > " + out},
		Interactive: true,
	})
	if err != nil {
		t.Fatalf("interactive run: %v", err)
	}
	if got, want := readPGID(t, out), selfPGID(t); got != want {
		t.Errorf("interactive child pgid = %d, want %d (rc's group)", got, want)
	}
	if InteractiveActive() {
		t.Error("InteractiveActive should be false after the child exits")
	}
}

func TestInteractiveCanReceiveProvidedStdin(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	out := filepath.Join(t.TempDir(), "stdin")
	_, err := New().Run(context.Background(), Spec{
		Name:        "sh",
		Args:        []string{"-c", "cat > " + out},
		Stdin:       "from rc\n",
		Interactive: true,
	})
	if err != nil {
		t.Fatalf("interactive run: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read stdin file: %v", err)
	}
	if got := string(data); got != "from rc\n" {
		t.Errorf("interactive stdin = %q, want provided stdin", got)
	}
}

// TestCapturedRunsInOwnGroup verifies that captured children get their own
// process group so cancellation can signal the whole group.
func TestCapturedRunsInOwnGroup(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	res, err := New().Run(context.Background(), Spec{
		Name: "sh",
		Args: []string{"-c", "ps -o pgid= -p $$ | tr -d ' '"},
	})
	if err != nil {
		t.Fatalf("captured run: %v", err)
	}
	got, err := strconv.Atoi(strings.TrimSpace(res.Stdout))
	if err != nil {
		t.Fatalf("parse pgid %q: %v", res.Stdout, err)
	}
	if got == selfPGID(t) {
		t.Errorf("captured child pgid = %d, want a separate group", got)
	}
}
