package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var varPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Vars holds the variables available for path expansion.
type Vars map[string]string

var allowedVars = map[string]bool{
	"HOME":   true,
	"HOST":   true,
	"GOPATH": true,
}

// expand resolves a leading "~" and the allowlisted "${NAME}" references using
// vars. Unsupported variables, and references to unset or empty variables, are
// validation errors.
func expand(s string, vars Vars) (string, error) {
	if s == "~" {
		if vars["HOME"] == "" {
			return "", fmt.Errorf("unset variable(s) HOME in %q", s)
		}
		s = vars["HOME"]
	} else if strings.HasPrefix(s, "~/") {
		if vars["HOME"] == "" {
			return "", fmt.Errorf("unset variable(s) HOME in %q", s)
		}
		s = vars["HOME"] + s[1:]
	}

	var missing []string
	var unsupported []string
	out := varPattern.ReplaceAllStringFunc(s, func(m string) string {
		name := m[2 : len(m)-1]
		if !allowedVars[name] {
			unsupported = append(unsupported, name)
			return m
		}
		v, ok := vars[name]
		if !ok || v == "" {
			missing = append(missing, name)
			return m
		}
		return v
	})
	if len(unsupported) > 0 {
		sort.Strings(unsupported)
		return "", fmt.Errorf("unsupported variable(s) %s in %q", strings.Join(dedupe(unsupported), ", "), s)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("unset variable(s) %s in %q", strings.Join(dedupe(missing), ", "), s)
	}
	return out, nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
