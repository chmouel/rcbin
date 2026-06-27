package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func checksumFor(data []byte, name string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[len(fields)-1] != name {
			continue
		}
		sum := fields[0]
		if len(sum) != sha256.Size*2 {
			return "", fmt.Errorf("invalid checksum for %s", name)
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return "", fmt.Errorf("invalid checksum for %s: %w", name, err)
		}
		return strings.ToLower(sum), nil
	}
	return "", fmt.Errorf("checksums.txt has no entry for %s", name)
}

func extractRCBinary(archivePath, dir string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	gz, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("opening %s: %w", archivePath, err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", archivePath, err)
		}
		if filepath.Base(filepath.Clean(header.Name)) != "rc" {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return "", fmt.Errorf("archive entry %s is not a regular file", header.Name)
		}
		if header.Size <= 0 || header.Size > maxBinarySize {
			return "", fmt.Errorf("archive entry %s has invalid size %d", header.Name, header.Size)
		}

		tmp, err := os.CreateTemp(dir, ".rc-new-*")
		if err != nil {
			return "", err
		}
		tmpName := tmp.Name()
		written, err := io.CopyN(tmp, tr, header.Size)
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
			return "", fmt.Errorf("extracting rc binary: %w", err)
		}
		if written != header.Size {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
			return "", fmt.Errorf("extracting rc binary: wrote %d bytes, want %d", written, header.Size)
		}
		if err := tmp.Close(); err != nil {
			_ = os.Remove(tmpName)
			return "", err
		}
		mode := header.FileInfo().Mode().Perm()
		if mode&0o111 == 0 {
			mode = 0o755
		}
		if err := os.Chmod(tmpName, mode); err != nil {
			_ = os.Remove(tmpName)
			return "", err
		}
		return tmpName, nil
	}
	return "", fmt.Errorf("%s does not contain an rc binary", archivePath)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rc-completion-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
