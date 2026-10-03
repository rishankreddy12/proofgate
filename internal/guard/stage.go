// Package guard provides enterprise-grade capabilities, configuration, and structural components for the guard subsystem.
package guard

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/pipeline"
)

// Stage defines the core enterprise configuration and state for Stage.
// It is responsible for managing the lifecycle, validation, and schema of the Stage entity.
type Stage struct{}

// NewStage executes the primary logic for the NewStage operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func NewStage() *Stage { return &Stage{} }

// Name executes the primary logic for the Name operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (s *Stage) Name() string { return "guard" }

const (
	// PIIMappingKey defines a specific variation or structural setting for PIIMappingKey.
	PIIMappingKey = "guard.pii_mapping"
	// ChunkFilterKey defines a specific variation or structural setting for ChunkFilterKey.
	ChunkFilterKey = "guard.chunk_filter"
)

// Before executes the primary logic for the Before operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (s *Stage) Before(ctx context.Context, c *pipeline.Call) (bool, error) {
	if c.Route == nil || c.Request == nil {
		return false, nil
	}

	guardCfg := c.Route.Guard

	// 1. Prompt Injection Detection
	if guardCfg.Injection.Enabled {
		th := guardCfg.Injection.Threshold
		if th <= 0 {
			th = 0.70
		}
		var maxScore float64
		for _, m := range c.Request.Messages {
			score := DetectInjection(m.Content.PlainText())
			if score > maxScore {
				maxScore = score
			}
		}
		if maxScore >= th {
			if guardCfg.Injection.Action == "warn" {
				c.Header.Set("X-ProofGate-Guard-Warning", "injection")
			} else {
				return false, &api.Error{
					Status:  http.StatusBadRequest,
					Code:    "prompt_injection_detected",
					Message: "Prompt injection detected",
					Type:    "invalid_request_error",
				}
			}
		}
	}

	// 2. Reversible PII Redaction
	if guardCfg.PII.Enabled {
		mode := guardCfg.PII.Mode
		if mode == "" {
			mode = "redact"
		}
		num := make(Numberer)
		allMapping := make(map[string]string)
		totalRedacted := 0

		for i := range c.Request.Messages {
			msg := &c.Request.Messages[i]
			if msg.Content.Parts != nil {
				// Preserve multimodal parts: only redact text parts, leaving images intact
				for pIdx := range msg.Content.Parts {
					part := &msg.Content.Parts[pIdx]
					if part.Type == "text" && part.Text != "" {
						matches := DetectPII(part.Text)
						if len(matches) > 0 {
							res := Redact(part.Text, matches, mode, num)
							part.Text = res.Redacted
							for k, v := range res.Mapping {
								if existing, ok := allMapping[k]; ok && existing != v {
									slog.Warn("PII mapping collision detected", "placeholder", k, "existing", existing, "new", v)
								}
								allMapping[k] = v
							}
							totalRedacted += len(matches)
						}
					}
				}
			} else {
				text := msg.Content.Text
				matches := DetectPII(text)
				if len(matches) > 0 {
					res := Redact(text, matches, mode, num)
					msg.Content = api.Content{Text: res.Redacted}
					for k, v := range res.Mapping {
						if existing, ok := allMapping[k]; ok && existing != v {
							slog.Warn("PII mapping collision detected", "placeholder", k, "existing", existing, "new", v)
						}
						allMapping[k] = v
					}
					totalRedacted += len(matches)
				}
			}
		}

		if totalRedacted > 0 {
			c.Values[PIIMappingKey] = allMapping
			c.Header.Set("X-ProofGate-PII-Redacted", strconv.Itoa(totalRedacted))

			// If streaming, install chunk filter
			if c.Stream && len(allMapping) > 0 {
				restorer := NewStreamRestorer(allMapping)
				c.Values[ChunkFilterKey] = func(ch *api.ChatChunk) *api.ChatChunk {
					if ch == nil || len(ch.Choices) == 0 {
						return ch
					}
					out := *ch
					out.Choices = make([]api.ChunkChoice, len(ch.Choices))
					copy(out.Choices, ch.Choices)
					deltaText := out.Choices[0].Delta.Content
					out.Choices[0].Delta.Content = restorer.Process(deltaText)
					return &out
				}
				c.Values["guard.stream_flush"] = func() string {
					return restorer.Flush()
				}
			}
		}
	}

	return false, nil
}

// Respond builds the client view with restored PII before writing to the client.
// Canonical c.Response (holding placeholders) remains pure for caching and analytics.
func (s *Stage) Respond(ctx context.Context, c *pipeline.Call) {
	mapping, ok := c.Values[PIIMappingKey].(map[string]string)
	if !ok || len(mapping) == 0 || c.Response == nil || c.Stream {
		return
	}

	cr := c.Response.Clone()
	for i := range cr.Choices {
		text := cr.Choices[i].Message.Content.PlainText()
		cr.Choices[i].Message.Content = api.Content{Text: Restore(text, mapping)}
	}
	c.ClientResponse = cr
}

// After executes the primary logic for the After operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (s *Stage) After(ctx context.Context, c *pipeline.Call) {
	// In-place restore has moved to Respond (C2/C3) to prevent races with async caching
	// and ensure non-streaming clients receive the restored view.
}
