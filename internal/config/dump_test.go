package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDumpTOMLResolvedConfigIsDeterministic(t *testing.T) {
	layer := File{
		Roots: map[string]string{
			"zroot": "~/z",
			"aroot": "~/a",
		},
		Git: GitConfig{
			Provider: "git@example.com:me",
			Repositories: []DefaultRepo{{
				Path:  "${HOME}/git/repo",
				Clone: "repo",
			}},
		},
		Links: []Link{{
			SourceRoot: "aroot",
			Source:     "source",
			Target:     ".target",
			Optional:   true,
		}},
		Updates: []UpdateTask{{
			Name:     "custom",
			Commands: []Command{{Shell: "echo ok"}},
		}},
	}
	cfg, err := Build([]File{Defaults(), layer}, testVars(), "ibra")
	if err != nil {
		t.Fatal(err)
	}

	first, err := Dump(cfg, DumpFormatTOML)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Dump(cfg, DumpFormatTOML)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("dump output should be deterministic:\nfirst:\n%s\nsecond:\n%s", first, second)
	}

	out := string(first)
	for _, want := range []string{
		"hostname = 'ibra'",
		"provider = 'git@example.com:me'",
		"aroot = '/home/u/a'",
		"zroot = '/home/u/z'",
		"source = '/home/u/a/source'",
		"target = '/home/u/.target'",
		"path = '/home/u/git/repo'",
		"shell = 'echo ok'",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dump missing %q:\n%s", want, out)
		}
	}
}

func TestDumpJSONResolvedConfig(t *testing.T) {
	cfg, err := Build([]File{Defaults()}, testVars(), "ibra")
	if err != nil {
		t.Fatal(err)
	}
	data, err := Dump(cfg, DumpFormatJSON)
	if err != nil {
		t.Fatal(err)
	}

	var payload dumpConfig
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Hostname != "ibra" {
		t.Errorf("hostname = %q, want ibra", payload.Hostname)
	}
	if payload.Roots["home"] != "/home/u" {
		t.Errorf("home root = %q, want /home/u", payload.Roots["home"])
	}
	if payload.Sync.Concurrency != 4 {
		t.Errorf("concurrency = %d, want 4", payload.Sync.Concurrency)
	}
	if len(payload.Updates) == 0 {
		t.Errorf("expected default updates in dump")
	}
}

func TestDumpRejectsUnknownFormat(t *testing.T) {
	cfg, err := Build([]File{Defaults()}, testVars(), "ibra")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Dump(cfg, "yaml")
	if err == nil || !strings.Contains(err.Error(), "toml or json") {
		t.Fatalf("expected format error, got %v", err)
	}
}
