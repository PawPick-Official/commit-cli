package git

import (
	"fmt"
	"os/exec"
)

func CheckIfGitIsInstalled() error {
	_, err := exec.LookPath("git")
	if err != nil {
		return fmt.Errorf("git command not found in PATH: %v", err)
	}

	return nil
}

func CheckIfInGitRepo() error {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("not inside a git repository: %v", err)
	}

	return nil
}
