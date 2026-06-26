package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type legacyProfileContext struct {
	Roots               map[string]string
	Vars                Vars
	Hostname            string
	ClaimedSingletons   map[string]struct{}
	IncludeHostPayloads bool
}

func readLegacyProfile(profileDir string, ctx legacyProfileContext) (File, bool, error) {
	file := File{Version: 1}
	found := false

	if content, ok, err := readLegacyFile(filepath.Join(profileDir, "rc")); err != nil {
		return File{}, false, err
	} else if ok {
		found = true
		file.Links = append(file.Links, parseLegacyRC(content, ctx.Roots["rc"])...)
	}
	if content, ok, err := readLegacyFile(filepath.Join(profileDir, "chmouzies")); err != nil {
		return File{}, false, err
	} else if ok {
		found = true
		file.Bins = append(file.Bins, parseLegacyBinList(content, "chmouzies")...)
	}
	if content, ok, err := readLegacyFile(filepath.Join(profileDir, "repobins")); err != nil {
		return File{}, false, err
	} else if ok {
		found = true
		file.Bins = append(file.Bins, parseLegacyRepoBins(content, ctx.Roots, ctx.Vars)...)
	}
	if content, ok, err := readLegacyFile(filepath.Join(profileDir, "extra-dirs")); err != nil {
		return File{}, false, err
	} else if ok {
		found = true
		file.Repositories = append(file.Repositories, parseLegacyExtraDirs(content)...)
	}
	if ctx.IncludeHostPayloads {
		if ok, err := appendLegacyHostPayloads(&file, profileDir, ctx); err != nil {
			return File{}, false, err
		} else if ok {
			found = true
		}
	}

	if !found {
		return File{}, false, nil
	}
	var err error
	file, err = dedupeLegacyDuplicates(filepath.Base(profileDir), file)
	if err != nil {
		return File{}, false, err
	}
	return file, true, nil
}

func readLegacyFile(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return string(data), true, nil
	}
	if os.IsNotExist(err) {
		return "", false, nil
	}
	return "", false, fmt.Errorf("reading %s: %w", path, err)
}

func dedupeLegacyDuplicates(profile string, file File) (File, error) {
	linkTargets := map[string]Link{}
	links := make([]Link, 0, len(file.Links))
	for _, link := range file.Links {
		if prev, ok := linkTargets[link.Target]; ok {
			if prev != link {
				return File{}, fmt.Errorf("legacy profile %q: duplicate link target %q", profile, link.Target)
			}
			continue
		}
		linkTargets[link.Target] = link
		links = append(links, link)
	}
	file.Links = links

	binTargets := map[string]Bin{}
	bins := make([]Bin, 0, len(file.Bins))
	for _, bin := range file.Bins {
		if prev, ok := binTargets[bin.Target]; ok {
			if prev != bin {
				return File{}, fmt.Errorf("legacy profile %q: duplicate bin target %q", profile, bin.Target)
			}
			continue
		}
		binTargets[bin.Target] = bin
		bins = append(bins, bin)
	}
	file.Bins = bins
	return file, nil
}

func appendLegacyHostPayloads(file *File, profileDir string, ctx legacyProfileContext) (bool, error) {
	found := false

	if appendLegacySingletonLink(file, profileDir, "emacs/init.el", filepath.Join(ctx.Roots["emacs"], "lisp", "init-local.el"), ctx.ClaimedSingletons) {
		found = true
	}
	if appendLegacySingletonLink(file, profileDir, "shell/init.zsh", filepath.Join(ctx.Roots["zsh"], "hosts", ctx.Hostname+".sh"), ctx.ClaimedSingletons) {
		found = true
	}
	if appendLegacySingletonLink(file, profileDir, "shell/post.zsh", filepath.Join(ctx.Roots["zsh"], "hosts", ctx.Hostname+"-post.sh"), ctx.ClaimedSingletons) {
		found = true
	}

	if ok, err := appendLegacyDirLinks(
		file,
		filepath.Join(profileDir, "shell", "functions"),
		filepath.Join(ctx.Roots["zsh"], "functions", "hosts", ctx.Hostname),
	); err != nil {
		return false, err
	} else if ok {
		found = true
	}

	if ok, err := appendLegacyDirBins(file, filepath.Join(profileDir, "bin")); err != nil {
		return false, err
	} else if ok {
		found = true
	}

	return found, nil
}

func appendLegacySingletonLink(file *File, profileDir, rel, target string, claimed map[string]struct{}) bool {
	if target == "" {
		return false
	}
	if _, ok := claimed[target]; ok {
		return false
	}
	source := filepath.Join(profileDir, filepath.FromSlash(rel))
	if _, err := os.Lstat(source); err != nil {
		return false
	}
	file.Links = append(file.Links, Link{Source: source, Target: target})
	claimed[target] = struct{}{}
	return true
}

func appendLegacyDirLinks(file *File, sourceDir, targetDir string) (bool, error) {
	entries, err := legacyPayloadEntries(sourceDir)
	if err != nil || len(entries) == 0 {
		return false, err
	}
	for _, source := range entries {
		file.Links = append(file.Links, Link{
			Source: source,
			Target: filepath.Join(targetDir, filepath.Base(source)),
		})
	}
	return true, nil
}

func appendLegacyDirBins(file *File, sourceDir string) (bool, error) {
	entries, err := legacyPayloadEntries(sourceDir)
	if err != nil || len(entries) == 0 {
		return false, err
	}
	for _, source := range entries {
		file.Bins = append(file.Bins, Bin{
			Source: source,
			Target: filepath.Base(source),
		})
	}
	return true, nil
}

func legacyPayloadEntries(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		out = append(out, filepath.Join(dir, entry.Name()))
	}
	return out, nil
}

func readLegacySystemdLinks(roots map[string]string) (File, bool, error) {
	rcRoot := roots["rc"]
	targetDir := roots["systemd_user"]
	if rcRoot == "" || targetDir == "" {
		return File{}, false, nil
	}
	sourceDir := filepath.Join(rcRoot, "systemd")
	if info, err := os.Stat(targetDir); err != nil || !info.IsDir() {
		return File{}, false, nil
	}

	file := File{Version: 1}
	found, err := appendLegacyDirLinks(&file, sourceDir, targetDir)
	if err != nil || !found {
		return File{}, false, err
	}
	return file, true, nil
}

// convertLegacyVars rewrites legacy "$NAME" references into "${NAME}" form.
// Existing "${NAME}" references and a leading "~" are left untouched.
func convertLegacyVars(s string) string {
	return legacyVar.ReplaceAllStringFunc(s, func(m string) string {
		name := m[1:]
		return "${" + name + "}"
	})
}

var legacyVar = regexp.MustCompile(`\$([A-Za-z_][A-Za-z0-9_]+)`)

func isLegacyAbsSymbolic(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") || strings.HasPrefix(s, "${")
}

func underLegacyHomeSymbolic(s string) bool {
	return strings.HasPrefix(s, "~") || strings.HasPrefix(s, "${HOME}")
}

func classifyLegacyTarget(rest string) (target string, privileged bool) {
	d := convertLegacyVars(rest)
	switch {
	case strings.HasPrefix(d, "/"):
		return d, true
	case underLegacyHomeSymbolic(d):
		return d, false
	case strings.HasPrefix(d, "${"):
		return d, false
	case strings.Contains(d, "/"):
		return "~/" + d, false
	default:
		return "~/.config/" + d, false
	}
}

func parseLegacyRC(content, rcAssetsRoot string) []Link {
	var links []Link

	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		optional := false
		if strings.HasPrefix(line, "?") {
			optional = true
			line = strings.TrimSpace(line[1:])
		}

		left := line
		rest := ""
		if i := strings.IndexFunc(line, isLegacySpace); i >= 0 {
			left = line[:i]
			rest = strings.TrimSpace(line[i+1:])
		}

		root, source := classifyLegacyRCSource(left, rcAssetsRoot)

		var target string
		var privileged bool
		if rest == "" {
			target = "~/.config/" + filepath.Base(left)
		} else {
			target, privileged = classifyLegacyTarget(rest)
		}

		links = append(links, Link{
			SourceRoot: root,
			Source:     source,
			Target:     target,
			Optional:   optional,
			Privileged: privileged,
		})
	}
	return links
}

func classifyLegacyRCSource(left, rcAssetsRoot string) (root, source string) {
	if !strings.Contains(left, "/") {
		return "rc", left
	}
	if rcAssetsRoot != "" {
		if _, err := os.Stat(filepath.Join(rcAssetsRoot, left)); err == nil {
			return "rc", left
		}
	}
	el := convertLegacyVars(left)
	if isLegacyAbsSymbolic(el) {
		return "", el
	}
	return "home", left
}

func parseLegacyBinList(content, sourceRoot string) []Bin {
	var bins []Bin
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
			if line == "" {
				continue
			}
		}
		optional := false
		if strings.HasPrefix(line, "?") {
			optional = true
			line = strings.TrimSpace(line[1:])
		}

		var source, target string
		if i := strings.Index(line, " :: "); i >= 0 {
			source = strings.TrimSpace(line[:i])
			right := strings.TrimSpace(line[i+4:])
			if strings.Contains(right, "/") {
				t, _ := classifyLegacyTarget(right)
				target = t
			} else {
				target = right
			}
		} else {
			source = line
			target = filepath.Base(line)
		}

		bins = append(bins, Bin{
			SourceRoot:         sourceRoot,
			Source:             convertLegacyVars(source),
			Target:             target,
			Optional:           optional,
			DiscoverCompletion: true,
		})
	}
	return bins
}

func parseLegacyRepoBins(content string, roots map[string]string, vars Vars) []Bin {
	bins := parseLegacyBinList(content, "repo_base")
	for i := range bins {
		root, source := classifyLegacyRepoBinSource(bins[i].Source, roots, vars)
		bins[i].SourceRoot = root
		bins[i].Source = source
	}
	return bins
}

func classifyLegacyRepoBinSource(source string, roots map[string]string, vars Vars) (sourceRoot, resolvedSource string) {
	converted := convertLegacyVars(source)
	expanded, err := expand(converted, vars)
	if err != nil {
		return "repo_base", converted
	}
	if filepath.IsAbs(expanded) {
		return "", expanded
	}

	var candidates []string
	if gopath := vars["GOPATH"]; gopath != "" {
		candidates = append(candidates,
			filepath.Join(gopath, "src", expanded),
			filepath.Join(gopath, "src", "github.com", expanded),
			filepath.Join(gopath, "src", "gitlab.com", expanded),
		)
	}
	if repoBase := roots["repo_base"]; repoBase != "" {
		candidates = append(candidates, filepath.Join(repoBase, expanded))
	}
	for _, candidate := range candidates {
		if _, err := os.Lstat(candidate); err == nil {
			return "", candidate
		}
	}
	return "repo_base", converted
}

var legacyHookRe = regexp.MustCompile(`([a-zA-Z_]+)=\{([^}]*)\}`)

func parseLegacyExtraDirs(content string) []Repository {
	var repos []Repository
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		dir := line
		rest := ""
		if i := strings.IndexFunc(line, isLegacySpace); i >= 0 {
			dir = line[:i]
			rest = line[i+1:]
		}

		repo := Repository{Path: convertLegacyVars(dir)}
		for _, m := range legacyHookRe.FindAllStringSubmatch(rest, -1) {
			name := m[1]
			cmd := legacyToCommand(strings.TrimSpace(m[2]))
			switch name {
			case "post_update":
				repo.Hooks.PostUpdate = &cmd
			case "always":
				repo.Hooks.Always = &cmd
			}
		}
		repos = append(repos, repo)
	}
	return repos
}

var legacyShellMeta = regexp.MustCompile(`[|&;<>$(){}*?\[\]` + "`" + `]`)

func legacyToCommand(body string) Command {
	if body == "" {
		return Command{}
	}
	if legacyShellMeta.MatchString(body) {
		return Command{Shell: body}
	}
	return Command{Argv: strings.Fields(body)}
}

func isLegacySpace(r rune) bool { return r == ' ' || r == '\t' }
