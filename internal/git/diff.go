package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// Diff returns the diff of the file as displayed by git diff. For untracked
// files it diffs against /dev/null, showing the whole file as an addition.
func (file *FileStatus) Diff() (string, error) {
	var args []string
	switch {
	case file.StatusIndex == UNTRACKED:
		args = []string{"diff", "--no-index", "--", "/dev/null", file.Path}
	case file.StatusIndex != NOTHING:
		// Both staged and unstaged changes relative to HEAD.
		args = []string{"diff", "HEAD", "--", file.Path}
	default:
		args = []string{"diff", "--", file.Path}
	}

	cmd := Command(args...)
	outputBytes, err := cmd.Output()
	if err != nil {
		// git diff --no-index exits with 1 when files differ, which is the
		// expected case for untracked files.
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
			return "", fmt.Errorf("failed to diff file %s: %v", file.Path, err)
		}
	}

	return string(outputBytes), nil
}

// EnsureDiffable makes sure a newly added file has a diff against HEAD by
// registering it with intent-to-add when the index has no entry for it yet.
// It must be called after Stage for files that were untracked.
func (file *FileStatus) EnsureDiffable() error {
	if file.StatusIndex != ADDED || file.StatusWorktree != NOTHING {
		return nil
	}

	cmd := Command("ls-files", "--error-unmatch", "--", file.Path)
	if err := cmd.Run(); err == nil {
		return nil // already tracked, nothing to do
	}

	cmd = Command("add", "-N", "--", file.Path)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to register intent-to-add for %s: %v", file.Path, err)
	}

	return file.Update()
}

// DiffHeader returns the first line of the file's diff, or an empty string.
func (file *FileStatus) DiffHeader() string {
	diff, err := file.Diff()
	if err != nil {
		return ""
	}
	header, _, _ := strings.Cut(diff, "\n")
	return header
}
