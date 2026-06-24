// Package migrate is the only component that understands the legacy line-based
// configuration formats. It converts legacy host files (rc, chmouzies,
// repobins, extra-dirs) into TOML overlays. The runtime never reads legacy
// formats.
package migrate

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/chmouel/rc/internal/config"
)

// convertVars rewrites legacy "$NAME" references into "${NAME}" form. Existing
// "${NAME}" references and a leading "~" are left untouched.
func convertVars(s string) string {
	return legacyVar.ReplaceAllStringFunc(s, func(m string) string {
		// Skip "${...}" which legacyVar does not match anyway.
		name := m[1:]
		return "${" + name + "}"
	})
}

var legacyVar = regexp.MustCompile(`\$([A-Za-z_][A-Za-z0-9_]+)`)

func isAbsSymbolic(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") || strings.HasPrefix(s, "${")
}

func underHomeSymbolic(s string) bool {
	return strings.HasPrefix(s, "~") || strings.HasPrefix(s, "${HOME}")
}

// classifyTarget converts a legacy destination token to a TOML target and
// reports whether it requires privileged (sudo) operations.
func classifyTarget(rest string) (target string, privileged bool) {
	d := convertVars(rest)
	switch {
	case strings.HasPrefix(d, "/"):
		return d, true
	case underHomeSymbolic(d):
		return d, false
	case strings.HasPrefix(d, "${"):
		// Absolute via another variable such as ${GOPATH}.
		return d, false
	case strings.Contains(d, "/"):
		return "~/" + d, false
	default:
		return "~/.config/" + d, false
	}
}

// parseRC converts a legacy rc file into links. rcAssetsRoot, when non-empty,
// is consulted to decide whether a slashed source belongs to the rc repository
// or to $HOME, mirroring the legacy precedence.
func parseRC(content, rcAssetsRoot string) []config.Link {
	var links []config.Link

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
		if i := strings.IndexFunc(line, isSpace); i >= 0 {
			left = line[:i]
			rest = strings.TrimSpace(line[i+1:])
		}

		root, source := classifyRCSource(left, rcAssetsRoot)

		var target string
		var privileged bool
		if rest == "" {
			target = "~/.config/" + filepath.Base(left)
		} else {
			target, privileged = classifyTarget(rest)
		}

		links = append(links, config.Link{
			SourceRoot: root,
			Source:     source,
			Target:     target,
			Optional:   optional,
			Privileged: privileged,
		})
	}
	return links
}

func classifyRCSource(left, rcAssetsRoot string) (root, source string) {
	if !strings.Contains(left, "/") {
		return "rc", left
	}
	if rcAssetsRoot != "" {
		if _, err := os.Stat(filepath.Join(rcAssetsRoot, left)); err == nil {
			return "rc", left
		}
	}
	el := convertVars(left)
	if isAbsSymbolic(el) {
		return "", el
	}
	return "home", left
}

// parseBinList converts chmouzies/repobins-style lines into bins. sourceRoot is
// the configured root key for the discovered source; for repobins the caller
// may override per-line via the search roots.
func parseBinList(content, sourceRoot string) []config.Bin {
	var bins []config.Bin
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip inline comments (repobins permit them).
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
				t, _ := classifyTarget(right)
				target = t
			} else {
				target = right
			}
		} else {
			source = line
			target = filepath.Base(line)
		}

		bins = append(bins, config.Bin{
			SourceRoot:         sourceRoot,
			Source:             convertVars(source),
			Target:             target,
			Optional:           optional,
			DiscoverCompletion: true,
		})
	}
	return bins
}

var hookRe = regexp.MustCompile(`([a-zA-Z_]+)=\{([^}]*)\}`)

// parseExtraDirs converts a legacy extra-dirs file into repositories.
func parseExtraDirs(content string) []config.Repository {
	var repos []config.Repository
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		dir := line
		rest := ""
		if i := strings.IndexFunc(line, isSpace); i >= 0 {
			dir = line[:i]
			rest = line[i+1:]
		}

		repo := config.Repository{Path: convertVars(dir)}
		for _, m := range hookRe.FindAllStringSubmatch(rest, -1) {
			name := m[1]
			cmd := toCommand(strings.TrimSpace(m[2]))
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

var shellMeta = regexp.MustCompile(`[|&;<>$(){}*?\[\]` + "`" + `]`)

// toCommand prefers argv form for simple commands and falls back to shell form
// when the body contains shell metacharacters.
func toCommand(body string) config.Command {
	if body == "" {
		return config.Command{}
	}
	if shellMeta.MatchString(body) {
		return config.Command{Shell: body}
	}
	return config.Command{Argv: strings.Fields(body)}
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' }
