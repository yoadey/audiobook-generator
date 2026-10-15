package llm

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"text/template"
)

// Prompts are embedded templates below prompts/<name>/v<N>/:
//
//	system.tmpl  system message (text/template)
//	user.tmpl    user message (text/template)
//	schema.json  JSON schema of the answer (optional)
//
// Versions are never edited once results produced with them are stored;
// a changed prompt is a new version directory, and results record the
// prompt ID (name@vN) they came from.
//
//go:embed prompts
var promptFS embed.FS

// Prompt is one version of a prompt template.
type Prompt struct {
	Name    string
	Version int
	system  *template.Template
	user    *template.Template
	schema  *Schema
}

// ID is "name@vN".
func (p *Prompt) ID() string { return fmt.Sprintf("%s@v%d", p.Name, p.Version) }

// Schema of the expected answer (nil for free text).
func (p *Prompt) Schema() *Schema { return p.schema }

// Render fills in the templates.
func (p *Prompt) Render(data any) (Request, error) {
	var sys, usr bytes.Buffer
	if err := p.system.Execute(&sys, data); err != nil {
		return Request{}, fmt.Errorf("prompt %s: %w", p.ID(), err)
	}
	if err := p.user.Execute(&usr, data); err != nil {
		return Request{}, fmt.Errorf("prompt %s: %w", p.ID(), err)
	}
	return Request{
		Prompt: p.ID(),
		Messages: []Message{
			{Role: "system", Content: strings.TrimSpace(sys.String())},
			{Role: "user", Content: strings.TrimSpace(usr.String())},
		},
		Schema: p.schema,
	}, nil
}

// LoadPrompt returns the newest version of a prompt.
func LoadPrompt(name string) (*Prompt, error) {
	entries, err := fs.ReadDir(promptFS, path.Join("prompts", name))
	if err != nil {
		return nil, fmt.Errorf("unknown prompt %q", name)
	}
	latest := 0
	for _, e := range entries {
		if v, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "v")); err == nil && e.IsDir() && v > latest {
			latest = v
		}
	}
	if latest == 0 {
		return nil, fmt.Errorf("prompt %q has no versions", name)
	}
	return LoadPromptVersion(name, latest)
}

// LoadPromptVersion returns a specific version of a prompt.
func LoadPromptVersion(name string, version int) (*Prompt, error) {
	dir := path.Join("prompts", name, fmt.Sprintf("v%d", version))
	p := &Prompt{Name: name, Version: version}
	parse := func(file string) (*template.Template, error) {
		src, err := fs.ReadFile(promptFS, path.Join(dir, file))
		if err != nil {
			return nil, err
		}
		return template.New(file).Option("missingkey=error").Parse(string(src))
	}
	var err error
	if p.system, err = parse("system.tmpl"); err != nil {
		return nil, fmt.Errorf("prompt %s: %w", p.ID(), err)
	}
	if p.user, err = parse("user.tmpl"); err != nil {
		return nil, fmt.Errorf("prompt %s: %w", p.ID(), err)
	}
	if raw, err := fs.ReadFile(promptFS, path.Join(dir, "schema.json")); err == nil {
		if !json.Valid(raw) {
			return nil, fmt.Errorf("prompt %s: schema.json is not valid JSON", p.ID())
		}
		p.schema = &Schema{Name: name, JSON: raw}
		if _, err := compile(p.schema); err != nil {
			return nil, fmt.Errorf("prompt %s: %w", p.ID(), err)
		}
	}
	return p, nil
}

// MustLoadPrompt is LoadPrompt for package-level variables.
func MustLoadPrompt(name string) *Prompt {
	p, err := LoadPrompt(name)
	if err != nil {
		panic(err)
	}
	return p
}
