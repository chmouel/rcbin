package config

import (
	"os"
	"path/filepath"

	"github.com/chmouel/rc/internal/host"
)

// Options controls how the layered configuration is located and loaded.
type Options struct {
	// GlobalPath overrides the global config file path.
	GlobalPath string
	// HostsRoot overrides the host host profile root (default: <yadm_config>/hosts).
	HostsRoot string
	// Hostname overrides the detected hostname.
	Hostname string
	// Vars overrides the expansion variables (default: process environment).
	Vars Vars
}

// Load detects the host, discovers host profiles, and builds the merged config.
func Load(opts Options) (*Config, error) {
	vars := opts.Vars
	if vars == nil {
		vars = EnvVars(opts.Hostname)
	}

	hostname := opts.Hostname
	if hostname == "" {
		h, err := host.Detect()
		if err != nil {
			return nil, err
		}
		hostname = h
		vars["HOST"] = hostname
	}

	globalPath := opts.GlobalPath
	if globalPath == "" {
		globalPath = DefaultGlobalPath(vars["HOME"])
	}

	layers := []File{Defaults()}
	if global, found, err := ReadFile(globalPath); err != nil {
		return nil, err
	} else if found {
		layers = append(layers, global)
	}

	baseRoots, err := ResolveRoots(layers, vars)
	if err != nil {
		return nil, err
	}

	hostsRoot := opts.HostsRoot
	if hostsRoot == "" {
		hostsRoot = filepath.Join(baseRoots["yadm_config"], "hosts")
	}

	profiles, err := host.Profiles(hostsRoot, hostname)
	if err != nil {
		return nil, err
	}
	claimedSingletons := map[string]struct{}{}
	for _, p := range profiles {
		profile, found, err := readHostProfile(p, hostProfileContext{
			Roots:               baseRoots,
			Vars:                vars,
			Hostname:            hostname,
			ClaimedSingletons:   claimedSingletons,
			IncludeHostPayloads: true,
		})
		if err != nil {
			return nil, err
		}
		if found {
			layers = append(layers, profile)
		}
	}
	if systemdLinks, found, err := readHostSystemdLinks(baseRoots); err != nil {
		return nil, err
	} else if found {
		layers = append(layers, systemdLinks)
	}

	return Build(layers, vars, hostname)
}

// DefaultGlobalPath returns the default global config path, honoring
// XDG_CONFIG_HOME.
func DefaultGlobalPath(home string) string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "rc", "config.toml")
	}
	return filepath.Join(home, ".config", "rc", "config.toml")
}

// EnvVars builds the allowlisted expansion variable set from the process
// environment. HOME is populated from the OS when absent; GOPATH is only
// available when explicitly set in the environment.
func EnvVars(hostname string) Vars {
	vars := Vars{}
	vars["HOME"] = os.Getenv("HOME")
	if vars["HOME"] == "" {
		if h, err := os.UserHomeDir(); err == nil {
			vars["HOME"] = h
		}
	}
	if hostname != "" {
		vars["HOST"] = hostname
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		vars["GOPATH"] = gopath
	}
	return vars
}
