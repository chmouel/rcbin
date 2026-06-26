package app

import (
	"github.com/spf13/cobra"
)

// versionOrDefault returns v, or "dev" when v is empty.
func versionOrDefault(v string) string {
	if v == "" {
		return "dev"
	}
	return v
}

// newRootCmd builds the full command tree.
func newRootCmd(deps Deps) *cobra.Command {
	g := &globals{}

	root := &cobra.Command{
		Use:           "rc",
		Short:         "Workstation orchestrator for dotfiles, repositories, and maintenance",
		Long:          "rc synchronizes YADM and Git repositories, manages symlinks, runs backups and updates, and reports diagnostics.",
		Version:       versionOrDefault(deps.Version),
		SilenceUsage:  true,
		SilenceErrors: true,
		// Running rc with no arguments prints help and exits successfully
		// without mutation.
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	pf := root.PersistentFlags()
	pf.StringVar(&g.configPath, "config", "", "path to the global config file")
	pf.StringVar(&g.host, "host", "", "override the detected hostname")
	pf.BoolVar(&g.verbose, "verbose", false, "enable verbose logging")
	pf.BoolVar(&g.noColor, "no-color", false, "disable colored output")
	pf.BoolVar(&g.nonInteractive, "non-interactive", false, "never launch interactive tools")
	pf.BoolVar(&g.dryRun, "dry-run", false, "show actions without performing them")

	root.AddCommand(
		newStatusCmd(g, deps),
		newSyncCmd(g, deps),
		newLinkCmd(g, deps),
		newBackupCmd(g, deps),
		newUpdateCmd(g, deps),
		newDoctorCmd(g, deps),
		newRunCmd(g, deps),
		newConfigCmd(g, deps),
	)

	return root
}
