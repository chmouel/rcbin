package commitui

import (
	"context"
	"fmt"
	"strings"

	"github.com/chmouel/rc/internal/runner"
)

type changedFile struct {
	Status  string
	Path    string
	OldPath string
}

func (f changedFile) displayPath() string {
	path := sanitizePath(f.Path)
	if f.OldPath == "" {
		return path
	}
	return sanitizePath(f.OldPath) + " -> " + path
}

func sanitizePath(path string) string {
	path = strings.ReplaceAll(path, "\n", `\n`)
	return strings.ReplaceAll(path, "\r", `\r`)
}

func (a *Adapter) changedFiles(ctx context.Context, dir string) ([]changedFile, error) {
	res, err := a.R.Run(ctx, runner.Spec{
		Name: "git",
		Args: []string{"-C", dir, "status", "--porcelain=v1", "-z", "--untracked-files=normal"},
		Dir:  dir,
	})
	if err != nil {
		return nil, err
	}
	return parseChangedFilesZ(res.Stdout), nil
}

func parseChangedFilesZ(out string) []changedFile {
	if out == "" {
		return nil
	}
	parts := strings.Split(out, "\x00")
	files := make([]changedFile, 0, len(parts))
	for i := 0; i < len(parts); i++ {
		entry := parts[i]
		if entry == "" || len(entry) < 4 || entry[2] != ' ' {
			continue
		}
		status := entry[:2]
		path := entry[3:]
		file := changedFile{Status: status, Path: path}
		if isRenameOrCopy(status) && i+1 < len(parts) {
			file.OldPath = parts[i+1]
			i++
		}
		files = append(files, file)
	}
	return files
}

func isRenameOrCopy(status string) bool {
	return strings.ContainsAny(status, "RC")
}

func (a *Adapter) showChanges(ctx context.Context, dir string, mode changeDisplayMode) error {
	if mode == changeDisplayFullDiff {
		return a.showDiff(ctx, dir)
	}
	return a.showStatus(ctx, dir)
}

func (a *Adapter) showStatus(ctx context.Context, dir string) error {
	files, err := a.changedFiles(ctx, dir)
	if err != nil {
		return err
	}
	for _, file := range files {
		a.Rep.Println(a.compactStatusLine(file))
	}
	return nil
}

func (a *Adapter) showDiff(ctx context.Context, dir string) error {
	diff, err := a.combinedDiff(ctx, dir)
	if err != nil {
		return err
	}
	printed := false
	if strings.TrimSpace(diff) != "" {
		if err := a.delta(ctx, diff); err != nil {
			return err
		}
		printed = true
	}

	files, err := a.changedFiles(ctx, dir)
	if err != nil {
		return err
	}
	printedUntracked := false
	for _, file := range files {
		if file.Status != "??" {
			continue
		}
		if printed && !printedUntracked {
			a.Rep.Println("")
		}
		a.Rep.Println(a.compactStatusLine(file))
		printed = true
		printedUntracked = true
	}
	return nil
}

func (a *Adapter) combinedDiff(ctx context.Context, dir string) (string, error) {
	var chunks []string
	for _, args := range [][]string{
		{"--cached"},
		nil,
	} {
		out, err := a.gitDiff(ctx, dir, args...)
		if err != nil {
			return "", err
		}
		out = strings.TrimRight(out, "\n")
		if strings.TrimSpace(out) == "" {
			continue
		}
		chunks = append(chunks, out)
	}
	if len(chunks) == 0 {
		return "", nil
	}
	return strings.Join(chunks, "\n") + "\n", nil
}

func (a *Adapter) delta(ctx context.Context, diff string) error {
	if _, ok := a.R.LookPath("delta"); !ok {
		return fmt.Errorf("delta not found for diff display")
	}
	_, err := a.R.Run(ctx, runner.Spec{Name: "delta", Args: []string{"--paging=auto"}, Stdin: diff, Interactive: true})
	return err
}

func (a *Adapter) gitDiff(ctx context.Context, dir string, args ...string) (string, error) {
	gitArgs := append([]string{"-C", dir, "diff", "--no-ext-diff"}, args...)
	res, err := a.R.Run(ctx, runner.Spec{Name: "git", Args: gitArgs, Dir: dir})
	return res.Stdout, err
}

func (a *Adapter) compactStatusLine(file changedFile) string {
	label := compactStatus(file.Status)
	path := file.displayPath()
	if !a.Rep.Color() || label == "" {
		if label == "" {
			return path
		}
		return label + compactStatusSeparator(label) + path
	}
	plain := label + compactStatusSeparator(label) + path
	if label == "??" {
		return a.Rep.Dim(plain)
	}
	if len(file.Status) < 2 {
		return plain
	}
	x, y := file.Status[0:1], file.Status[1:2]
	if x != " " && y != " " {
		return a.Rep.Good(x) + a.Rep.Bad(y) + " " + path
	}
	if x != " " {
		return a.Rep.Good(x) + "  " + path
	}
	if y != " " {
		return a.Rep.Bad(y) + "  " + path
	}
	return plain
}

func compactStatus(status string) string {
	if len(status) < 2 {
		return strings.TrimSpace(status)
	}
	x, y := status[0:1], status[1:2]
	if x == "?" && y == "?" {
		return "??"
	}
	if x != " " && y != " " {
		return x + y
	}
	if x != " " {
		return x
	}
	if y != " " {
		return y
	}
	return strings.TrimSpace(status)
}

func compactStatusSeparator(label string) string {
	if len(label) == 1 {
		return "  "
	}
	return " "
}
