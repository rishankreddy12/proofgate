package router

import (
	"github.com/proofgate/proofgate/internal/api"
)

// Capability defines the technical features and constraints of an LLM target.
type Capability struct {
	MaxContextTokens int   `json:"max_context_tokens" yaml:"max_context_tokens"`
	SupportsVision   bool  `json:"supports_vision" yaml:"supports_vision"`
	SupportsTools    *bool `json:"supports_tools" yaml:"supports_tools"`
}

// HasVisionContent checks if the request contains multimodal/image content.
func HasVisionContent(req *api.ChatRequest) bool {
	if req == nil {
		return false
	}
	for _, m := range req.Messages {
		if m.Content.Parts != nil {
			for _, p := range m.Content.Parts {
				if p.Type == "image_url" || p.ImageURL != nil {
					return true
				}
			}
		}
	}
	return false
}

// HasToolContent checks if the request declares or uses tools.
func HasToolContent(req *api.ChatRequest) bool {
	if req == nil {
		return false
	}
	if len(req.Tools) > 0 || len(req.ToolChoice) > 0 {
		return true
	}
	for _, m := range req.Messages {
		if m.Role == "tool" || len(m.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

// Compatible returns true if target capabilities can safely execute the request.
func Compatible(req *api.ChatRequest, cap Capability) bool {
	if req == nil {
		return true
	}

	// 1. Context length check: reject if required prompt + max completion exceeds target window
	if cap.MaxContextTokens > 0 {
		promptTokens := req.EstimatePromptTokens()
		maxCompletion := req.EffectiveMaxTokens(0)
		if promptTokens+maxCompletion > cap.MaxContextTokens {
			return false
		}
	}

	// 2. Vision check: reject text-only targets if request contains images
	if HasVisionContent(req) && !cap.SupportsVision {
		return false
	}

	// 3. Tools check: reject targets that explicitly disable tool calling
	if HasToolContent(req) && cap.SupportsTools != nil && !*cap.SupportsTools {
		return false
	}

	return true
}
