package git

import (
	"fmt"
	"strings"
)

const (
	UNTRACKED = iota
	MODIFIED
	DELETED
	RENAMED
	COPIED
	ADDED
	NOTHING
)

type FileStatus struct {
	Path           string
	StatusIndex    int
	StatusWorktree int
}

func (file *FileStatus) Update() error {
	cmd := Command("status", "--porcelain=v1", "-z", "-uall", file.Path)
	outputBytes, err := cmd.Output()
	if err != nil {
		return err
	}

	statuses, err := fileStatuses(string(outputBytes))
	if err != nil {
		return err
	}

	for _, status := range statuses {
		if status.Path == file.Path {
			file.StatusIndex = status.StatusIndex
			file.StatusWorktree = status.StatusWorktree
			return nil
		}
	}

	return fmt.Errorf("file %s not found in git status output", file.Path)
}

// parseFileStatus parses a single entry of `git status --porcelain=v1 -z`
// output. It returns the parsed status and the number of NUL-separated fields
// the entry spans. Renamed and copied entries span two fields: the new path is
// followed by the original path.
func parseFileStatus(entry string) (FileStatus, int, error) {
	if len(entry) < 3 {
		return FileStatus{}, 0, fmt.Errorf("malformed git status entry: %q", entry)
	}

	statusIndex, err := GetFileStatusFromByte(entry[0])
	if err != nil {
		return FileStatus{}, 0, err
	}
	statusWorktree, err := GetFileStatusFromByte(entry[1])
	if err != nil {
		return FileStatus{}, 0, err
	}

	fields := 1
	if statusIndex == RENAMED || statusIndex == COPIED {
		fields = 2
	}

	return FileStatus{
		Path:           entry[3:],
		StatusIndex:    statusIndex,
		StatusWorktree: statusWorktree,
	}, fields, nil
}

// fileStatuses parses the NUL-separated output of `git status --porcelain=v1 -z`.
func fileStatuses(output string) ([]FileStatus, error) {
	var statuses []FileStatus

	entries := strings.Split(output, "\x00")
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if entry == "" {
			continue
		}

		status, fields, err := parseFileStatus(entry)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)

		// Skip the original path of renamed/copied entries, which is not a
		// status entry of its own.
		i += fields - 1
	}

	return statuses, nil
}

func GetFileStatusFromByte(statusByte byte) (int, error) {
	switch statusByte {
	case 'M':
		return MODIFIED, nil
	case 'D':
		return DELETED, nil
	case 'R':
		return RENAMED, nil
	case 'C':
		return COPIED, nil
	case 'A':
		return ADDED, nil
	case '?':
		return UNTRACKED, nil
	case ' ':
		return NOTHING, nil
	default:
		return -1, fmt.Errorf("unknown status: %c", statusByte)
	}
}

func GetFileStatus(output string) (FileStatus, error) {
	status, _, err := parseFileStatus(output)
	return status, err
}

func GetAllFileStatuses() ([]FileStatus, error) {
	cmd := Command("status", "--porcelain=v1", "-z", "-uall")
	outputBytes, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	return fileStatuses(string(outputBytes))
}
