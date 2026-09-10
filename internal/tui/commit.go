package tui

import (
	"bytes"
	"fmt"
	"strings"

	"pawpick-official/commit-cli/internal/config"
	"pawpick-official/commit-cli/internal/git"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// commitWriter is the message page: it generates one huh field per token of
// the configured header. A token whose section exists in the YAML becomes a
// select, a token without a section becomes a free-text input. The commit
// message is assembled from the header tokens and the body template.
type commitWriter struct {
	form    *huh.Form
	preview viewport.Model

	config *config.Config
	files  []*git.FileStatus

	fields     []commitField // one per header token, in display order
	bodyfields []commitField // one per body token, in display order

	err error

	width  int
	height int
}

// commitField binds a header token to the value typed or picked by the
// user. The value lives in the slice so huh can take its address.
type commitField struct {
	name  string // header token name, e.g. "scope"
	value string // bound to the huh field
}

func newCommitWriter(cfg *config.Config, statuses []git.FileStatus) *commitWriter {
	c := &commitWriter{config: cfg}

	for i := range statuses {
		c.files = append(c.files, &statuses[i])
	}

	for _, t := range cfg.Header {
		if t.Name != "" {
			c.fields = append(c.fields, commitField{name: t.Name})
		}
	}

	c.rebuildForm()

	return c
}

// rebuildForm recreates the huh form from the config: one huh.Select per
// token with options (c.config.Section(name) ok), one huh.Input per token
// without. It returns the form's Init command, which must be run for the
// new form to become active and render.
func (c *commitWriter) rebuildForm() tea.Cmd {
	messageFields := make([]huh.Field, 0, len(c.fields))
	theme := huh.ThemeCatppuccin()

	theme.Focused.Base = theme.Focused.Base.MarginLeft(2)
	theme.Blurred.Base = theme.Blurred.Base.MarginLeft(2)

	for i := range c.fields {
		field := &c.fields[i] // pointer to the slice element, not a copy
		if options, ok := c.config.Section(field.name); ok {
			huhOptions := make([]huh.Option[string], 0, len(options))
			for _, option := range options {
				huhOptions = append(huhOptions, huh.NewOption(option.Name, option.Value))
			}
			messageFields = append(messageFields, huh.NewSelect[string]().Title(field.name).Options(huhOptions...).Value(&field.value))
		} else {
			messageFields = append(messageFields, huh.NewInput().Title(field.name).Value(&field.value))
		}
	}

	bodyFields := make([]huh.Field, 0, len(c.config.SectionsBody))
	c.bodyfields = make([]commitField, 0, len(c.config.SectionsBody))

	for i, field := range c.config.SectionsBody {
		c.bodyfields = append(c.bodyfields, commitField{name: field})
		bodyFields = append(bodyFields, huh.NewInput().Title(c.bodyfields[i].name).Value(&c.bodyfields[i].value))
	}

	allFields := append(
		[]huh.Field{huh.NewNote().Title("Commit Message")},
		messageFields...,
	)
	if len(bodyFields) > 0 {
		allFields = append(allFields, huh.NewNote().Title("Commit Body"))
		allFields = append(allFields, bodyFields...)
	}

	c.form = huh.NewForm(huh.NewGroup(allFields...)).WithTheme(theme).WithLayout(huh.LayoutStack)

	init := c.form.Init()

	return init
}

// header assembles the first line of the commit message by substituting
// each token of the configured header with the matching field value.
func (c *commitWriter) header() string {
	header := ""

	for _, token := range c.config.Header {
		if token.Name != "" {
			for _, field := range c.fields {
				if field.name == token.Name {
					header += field.value
					break
				}
			}
		} else {
			header += token.Raw
		}
	}

	return header
}

// body renders the configured body template with the collected values and
// the staged files.
func (c *commitWriter) body() string {
	tmpl, err := config.ParseBodyTemplate(c.config.Body)
	if err != nil {
		return fmt.Sprintf("error parsing body template: %v", err)
	}

	// Build the data map: user-filled fields + hard-coded file lists.
	data := make(map[string]any, len(c.bodyfields)+len(c.config.HardCoded))

	for _, field := range c.bodyfields {
		data[field.name] = field.value
	}

	// Hard-coded tags are populated from the staged files.
	staged := c.stagedFiles()
	for _, tag := range c.config.HardCoded {
		switch tag {
		case "Added":
			data["Added"] = filterByStatus(staged, git.ADDED)
		case "Modified":
			data["Modified"] = filterByStatus(staged, git.MODIFIED)
		case "Deleted":
			data["Deleted"] = filterByStatus(staged, git.DELETED)
		case "Files":
			data["Files"] = paths(staged)
		}
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Sprintf("error executing body template: %v", err)
	}

	return strings.TrimSpace(buf.String())
}

// stagedFiles returns the files that are staged (StatusIndex != NOTHING/UNTRACKED).
func (c *commitWriter) stagedFiles() []*git.FileStatus {
	var staged []*git.FileStatus
	for _, f := range c.files {
		if f.StatusIndex != git.NOTHING && f.StatusIndex != git.UNTRACKED {
			staged = append(staged, f)
		}
	}
	return staged
}

// filterByStatus returns the paths of files matching the given status.
func filterByStatus(files []*git.FileStatus, status int) []string {
	var out []string
	for _, f := range files {
		if f.StatusIndex == status {
			out = append(out, f.Path)
		}
	}
	return out
}

// paths returns the paths of all given files.
func paths(files []*git.FileStatus) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	return out
}

// message returns the full commit message: header + body.
func (c *commitWriter) message() string {
	header := c.header()
	body := c.body()

	if body == "" {
		return header
	}
	return header + "\n\n" + body
}

func (c *commitWriter) messageDisplay() string {
	header := c.header()
	body := c.body()

	if len(header) > 72 {
		body = "…" + header[72:] + "\n\n" + body
		header = header[:72] + "…"
	}

	return "Message:\n\n" + header + "\n\nDescription:\n\n" + body
}

// commit runs git commit with the assembled message.
func (c *commitWriter) commit() error {
	cmd := git.Command("commit", "-m", c.message())

	return cmd.Run()
}

func (c *commitWriter) Init() tea.Cmd {
	if c.form == nil {
		return nil
	}
	return c.form.Init()
}

func (c *commitWriter) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.WindowSizeMsg); ok {
		c.width, c.height = msg.Width, msg.Height
	}

	form, cmd := c.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		c.form = f
	}

	if c.form.State == huh.StateAborted {
		return c, tea.Quit
	}

	if c.form.State == huh.StateCompleted {
		if err := c.commit(); err != nil {
			c.err = err
		}
		return c, tea.Quit
	}
	return c, cmd
}

func (c *commitWriter) View() string {
	if c.form == nil || c.form.State != huh.StateNormal {
		return ""
	}

	list := c.form.View()
	if c.err != nil {
		list += "\n" + errorStyle.Render(c.err.Error())
	}

	// Default to 80x24 if the terminal size hasn't been reported yet.
	width := max(c.width, 80)
	height := max(c.height, 24)

	// Same sizing logic as the filePicker: 1/3 form, 2/3 preview.
	listWidth := max(20, width/2)
	previewWidth := width - listWidth - 4
	frameHeight := max(0, height-2)

	// Update the preview content and size.
	c.preview.SetContent(c.messageDisplay())
	c.preview.Width = previewWidth
	c.preview.Height = frameHeight

	preview := previewBorderStyle.
		Width(previewWidth).
		Height(frameHeight).
		Render(c.preview.View())

	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		paneStyle.Width(listWidth).Render(list),
		preview,
	)
}
