package git

import (
	"fmt"
)

func (file *FileStatus) Stage() error {
	if file.StatusIndex != NOTHING && file.StatusIndex != UNTRACKED && file.StatusWorktree == NOTHING {
		return fmt.Errorf("file %s is already staged", file.Path)
	}

	cmd := Command("add", file.Path)
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to stage file %s: %v", file.Path, err)
	}

	if err := file.Update(); err != nil {
		return err
	}

	// Newly added files need an intent-to-add entry to have a diff.
	return file.EnsureDiffable()
}

func (file *FileStatus) Unstage() error {
	if file.StatusIndex == NOTHING || file.StatusIndex == UNTRACKED {
		return fmt.Errorf("file %s is not staged", file.Path)
	}

	cmd := Command("restore", "--staged", file.Path)
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to unstage file %s: %v", file.Path, err)
	}

	return file.Update()
}
