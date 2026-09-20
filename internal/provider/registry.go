package provider

import (
	"fmt"
	"sort"
)

// Spec is the resolved configuration of one provider (API key already read from its source).
type Spec struct {
	Name    string
	Type    string // openai | anthropic | gemini
	BaseURL string
	APIKey  string
	Headers map[string]string
}

type Registry struct {
	byName map[string]Provider
}

func NewRegistry(specs []Spec) (*Registry, error) {
	r := &Registry{byName: map[string]Provider{}}
	for _, s := range specs {
		if _, dup := r.byName[s.Name]; dup {
			return nil, fmt.Errorf("duplicate provider name %q", s.Name)
		}
		var p Provider
		switch s.Type {
		case "openai":
			p = NewOpenAI(OpenAIConfig{Name: s.Name, BaseURL: s.BaseURL, APIKey: s.APIKey, Headers: s.Headers})
		case "anthropic":
			p = NewAnthropic(AnthropicConfig{Name: s.Name, BaseURL: s.BaseURL, APIKey: s.APIKey})
		case "gemini":
			p = NewGemini(GeminiConfig{Name: s.Name, BaseURL: s.BaseURL, APIKey: s.APIKey})
		default:
			return nil, fmt.Errorf("unknown provider type %q for %q", s.Type, s.Name)
		}
		r.byName[s.Name] = p
	}
	return r, nil
}

func (r *Registry) Get(name string) (Provider, bool) {
	p, ok := r.byName[name]
	return p, ok
}

func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.byName))
	for n := range r.byName {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
