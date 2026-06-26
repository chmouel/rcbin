package config

import (
	"fmt"
	"os"
	"path/filepath"
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

func TestParseHostRCBasic(t *testing.T) {
	content := `atuin
?tmux
pylint/pylintrc pylintrc
readline/inputrc ~/.inputrc
.local/share/desktop-config/krb5/krb5.conf /etc/krb5.conf
.local/share/desktop-config/jira ~/.config/.jira
?$GOPATH/bin/goimports .local/bin/goimports
# a comment
`
	links := parseHostRC(content, "")
	byTarget := map[string]Link{}
	for _, l := range links {
		byTarget[l.Target] = l
	}

	if l := byTarget["~/.config/atuin"]; l.SourceRoot != "rc" || l.Source != "atuin" {
		t.Errorf("atuin: got %+v", l)
	}
	if l := byTarget["~/.config/tmux"]; !l.Optional {
		t.Errorf("tmux should be optional: %+v", l)
	}
	if l := byTarget["~/.config/pylintrc"]; l.SourceRoot != "home" || l.Source != "pylint/pylintrc" {
		t.Errorf("pylintrc (no rc assets root): got %+v", l)
	}
	if l := byTarget["/etc/krb5.conf"]; !l.Privileged {
		t.Errorf("krb5 should be privileged: %+v", l)
	}
	if l := byTarget["~/.config/.jira"]; l.SourceRoot != "home" || l.Source != ".local/share/desktop-config/jira" {
		t.Errorf("jira: got %+v", l)
	}
	if l := byTarget["~/.local/bin/goimports"]; l.SourceRoot != "" || l.Source != "${GOPATH}/bin/goimports" || !l.Optional {
		t.Errorf("goimports: got %+v", l)
	}
}

func TestParseHostRCWithAssetsRoot(t *testing.T) {
	assets := t.TempDir()
	if err := os.MkdirAll(filepath.Join(assets, "pylint"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "pylint", "pylintrc"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	links := parseHostRC("pylint/pylintrc pylintrc\n", assets)
	if links[0].SourceRoot != "rc" {
		t.Errorf("with assets present, source should be rc: %+v", links[0])
	}
}

func TestParseHostBins(t *testing.T) {
	content := `git/gh-clone
graphical/copy-path :: rf
perso/x :: .config/zsh/funcs/$HOST/x
?maybe/missing
`
	bins := parseHostBinList(content, "chmouzies")
	if bins[0].SourceRoot != "chmouzies" || bins[0].Target != "gh-clone" {
		t.Errorf("gh-clone: %+v", bins[0])
	}
	if bins[1].Target != "rf" {
		t.Errorf("bare alias target should be desktop-bin name: %+v", bins[1])
	}
	if bins[2].Target != "~/.config/zsh/funcs/${HOST}/x" {
		t.Errorf("slashed alias target: %+v", bins[2])
	}
	if !bins[3].Optional {
		t.Errorf("optional bin: %+v", bins[3])
	}
}

func TestParseHostExtraDirs(t *testing.T) {
	content := `pac/infra
perso/lazyworktree post_update={ make build }
perso/x always={ echo hi | cat }
`
	repos := parseHostExtraDirs(content)
	if repos[0].Path != "pac/infra" || repos[0].Hooks.PostUpdate != nil {
		t.Errorf("infra: %+v", repos[0])
	}
	if repos[1].Hooks.PostUpdate == nil || len(repos[1].Hooks.PostUpdate.Argv) != 2 {
		t.Errorf("lazyworktree post_update should be argv [make build]: %+v", repos[1].Hooks)
	}
	if repos[2].Hooks.Always == nil || repos[2].Hooks.Always.Shell == "" {
		t.Errorf("piped hook should be shell form: %+v", repos[2].Hooks)
	}
}

func TestLoadHostHostProfiles(t *testing.T) {
	home := t.TempDir()
	hosts := filepath.Join(home, ".config", "yadm", "hosts")
	common := filepath.Join(hosts, "common")
	exact := filepath.Join(hosts, "ibra")
	for _, dir := range []string{common, exact} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	globalPath := filepath.Join(home, ".config", "rc", "config.toml")
	if err := os.MkdirAll(filepath.Dir(globalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	global := fmt.Sprintf("version = 1\n\n[roots]\nchmouzies = %q\n", filepath.Join(home, "chmouzies"))
	if err := os.WriteFile(globalPath, []byte(global), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(common, "rc"), []byte("git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(common, "chmouzies"), []byte("git/gh-clone\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(common, "extra-dirs"), []byte("perso/lazyworktree post_update={ make build }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exact, "rc"), []byte("git-host git\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Options{
		GlobalPath: globalPath,
		HostsRoot:  hosts,
		Hostname:   "ibra",
		Vars:       Vars{"HOME": home, "HOST": "ibra", "GOPATH": filepath.Join(home, "go")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Links) != 1 {
		t.Fatalf("expected exact host to override common link, got %d links", len(cfg.Links))
	}
	if got, want := cfg.Links[0].Source, filepath.Join(home, ".local", "share", "rc", "git-host"); got != want {
		t.Errorf("link source = %q, want %q", got, want)
	}
	if got, want := cfg.Links[0].Target, filepath.Join(home, ".config", "git"); got != want {
		t.Errorf("link target = %q, want %q", got, want)
	}
	if len(cfg.Bins) != 1 {
		t.Fatalf("expected 1 bin, got %d", len(cfg.Bins))
	}
	if got, want := cfg.Bins[0].Source, filepath.Join(home, "chmouzies", "git", "gh-clone"); got != want {
		t.Errorf("bin source = %q, want %q", got, want)
	}
	if got, want := cfg.Bins[0].Target, filepath.Join(home, ".local", "bin", "desktop", "gh-clone"); got != want {
		t.Errorf("bin target = %q, want %q", got, want)
	}
	if len(cfg.Repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(cfg.Repos))
	}
	if got, want := cfg.Repos[0].Path, filepath.Join(home, "git", "perso", "lazyworktree"); got != want {
		t.Errorf("repo path = %q, want %q", got, want)
	}
	if cfg.Repos[0].Hooks.PostUpdate == nil || strings.Join(cfg.Repos[0].Hooks.PostUpdate.Argv, " ") != "make build" {
		t.Errorf("repo hook = %+v, want post_update argv [make build]", cfg.Repos[0].Hooks)
	}
}

func TestLoadHostHostPayloadsAndSystemd(t *testing.T) {
	home := t.TempDir()
	hosts := filepath.Join(home, ".config", "yadm", "hosts")
	common := filepath.Join(hosts, "common")
	exact := filepath.Join(hosts, "ibra")
	for _, dir := range []string{common, exact} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	writeConfigTestFile(t, filepath.Join(common, "emacs", "init.el"), "common emacs")
	writeConfigTestFile(t, filepath.Join(exact, "emacs", "init.el"), "exact emacs")
	writeConfigTestFile(t, filepath.Join(common, "shell", "init.zsh"), "common init")
	writeConfigTestFile(t, filepath.Join(exact, "shell", "init.zsh"), "exact init")
	writeConfigTestFile(t, filepath.Join(exact, "shell", "post.zsh"), "exact post")
	writeConfigTestFile(t, filepath.Join(common, "shell", "functions", "shared"), "common function")
	writeConfigTestFile(t, filepath.Join(exact, "shell", "functions", "shared"), "exact function")
	writeConfigTestFile(t, filepath.Join(common, "bin", "rhpass"), "common bin")
	writeConfigTestFile(t, filepath.Join(exact, "bin", "rhpass"), "exact bin")

	rcRoot := filepath.Join(home, ".local", "share", "rc")
	writeConfigTestFile(t, filepath.Join(rcRoot, "systemd", "demo.service"), "[Service]\n")
	if err := os.MkdirAll(filepath.Join(home, ".config", "systemd", "user"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Options{
		GlobalPath: filepath.Join(home, ".config", "rc", "config.toml"),
		HostsRoot:  hosts,
		Hostname:   "ibra",
		Vars:       Vars{"HOME": home, "HOST": "ibra"},
	})
	if err != nil {
		t.Fatal(err)
	}

	links := map[string]ResolvedLink{}
	for _, link := range cfg.Links {
		links[link.Target] = link
	}
	emacsTarget := filepath.Join(home, ".config", "emacs", "lisp", "init-local.el")
	if got, want := links[emacsTarget].Source, filepath.Join(common, "emacs", "init.el"); got != want {
		t.Errorf("emacs singleton source = %q, want first existing %q", got, want)
	}
	initTarget := filepath.Join(home, ".config", "zsh", "hosts", "ibra.sh")
	if got, want := links[initTarget].Source, filepath.Join(common, "shell", "init.zsh"); got != want {
		t.Errorf("shell init singleton source = %q, want first existing %q", got, want)
	}
	postTarget := filepath.Join(home, ".config", "zsh", "hosts", "ibra-post.sh")
	if got, want := links[postTarget].Source, filepath.Join(exact, "shell", "post.zsh"); got != want {
		t.Errorf("shell post source = %q, want %q", got, want)
	}
	functionTarget := filepath.Join(home, ".config", "zsh", "functions", "hosts", "ibra", "shared")
	if got, want := links[functionTarget].Source, filepath.Join(exact, "shell", "functions", "shared"); got != want {
		t.Errorf("shell function source = %q, want exact host override %q", got, want)
	}
	systemdTarget := filepath.Join(home, ".config", "systemd", "user", "demo.service")
	if got, want := links[systemdTarget].Source, filepath.Join(rcRoot, "systemd", "demo.service"); got != want {
		t.Errorf("systemd source = %q, want %q", got, want)
	}

	if len(cfg.Bins) != 1 {
		t.Fatalf("expected one resolved host bin after exact override, got %d", len(cfg.Bins))
	}
	if got, want := cfg.Bins[0].Source, filepath.Join(exact, "bin", "rhpass"); got != want {
		t.Errorf("host bin source = %q, want exact host override %q", got, want)
	}
	if got, want := cfg.Bins[0].Target, filepath.Join(home, ".local", "bin", "desktop", "rhpass"); got != want {
		t.Errorf("host bin target = %q, want %q", got, want)
	}
}

func TestLoadHostRepoBinsGOPATHFallback(t *testing.T) {
	home := t.TempDir()
	gopath := filepath.Join(home, "go")
	hosts := filepath.Join(home, ".config", "yadm", "hosts")
	profile := filepath.Join(hosts, "common")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(gopath, "src", "github.com", "acme", "tool")
	writeConfigTestFile(t, source, "tool")
	if err := os.WriteFile(filepath.Join(profile, "repobins"), []byte("acme/tool\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Options{
		GlobalPath: filepath.Join(home, ".config", "rc", "config.toml"),
		HostsRoot:  hosts,
		Hostname:   "ibra",
		Vars:       Vars{"HOME": home, "HOST": "ibra", "GOPATH": gopath},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Bins) != 1 {
		t.Fatalf("expected 1 repobin, got %d", len(cfg.Bins))
	}
	if got := cfg.Bins[0].Source; got != source {
		t.Errorf("repobin source = %q, want GOPATH fallback %q", got, source)
	}
}

func writeConfigTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadIgnoresHostTOML(t *testing.T) {
	home := t.TempDir()
	hosts := filepath.Join(home, ".config", "yadm", "hosts")
	profile := filepath.Join(hosts, "ibra")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "rc.toml"), []byte("%%% invalid toml"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Options{
		GlobalPath: filepath.Join(home, ".config", "rc", "config.toml"),
		HostsRoot:  hosts,
		Hostname:   "ibra",
		Vars:       Vars{"HOME": home, "HOST": "ibra"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Links) != 0 || len(cfg.Bins) != 0 || len(cfg.Repos) != 0 {
		t.Fatalf("host rc.toml should be ignored, got links=%d bins=%d repos=%d", len(cfg.Links), len(cfg.Bins), len(cfg.Repos))
	}
}

func TestLoadHostDuplicateTargetsError(t *testing.T) {
	home := t.TempDir()
	hosts := filepath.Join(home, ".config", "yadm", "hosts")
	profile := filepath.Join(hosts, "common")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "rc"), []byte("a x\nb x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(Options{
		GlobalPath: filepath.Join(home, ".config", "rc", "config.toml"),
		HostsRoot:  hosts,
		Hostname:   "ibra",
		Vars:       Vars{"HOME": home, "HOST": "ibra"},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate link target") {
		t.Fatalf("expected duplicate link target error, got %v", err)
	}
}

func TestLoadHostExactDuplicateTargetsAreIgnored(t *testing.T) {
	home := t.TempDir()
	hosts := filepath.Join(home, ".config", "yadm", "hosts")
	profile := filepath.Join(hosts, "common")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "rc"), []byte("a x\na x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Options{
		GlobalPath: filepath.Join(home, ".config", "rc", "config.toml"),
		HostsRoot:  hosts,
		Hostname:   "ibra",
		Vars:       Vars{"HOME": home, "HOST": "ibra"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Links) != 1 {
		t.Fatalf("expected exact duplicate link to be deduped, got %d", len(cfg.Links))
	}
}

func TestLoadHostDuplicateBinTargetsError(t *testing.T) {
	home := t.TempDir()
	hosts := filepath.Join(home, ".config", "yadm", "hosts")
	profile := filepath.Join(hosts, "common")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "chmouzies"), []byte("a\nb :: a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(Options{
		GlobalPath: filepath.Join(home, ".config", "rc", "config.toml"),
		HostsRoot:  hosts,
		Hostname:   "ibra",
		Vars:       Vars{"HOME": home, "HOST": "ibra"},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate bin target") {
		t.Fatalf("expected duplicate bin target error, got %v", err)
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
	if _, ok := byTarget["/home/u/.local/bin/desktop/gh-clone"]; !ok {
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
