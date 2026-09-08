package git

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

var (
	rootOnce sync.Once
	rootDir  string
	rootErr  error
)

// RepoRoot returns the absolute path of the repository's work tree root.
// The result is cached: the root cannot change while the program runs.
func RepoRoot() (string, error) {
	rootOnce.Do(func() {
		cmd := exec.Command("git", "rev-parse", "--show-toplevel")
		output, err := cmd.Output()
		if err != nil {
			rootErr = fmt.Errorf("failed to resolve repository root: %v", err)
			return
		}
		rootDir = strings.TrimSpace(string(output))
	})

	return rootDir, rootErr
}

// ResetRepoRootCache clears the cached repository root. Production code
// never calls it: it exists for tests that chdir between repositories and
// need RepoRoot to resolve again from the new working directory.
func ResetRepoRootCache() {
	rootOnce = sync.Once{}
	rootDir, rootErr = "", nil
}

// Command returns a git command pinned to the repository root. git status
// porcelain output is always relative to the root, so all commands using
// those paths must run from there, even when the program was started in a
// subdirectory. Falls back to the current directory if the root cannot be
// resolved.
func Command(args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	if root, err := RepoRoot(); err == nil && root != "" {
		cmd.Dir = root
	}
	return cmd
}
