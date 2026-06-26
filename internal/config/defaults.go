package config

func boolPtr(v bool) *bool { return &v }

// Defaults returns the built-in global configuration. It encodes only neutral,
// non-personal structure: generic roots, sensible sync/tool/doctor settings, and
// OS/tool update tasks that are skipped unless their commands exist. Personal
// data (Git provider, repositories to clone, the YADM backup remote, and backup
// tasks that commit into your own config repo) intentionally lives in the user
// global file, not the binary. See examples/config.toml for a template. The
// user's global file and legacy host profiles merge on top of these values.
func Defaults() File {
	return File{
		Roots: map[string]string{
			"home":         "~",
			"config":       "~/.config",
			"rc":           "~/.local/share/rc",
			"chmouzies":    "~/.local/share/chmouzies",
			"repo_base":    "~/git",
			"desktop_bin":  "~/.local/bin/desktop",
			"yadm_config":  "~/.config/yadm",
			"yadm_state":   "~/.local/share/yadm",
			"emacs":        "~/.config/emacs",
			"zsh":          "~/.config/zsh",
			"systemd_user": "~/.config/systemd/user",
		},
		Yadm: YadmConfig{
			Track: []string{"${HOME}/.config/yadm"},
		},
		Sync:  SyncConfig{Concurrency: 4},
		Tools: ToolsLayer{Lazygit: "lazygit", Aicommit: "aicommit", Shell: "sh", PreferEmacs: boolPtr(true)},
		Doctor: DoctorConfig{
			TimeoutSeconds: 5,
			Endpoints: []Endpoint{
				{Name: "github", URL: "https://github.com"},
				{Name: "gitlab", URL: "https://gitlab.com"},
			},
		},
		Updates: defaultUpdates(),
	}
}

func defaultUpdates() []UpdateTask {
	return []UpdateTask{
		{
			Name:     "lazyvim",
			Requires: []string{"lazyvim"},
			Commands: []Command{{Argv: []string{"lazyvim", "--headless", "+Lazy! sync", "+qa"}}},
		},
		{
			Name:      "brew",
			Platforms: []string{"linux", "darwin"},
			Requires:  []string{"brew"},
			Commands: []Command{
				{Argv: []string{"brew", "update"}},
				{Argv: []string{"brew", "upgrade"}},
				{Argv: []string{"brew", "autoremove"}},
				{Argv: []string{"brew", "cleanup", "--prune=3"}},
			},
			ContinueOnError: true,
		},
		{
			Name:     "atuin",
			Requires: []string{"atuin"},
			Commands: []Command{{Argv: []string{"atuin", "sync"}}},
		},
		{
			Name:      "pacman",
			Platforms: []string{"linux"},
			Requires:  []string{"pacman", "yay"},
			Commands:  []Command{{Argv: []string{"yay"}}, {Argv: []string{"yay", "-Sc", "--noconfirm"}}},
		},
		{
			Name:     "nix",
			Requires: []string{"nix-update"},
			Commands: []Command{{Argv: []string{"nix-update"}}},
		},
		{
			Name:     "home-manager",
			Requires: []string{"hm-update"},
			Commands: []Command{{Argv: []string{"hm-update"}}},
		},
		{
			Name:      "apt",
			Platforms: []string{"linux"},
			Requires:  []string{"apt-get"},
			Commands: []Command{
				{Argv: []string{"sudo", "apt-get", "-y", "update"}},
				{Argv: []string{"sudo", "apt-get", "-y", "dist-upgrade"}},
				{Argv: []string{"sudo", "apt-get", "-y", "autoremove"}},
			},
		},
		{
			Name:            "gh-extensions",
			Requires:        []string{"gh"},
			Commands:        []Command{{Argv: []string{"gh", "extension", "upgrade", "--all"}}},
			ContinueOnError: true,
		},
	}
}
