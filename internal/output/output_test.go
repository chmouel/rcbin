package output

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
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

func TestStyleHelpersColorOff(t *testing.T) {
	rep := New(io.Discard, io.Discard, false, false)
	cases := map[string]string{
		"Bold":   rep.Bold("x"),
		"Dim":    rep.Dim("x"),
		"Accent": rep.Accent("x"),
		"Key":    rep.Key("x"),
		"Good":   rep.Good("x"),
		"Bad":    rep.Bad("x"),
	}
	for name, got := range cases {
		if got != "x" {
			t.Errorf("%s with color off must be plain, got %q", name, got)
		}
	}
	if strings.Contains(rep.RuleString("title"), "\033[") {
		t.Errorf("RuleString with color off must not emit ANSI, got %q", rep.RuleString("title"))
	}
	if !strings.Contains(rep.RuleString("title"), "title") {
		t.Errorf("RuleString must contain the title, got %q", rep.RuleString("title"))
	}
}

func TestStyleHelpersColorOn(t *testing.T) {
	rep := New(io.Discard, io.Discard, true, false)
	for name, got := range map[string]string{
		"Bold":   rep.Bold("x"),
		"Accent": rep.Accent("x"),
		"Key":    rep.Key("x"),
		"Good":   rep.Good("x"),
	} {
		if !strings.Contains(got, "\033[") || !strings.HasSuffix(got, "\033[0m") {
			t.Errorf("%s with color on must wrap in ANSI, got %q", name, got)
		}
	}
	if !strings.Contains(rep.SuccessLine("done"), "\033[") {
		t.Errorf("SuccessLine with color on must emit ANSI, got %q", rep.SuccessLine("done"))
	}
}

func TestColorForRespectsNoColorEnv(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if ColorFor(os.Stdout, false) {
		t.Error("NO_COLOR set must disable color")
	}
}

func TestColorForDisabledFlag(t *testing.T) {
	if ColorFor(os.Stdout, true) {
		t.Error("explicit disable must win")
	}
}

func TestColorForNonTerminal(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	os.Unsetenv("NO_COLOR")
	var buf bytes.Buffer
	if ColorFor(&buf, false) {
		t.Error("non-terminal writer must disable color")
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
