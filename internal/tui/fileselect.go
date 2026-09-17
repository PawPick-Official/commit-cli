package tui

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alecthomas/chroma/v2/quick"
	"github.com/charmbracelet/bubbles/spinner"
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

	// Background preview queue: previews render one by one in tea.Cmds
	// (Bubble Tea runs those off the main thread) and come back as
	// previewReadyMsg. rendering holds the path in flight ("" = idle);
	// staleRendering drops its result when a toggle outdated it mid-flight.
	rendering      string
	staleRendering bool
	spinner        spinner.Model

	hovered string
	err     error

	width        int
	height       int
	listWidth    int
	previewWidth int
	frameHeight  int
}

func newFilePicker(statuses []git.FileStatus) *filePicker {
	spin := spinner.New(spinner.WithSpinner(spinner.Line))
	spin.Style = loaderStyle
	p := &filePicker{
		files:     make(map[string]*git.FileStatus, len(statuses)),
		synced:    make(map[string]bool, len(statuses)),
		diffCache: make(map[string]string, len(statuses)),
		spinner:   spin,
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

	// rebuildForm queued a render whose Cmd is discarded here (the chain
	// really starts in Init): reset the flag so the queue can start.
	p.rendering = ""

	return p
}

// optionPrefixWidth is the width huh reserves at the start of every option
// line: the "> " cursor selector plus the "[•] "/"[ ] " selection prefix.
const optionPrefixWidth = 6

// listPaneWidth returns the outer width of the file list pane for a terminal
// of the given width.
func listPaneWidth(termWidth int) int {
	return max(20, termWidth/3)
}

// labelWidth returns the max width of a file label ("XY path") so that an
// option always fits on a single line of the list pane.
func (p *filePicker) labelWidth() int {
	width := p.width
	if width <= 0 {
		width = 80
	}
	pane := listPaneWidth(width)
	// Pane padding (2) + huh base margin (2) + option prefix (6).
	return max(8, pane-2-2-optionPrefixWidth)
}

// applySize constrains the fresh form to the terminal dimensions. huh only
// auto-sizes a form when it receives a WindowSizeMsg, which a rebuilt form
// never gets: without this, toggling a file (which rebuilds the form)
// renders every option at full width, pushing the header off-screen and
// wrapping long paths onto two lines.
func (p *filePicker) applySize() {
	if p.width <= 0 || p.height <= 0 {
		return
	}
	pane := listPaneWidth(p.width)
	// The field box plus the huh base margin must fit the pane content, so
	// pre-truncated labels never wrap onto a second line.
	p.form.WithWidth(max(10, pane-4))

	// Only cap when the options overflow, so small repos keep a compact
	// list. Both levels are capped: the field height pins the
	// title/description and scrolls the options, the form height shrinks
	// the group viewport (which would otherwise pad back to full height).
	// The exact chrome (title + wrapped description lines) is measured, so
	// shrink until the form fits. Must run after form.Init() so the
	// viewports hold content.
	if needed := len(p.order) + 1 + 2; needed > p.height {
		h := max(5, p.height-4)
		for {
			p.select_.Height(h)
			p.form.WithHeight(h + 2)
			if h <= 5 || lipgloss.Height(p.form.View()) <= p.height-2 {
				break
			}
			h--
		}
	}
}

// rebuildForm recreates the multiselect with labels matching the current file
// statuses, keeping the cursor in place so toggling feels uninterrupted. It
// returns the form's Init command, which must be run for the new form to
// become active and render.
func (p *filePicker) rebuildForm() tea.Cmd {
	maxPath := max(1, p.labelWidth()-7) // room for the "XY " status prefix
	options := make([]huh.Option[string], 0, len(p.order))
	for _, path := range p.order {
		options = append(options, huh.NewOption(statusLabel(p.files[path], maxPath), path))
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

	// The group help footer is hidden: its single-line key list overflows
	// the narrow pane, and the description already documents the keys.
	p.form = huh.NewForm(huh.NewGroup(p.select_).Title("Select file to commit\n").WithShowHelp(false)).WithTheme(theme)

	// A fresh form renders nothing until Init activates its group.
	init := p.form.Init()

	// Size after Init so applySize measures a rendered form.
	p.applySize()

	p.setCursor(cursor)
	p.refreshPreview()

	// Restart the queue if idle (e.g. a toggle invalidated a preview while
	// nothing was in flight); no-op when a render already runs.
	return tea.Batch(init, p.startNextRender())
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

// previewReadyMsg carries a background-rendered preview back to the main
// thread: git diff + chroma highlighting run inside the tea.Cmd.
type previewReadyMsg struct {
	path    string
	content string
}

// renderPreview computes the colored diff of a file, or a placeholder when
// there is nothing to show. It only touches its by-value status copy, so it
// is safe to run off the main thread: toggling a file mutates the original
// struct while this may be reading.
func renderPreview(status git.FileStatus) string {
	content := diffContent(&status)
	if content == "" {
		content = lipgloss.NewStyle().Foreground(lipgloss.Color("#7f849c")).
			Render("No diff for " + status.Path)
	}
	return content
}

// startNextRender queues the render of the next uncached preview, one at a
// time with no priority: the hovered file simply shows the loader until its
// turn comes. It returns nil when a render is already in flight or the queue
// is drained, so at most one background render ever runs.
func (p *filePicker) startNextRender() tea.Cmd {
	if p.rendering != "" {
		return nil
	}
	for _, path := range p.order {
		if _, ok := p.diffCache[path]; ok {
			continue
		}
		status := *p.files[path] // copy: the background Cmd must not share state
		p.rendering = path
		p.staleRendering = false
		return tea.Batch(
			func() tea.Msg { return previewReadyMsg{path: path, content: renderPreview(status)} },
			p.spinner.Tick,
		)
	}
	return nil
}

// handlePreviewReady stores a background render and chains the next one. A
// result outdated by a mid-flight toggle is dropped and re-queued.
func (p *filePicker) handlePreviewReady(msg previewReadyMsg) (tea.Model, tea.Cmd) {
	p.rendering = ""
	if p.staleRendering {
		p.staleRendering = false
		delete(p.diffCache, msg.path)
		return p, p.startNextRender()
	}
	p.diffCache[msg.path] = msg.content
	p.refreshPreview() // swaps the loader for content if still hovered
	return p, p.startNextRender()
}

// loaderView renders the waiting state shown while the hovered file has no
// cached preview yet.
func (p *filePicker) loaderView() string {
	return loaderStyle.Render(p.spinner.View() + " Loading diff…")
}

// refreshPreview loads the diff of the currently hovered file into the
// viewport from the cache, or the loader when its background render has not
// finished yet. It never renders synchronously: the main thread stays
// responsive no matter how slow git or chroma are.
func (p *filePicker) refreshPreview() {
	hovered, ok := p.select_.Hovered()
	if !ok {
		return
	}

	content, cached := p.diffCache[hovered]
	if !cached {
		// Background render pending: show the loader until previewReadyMsg.
		p.preview.SetContent(p.loaderView())
		if p.hovered != hovered {
			p.hovered = hovered
			p.preview.GotoTop()
		}
		return
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
	// The form activates on the main thread while the first preview renders
	// in the background; not-yet-rendered files show the loader.
	return tea.Batch(p.form.Init(), p.startNextRender())
}

func (p *filePicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Background renders land here, including while another page is shown
	// (see rootModel): the queue must keep draining.
	if ready, ok := msg.(previewReadyMsg); ok {
		return p.handlePreviewReady(ready)
	}

	// Advance the loader animation while renders are in flight. Ticks stop
	// with the queue: no Tick cmd is returned once idle.
	if _, ok := msg.(spinner.TickMsg); ok {
		var cmd tea.Cmd
		p.spinner, cmd = p.spinner.Update(msg)
		if p.rendering == "" {
			return p, nil
		}
		if _, cached := p.diffCache[p.hovered]; !cached {
			p.preview.SetContent(p.loaderView())
		}
		return p, cmd
	}

	if msg, ok := msg.(tea.WindowSizeMsg); ok {
		// Rebuild so labels are re-truncated and the option viewport is
		// height-limited for the new size. Only when the size actually
		// changed: form.Init() re-emits WindowSize, which must not loop.
		if msg.Width != p.width || msg.Height != p.height {
			p.width, p.height = msg.Width, msg.Height
			return p, p.rebuildForm()
		}
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
		if path == p.rendering {
			// A render of this file is in flight with the old state: drop
			// its result on arrival and re-queue it.
			p.staleRendering = true
		}
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
// previewWidth/frameHeight are the preview's outer box (border included);
// the viewport is sized to the inner area so its lines never overflow the
// border and wrap onto extra lines.
func (p *filePicker) setSize(width, height int) {
	p.width, p.height = width, height

	p.listWidth = listPaneWidth(p.width)
	// The border stacks outside Width/Height: the preview totals
	// previewWidth+2 by frameHeight+2.
	p.previewWidth = max(8, p.width-p.listWidth-2)
	p.frameHeight = max(2, p.height-2)

	innerW := max(0, p.previewWidth-2)
	innerH := max(0, p.frameHeight)
	if p.preview.Width != innerW || p.preview.Height != innerH {
		p.preview.Width = innerW
		p.preview.Height = innerH
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
			b.WriteString(diffRemoveStyle.Render("-") + " │  " + highlightCode(strings.TrimPrefix(line, "-"), file.Path) + "\n")
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

func statusLabel(status *git.FileStatus, maxPathWidth int) string {
	return fmt.Sprintf("%s%s %s",
		statusLetter(status.StatusIndex),
		statusLetter(status.StatusWorktree),
		truncateStart(status.Path, maxPathWidth))
}

// truncateStart shortens s to fit maxWidth, keeping the end (the file name)
// visible: "…/long/path/file.go". Widths are cell widths, not bytes.
func truncateStart(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	const ellipsis = "…"
	budget := maxWidth - lipgloss.Width(ellipsis)
	if budget <= 0 {
		return ellipsis
	}
	runes := []rune(s)
	w := 0
	i := len(runes)
	for i > 0 {
		rw := lipgloss.Width(string(runes[i-1]))
		if w+rw > budget {
			break
		}
		w += rw
		i--
	}
	return ellipsis + string(runes[i:])
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
