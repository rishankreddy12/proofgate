// Package api defines the OpenAI-compatible wire types that ProofGate exposes.
// This package is responsible for standardizing the structures used for cross-provider
// communication. Types here are marshaled/unmarshaled directly from HTTP JSON payloads.
package api

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Content represents the body of a chat message within the LLM Gateway pipeline.
// In the OpenAI API schema, content can exist as either a plain string (legacy/simple format)
// or an array of rich parts (multimodal/complex format). This struct unifies both forms.
type Content struct {
	// Text contains the content when the message is a simple string.
	// It remains empty if the payload is multimodal (Parts is populated).
	Text string
	// Parts contains the rich media/text array for multimodal payloads.
	// It remains nil if the payload is a simple string.
	Parts []ContentPart
}

// ContentPart represents a single multimodal segment within a rich message payload.
// It maps directly to an object in the JSON array of a multimodal 'content' block.
type ContentPart struct {
	// Type specifies the media type of the part (e.g., "text", "image_url").
	Type string `json:"type"`
	// Text holds the string payload if Type is "text". Ignored otherwise.
	Text string `json:"text,omitempty"`
	// ImageURL holds a reference to an image if Type is "image_url". Ignored otherwise.
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL describes an external or base64-encoded image attached to a message.
type ImageURL struct {
	// URL is the absolute web path or base64 data URI of the image.
	URL string `json:"url"`
	// Detail specifies the resolution or processing quality of the image (e.g., "high", "low").
	Detail string `json:"detail,omitempty"`
}

// MarshalJSON implements the json.Marshaler interface for Content.
// It serializes the struct back into the exact JSON shape provided by the client
// (either a simple JSON string or a JSON array of objects).
func (c Content) MarshalJSON() ([]byte, error) {
	if c.Parts != nil {
		return json.Marshal(c.Parts)
	}
	return json.Marshal(c.Text)
}

// UnmarshalJSON implements the json.Unmarshaler interface for Content.
// It inspects the first non-whitespace byte of the payload to dynamically detect
// whether the content is a JSON string or a JSON array, routing the unmarshal appropriately.
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

// PlainText resolves a unified string representation of the Content.
// If the content is a simple string, it returns that string.
// If the content is multimodal, it filters out all non-text parts (e.g., images)
// and concatenates the remaining text segments with newlines.
// Note: This operation allocates memory for string joining when parts exist.
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

// Clone performs a deep copy of the Content structure.
// This is essential when modifying request contents in the middleware/guard pipeline
// to prevent data races and unintended side-effects on concurrent read-only streams.
func (c Content) Clone() Content {
	out := c
	if c.Parts != nil {
		out.Parts = make([]ContentPart, len(c.Parts))
		for i, p := range c.Parts {
			out.Parts[i] = p
			if p.ImageURL != nil {
				img := *p.ImageURL
				out.Parts[i].ImageURL = &img
			}
		}
	}
	return out
}

// StringOrSlice is a polymorphic wrapper used to deserialize fields that can be
// either a single string or an array of strings in upstream APIs (e.g., stop sequences).
// It normalizes both input forms into a consistent string slice in memory.
type StringOrSlice []string

// UnmarshalJSON implements the json.Unmarshaler interface for StringOrSlice.
// It dynamically checks if the incoming JSON is a single string primitive and wraps it,
// otherwise unmarshals it as a standard slice of strings.
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
