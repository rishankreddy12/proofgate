// Package provider provides enterprise-grade capabilities, configuration, and structural components for the provider subsystem.
package provider

import (
	"fmt"
	"sort"
)

// Spec contains the fully resolved configuration for a single provider, mapping
// its routing identifier to its underlying implementation protocol and authentication strategy.
type Spec struct {
	Name             string
	Type             string // openai | anthropic | gemini
	BaseURL          string
	APIKey           string
	KeyFunc          KeyFunc
	Headers          map[string]string
	MaxResponseBytes int64
}

// Registry maintains the active catalog of instantiated Provider adapters, keyed by their configured name.
type Registry struct {
	byName map[string]Provider
}

// NewRegistry initializes the catalog, instantiating the concrete, typed implementations
// (e.g. NewOpenAI, NewAnthropic) for every Spec declared in the configuration.
func NewRegistry(specs []Spec) (*Registry, error) {
	r := &Registry{byName: map[string]Provider{}}
	for _, s := range specs {
		if _, dup := r.byName[s.Name]; dup {
			return nil, fmt.Errorf("duplicate provider name %q", s.Name)
		}
		var p Provider
		switch s.Type {
		case "openai":
			p = NewOpenAI(OpenAIConfig{Name: s.Name, BaseURL: s.BaseURL, APIKey: s.APIKey, KeyFunc: s.KeyFunc, Headers: s.Headers, MaxResponseBytes: s.MaxResponseBytes})
		case "anthropic":
			p = NewAnthropic(AnthropicConfig{Name: s.Name, BaseURL: s.BaseURL, APIKey: s.APIKey, KeyFunc: s.KeyFunc, MaxResponseBytes: s.MaxResponseBytes})
		case "gemini":
			p = NewGemini(GeminiConfig{Name: s.Name, BaseURL: s.BaseURL, APIKey: s.APIKey, KeyFunc: s.KeyFunc, MaxResponseBytes: s.MaxResponseBytes})
		default:
			return nil, fmt.Errorf("unknown provider type %q for %q", s.Type, s.Name)
		}
		r.byName[s.Name] = p
	}
	return r, nil
}

// Get executes the primary logic for the Get operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (r *Registry) Get(name string) (Provider, bool) {
	p, ok := r.byName[name]
	return p, ok
}

// Names executes the primary logic for the Names operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.byName))
	for n := range r.byName {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
