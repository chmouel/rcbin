package config

import (
	"fmt"
	"sort"
	"strings"
)

// Vars holds the variables available for path expansion.
type Vars map[string]string

var allowedVars = map[string]bool{
	"HOME":   true,
	"HOST":   true,
	"GOPATH": true,
}

// expand resolves a leading "~" and the allowlisted "${NAME}" and
// "${NAME:-default}" references using vars. A default is used when NAME is
// unset or empty; it may itself start with "~" and contain references.
// Unsupported variables, and references to unset or empty variables without a
// default, are validation errors.
func expand(s string, vars Vars) (string, error) {
	var missing, unsupported []string
	s = expandTilde(s, vars, &missing)
	if len(missing) > 0 {
		return "", fmt.Errorf("unset variable(s) HOME in %q", s)
	}

	out, err := expandRefs(s, vars, &missing, &unsupported)
	if err != nil {
		return "", fmt.Errorf("%w in %q", err, s)
	}
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

func expandTilde(s string, vars Vars, missing *[]string) string {
	if s != "~" && !strings.HasPrefix(s, "~/") {
		return s
	}
	if vars["HOME"] == "" {
		*missing = append(*missing, "HOME")
		return s
	}
	return vars["HOME"] + s[1:]
}

func expandRefs(s string, vars Vars, missing, unsupported *[]string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		if !strings.HasPrefix(s[i:], "${") {
			b.WriteByte(s[i])
			i++
			continue
		}
		j := i + 2
		for j < len(s) && isNameByte(s[j], j == i+2) {
			j++
		}
		name := s[i+2 : j]

		var def string
		hasDef := false
		var end int
		switch {
		case name != "" && strings.HasPrefix(s[j:], "}"):
			end = j + 1
		case name != "" && strings.HasPrefix(s[j:], ":-"):
			k := closingBrace(s, j+2)
			if k < 0 {
				return "", fmt.Errorf("unterminated variable reference %q", s[i:])
			}
			def, hasDef, end = s[j+2:k], true, k+1
		default:
			b.WriteString("${")
			i += 2
			continue
		}
		ref := s[i:end]
		i = end

		if !allowedVars[name] {
			*unsupported = append(*unsupported, name)
			b.WriteString(ref)
			continue
		}
		if v := vars[name]; v != "" {
			b.WriteString(v)
			continue
		}
		if !hasDef {
			*missing = append(*missing, name)
			b.WriteString(ref)
			continue
		}
		d, err := expandRefs(expandTilde(def, vars, missing), vars, missing, unsupported)
		if err != nil {
			return "", err
		}
		b.WriteString(d)
	}
	return b.String(), nil
}

func isNameByte(c byte, first bool) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (!first && c >= '0' && c <= '9')
}

// closingBrace returns the index of the "}" closing a reference whose body
// starts at start, skipping nested "${...}" references, or -1.
func closingBrace(s string, start int) int {
	depth := 0
	for i := start; i < len(s); i++ {
		switch {
		case strings.HasPrefix(s[i:], "${"):
			depth++
			i++
		case s[i] == '}':
			if depth == 0 {
				return i
			}
			depth--
		}
	}
	return -1
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
