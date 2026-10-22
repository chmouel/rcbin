//go:build darwin

package pi

import "testing"

func TestRenameNoReplaceDarwin(t *testing.T) {
	testRenameNoReplace(t)
}
