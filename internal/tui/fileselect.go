package tui

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alecthomas/chroma/v2/quick"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"pawpick-official/commit-cli/internal/git"
)

// filePicker is the file selection page: a huh multiselect on the left and a
// scrollable diff preview of the hovered file on the right. Since huh has no
// per-toggle callback, the selection is diffed after every update and files
// are staged/unstaged accordingly.
type filePicker struct {
	form    *huh.Form
	select_ *huh.MultiSelect[string]
	preview viewport.Model

	files    map[string]*git.FileStatus
	order    []string // paths in display order, keeps the selection stable
	selected []string // bound to the huh.MultiSelect value
	synced   map[string]bool

	diffCache map[string]string // rendered diff per path, avoids git exec per message

	hovered string
	err     error

	width        int
	height       int
	listWidth    int
	previewWidth int
	frameHeight  int
}

func newFilePicker(statuses []git.FileStatus) *filePicker {
	p := &filePicker{
		files:     make(map[string]*git.FileStatus, len(statuses)),
		synced:    make(map[string]bool, len(statuses)),
		diffCache: make(map[string]string, len(statuses)),
	}

	for i := range statuses {
		status := &statuses[i]
		p.files[status.Path] = status
		p.order = append(p.order, status.Path)
		if isStaged(status) {
			p.selected = append(p.selected, status.Path)
			p.synced[status.Path] = true
		}
	}

	p.rebuildForm()

	return p
}

// rebuildForm recreates the multiselect with labels matching the current file
// statuses, keeping the cursor in place so toggling feels uninterrupted. It
// returns the form's Init command, which must be run for the new form to
// become active and render.
func (p *filePicker) rebuildForm() tea.Cmd {
	options := make([]huh.Option[string], 0, len(p.order))
	for _, path := range p.order {
		options = append(options, huh.NewOption(statusLabel(p.files[path]), path))
	}

	cursor := p.cursorPosition()
	theme := huh.ThemeCatppuccin()

	theme.Focused.Base = theme.Focused.Base.MarginLeft(2)
	theme.Blurred.Base = theme.Blurred.Base.MarginLeft(2)

	p.select_ = huh.NewMultiSelect[string]().
		Description("space: stage/unstage • enter: confirm • esc: quit").
		Options(options...).
		Value(&p.selected).
		Filterable(true).
		Validate(func(value []string) error {
			if len(value) == 0 {
				return fmt.Errorf("select at least one file to commit")
			}
			return nil
		})

	p.form = huh.NewForm(huh.NewGroup(p.select_).Title("Select file to commit\n")).WithTheme(theme)

	// A fresh form renders nothing until Init activates its group.
	init := p.form.Init()

	p.setCursor(cursor)
	p.refreshPreview()

	return init
}

// cursorPosition returns the index of the hovered option, used to restore the
// cursor after a rebuild.
func (p *filePicker) cursorPosition() int {
	if p.select_ == nil {
		return 0
	}
	if hovered, ok := p.select_.Hovered(); ok {
		for i, path := range p.order {
			if path == hovered {
				return i
			}
		}
	}
	return 0
}

// setCursor moves the form cursor to the given option index by sending
// synthetic key presses.
func (p *filePicker) setCursor(cursor int) {
	for range cursor {
		p.form.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
}

// renderDiff computes the colored diff of a file, or a placeholder when
// there is nothing to show.
func (p *filePicker) renderDiff(path string) string {
	content := diffContent(p.files[path])
	if content == "" {
		content = lipgloss.NewStyle().Foreground(lipgloss.Color("#7f849c")).
			Render("No diff for " + path)
	}
	return content
}

// refreshPreview loads the diff of the currently hovered file into the
// viewport, using the cache to avoid re-running git and chroma on every
// message (scroll keys included).
func (p *filePicker) refreshPreview() {
	hovered, ok := p.select_.Hovered()
	if !ok {
		return
	}

	content, cached := p.diffCache[hovered]
	if !cached {
		content = p.renderDiff(hovered)
		p.diffCache[hovered] = content
	}

	// Keep the scroll position when the preview is still about the same file
	// (e.g. after a stage/unstage toggle).
	if p.hovered == hovered {
		offset := p.preview.YOffset
		p.preview.SetContent(content)
		p.preview.SetYOffset(offset)
	} else {
		p.hovered = hovered
		p.preview.SetContent(content)
		p.preview.GotoTop()
	}
}

func (p *filePicker) Init() tea.Cmd {
	return p.form.Init()
}

func (p *filePicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.WindowSizeMsg); ok {
		p.width, p.height = msg.Width, msg.Height
	}

	// Coming back to a validated page (shift+tab): reactivate the form so
	// the selection can be edited again.
	if p.form.State == huh.StateCompleted {
		p.form.State = huh.StateNormal
	}

	form, cmd := p.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		p.form = f
	}

	if p.form.State == huh.StateAborted {
		return p, tea.Quit
	}
	if p.form.State == huh.StateCompleted {
		return p, func() tea.Msg { return pageCompletedMsg{} }
	}

	cmd = tea.Batch(cmd, p.syncStaging())

	p.refreshPreview()
	p.scrollPreview(msg)

	return p, cmd
}

// syncStaging aligns the git index with the multiselect selection: files
// newly selected are staged, files deselected are unstaged. The FileStatus
// structs are shared with the commitWriter page, so refreshing them here
// makes the staged lists (Added/Modified/Deleted) visible over there.
func (p *filePicker) syncStaging() tea.Cmd {
	selected := make(map[string]bool, len(p.selected))
	for _, path := range p.selected {
		selected[path] = true
	}

	changed := false
	for _, path := range p.order {
		want := selected[path]
		if want == p.synced[path] {
			continue
		}

		var err error
		if want {
			err = p.files[path].Stage()
		} else {
			err = p.files[path].Unstage()
		}
		if err != nil {
			p.err = err
			continue
		}
		p.err = nil
		p.synced[path] = want
		delete(p.diffCache, path) // the diff changes with the staging state
		changed = true
	}

	if !changed {
		return nil
	}

	// Rebuild so the labels show the updated status letters (e.g. " M" → "M ").
	return p.rebuildForm()
}

func (p *filePicker) View() string {
	if p.form.State != huh.StateNormal {
		return ""
	}

	list := p.form.View()
	if p.err != nil {
		list += "\n" + errorStyle.Render(p.err.Error())
	}

	p.setSize(p.width, p.height)

	preview := previewBorderStyle.
		Width(p.previewWidth).
		Height(p.frameHeight).
		Render(p.preview.View())

	joined := lipgloss.JoinHorizontal(
		lipgloss.Top,
		paneStyle.Width(p.listWidth).Render(list),
		preview,
	)

	// Clear the remaining lines so the previous longer view cannot linger.
	return joined + strings.Repeat("\n", max(0, p.height-lipgloss.Height(joined)))
}

// setSize syncs this page with the terminal dimensions, so the file list and
// the preview can fill the whole page. It must be called before every render.
func (p *filePicker) setSize(width, height int) {
	p.width, p.height = width, height

	listWidth := p.width / 3
	listWidth = max(20, listWidth)
	previewWidth := p.width - listWidth

	pageHeight := p.height

	p.frameHeight = max(0, pageHeight-2)
	p.listWidth = listWidth
	p.previewWidth = previewWidth - 4

	if p.preview.Width != previewWidth-2 || p.preview.Height != p.frameHeight-2 {
		p.preview.Width = previewWidth
		p.preview.Height = max(0, p.frameHeight)
	}
}

// scrollPreview moves the preview viewport, ignoring keys handled by huh.
func (p *filePicker) scrollPreview(msg tea.Msg) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "ctrl+d":
			p.preview.HalfPageDown()
		case "ctrl+u":
			p.preview.HalfPageUp()
		case "alt+down":
			p.preview.ScrollDown(1)
		case "alt+up":
			p.preview.ScrollUp(1)
		}
	}
}

func isStaged(status *git.FileStatus) bool {
	return status.StatusIndex != git.NOTHING && status.StatusIndex != git.UNTRACKED
}

// diffContent renders a git diff in a compact, opencode-style format: the
// metadata headers are collapsed into one line, added/removed lines get a
// colored background, and code is syntax-highlighted via chroma.
func diffContent(file *git.FileStatus) string {
	diff, err := file.Diff()
	if err != nil {
		return ""
	}

	var b strings.Builder
	var adds, removes int

	flushMeta := func() {
		if adds > 0 || removes > 0 {
			b.WriteString(diffMetaStyle.Render("-/+"))
			if removes > 0 {
				b.WriteString(" " + diffRemoveStyle.Render(fmt.Sprintf("%d", removes)))
			}
			if adds > 0 {
				b.WriteString(" " + diffAddStyle.Render(fmt.Sprintf("%d", adds)))
			}
			b.WriteString("\n")
			adds, removes = 0, 0
		}
	}

	for line := range strings.Lines(diff) {
		line = strings.TrimSuffix(line, "\n")
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			// Dropped.
		case strings.HasPrefix(line, "+"):
			b.WriteString(diffAddStyle.Render("+") + " │  " + highlightCode(strings.TrimPrefix(line, "+"), file.Path) + "\n")
			adds++
		case strings.HasPrefix(line, "-"):
			b.WriteString(diffAddStyle.Render("-") + " │  " + highlightCode(strings.TrimPrefix(line, "-"), file.Path) + "\n")
			removes++
		case strings.HasPrefix(line, "@@"):
			continue
		case strings.HasPrefix(line, "diff ") || strings.HasPrefix(line, "index ") || strings.HasPrefix(line, "new file") || strings.HasPrefix(line, "deleted file"):
		default:
			continue
		}
	}
	flushMeta()

	return b.String()
}

// highlightCode syntax-highlights a line of code using chroma. It falls back
// to the raw line on failure (e.g. no lexer for this extension).
func highlightCode(line, path string) string {
	extension := filepath.Ext(path)
	if extension == "" {
		return line
	}
	var buf bytes.Buffer
	if err := quick.Highlight(&buf, line, strings.TrimPrefix(extension, "."), "terminal256", "catppuccin-mocha"); err != nil {
		return line
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

func statusLabel(status *git.FileStatus) string {
	return fmt.Sprintf("%s%s %s", statusLetter(status.StatusIndex), statusLetter(status.StatusWorktree), status.Path)
}

func statusLetter(status int) string {
	switch status {
	case git.MODIFIED:
		return "M"
	case git.ADDED:
		return "A"
	case git.DELETED:
		return "D"
	case git.RENAMED:
		return "R"
	case git.COPIED:
		return "C"
	case git.UNTRACKED:
		return "?"
	default:
		return " "
	}
}
