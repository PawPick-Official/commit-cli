package config

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"text/template"
	"text/template/parse"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"
)

// Option is one possible value for a header token. Name is its label and
// Value is what ends up in the commit message. Both are equal for plain
// string options.
type Option struct {
	Name  string
	Value string
}

// Section holds the options of a header token (e.g. scope, type). The YAML
// list may mix plain strings ("- TOOL") and single-key maps ("- Add: '+'").
type Section []Option

var BodyDefaultTag = []string{
	"Added",
	"Modified",
	"Deleted",
	"Files",
}

// UnmarshalYAML accepts both plain string entries and single-key map
// entries in the options list.
func (s *Section) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		return fmt.Errorf("options must be a list of strings or name/value pairs")
	}
	for i, item := range node.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			*s = append(*s, Option{Name: item.Value, Value: item.Value})
		case yaml.MappingNode:
			if len(item.Content) != 2 {
				return fmt.Errorf("option %d: map must contain a single name/value pair", i+1)
			}
			*s = append(*s, Option{
				Name:  item.Content[0].Value,
				Value: item.Content[1].Value,
			})
		default:
			return fmt.Errorf("option %d: must be a string or a name/value pair", i+1)
		}
	}
	return nil
}

// ConfigRaw mirrors the YAML file. Any key unknown to the struct fields is
// captured into Sections, so adding a new category to the file never
// requires a code change.
type ConfigRaw struct {
	Header   string             `yaml:"header"`
	Body     string             `yaml:"body"`
	Rules    Rules              `yaml:"rules"`
	Sections map[string]Section `yaml:",inline"`
}

// Config is the parsed configuration. Header tokens carry their section
// name; Sections maps each name to its options. A token without a matching
// section is resolved as a free-text input in the form.
type Config struct {
	Header       []token
	Body         string
	Rules        Rules
	sections     map[string]Section
	SectionsBody []string
	HardCoded    []string
}

type token struct {
	Name  string
	Raw   string
	Index int
}

type Rules struct {
	OptionalMessage bool `yaml:"optionalMessage"`
}

func LoadConfig(filePath string) (*Config, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	var cfgRaw ConfigRaw
	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(&cfgRaw); err != nil {
		return nil, fmt.Errorf("failed to load the config: %w", err)
	}

	cfg, err := CreateConfigFromRaw(&cfgRaw)
	if err != nil {
		return nil, fmt.Errorf("failed to create config from raw: %w", err)
	}

	cfg.LoadBody()

	return cfg, nil
}

func CreateConfigFromRaw(raw *ConfigRaw) (*Config, error) {
	return &Config{
		Header:   TokenizeHeader(raw.Header),
		Body:     raw.Body,
		Rules:    raw.Rules,
		sections: raw.Sections,
	}, nil
}

// Section returns the options bound to a header token. ok is false when the
// token has no (or an empty) section in the YAML file, meaning it should be
// filled as a free-text input instead of picked from a select.
func (c *Config) Section(name string) (Section, bool) {
	s, ok := c.sections[name]
	if !ok || len(s) == 0 {
		return nil, false
	}
	return s, true
}

// GetSections returns the human-readable titles of the tokens found in the
// header, in order (e.g. ["Scope", "Type", "Changes"]).
func (c *Config) GetSections() []string {
	sections := []string{}
	titleCaser := cases.Title(language.English, cases.NoLower)

	for _, t := range c.Header {
		if t.Name != "" {
			sections = append(sections, titleCaser.String(t.Name))
		}
	}
	return sections
}

func TokenizeHeader(header string) []token {
	var tokens []token
	var currentToken token
	headerRunes := []rune(header)

	currentToken.Index = 0
	for i := 0; i < len(headerRunes); i++ {
		if headerRunes[i] == '<' {
			if currentToken.Index != i {
				currentToken.Raw = string(headerRunes[currentToken.Index:i])
				tokens = append(tokens, currentToken)
				currentToken = token{Index: i}
			}

			for ; i < len(headerRunes) && headerRunes[i] != '>'; i++ {
			}

			if headerRunes[i] == '>' {
				currentToken.Raw = string(headerRunes[currentToken.Index : i+1])
				currentToken.Name = strings.Trim(currentToken.Raw, "<>")
				tokens = append(tokens, currentToken)
				currentToken = token{Index: i + 1}
			}
		}
	}

	if currentToken.Index < len(headerRunes)-1 {
		currentToken.Raw = string(headerRunes[currentToken.Index:])
		tokens = append(tokens, currentToken)
	}

	return tokens
}

// parseBodyTemplate parses the configured body as a Go text/template.
func ParseBodyTemplate(body string) (*template.Template, error) {
	tmpl, err := template.New("body").Parse(body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse body template: %w", err)
	}
	return tmpl, nil
}

// BodyFields returns the top-level fields referenced by the body template
// (e.g. ".Message" yields "Message"), deduplicated, in order of first
// appearance. Each field can then be matched against the sections of the
// config to build the form.
func (c *Config) BodyFields() ([]string, error) {
	tmpl, err := ParseBodyTemplate(c.Body)
	if err != nil {
		return nil, err
	}

	var fields []string
	seen := map[string]bool{}
	walkTemplateFields(tmpl.Tree.Root, true, &fields, seen)
	return fields, nil
}

// LoadBody initializes the sectionsBody map with the fields referenced by
// the body template. Fields that are hard-coded in the template are added to
// the hardCoded slice instead.
func (c *Config) LoadBody() error {
	fields, err := c.BodyFields()
	if err != nil {
		return err
	}

	for _, field := range fields {
		if slices.Contains(BodyDefaultTag, field) {
			c.HardCoded = append(c.HardCoded, field)
		} else {
			c.SectionsBody = append(c.SectionsBody, field)
		}
	}
	return nil
}

// walkTemplateFields collects the first identifier of every FieldNode in
// the template AST (".Message" → "Message", ".A.B" → "A"). Fields accessed
// inside a range or with block belong to the current element, not the root
// data, so they are skipped (root=false).
func walkTemplateFields(node parse.Node, root bool, fields *[]string, seen map[string]bool) {
	if node == nil {
		return
	}
	switch n := node.(type) {
	case *parse.ListNode:
		// ElseList is a *ListNode left nil without an {{else}} branch; the
		// node == nil check above cannot catch a typed nil pointer.
		if n == nil {
			return
		}
		for _, child := range n.Nodes {
			walkTemplateFields(child, root, fields, seen)
		}
	case *parse.ActionNode:
		walkTemplateFields(n.Pipe, root, fields, seen)
	case *parse.IfNode:
		walkTemplateFields(n.Pipe, root, fields, seen)
		walkTemplateFields(n.List, root, fields, seen)
		walkTemplateFields(n.ElseList, root, fields, seen)
	case *parse.RangeNode:
		walkTemplateFields(n.Pipe, root, fields, seen)
		walkTemplateFields(n.List, false, fields, seen)
		walkTemplateFields(n.ElseList, false, fields, seen)
	case *parse.WithNode:
		walkTemplateFields(n.Pipe, root, fields, seen)
		walkTemplateFields(n.List, false, fields, seen)
		walkTemplateFields(n.ElseList, false, fields, seen)
	case *parse.PipeNode:
		for _, cmd := range n.Cmds {
			walkTemplateFields(cmd, root, fields, seen)
		}
	case *parse.CommandNode:
		for _, arg := range n.Args {
			walkTemplateFields(arg, root, fields, seen)
		}
	case *parse.FieldNode:
		if !root || len(n.Ident) == 0 {
			return
		}
		if name := n.Ident[0]; !seen[name] {
			seen[name] = true
			*fields = append(*fields, name)
		}
	}
}
