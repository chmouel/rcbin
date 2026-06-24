package host

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"Ibra":             "ibra",
		"ibra.example.com": "ibra",
		"  MAXIMUS\n":      "maximus",
	}
	for in, want := range cases {
		if got := normalize(in); got != want {
			t.Errorf("normalize(%q)=%q want %q", in, got, want)
		}
	}
}

func TestProfilesOrdering(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"common", "ibra", "ibra,maximus", "alpha,ibra", "zeta,ibra", "other", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	got, err := Profiles(root, "ibra")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(root, "common"),
		filepath.Join(root, "alpha,ibra"),
		filepath.Join(root, "ibra,maximus"),
		filepath.Join(root, "zeta,ibra"),
		filepath.Join(root, "ibra"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Profiles ordering mismatch:\n got=%v\nwant=%v", got, want)
	}
}

func TestProfilesMissingRoot(t *testing.T) {
	got, err := Profiles(filepath.Join(t.TempDir(), "nope"), "ibra")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no profiles, got %v", got)
	}
}
