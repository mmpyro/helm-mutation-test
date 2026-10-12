package incremental

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Change is the set of chart files that differ from a base revision.
type Change struct {
	// BaseCommit is the merge-base of the requested ref and HEAD.
	BaseCommit string
	// Files are chart-relative, slash-separated, sorted and unique.
	Files []string
}

// ChangedFiles lists the files under chartDir that differ between the
// merge-base of ref and HEAD and the working tree, plus untracked files.
//
// The working tree rather than HEAD is the comparison point so a local run sees
// the edits being made; in CI the two are the same. Paths outside chartDir are
// dropped: they cannot change what the chart renders.
func ChangedFiles(chartDir, ref string) (Change, error) {
	base, err := git(chartDir, "merge-base", ref, "HEAD")
	if err != nil {
		return Change{}, fmt.Errorf("--since %s: cannot find a merge-base with HEAD "+
			"(in a shallow CI clone, fetch the base ref, e.g. actions/checkout with fetch-depth: 0): %w", ref, err)
	}
	base = strings.TrimSpace(base)

	diff, err := git(chartDir, "diff", "--name-only", "--no-renames", "--relative", base, "--")
	if err != nil {
		return Change{}, fmt.Errorf("--since %s: listing changed files: %w", ref, err)
	}
	untracked, err := git(chartDir, "ls-files", "--others", "--exclude-standard", "--", ".")
	if err != nil {
		return Change{}, fmt.Errorf("--since %s: listing untracked files: %w", ref, err)
	}

	var files []string
	for _, out := range []string{diff, untracked} {
		for _, line := range strings.Split(out, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				files = append(files, clean(line))
			}
		}
	}
	return Change{BaseCommit: base, Files: sortedUnique(files)}, nil
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return stdout.String(), nil
}
