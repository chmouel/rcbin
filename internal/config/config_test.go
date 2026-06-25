package config

import (
	"strings"
	"testing"
)

func testVars() Vars {
	return Vars{"HOME": "/home/u", "HOST": "ibra", "GOPATH": "/home/u/go"}
}

func TestExpand(t *testing.T) {
	vars := testVars()
	cases := []struct {
		in, want string
	}{
		{"~", "/home/u"},
		{"~/.config", "/home/u/.config"},
		{"${HOME}/git", "/home/u/git"},
		{"${GOPATH}/src/${HOST}", "/home/u/go/src/ibra"},
		{"no/vars", "no/vars"},
	}
	for _, c := range cases {
		got, err := expand(c.in, vars)
		if err != nil {
			t.Fatalf("expand(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("expand(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestExpandUnsetVar(t *testing.T) {
	vars := testVars()
	delete(vars, "GOPATH")
	_, err := expand("${GOPATH}/x", vars)
	if err == nil || !strings.Contains(err.Error(), "GOPATH") {
		t.Fatalf("expected unset-var error, got %v", err)
	}
}

func TestExpandUnsupportedVar(t *testing.T) {
	_, err := expand("${NOPE}/x", Vars{"HOME": "/home/u", "HOST": "ibra", "GOPATH": "/go", "NOPE": "/tmp"})
	if err == nil || !strings.Contains(err.Error(), "unsupported") || !strings.Contains(err.Error(), "NOPE") {
		t.Fatalf("expected unsupported-var error, got %v", err)
	}
}

func TestEnvVarsOnlyExposeAllowedVariables(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv("HOST", "ambient-host")
	t.Setenv("GOPATH", "")
	t.Setenv("NOPE", "/tmp/nope")

	vars := EnvVars("configured-host")
	if vars["HOME"] != "/home/u" {
		t.Fatalf("HOME = %q, want /home/u", vars["HOME"])
	}
	if vars["HOST"] != "configured-host" {
		t.Fatalf("HOST = %q, want configured-host", vars["HOST"])
	}
	if _, ok := vars["GOPATH"]; ok {
		t.Fatal("GOPATH should not be synthesized when unset")
	}
	if _, ok := vars["NOPE"]; ok {
		t.Fatal("unsupported environment variable should not be exposed")
	}
}

func TestBooleanScalarCanOverrideDefaultToFalse(t *testing.T) {
	no := false
	cfg, err := Build([]File{Defaults(), {Tools: ToolsLayer{PreferEmacs: &no}}}, testVars(), "ibra")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools.PreferEmacs {
		t.Fatal("expected explicit prefer_emacs=false to override default true")
	}
}

func TestMergeOrderingAndOverride(t *testing.T) {
	base := Defaults()
	common := File{
		Links: []Link{{SourceRoot: "rc", Source: "git", Target: "~/.config/git"}},
	}
	exact := File{
		// Override the common link's target with a different source.
		Links: []Link{{SourceRoot: "rc", Source: "git-host", Target: "~/.config/git"}},
	}
	cfg, err := Build([]File{base, common, exact}, testVars(), "ibra")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Links) != 1 {
		t.Fatalf("expected 1 link after override, got %d", len(cfg.Links))
	}
	if !strings.HasSuffix(cfg.Links[0].Source, "/git-host") {
		t.Errorf("exact host should win, got source %q", cfg.Links[0].Source)
	}
	if cfg.Links[0].Target != "/home/u/.config/git" {
		t.Errorf("unexpected target %q", cfg.Links[0].Target)
	}
}

func TestDuplicateWithinLayerIsError(t *testing.T) {
	layer := File{
		Links: []Link{
			{SourceRoot: "rc", Source: "a", Target: "~/.config/x"},
			{SourceRoot: "rc", Source: "b", Target: "~/.config/x"},
		},
	}
	_, err := Build([]File{Defaults(), layer}, testVars(), "ibra")
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestBinTargetResolution(t *testing.T) {
	layer := File{
		Bins: []Bin{
			{SourceRoot: "rc", Source: "git/gh-clone", Target: "gh-clone"},
			{SourceRoot: "rc", Source: "x", Target: "sub/dir/name"},
		},
	}
	cfg, err := Build([]File{Defaults(), layer}, testVars(), "ibra")
	if err != nil {
		t.Fatal(err)
	}
	byTarget := map[string]ResolvedBin{}
	for _, b := range cfg.Bins {
		byTarget[b.Target] = b
	}
	if _, ok := byTarget["/home/u/.local/bin/gh-clone"]; !ok {
		t.Errorf("bare bin target not placed under desktop_bin: %v", cfg.Bins)
	}
	if _, ok := byTarget["/home/u/sub/dir/name"]; !ok {
		t.Errorf("slashed bin target not placed under home: %v", cfg.Bins)
	}
}

func TestCommandFormValidation(t *testing.T) {
	bad := File{
		Updates: []UpdateTask{{Name: "x", Commands: []Command{{Argv: []string{"a"}, Shell: "b"}}}},
	}
	if _, err := Build([]File{Defaults(), bad}, testVars(), "ibra"); err == nil {
		t.Fatal("expected error for command with both forms")
	}

	none := File{
		Updates: []UpdateTask{{Name: "y", Commands: []Command{{}}}},
	}
	if _, err := Build([]File{Defaults(), none}, testVars(), "ibra"); err == nil {
		t.Fatal("expected error for command with neither form")
	}
}

func TestUnknownSourceRoot(t *testing.T) {
	layer := File{Links: []Link{{SourceRoot: "nope", Source: "a", Target: "~/x"}}}
	_, err := Build([]File{Defaults(), layer}, testVars(), "ibra")
	if err == nil || !strings.Contains(err.Error(), "source_root") {
		t.Fatalf("expected unknown source_root error, got %v", err)
	}
}

func TestDefaultsResolve(t *testing.T) {
	cfg, err := Build([]File{Defaults()}, testVars(), "ibra")
	if err != nil {
		t.Fatalf("defaults should resolve cleanly: %v", err)
	}
	if cfg.WorkerLimit() != 4 {
		t.Errorf("expected worker limit 4, got %d", cfg.WorkerLimit())
	}
	if len(cfg.Updates) == 0 {
		t.Error("expected default update tasks")
	}
	// Defaults must stay neutral: no personal provider, backup remote, repos, or
	// backup tasks are baked into the binary.
	if cfg.Provider != "" {
		t.Errorf("expected empty default provider, got %q", cfg.Provider)
	}
	if cfg.Yadm.Remote != "" {
		t.Errorf("expected empty default yadm remote, got %q", cfg.Yadm.Remote)
	}
	if len(cfg.Repos) != 0 {
		t.Errorf("expected no default repositories, got %v", cfg.Repos)
	}
	if len(cfg.Backups) != 0 {
		t.Errorf("expected no default backups, got %v", cfg.Backups)
	}
}

func TestBackupOutputExpandsHost(t *testing.T) {
	layer := File{
		Roots: map[string]string{"cfgrepo": "~/cfg"},
		Backups: []BackupTask{{
			Name:    "dconf",
			Command: Command{Argv: []string{"true"}},
			Repo:    "cfgrepo",
			Output:  "dconf/dconf.reg-${HOST}",
		}},
	}
	cfg, err := Build([]File{Defaults(), layer}, testVars(), "ibra")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Backups) != 1 {
		t.Fatalf("expected 1 backup, got %d", len(cfg.Backups))
	}
	if got := cfg.Backups[0].Output; got != "/home/u/cfg/dconf/dconf.reg-ibra" {
		t.Errorf("backup output not expanded under repo root: %q", got)
	}
}
