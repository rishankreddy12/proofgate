package guard

import (
	"context"
	"net/http"
	"strconv"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/pipeline"
)

type Stage struct{}

func NewStage() *Stage { return &Stage{} }

func (s *Stage) Name() string { return "guard" }

const (
	PIIMappingKey  = "guard.pii_mapping"
	ChunkFilterKey = "guard.chunk_filter"
)

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
		allMapping := make(map[string]string)
		totalRedacted := 0

		for i := range c.Request.Messages {
			text := c.Request.Messages[i].Content.PlainText()
			matches := DetectPII(text)
			if len(matches) > 0 {
				res := Redact(text, matches, mode)
				c.Request.Messages[i].Content = api.Content{Text: res.Redacted}
				for k, v := range res.Mapping {
					allMapping[k] = v
				}
				totalRedacted += len(matches)
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

func (s *Stage) After(ctx context.Context, c *pipeline.Call) {
	mapping, ok := c.Values[PIIMappingKey].(map[string]string)
	if !ok || len(mapping) == 0 || c.Response == nil {
		return
	}

	for i := range c.Response.Choices {
		text := c.Response.Choices[i].Message.Content.PlainText()
		c.Response.Choices[i].Message.Content = api.Content{Text: Restore(text, mapping)}
	}
}
