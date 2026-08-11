// Package workspace manages isolated on-disk copies of the chart under test.
//
// A mutation has to exist on disk for Helm to load it, so parallel workers cannot
// share one chart directory. Each worker gets its own full copy: it writes a
// mutated file, runs, then restores the original bytes. Copying once per worker
// rather than once per mutant keeps the filesystem cost proportional to the
// worker count instead of the mutant count.
package workspace

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Workspace is one worker's private copy of the chart.
type Workspace struct {
	// Root is the copied chart directory.
	Root string
	// keep suppresses cleanup, for --keep-workdir debugging.
	keep    bool
	baseDir string
}

// Discovered is the set of chart files eligible for mutation.
type Discovered struct {
	// Templates are chart-relative paths under templates/, including partials.
	Templates []string
	// Values is the chart-relative path to values.yaml, empty when absent.
	Values string
}

// New creates an isolated copy of chartDir under a fresh temp directory.
func New(chartDir string, keep bool) (*Workspace, error) {
	abs, err := filepath.Abs(chartDir)
	if err != nil {
		return nil, err
	}
	base, err := os.MkdirTemp("", "helm-mutation-test-")
	if err != nil {
		return nil, fmt.Errorf("creating workspace: %w", err)
	}
	root := filepath.Join(base, filepath.Base(abs))
	if err := copyTree(abs, root); err != nil {
		os.RemoveAll(base)
		return nil, fmt.Errorf("copying chart into workspace: %w", err)
	}
	return &Workspace{Root: root, keep: keep, baseDir: base}, nil
}

// Close removes the workspace unless keep was requested.
func (w *Workspace) Close() error {
	if w.keep {
		return nil
	}
	return os.RemoveAll(w.baseDir)
}

// Path resolves a chart-relative path inside this workspace.
func (w *Workspace) Path(rel string) string {
	return filepath.Join(w.Root, filepath.FromSlash(rel))
}

// WriteMutation replaces a file's contents, returning a restore function.
//
// The caller must always invoke restore, normally via defer: a leaked mutation
// would silently contaminate every later mutant evaluated by this worker.
func (w *Workspace) WriteMutation(rel string, content []byte) (restore func() error, err error) {
	target := w.Path(rel)
	original, err := os.ReadFile(target)
	if err != nil {
		return nil, fmt.Errorf("reading %s from workspace: %w", rel, err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(target, content, info.Mode().Perm()); err != nil {
		return nil, fmt.Errorf("writing mutation to %s: %w", rel, err)
	}
	return func() error { return os.WriteFile(target, original, info.Mode().Perm()) }, nil
}

// Discover lists the chart files eligible for mutation, relative to chartDir.
//
// Only templates/ and values.yaml are considered. Chart.yaml is excluded: its
// fields are chart identity rather than rendered configuration, and mutating the
// version or name would change every manifest at once.
func Discover(chartDir string) (Discovered, error) {
	var d Discovered
	templatesDir := filepath.Join(chartDir, "templates")

	if _, err := os.Stat(templatesDir); err == nil {
		err := filepath.WalkDir(templatesDir, func(p string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				// helm-unittest keeps its snapshots beside the tests; never mutate them.
				if entry.Name() == "__snapshot__" {
					return filepath.SkipDir
				}
				return nil
			}
			if !isMutableTemplate(entry.Name()) {
				return nil
			}
			rel, err := filepath.Rel(chartDir, p)
			if err != nil {
				return err
			}
			d.Templates = append(d.Templates, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return d, fmt.Errorf("scanning templates: %w", err)
		}
	}

	for _, name := range []string{"values.yaml", "values.yml"} {
		if _, err := os.Stat(filepath.Join(chartDir, name)); err == nil {
			d.Values = name
			break
		}
	}
	return d, nil
}

func isMutableTemplate(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml", ".tpl", ".txt":
		return true
	}
	return false
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		// Symlinks inside a chart are unusual; copy the target's bytes rather than
		// recreating a link that could point outside the workspace.
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(p)
			if err != nil {
				return nil // a broken link is not worth failing the run over
			}
			ri, err := os.Stat(resolved)
			if err != nil || ri.IsDir() {
				return nil
			}
			return copyFile(resolved, target, 0o644)
		}
		return copyFile(p, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
