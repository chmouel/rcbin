package migrate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chmouel/rc/internal/config"
)

func TestParseRCBasic(t *testing.T) {
	content := `atuin
?tmux
pylint/pylintrc pylintrc
readline/inputrc ~/.inputrc
.local/share/desktop-config/krb5/krb5.conf /etc/krb5.conf
.local/share/desktop-config/jira ~/.config/.jira
?$GOPATH/bin/goimports .local/bin/goimports
# a comment
`
	// rcAssetsRoot empty: slashed sources that are not absolute fall back to home.
	links := parseRC(content, "")
	byTarget := map[string]config.Link{}
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

func TestParseRCWithAssetsRoot(t *testing.T) {
	assets := t.TempDir()
	if err := os.MkdirAll(filepath.Join(assets, "pylint"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "pylint", "pylintrc"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	links := parseRC("pylint/pylintrc pylintrc\n", assets)
	if links[0].SourceRoot != "rc" {
		t.Errorf("with assets present, source should be rc: %+v", links[0])
	}
}

func TestParseChmouzies(t *testing.T) {
	content := `git/gh-clone
graphical/copy-path :: rf
perso/x :: .config/zsh/funcs/$HOST/x
?maybe/missing
`
	bins := parseBinList(content, "chmouzies")
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

func TestParseExtraDirs(t *testing.T) {
	content := `pac/infra
perso/lazyworktree post_update={ make build }
perso/x always={ echo hi | cat }
`
	repos := parseExtraDirs(content)
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

func TestRenderRoundTrips(t *testing.T) {
	overlay := overlayOut{
		Version: 1,
		Links:   []config.Link{{SourceRoot: "rc", Source: "git", Target: "~/.config/git"}},
		Bins:    []config.Bin{{SourceRoot: "chmouzies", Source: "git/gh-clone", Target: "gh-clone", DiscoverCompletion: true}},
		Repositories: []repoOut{
			toRepoOut(config.Repository{Path: "perso/lazyworktree", Hooks: config.Hooks{PostUpdate: &config.Command{Argv: []string{"make", "build"}}}}),
		},
	}
	data, err := Render(overlay, []string{"example warning"})
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "rc.toml")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	file, found, err := config.ReadFile(path)
	if err != nil || !found {
		t.Fatalf("generated TOML must parse: found=%v err=%v", found, err)
	}
	vars := config.Vars{"HOME": "/home/u", "HOST": "ibra", "GOPATH": "/home/u/go"}
	// Migrated overlays reference the legacy "chmouzies" root, which a migrated
	// user defines in their global config (see examples/config.toml); supply it
	// here so the overlay validates in isolation.
	globalRoots := config.File{Roots: map[string]string{"chmouzies": "~/.local/share/chmouzies"}}
	cfg, err := config.Build([]config.File{config.Defaults(), globalRoots, file}, vars, "ibra")
	if err != nil {
		t.Fatalf("generated TOML must validate: %v", err)
	}
	if len(cfg.Links) == 0 || len(cfg.Bins) == 0 {
		t.Error("round-tripped config lost links or bins")
	}
}

// TestMigrateRealHostFiles converts the live legacy configuration when present
// and asserts every generated overlay parses and validates.
func TestMigrateRealHostFiles(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	legacy := filepath.Join(home, ".config", "yadm", "hosts")
	if _, err := os.Stat(legacy); err != nil {
		t.Skip("no legacy host config on this machine")
	}

	out := t.TempDir()
	results, err := Migrate(Options{
		LegacyRoot:   legacy,
		OutputRoot:   out,
		RCAssetsRoot: filepath.Join(home, ".local", "share", "rc"),
		RepoBase:     filepath.Join(home, "git"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Skip("no convertible host files")
	}

	vars := config.Vars{"HOME": "/home/u", "HOST": "ibra", "GOPATH": "/home/u/go"}
	globalRoots := config.File{Roots: map[string]string{"chmouzies": "~/.local/share/chmouzies"}}
	for _, r := range results {
		file, found, err := config.ReadFile(r.OutputFile)
		if err != nil || !found {
			t.Fatalf("%s: generated TOML must parse: %v", r.Profile, err)
		}
		if _, err := config.Build([]config.File{config.Defaults(), globalRoots, file}, vars, "ibra"); err != nil {
			t.Fatalf("%s: generated TOML must validate: %v", r.Profile, err)
		}
		t.Logf("%s: links=%d bins=%d repos=%d", r.Profile, r.Links, r.Bins, r.Repos)
	}
}
