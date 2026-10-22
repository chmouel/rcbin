package pi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/chmouel/rc/internal/runner"
)

const (
	// DefaultAuthEntry is the pass entry holding auth.json when none is set.
	DefaultAuthEntry = "pi/auth.json"

	// Pi guards auth.json with proper-lockfile: a "<file>.lock" directory
	// whose mtime is refreshed while held and considered stale after 10s.
	piLockStale       = 10 * time.Second
	defaultPiLockWait = 12 * time.Second
)

// authRecord is the last state both sides agreed on: the plaintext hash and
// the hash of the encrypted pass file at that time.
type authRecord struct {
	Base   string `json:"base"`
	Cipher string `json:"cipher"`
}

type authState struct {
	Entries map[string]authRecord `json:"entries"`
}

// authSide is what one side of the sync currently holds.
type authSide struct {
	present bool
	content string
	hash    string
}

func newAuthSide(content []byte) authSide {
	return authSide{present: true, content: string(content), hash: hashBytes(content)}
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *Syncer) authEntry() string {
	if s.AuthEntry != "" {
		return s.AuthEntry
	}
	return DefaultAuthEntry
}

// syncAuth keeps auth.json in sync with an encrypted pass entry. It runs a
// three-way comparison against the last synced state: a side that changed
// alone wins, and a change on both sides is a conflict that --adopt-pi
// resolves in favor of pass after backing up the local copy.
func (s *Syncer) syncAuth(ctx context.Context, agentDir string) error {
	if !s.SyncAuth {
		return nil
	}
	entry := s.authEntry()
	if _, ok := s.R.LookPath("pass"); !ok {
		s.Rep.Debugf("pi: pass is not installed, skipping %s sync", AuthFile)
		return nil
	}
	if s.PasswordStore == "" || !hasGPGID(s.PasswordStore, entry) {
		s.Rep.Debugf("pi: password store %s is not initialized for %s, skipping %s sync", s.PasswordStore, entry, AuthFile)
		return nil
	}

	localPath := filepath.Join(agentDir, AuthFile)
	cipherPath := filepath.Join(s.PasswordStore, entry+".gpg")
	key := agentDir + "\x00" + s.PasswordStore + "\x00" + entry

	state, err := loadAuthState(s.AuthState)
	if err != nil {
		return err
	}
	rec, hasRec := state.Entries[key]

	localKind, local, err := readLocalAuth(localPath)
	if err != nil {
		return err
	}
	cipher, err := readOptional(cipherPath)
	if err != nil {
		return err
	}
	cipherHash := ""
	if cipher != nil {
		cipherHash = hashBytes(cipher)
	}
	// An unchanged ciphertext means pass still holds the recorded base, so
	// most runs never decrypt and never prompt for a passphrase.
	passIsBase := hasRec && cipher != nil && cipherHash == rec.Cipher

	var pass authSide
	decrypt := func() error {
		out, err := s.passShow(ctx, entry)
		if err != nil {
			return err
		}
		pass = newAuthSide([]byte(out))
		return nil
	}

	record := func(base string) error {
		h, err := hashOptional(cipherPath)
		if err != nil {
			return err
		}
		state.Entries[key] = authRecord{Base: base, Cipher: h}
		return saveAuthState(s.AuthState, state)
	}

	pull := func(replace bool) error {
		if s.DryRun {
			s.Rep.Infof("pi: would update %s from pass %s", localPath, entry)
			return nil
		}
		if err := s.writeLocalAuth(ctx, localPath, local, localKind, pass.content, replace); err != nil {
			return err
		}
		s.Rep.Successf("pi: updated %s from pass %s", localPath, entry)
		return record(pass.hash)
	}

	push := func() error {
		if s.DryRun {
			s.Rep.Infof("pi: would store %s in pass %s", localPath, entry)
			return nil
		}
		if err := s.passInsert(ctx, entry, local.content); err != nil {
			return err
		}
		s.Rep.Successf("pi: stored %s in pass %s", localPath, entry)
		return record(local.hash)
	}

	adopt := func(content string, hash string) error {
		if s.DryRun {
			s.Rep.Infof("pi: would back up %s and replace it with pass %s", localPath, entry)
			return nil
		}
		backup, err := s.adoptLocalAuth(ctx, localPath, local, localKind, content)
		if err != nil {
			return err
		}
		s.Rep.Successf("pi: adopted %s from pass %s (backup %s)", localPath, entry, backup)
		return record(hash)
	}

	if localKind == kindSymlink {
		// Left over from the plaintext sync_auth mode, which linked auth.json
		// into the canonical Git tree.
		if !s.Adopt {
			return fmt.Errorf("%s is a symlink left by the plaintext sync_auth mode; rerun with --adopt-pi to replace it with a local file kept in pass %s", localPath, entry)
		}
		switch {
		case cipher != nil:
			if err := decrypt(); err != nil {
				return err
			}
			return adopt(pass.content, pass.hash)
		case local.present:
			if err := push(); err != nil {
				return err
			}
			return adopt(local.content, local.hash)
		default:
			s.Rep.Warnf("pi: %s is a broken symlink and pass %s does not exist; leaving it untouched", localPath, entry)
			return nil
		}
	}

	switch {
	case !local.present && cipher == nil:
		s.Rep.Debugf("pi: neither %s nor pass %s exist", localPath, entry)
		return nil
	case !local.present:
		if err := decrypt(); err != nil {
			return err
		}
		return pull(false)
	case cipher == nil:
		return push()
	case passIsBase:
		if local.hash == rec.Base {
			s.Rep.Debugf("pi: %s is in sync with pass %s", localPath, entry)
			return nil
		}
		return push()
	}

	if err := decrypt(); err != nil {
		return err
	}
	switch {
	case local.hash == pass.hash:
		if s.DryRun || (hasRec && rec.Base == local.hash && rec.Cipher == cipherHash) {
			return nil
		}
		return record(local.hash)
	case hasRec && local.hash == rec.Base:
		return pull(true)
	case hasRec && pass.hash == rec.Base:
		return push()
	case s.Adopt:
		return adopt(pass.content, pass.hash)
	default:
		return fmt.Errorf("%s and pass %s both changed since the last sync; rerun with --adopt-pi to back up the local copy and take the pass entry", localPath, entry)
	}
}

func (s *Syncer) passEnv() []string {
	return []string{"PASSWORD_STORE_DIR=" + s.PasswordStore}
}

func (s *Syncer) passShow(ctx context.Context, entry string) (string, error) {
	res, err := s.R.Run(ctx, runner.Spec{Name: "pass", Args: []string{"show", entry}, Extra: s.passEnv()})
	if err != nil {
		return "", fmt.Errorf("decrypting pass %s: %w", entry, err)
	}
	return res.Stdout, nil
}

func (s *Syncer) passInsert(ctx context.Context, entry, content string) error {
	_, err := s.R.Run(ctx, runner.Spec{Name: "pass", Args: []string{"insert", "--multiline", "--force", entry}, Extra: s.passEnv(), Stdin: content})
	if err != nil {
		return fmt.Errorf("storing %s in pass: %w", entry, err)
	}
	return nil
}

// hasGPGID reports whether pass can encrypt entry: a .gpg-id must exist in the
// entry's directory or one of its parents up to the store root.
func hasGPGID(store, entry string) bool {
	dir := filepath.Dir(filepath.Join(store, entry))
	for {
		if _, err := os.Stat(filepath.Join(dir, ".gpg-id")); err == nil {
			return true
		}
		if dir == store || len(dir) <= len(store) {
			return false
		}
		dir = filepath.Dir(dir)
	}
}

func readOptional(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return b, nil
}

func hashOptional(path string) (string, error) {
	b, err := readOptional(path)
	if err != nil || b == nil {
		return "", err
	}
	return hashBytes(b), nil
}

// readLocalAuth reads auth.json, following a symlink so a legacy link can be
// migrated with its current content.
func readLocalAuth(path string) (kind, authSide, error) {
	k, err := lstatKind(path)
	if err != nil {
		return k, authSide{}, err
	}
	switch k {
	case kindAbsent:
		return k, authSide{}, nil
	case kindFile, kindSymlink:
		b, err := readOptional(path)
		if err != nil {
			return k, authSide{}, err
		}
		if b == nil {
			return k, authSide{}, nil
		}
		return k, newAuthSide(b), nil
	default:
		return k, authSide{}, fmt.Errorf("%s is a %s, not a regular file", path, k)
	}
}

func loadAuthState(path string) (*authState, error) {
	st := &authState{Entries: map[string]authRecord{}}
	if path == "" {
		return st, nil
	}
	b, err := readOptional(path)
	if err != nil || b == nil {
		return st, err
	}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if st.Entries == nil {
		st.Entries = map[string]authRecord{}
	}
	return st, nil
}

func saveAuthState(path string, st *authState) error {
	if path == "" {
		return nil
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	tmp, err := writeTemp(filepath.Dir(path), ".pi-auth-state-", append(b, '\n'))
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// writeTemp writes content to a new owner-only file in dir and returns its path.
func writeTemp(dir, pattern string, content []byte) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", fmt.Errorf("creating a temporary file in %s: %w", dir, err)
	}
	name := f.Name()
	_, werr := f.Write(content)
	if werr == nil {
		werr = f.Chmod(0o600)
	}
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("writing %s: %w", name, werr)
	}
	return name, nil
}

// writeLocalAuth installs content as auth.json under Pi's own lock, after
// checking that auth.json still matches what the sync decision was based on.
// replace is only set when that content is the last synced base, so replacing
// it loses nothing.
func (s *Syncer) writeLocalAuth(ctx context.Context, path string, expected authSide, expectedKind kind, content string, replace bool) error {
	unlock, err := s.lockPiAuth(ctx, path)
	if err != nil {
		return err
	}
	defer unlock()
	if err := checkLocalAuthUnchanged(path, expected, expectedKind); err != nil {
		return err
	}
	tmp, err := writeTemp(filepath.Dir(path), ".auth.json.rc-", []byte(content))
	if err != nil {
		return err
	}
	if replace {
		err = os.Rename(tmp, path)
	} else {
		err = s.rename()(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("installing %s: %w", path, err)
	}
	return nil
}

// adoptLocalAuth moves the local auth.json, or the legacy symlink, to a backup
// and installs content in its place.
func (s *Syncer) adoptLocalAuth(ctx context.Context, path string, expected authSide, expectedKind kind, content string) (string, error) {
	unlock, err := s.lockPiAuth(ctx, path)
	if err != nil {
		return "", err
	}
	defer unlock()
	if err := checkLocalAuthUnchanged(path, expected, expectedKind); err != nil {
		return "", err
	}
	tmp, err := writeTemp(filepath.Dir(path), ".auth.json.rc-", []byte(content))
	if err != nil {
		return "", err
	}
	backup, err := nextFreeBackup(path)
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := s.moveNoReplace(path, backup); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := s.secureBackup(backup); err != nil {
		_ = os.Remove(tmp)
		return backup, err
	}
	if err := s.rename()(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return backup, fmt.Errorf("installing %s: %w (the previous copy is at %s)", path, err, backup)
	}
	return backup, nil
}

func checkLocalAuthUnchanged(path string, expected authSide, expectedKind kind) error {
	k, now, err := readLocalAuth(path)
	if err != nil {
		return err
	}
	if k != expectedKind || now.present != expected.present || now.hash != expected.hash {
		return fmt.Errorf("%s changed during the sync; rerun rc sync", path)
	}
	return nil
}

// lockPiAuth takes the proper-lockfile lock Pi uses for auth.json, so Pi never
// writes the file between rc's check and its write. A lock older than Pi's
// stale threshold is reclaimed, as proper-lockfile itself does.
func (s *Syncer) lockPiAuth(ctx context.Context, path string) (func(), error) {
	lockDir := path + ".lock"
	wait := s.piLockWait
	if wait == 0 {
		wait = defaultPiLockWait
	}
	deadline := time.Now().Add(wait)
	for {
		err := os.Mkdir(lockDir, 0o755)
		if err == nil {
			return func() { _ = os.Remove(lockDir) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("locking %s: %w", path, err)
		}
		if info, statErr := os.Stat(lockDir); statErr == nil && time.Since(info.ModTime()) > piLockStale {
			_ = os.Remove(lockDir)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s is locked by a running Pi (%s); retry once it finishes", path, lockDir)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// warnPlaintextAuth flags credentials left in the canonical Git tree by the
// former plaintext sync_auth mode. rc never deletes them.
func (s *Syncer) warnPlaintextAuth(source string) {
	for _, p := range []string{filepath.Join(source, AuthFile), filepath.Join(source, HostDir, s.Host, AuthFile)} {
		if k, err := lstatKind(p); err == nil && k != kindAbsent {
			s.Rep.Warnf("pi: %s holds plaintext credentials that rc no longer uses; remove it from Git once pass holds them", p)
		}
	}
}
