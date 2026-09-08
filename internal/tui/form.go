package tui

import (
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"pawpick-official/commit-cli/internal/config"
	"pawpick-official/commit-cli/internal/git"
)

// ErrNoChanges is returned by Run when the working tree is clean.
var ErrNoChanges = errors.New("no changes to commit")

// pageCompletedMsg is emitted by a page when the user validates its form
// (e.g. confirms the huh form); the root model advances to the next page.
type pageCompletedMsg struct{}

// Run opens the interactive commit form.
func Run(cfg *config.Config) error {
	statuses, err := git.GetAllFileStatuses()
	if err != nil {
		return fmt.Errorf("failed to read git status: %w", err)
	}
	if len(statuses) == 0 {
		return ErrNoChanges
	}

	_, err = tea.NewProgram(newRootModel(statuses, cfg), tea.WithAltScreen()).Run()
	return err
}

// rootModel orchestrates the pages of the commit form, moving to the next
// page each time one is validated.
type rootModel struct {
	pages []tea.Model
	page  int
}

func newRootModel(statuses []git.FileStatus, cfg *config.Config) *rootModel {
	return &rootModel{
		pages: []tea.Model{
			newFilePicker(statuses),
			newCommitWriter(cfg, statuses),
		},
	}
}

func (m *rootModel) Init() tea.Cmd {
	var cmds []tea.Cmd
	for _, page := range m.pages {
		cmds = append(cmds, page.Init())
	}
	return tea.Batch(cmds...)
}

func (m *rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(pageCompletedMsg); ok {
		if m.page < len(m.pages)-1 {
			m.page++
		}
		return m, nil
	}

	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "esc" {
		return m, tea.Quit
	}

	// WindowSizeMsg is only emitted once at startup (and on resizes): every
	// page must know the terminal size, including the ones not active yet.
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		var cmds []tea.Cmd
		for _, page := range m.pages {
			_, cmd := page.Update(msg)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}

	_, cmd := m.pages[m.page].Update(msg)
	return m, cmd
}

func (m *rootModel) View() string {
	return m.pages[m.page].View()
}

// placeholder is a stub page shown for the sections that are not implemented
// yet.
type placeholder struct{ text string }

func newPlaceholder(text string) *placeholder {
	return &placeholder{text: text}
}

func (p *placeholder) Init() tea.Cmd { return nil }
func (p *placeholder) Update(tea.Msg) (tea.Model, tea.Cmd) {
	return p, nil
}
func (p *placeholder) View() string {
	return "\n  " + p.text
}
