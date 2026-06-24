package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestWaybarZero(t *testing.T) {
	s, err := Waybar(nil)
	if err != nil {
		t.Fatal(err)
	}
	var p WaybarPayload
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		t.Fatalf("invalid JSON: %v (%s)", err, s)
	}
	if p.Text != "0" {
		t.Errorf("expected text 0, got %q", p.Text)
	}
	if p.Tooltip != "" {
		t.Errorf("expected empty tooltip, got %q", p.Tooltip)
	}
}

func TestWaybarMany(t *testing.T) {
	s, err := Waybar([]string{"/a", "/b", "/c"})
	if err != nil {
		t.Fatal(err)
	}
	var p WaybarPayload
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if p.Text != "3" {
		t.Errorf("expected text 3, got %q", p.Text)
	}
	if p.Tooltip != "/a\n/b\n/c" {
		t.Errorf("unexpected tooltip %q", p.Tooltip)
	}
}

func TestWaybarEscaping(t *testing.T) {
	s, err := Waybar([]string{"a\"b", "c\td"})
	if err != nil {
		t.Fatal(err)
	}
	var p WaybarPayload
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		t.Fatalf("special characters must produce valid JSON: %v", err)
	}
}

func TestReporterHumanOutputUsesStderr(t *testing.T) {
	var out, errBuf bytes.Buffer
	rep := New(&out, &errBuf, false, false)

	rep.Successf("done")
	rep.Failf("bad")
	rep.Skipf("skip")
	rep.Println("plain")
	rep.Printf("format %s", "line")

	if out.String() != "" {
		t.Fatalf("human reporter output should not use stdout, got %q", out.String())
	}
	got := errBuf.String()
	for _, want := range []string{"done", "bad", "skip", "plain", "format line"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stderr missing %q in %q", want, got)
		}
	}
}
