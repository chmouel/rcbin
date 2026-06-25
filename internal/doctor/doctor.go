// Package doctor runs structured diagnostic checks against the same parsed
// configuration used for execution. Every scheduled check runs and the command
// always prints a summary. Network checks honor offline mode and bounded
// timeouts.
package doctor

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/chmouel/rc/internal/config"
	"github.com/chmouel/rc/internal/linker"
	"github.com/chmouel/rc/internal/output"
	"github.com/chmouel/rc/internal/repo"
	"github.com/chmouel/rc/internal/runner"
)

// Status classifies a check outcome.
type Status int

const (
	Pass Status = iota
	Warn
	Fail
	Info
	Skip
)

// Result is the outcome of one check.
type Result struct {
	Name   string
	Status Status
	Detail string
}

// Doctor runs diagnostics.
type Doctor struct {
	R       runner.Runner
	Rep     *output.Reporter
	Cfg     *config.Config
	Offline bool
	Client  *http.Client
	results []Result
}

// Summary reports counts after a run.
type Summary struct {
	Pass, Warn, Fail, Info, Skip int
}

// Run executes every check and prints a summary. It returns the summary and a
// non-nil error when any check failed.
func (d *Doctor) Run(ctx context.Context) (Summary, error) {
	d.checkCommands()
	d.checkRoots()
	d.checkSSH()
	d.checkConfig()
	d.checkRepositories(ctx)
	d.checkYadm()
	d.checkManifest()
	d.checkConnectivity(ctx)
	d.checkGitHubAuth(ctx)

	sum := d.print()
	if sum.Fail > 0 {
		return sum, fmt.Errorf("%d diagnostic check(s) failed", sum.Fail)
	}
	return sum, nil
}

func (d *Doctor) add(name string, status Status, format string, a ...any) {
	d.results = append(d.results, Result{Name: name, Status: status, Detail: fmt.Sprintf(format, a...)})
}

func (d *Doctor) checkCommands() {
	required := []string{"git"}
	optional := []string{"yadm", "lazygit", "gh", "atuin", "aicommit", "delta", "emacs"}
	for _, c := range required {
		if _, ok := d.R.LookPath(c); ok {
			d.add("command:"+c, Pass, "found")
		} else {
			d.add("command:"+c, Fail, "required command not found")
		}
	}
	for _, c := range optional {
		if _, ok := d.R.LookPath(c); ok {
			d.add("command:"+c, Pass, "found")
		} else {
			d.add("command:"+c, Info, "optional command not found")
		}
	}
}

func (d *Doctor) checkRoots() {
	for name, path := range d.Cfg.Roots {
		if info, err := os.Stat(path); err == nil {
			_ = info
			d.add("root:"+name, Pass, "%s", path)
		} else {
			d.add("root:"+name, Warn, "%s does not exist", path)
		}
	}
}

func (d *Doctor) checkSSH() {
	ssh := filepath.Join(d.Cfg.Vars["HOME"], ".ssh")
	if info, err := os.Stat(ssh); err == nil && info.IsDir() {
		d.add("ssh", Pass, "%s present", ssh)
	} else {
		d.add("ssh", Warn, "%s missing", ssh)
	}
}

func (d *Doctor) checkConfig() {
	d.add("config", Info, "%d link(s), %d bin(s), %d repo(s), %d backup(s), %d update(s)",
		len(d.Cfg.Links), len(d.Cfg.Bins), len(d.Cfg.Repos), len(d.Cfg.Backups), len(d.Cfg.Updates))
}

func (d *Doctor) checkRepositories(ctx context.Context) {
	for _, r := range d.Cfg.Repos {
		info, err := os.Stat(r.Path)
		switch {
		case err == nil && info.IsDir():
			if repo.IsWorkTree(ctx, d.R, r.Path) {
				d.add("repo:"+filepath.Base(r.Path), Pass, "%s", r.Path)
			} else {
				d.add("repo:"+filepath.Base(r.Path), Warn, "%s is not a git work tree", r.Path)
			}
		case r.Optional:
			d.add("repo:"+filepath.Base(r.Path), Info, "%s missing (optional)", r.Path)
		default:
			d.add("repo:"+filepath.Base(r.Path), Warn, "%s missing", r.Path)
		}
	}
}

func (d *Doctor) checkYadm() {
	state := d.Cfg.Roots["yadm_state"]
	if state == "" {
		return
	}
	if info, err := os.Stat(state); err == nil && info.IsDir() {
		d.add("yadm", Pass, "initialized at %s", state)
	} else {
		d.add("yadm", Warn, "state dir %s missing", state)
	}
}

func (d *Doctor) checkManifest() {
	m, err := linker.LoadManifest(d.Cfg.ManifestPath)
	if err != nil {
		d.add("manifest", Warn, "could not read manifest: %v", err)
		return
	}
	var broken int
	for target := range m.Links {
		if _, err := os.Stat(target); err != nil {
			broken++
		}
	}
	if broken == 0 {
		d.add("manifest", Pass, "%d managed link(s) valid", len(m.Links))
	} else {
		d.add("manifest", Warn, "%d of %d managed link(s) broken", broken, len(m.Links))
	}
}

func (d *Doctor) checkConnectivity(ctx context.Context) {
	if d.Offline {
		d.add("connectivity", Skip, "offline mode")
		return
	}
	client := d.Client
	if client == nil {
		timeout := time.Duration(d.Cfg.Doctor.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}
	for _, e := range d.Cfg.Doctor.Endpoints {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, e.URL, nil)
		if err != nil {
			d.add("net:"+e.Name, Warn, "%v", err)
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			d.add("net:"+e.Name, Fail, "unreachable: %v", err)
			continue
		}
		_ = resp.Body.Close()
		d.add("net:"+e.Name, Pass, "%s (%d)", e.URL, resp.StatusCode)
	}
}

func (d *Doctor) checkGitHubAuth(ctx context.Context) {
	if d.Offline {
		d.add("github-auth", Skip, "offline mode")
		return
	}
	if _, ok := d.R.LookPath("gh"); !ok {
		d.add("github-auth", Info, "gh not installed")
		return
	}
	if _, err := d.R.Run(ctx, runner.Spec{Name: "gh", Args: []string{"auth", "status"}}); err != nil {
		d.add("github-auth", Warn, "not authenticated")
		return
	}
	d.add("github-auth", Pass, "authenticated")
}

func (d *Doctor) print() Summary {
	var sum Summary
	for _, r := range d.results {
		switch r.Status {
		case Pass:
			sum.Pass++
			d.Rep.Successf("%s: %s", r.Name, r.Detail)
		case Warn:
			sum.Warn++
			d.Rep.Warnf("%s: %s", r.Name, r.Detail)
		case Fail:
			sum.Fail++
			d.Rep.Failf("%s: %s", r.Name, r.Detail)
		case Skip:
			sum.Skip++
			d.Rep.Skipf("%s: %s", r.Name, r.Detail)
		default:
			sum.Info++
			d.Rep.Infof("%s: %s", r.Name, r.Detail)
		}
	}
	d.Rep.Println("")
	d.Rep.Println(fmt.Sprintf("%s %s  %s  %s  %s",
		d.Rep.Bold("Summary:"),
		d.Rep.Good(fmt.Sprintf("%d passed", sum.Pass)),
		d.Rep.Caution(fmt.Sprintf("%d warnings", sum.Warn)),
		d.Rep.Bad(fmt.Sprintf("%d failures", sum.Fail)),
		d.Rep.Dim(fmt.Sprintf("%d skipped", sum.Skip))))
	return sum
}
