package config

// ResolvedLink is a link with absolute source and target paths.
type ResolvedLink struct {
	Source     string
	Target     string
	Optional   bool
	Privileged bool
}

// ResolvedBin is a binary link with absolute source/target and completion flag.
type ResolvedBin struct {
	Source             string
	Target             string
	Optional           bool
	DiscoverCompletion bool
}

// RepoTarget is a fully resolved repository participating in synchronization.
type RepoTarget struct {
	Path     string
	Clone    string
	Optional bool
	Hooks    Hooks
}

// ResolvedBackup is a backup task with absolute repo and output paths.
type ResolvedBackup struct {
	Name      string
	Platforms []string
	Requires  []string
	Command   Command
	RepoRoot  string
	Output    string // absolute path = RepoRoot/<output>
	Signoff   bool
}

// ResolvedUpdate is an update task ready for execution.
type ResolvedUpdate struct {
	Name            string
	Platforms       []string
	Requires        []string
	Commands        []Command
	Dir             string
	ContinueOnError bool
}

// Config is the fully merged, expanded, and validated configuration consumed by
// every subsystem and by doctor.
type Config struct {
	Hostname string
	Vars     Vars
	Roots    map[string]string

	Provider    string
	Yadm        YadmConfig
	Concurrency int
	Tools       ToolsConfig
	Doctor      DoctorConfig

	Links []ResolvedLink
	Bins  []ResolvedBin
	Repos []RepoTarget

	Backups []ResolvedBackup
	Updates []ResolvedUpdate

	// ManifestPath is where the linker records managed links.
	ManifestPath string
}

// Shell returns the configured shell for shell-form commands, defaulting to sh.
func (c *Config) Shell() string {
	if c.Tools.Shell != "" {
		return c.Tools.Shell
	}
	return "sh"
}

// WorkerLimit returns the bounded concurrency for repository sync.
func (c *Config) WorkerLimit() int {
	if c.Concurrency > 0 {
		return c.Concurrency
	}
	return 4
}
