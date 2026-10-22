package pi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/rc/internal/runner"
)

// passRunner emulates pass on top of a fake runner. Entries are stored with a
// fresh nonce line on every write, so the ciphertext changes like gpg's does.
type passRunner struct {
	*runner.Fake
	store string

	mu      sync.Mutex
	nonce   int
	shows   int
	inserts int
}

func (p *passRunner) Run(ctx context.Context, spec runner.Spec) (runner.Result, error) {
	if spec.Name != "pass" {
		return p.Fake.Run(ctx, spec)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if got := strings.Join(spec.Extra, " "); got != "PASSWORD_STORE_DIR="+p.store {
		return runner.Result{}, fmt.Errorf("pass ran with env %q", got)
	}
	path := filepath.Join(p.store, spec.Args[len(spec.Args)-1]+".gpg")
	switch spec.Args[0] {
	case "show":
		p.shows++
		b, err := os.ReadFile(path)
		if err != nil {
			return runner.Result{ExitCode: 1}, err
		}
		_, plain, _ := strings.Cut(string(b), "\n")
		return runner.Result{Stdout: plain}, nil
	case "insert":
		p.inserts++
		return runner.Result{}, p.write(path, spec.Stdin)
	}
	return runner.Result{}, fmt.Errorf("unexpected pass call %v", spec.Args)
}

func (p *passRunner) write(path, content string) error {
	p.nonce++
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(fmt.Sprintf("%d\n%s", p.nonce, content)), 0o600)
}

type authFixture struct {
	*fixture
	pass  *passRunner
	local string
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	f := newFixture(t)
	store := filepath.Join(f.root, "store")
	writeFile(t, filepath.Join(store, ".gpg-id"), "me@example.com")
	return &authFixture{
		fixture: f,
		pass:    &passRunner{Fake: f.fake, store: store},
		local:   filepath.Join(f.agent, AuthFile),
	}
}

func (a *authFixture) syncer() *Syncer {
	s := a.fixture.syncer()
	s.R = a.pass
	s.SyncAuth = true
	s.PasswordStore = a.pass.store
	s.AuthState = filepath.Join(a.root, "state", "pi-auth-state.json")
	s.piLockWait = 300 * time.Millisecond
	return s
}

func (a *authFixture) setPass(t *testing.T, content string) {
	t.Helper()
	if err := a.pass.write(filepath.Join(a.pass.store, DefaultAuthEntry+".gpg"), content); err != nil {
		t.Fatal(err)
	}
}

func (a *authFixture) passContent(t *testing.T) string {
	t.Helper()
	_, plain, _ := strings.Cut(readFile(t, filepath.Join(a.pass.store, DefaultAuthEntry+".gpg")), "\n")
	return plain
}

func (a *authFixture) sync(t *testing.T, s *Syncer) {
	t.Helper()
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSyncAuthSeedsPassThenSkipsDecryption(t *testing.T) {
	a := newAuthFixture(t)
	writeFile(t, a.local, "local-token")

	a.sync(t, a.syncer())
	if got := a.passContent(t); got != "local-token" {
		t.Fatalf("pass holds %q, want the local credentials", got)
	}
	requireNoSymlink(t, a.local)

	// Nothing changed on either side: the unchanged ciphertext proves pass
	// still holds the base, so pass is never decrypted.
	a.sync(t, a.syncer())
	if a.pass.shows != 0 || a.pass.inserts != 1 {
		t.Fatalf("shows=%d inserts=%d, want 0 and 1", a.pass.shows, a.pass.inserts)
	}
}

func TestSyncAuthRestoresOnANewMachine(t *testing.T) {
	a := newAuthFixture(t)
	a.setPass(t, "from-pass")

	a.sync(t, a.syncer())
	if got := readFile(t, a.local); got != "from-pass" {
		t.Fatalf("auth.json = %q, want the pass entry", got)
	}
	requireMode(t, a.local, 0o600)
}

func TestSyncAuthThreeWay(t *testing.T) {
	cases := []struct {
		name       string
		local      string
		pass       string
		adopt      bool
		wantLocal  string
		wantPass   string
		wantErr    string
		wantBackup bool
	}{
		{name: "local changed is pushed", local: "refreshed", pass: "base", wantLocal: "refreshed", wantPass: "refreshed"},
		{name: "pass changed is pulled", local: "base", pass: "other-machine", wantLocal: "other-machine", wantPass: "other-machine"},
		{name: "both changed conflicts", local: "refreshed", pass: "other-machine", wantLocal: "refreshed", wantPass: "other-machine", wantErr: "both changed"},
		{name: "adopt takes pass", local: "refreshed", pass: "other-machine", adopt: true, wantLocal: "other-machine", wantPass: "other-machine", wantBackup: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newAuthFixture(t)
			writeFile(t, a.local, "base")
			a.sync(t, a.syncer())

			writeFile(t, a.local, c.local)
			if c.pass != "base" {
				a.setPass(t, c.pass)
			}
			s := a.syncer()
			s.Adopt = c.adopt
			err := s.Sync(context.Background())
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("error = %v, want %q", err, c.wantErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, a.local); got != c.wantLocal {
				t.Errorf("auth.json = %q, want %q", got, c.wantLocal)
			}
			if got := a.passContent(t); got != c.wantPass {
				t.Errorf("pass = %q, want %q", got, c.wantPass)
			}
			backup := a.local + ".rc-adopt.bak"
			if c.wantBackup {
				if got := readFile(t, backup); got != c.local {
					t.Errorf("backup = %q, want %q", got, c.local)
				}
				requireMode(t, backup, 0o600)
			} else if _, err := os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("unexpected backup %s", backup)
			}
		})
	}
}

func TestSyncAuthFirstRunWithDifferentContentConflicts(t *testing.T) {
	a := newAuthFixture(t)
	writeFile(t, a.local, "local")
	a.setPass(t, "pass")

	err := a.syncer().Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "--adopt-pi") {
		t.Fatalf("error = %v, want a conflict without a recorded base", err)
	}
	if got := readFile(t, a.local); got != "local" {
		t.Fatalf("auth.json = %q, want it untouched", got)
	}
}

func TestSyncAuthMigratesLegacySymlink(t *testing.T) {
	a := newAuthFixture(t)
	plaintext := filepath.Join(a.source, AuthFile)
	writeFile(t, plaintext, "plaintext")
	mkdir(t, a.agent)
	if err := os.Symlink(plaintext, a.local); err != nil {
		t.Fatal(err)
	}

	err := a.syncer().Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "--adopt-pi") {
		t.Fatalf("error = %v, want the legacy symlink reported", err)
	}
	requireLog(t, a.logs, "plaintext credentials")

	s := a.syncer()
	s.Adopt = true
	a.sync(t, s)
	requireNoSymlink(t, a.local)
	if got := readFile(t, a.local); got != "plaintext" {
		t.Fatalf("auth.json = %q, want the linked content", got)
	}
	if got := a.passContent(t); got != "plaintext" {
		t.Fatalf("pass = %q, want the linked content", got)
	}
	requireSymlinkTo(t, a.local+".rc-adopt.bak", plaintext)
	if got := readFile(t, plaintext); got != "plaintext" {
		t.Fatal("the plaintext copy in Git must be left for the user to remove")
	}
}

func TestSyncAuthSkipsWithoutPass(t *testing.T) {
	a := newAuthFixture(t)
	a.fake.Missing["pass"] = true
	writeFile(t, a.local, "local")

	a.sync(t, a.syncer())
	if a.pass.inserts != 0 {
		t.Fatal("pass must not run when it is not installed")
	}
}

func TestSyncAuthSkipsUninitializedStore(t *testing.T) {
	a := newAuthFixture(t)
	if err := os.Remove(filepath.Join(a.pass.store, ".gpg-id")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, a.local, "local")

	a.sync(t, a.syncer())
	if a.pass.inserts != 0 {
		t.Fatal("pass must not run without a .gpg-id")
	}
}

func TestSyncAuthHonorsPiLock(t *testing.T) {
	a := newAuthFixture(t)
	a.setPass(t, "from-pass")
	mkdir(t, a.local+".lock")

	err := a.syncer().Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "locked by a running Pi") {
		t.Fatalf("error = %v, want the held Pi lock reported", err)
	}
	if _, err := os.Lstat(a.local); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("auth.json must not be written while Pi holds its lock")
	}

	stale := time.Now().Add(-2 * piLockStale)
	if err := os.Chtimes(a.local+".lock", stale, stale); err != nil {
		t.Fatal(err)
	}
	a.sync(t, a.syncer())
	if got := readFile(t, a.local); got != "from-pass" {
		t.Fatalf("auth.json = %q after reclaiming a stale lock", got)
	}
	if _, err := os.Stat(a.local + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the lock must be released")
	}
}

func TestSyncAuthDryRunWritesNothing(t *testing.T) {
	a := newAuthFixture(t)
	writeFile(t, a.local, "local")

	s := a.syncer()
	s.DryRun = true
	a.sync(t, s)
	if a.pass.inserts != 0 {
		t.Fatal("dry run stored the credentials")
	}
	if _, err := os.Stat(s.AuthState); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("dry run recorded state")
	}
	requireLog(t, a.logs, "would store")
}
