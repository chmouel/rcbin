package config

func boolPtr(v bool) *bool { return &v }

// Defaults returns the built-in global configuration. It encodes the legacy
// hard-coded roots, repositories, and maintenance tasks so the tool is usable
// before any user file exists. The user's global file and host overlays merge
// on top of these values.
func Defaults() File {
	return File{
		Roots: map[string]string{
			"home":           "~",
			"config":         "~/.config",
			"rc":             "~/.local/share/rc",
			"chmouzies":      "~/.local/share/chmouzies",
			"repo_base":      "~/git",
			"desktop_bin":    "~/.local/bin/desktop",
			"yadm_config":    "~/.config/yadm",
			"yadm_state":     "~/.local/share/yadm",
			"desktop_config": "~/.local/share/desktop-config",
			"emacs":          "~/.config/emacs",
			"zsh":            "~/.config/zsh",
			"systemd_user":   "~/.config/systemd/user",
		},
		Git: GitConfig{
			Provider: "git@gitlab.com:chmouel",
			Repositories: []DefaultRepo{
				{Path: "${HOME}/.local/share/rc", Clone: "rc-config"},
				{Path: "${HOME}/.local/share/chmouzies", Clone: "git@gitlab.com:chmouel/chmouzies"},
				{Path: "${HOME}/.config/emacs", Clone: "emacs-config"},
				{Path: "${HOME}/.local/share/desktop-config", Clone: "desktop-config"},
				{Path: "${HOME}/git/perso/zmk", Optional: true},
			},
		},
		Yadm: YadmConfig{
			Remote: "ssh://chmouel@ssh.chmouel.com:/media/bigdisk/Backup/Config/",
			Track: []string{
				"${HOME}/.config/yadm",
				"${HOME}/.password-store",
				"${HOME}/.local/share/applications",
			},
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
		Backups: defaultBackups(),
		Updates: defaultUpdates(),
	}
}

func defaultBackups() []BackupTask {
	return []BackupTask{
		{
			Name:     "dconf",
			Requires: []string{"dconf"},
			Command:  Command{Shell: "dconf dump / | grep -Ev '(nag-check=|timestamp=|reminders-past|token|last-backup|last-run|token)'"},
			Repo:     "desktop_config",
			Output:   "dconf/dconf.reg-${HOST}",
			Signoff:  true,
		},
		{
			Name:      "brew-packages",
			Platforms: []string{"linux", "darwin"},
			Requires:  []string{"brew"},
			Command:   Command{Argv: []string{"brew", "list", "-1", "--installed-on-request"}},
			Repo:      "desktop_config",
			Output:    "homebrew/packages-${HOST}",
			Signoff:   true,
		},
		{
			Name:      "brew-casks",
			Platforms: []string{"darwin"},
			Requires:  []string{"brew"},
			Command:   Command{Argv: []string{"brew", "list", "-1", "--cask"}},
			Repo:      "desktop_config",
			Output:    "homebrew/packages-casks-${HOST}",
			Signoff:   true,
		},
		{
			Name:      "pacman",
			Platforms: []string{"linux"},
			Requires:  []string{"pacman"},
			Command:   Command{Argv: []string{"pacman", "-Qq"}},
			Repo:      "desktop_config",
			Output:    "arch/packages-${HOST}",
			Signoff:   true,
		},
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
