package main

import (
	"errors"

	"pawpick-official/commit-cli/internal/config"
	"pawpick-official/commit-cli/internal/git"
	"pawpick-official/commit-cli/internal/tui"
)

func main() {
	if err := git.CheckIfGitIsInstalled(); err != nil {
		tui.Fatal(err)
	}

	if err := git.CheckIfInGitRepo(); err != nil {
		tui.Fatal(err)
	}

	root, err := git.RepoRoot()
	if err != nil {
		tui.Fatal(err)
	}

	root += "/.commit-norm.yaml"

	cfg, err := config.LoadConfig(root)
	if err != nil {
		tui.Fatal(err)
	}

	err = tui.Run(cfg)
	if errors.Is(err, tui.ErrNoChanges) {
		tui.Info("No changes to commit.")
		return
	}

	if err != nil {
		tui.Fatal(err)
	}
}
