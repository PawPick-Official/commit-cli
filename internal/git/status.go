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

	for line := range strings.SplitSeq(string(outputBytes), "\x00") {
		if line == "" {
			continue
		}
		status, err := GetFileStatus(line)
		if err != nil {
			return err
		}
		if status.Path == file.Path {
			file.StatusIndex = status.StatusIndex
			file.StatusWorktree = status.StatusWorktree
			return nil
		}
	}

	return fmt.Errorf("file %s not found in git status output", file.Path)
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
	statusBytes := []byte(string([]rune(output)[0:2]))
	statusIndex, err := GetFileStatusFromByte(statusBytes[0])
	if err != nil {
		return FileStatus{}, err
	}
	statusWorktree, err := GetFileStatusFromByte(statusBytes[1])
	if err != nil {
		return FileStatus{}, err
	}

	return FileStatus{
		Path:           string([]rune(output)[3:]),
		StatusIndex:    statusIndex,
		StatusWorktree: statusWorktree,
	}, nil
}

func GetAllFileStatuses() ([]FileStatus, error) {
	var statuses []FileStatus

	cmd := Command("status", "--porcelain=v1", "-z", "-uall")
	outputBytes, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	for line := range strings.SplitSeq(string(outputBytes), "\x00") {
		if line == "" {
			continue
		}
		status, err := GetFileStatus(line)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}

	return statuses, nil
}
