// Package router provides enterprise-grade capabilities, configuration, and structural components for the router subsystem.
package router

import (
	"github.com/proofgate/proofgate/internal/api"
)

// Capability defines the technical features and constraints of an LLM target.
// Used by the Router to filter out incompatible models during execution planning.
type Capability struct {
	MaxContextTokens int   `json:"max_context_tokens" yaml:"max_context_tokens"`
	SupportsVision   bool  `json:"supports_vision" yaml:"supports_vision"`
	SupportsTools    *bool `json:"supports_tools" yaml:"supports_tools"`
}

// HasVisionContent inspects the ChatRequest tree to determine if multimodal/image
// payloads are present in any of the messages.
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

// HasToolContent inspects the ChatRequest tree to determine if the user has
// supplied function/tool definitions or if prior tool-call history exists.
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

// Compatible evaluates a request against a target's hardware/API capabilities.
// Returns true if the target can safely execute the request without triggering
// fundamental upstream errors (e.g. 400 Context Length Exceeded).
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
