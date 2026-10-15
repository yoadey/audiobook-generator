package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// InvalidOutputError is a response that is not JSON matching the schema,
// even after a repair round.
type InvalidOutputError struct {
	Err     error
	Content string
	// Truncated is set when the answer hit the output token limit (a
	// smaller request may fit).
	Truncated bool
}

func (e *InvalidOutputError) Error() string { return "invalid model output: " + e.Err.Error() }
func (e *InvalidOutputError) Unwrap() error { return e.Err }

var (
	thinkRe = regexp.MustCompile(`(?s)<think>.*?</think>`)
	fenceRe = regexp.MustCompile("(?s)^```[a-zA-Z]*\\s*\n(.*?)\n?```$")
)

// extractJSON strips what models like to wrap around JSON: reasoning
// blocks, Markdown fences and text before the first or after the last brace.
func extractJSON(content string) []byte {
	s := strings.TrimSpace(thinkRe.ReplaceAllString(content, ""))
	if m := fenceRe.FindStringSubmatch(s); m != nil {
		s = strings.TrimSpace(m[1])
	}
	if i, j := strings.IndexAny(s, "{["), strings.LastIndexAny(s, "}]"); i >= 0 && j > i {
		s = s[i : j+1]
	}
	return []byte(s)
}

var schemas sync.Map // schema name+content → *jsonschema.Schema

func compile(s *Schema) (*jsonschema.Schema, error) {
	key := s.Name + "\x00" + string(s.JSON)
	if v, ok := schemas.Load(key); ok {
		return v.(*jsonschema.Schema), nil
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(s.JSON))
	if err != nil {
		return nil, fmt.Errorf("schema %s: %w", s.Name, err)
	}
	c := jsonschema.NewCompiler()
	url := "mem:///" + s.Name + ".json"
	if err := c.AddResource(url, doc); err != nil {
		return nil, fmt.Errorf("schema %s: %w", s.Name, err)
	}
	compiled, err := c.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("schema %s: %w", s.Name, err)
	}
	schemas.Store(key, compiled)
	return compiled, nil
}

// validate checks raw against the schema and decodes it into out.
func validate(s *Schema, raw []byte, out any) error {
	compiled, err := compile(s)
	if err != nil {
		return err
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("not valid JSON: %w", err)
	}
	if err := compiled.Validate(inst); err != nil {
		var ve *jsonschema.ValidationError
		if errors.As(err, &ve) {
			return errors.New(oneLine(ve))
		}
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// oneLine flattens a validation error tree into a compact message the
// model can act on in a repair request.
func oneLine(ve *jsonschema.ValidationError) string {
	printer := message.NewPrinter(language.English)
	var parts []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			loc := "/" + strings.Join(e.InstanceLocation, "/")
			parts = append(parts, fmt.Sprintf("at %s: %s", loc, e.ErrorKind.LocalizedString(printer)))
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	if len(parts) > 10 {
		parts = append(parts[:10], fmt.Sprintf("and %d more", len(parts)-10))
	}
	return strings.Join(parts, "; ")
}
