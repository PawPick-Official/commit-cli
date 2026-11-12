package git

import "testing"

func TestFileStatusesRenamedEntry(t *testing.T) {
	// `git status --porcelain=v1 -z` emits renamed entries as:
	// "R  <new path>\0<original path>\0".
	output := "R  src/content/new.md\x00src/content/old.md\x00 M other.go\x00"

	statuses, err := fileStatuses(output)
	if err != nil {
		t.Fatalf("fileStatuses() error = %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("fileStatuses() returned %d statuses, want 2", len(statuses))
	}

	renamed := statuses[0]
	if renamed.Path != "src/content/new.md" {
		t.Errorf("renamed path = %q, want %q", renamed.Path, "src/content/new.md")
	}
	if renamed.StatusIndex != RENAMED {
		t.Errorf("renamed index status = %d, want RENAMED", renamed.StatusIndex)
	}
	if renamed.StatusWorktree != NOTHING {
		t.Errorf("renamed worktree status = %d, want NOTHING", renamed.StatusWorktree)
	}

	modified := statuses[1]
	if modified.Path != "other.go" {
		t.Errorf("modified path = %q, want %q", modified.Path, "other.go")
	}
	if modified.StatusIndex != NOTHING || modified.StatusWorktree != MODIFIED {
		t.Errorf("modified status = (%d, %d), want (NOTHING, MODIFIED)", modified.StatusIndex, modified.StatusWorktree)
	}
}

func TestFileStatusesCopiedEntry(t *testing.T) {
	output := "C  copy.md\x00source.md\x00?? untracked.md\x00"

	statuses, err := fileStatuses(output)
	if err != nil {
		t.Fatalf("fileStatuses() error = %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("fileStatuses() returned %d statuses, want 2", len(statuses))
	}
	if statuses[0].Path != "copy.md" || statuses[0].StatusIndex != COPIED {
		t.Errorf("copied status = %+v, want path copy.md with COPIED index", statuses[0])
	}
	if statuses[1].Path != "untracked.md" || statuses[1].StatusIndex != UNTRACKED {
		t.Errorf("untracked status = %+v, want path untracked.md with UNTRACKED index", statuses[1])
	}
}

func TestFileStatusesRejectsMalformedEntry(t *testing.T) {
	if _, err := fileStatuses("s"); err == nil {
		t.Fatal("fileStatuses() error = nil, want an error for a malformed entry")
	}
}
