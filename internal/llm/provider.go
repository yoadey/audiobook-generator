package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/yoadey/audiobook-generator/internal/store"
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// StructuredOutputModes are the accepted values of
// LLMProvider.StructuredOutput.
var StructuredOutputModes = []string{store.StructuredJSONSchema, store.StructuredJSONObject, store.StructuredPrompt}

// Normalize fills in defaults of a provider definition.
func Normalize(p *store.LLMProvider) {
	p.Name = strings.TrimSpace(p.Name)
	p.BaseURL = strings.TrimSpace(p.BaseURL)
	p.Model = strings.TrimSpace(p.Model)
	p.ExtraParams = strings.TrimSpace(p.ExtraParams)
	if p.StructuredOutput == "" {
		p.StructuredOutput = store.StructuredJSONSchema
	}
	if p.TimeoutSeconds == 0 {
		p.TimeoutSeconds = 300
	}
	if p.QuotaTimezone == "" {
		p.QuotaTimezone = "UTC"
	}
}

// Validate checks a (normalized) provider definition.
func Validate(p *store.LLMProvider) error {
	var errs []error
	if !nameRe.MatchString(p.Name) {
		errs = append(errs, errors.New("name must be 1-64 letters, digits, '.', '_' or '-'"))
	}
	if u, err := url.Parse(p.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, fmt.Errorf("baseUrl must be an http(s) URL, got %q", p.BaseURL))
	}
	if p.Model == "" {
		errs = append(errs, errors.New("model must not be empty"))
	}
	valid := false
	for _, m := range StructuredOutputModes {
		valid = valid || p.StructuredOutput == m
	}
	if !valid {
		errs = append(errs, fmt.Errorf("structuredOutput must be one of %s", strings.Join(StructuredOutputModes, ", ")))
	}
	if _, err := extraParams(p.ExtraParams); err != nil {
		errs = append(errs, err)
	}
	if p.RPM < 0 || p.TPM < 0 || p.RPD < 0 || p.TPD < 0 || p.MaxOutputTokens < 0 {
		errs = append(errs, errors.New("limits must not be negative"))
	}
	if p.TimeoutSeconds < 1 || p.TimeoutSeconds > 3600 {
		errs = append(errs, errors.New("timeoutSeconds must be between 1 and 3600"))
	}
	if _, err := time.LoadLocation(p.QuotaTimezone); err != nil {
		errs = append(errs, fmt.Errorf("quotaTimezone: %w", err))
	}
	return errors.Join(errs...)
}

// extraParams parses the JSON object merged into request bodies.
func extraParams(s string) (map[string]any, error) {
	if s == "" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil || m == nil {
		return nil, errors.New("extraParams must be a JSON object")
	}
	for _, k := range []string{"model", "messages", "response_format", "stream"} {
		if _, ok := m[k]; ok {
			return nil, fmt.Errorf("extraParams must not set %q", k)
		}
	}
	return m, nil
}

// ProviderConfig is the definition of a provider in the configuration
// (ABG_LLM_PROVIDERS, rendered by the Helm chart from llm.providers).
type ProviderConfig struct {
	Name             string         `json:"name"`
	BaseURL          string         `json:"baseUrl"`
	Model            string         `json:"model"`
	APIKeyEnv        string         `json:"apiKeyEnv"`
	StructuredOutput string         `json:"structuredOutput"`
	ExtraParams      map[string]any `json:"extraParams"`
	Limits           struct {
		RPM int `json:"rpm"`
		TPM int `json:"tpm"`
		RPD int `json:"rpd"`
		TPD int `json:"tpd"`
	} `json:"limits"`
	MaxOutputTokens int    `json:"maxOutputTokens"`
	TimeoutSeconds  int    `json:"timeoutSeconds"`
	QuotaTimezone   string `json:"quotaTimezone"`
	Enabled         *bool  `json:"enabled"`
}

// ParseProviderConfig turns the JSON array of ABG_LLM_PROVIDERS into
// validated providers, positioned in list order.
func ParseProviderConfig(raw string) ([]store.LLMProvider, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var cfgs []ProviderConfig
	if err := dec.Decode(&cfgs); err != nil {
		return nil, fmt.Errorf("ABG_LLM_PROVIDERS: %w", err)
	}
	seen := map[string]bool{}
	out := make([]store.LLMProvider, 0, len(cfgs))
	var errs []error
	for i, c := range cfgs {
		p := store.LLMProvider{
			Name:             c.Name,
			Source:           store.ProviderSourceConfig,
			Position:         i,
			Enabled:          c.Enabled == nil || *c.Enabled,
			BaseURL:          c.BaseURL,
			Model:            c.Model,
			APIKeyEnv:        c.APIKeyEnv,
			StructuredOutput: c.StructuredOutput,
			RPM:              c.Limits.RPM,
			TPM:              c.Limits.TPM,
			RPD:              c.Limits.RPD,
			TPD:              c.Limits.TPD,
			MaxOutputTokens:  c.MaxOutputTokens,
			TimeoutSeconds:   c.TimeoutSeconds,
			QuotaTimezone:    c.QuotaTimezone,
		}
		if len(c.ExtraParams) > 0 {
			b, err := json.Marshal(c.ExtraParams)
			if err != nil {
				return nil, err
			}
			p.ExtraParams = string(b)
		}
		Normalize(&p)
		if err := Validate(&p); err != nil {
			errs = append(errs, fmt.Errorf("ABG_LLM_PROVIDERS[%d] (%s): %w", i, c.Name, err))
		}
		if seen[p.Name] {
			errs = append(errs, fmt.Errorf("ABG_LLM_PROVIDERS: duplicate name %q", p.Name))
		}
		seen[p.Name] = true
		out = append(out, p)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return out, nil
}
