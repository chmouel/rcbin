//go:build linux

package pi

import "testing"

func TestRenameNoReplaceLinux(t *testing.T) {
	testRenameNoReplace(t)
}
