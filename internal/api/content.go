package api

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Content is a message content that is either a plain string or a list of parts.
type Content struct {
	Text  string
	Parts []ContentPart
}

type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

func (c Content) MarshalJSON() ([]byte, error) {
	if c.Parts != nil {
		return json.Marshal(c.Parts)
	}
	return json.Marshal(c.Text)
}

func (c *Content) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	*c = Content{}
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		return json.Unmarshal(b, &c.Text)
	}
	return json.Unmarshal(b, &c.Parts)
}

// PlainText joins the text of all parts with newlines. Non-text parts are skipped.
func (c Content) PlainText() string {
	if c.Parts == nil {
		return c.Text
	}
	var texts []string
	for _, p := range c.Parts {
		if p.Type == "text" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// StringOrSlice accepts "x" or ["x","y"] and always marshals as a list.
type StringOrSlice []string

func (s *StringOrSlice) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if string(b) == "null" {
		*s = nil
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var one string
		if err := json.Unmarshal(b, &one); err != nil {
			return err
		}
		*s = StringOrSlice{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*s = many
	return nil
}
