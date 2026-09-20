package guard

import (
	"math"
	"regexp"
)

type Heuristic struct {
	Name    string
	Pattern *regexp.Regexp
	Weight  float64
}

var heuristics = []Heuristic{
	// 1. Instruction overrides
	{
		Name:    "override_instructions",
		Pattern: regexp.MustCompile(`(?i)\b(?:ignore|disregard|forget|bypass)\s+(?:all\s+|everything\s+)?(?:previous|prior|above)?\s*(?:instructions?|directives?|rules?|commands?|above)\b`),
		Weight:  0.50,
	},
	{
		Name:    "developer_dan_mode",
		Pattern: regexp.MustCompile(`(?i)\b(?:developer\s+mode|dan\s+mode|jailbreak|unrestricted\s+mode|unconstrained|do\s+anything\s+now)\b`),
		Weight:  0.50,
	},
	{
		Name:    "system_directives",
		Pattern: regexp.MustCompile(`(?i)(?:system\s+prompt\s*:|new\s+instructions?\s*:|override\s+directive\s*:)`),
		Weight:  0.40,
	},

	// 2. Role confusion
	{
		Name:    "role_delimiters",
		Pattern: regexp.MustCompile(`(?i)(?:\n\s*human\s*:|\n\s*assistant\s*:|\n\s*system\s*:)`),
		Weight:  0.40,
	},
	{
		Name:    "special_tokens",
		Pattern: regexp.MustCompile(`(?i)(?:<\|im_start\|>|<\|im_end\|>|<\|system\|>|<\|user\|>|<\|assistant\|>)`),
		Weight:  0.50,
	},
	{
		Name:    "fake_system_tags",
		Pattern: regexp.MustCompile(`(?i)(?:\[system\]|\[\/system\]|<system>|<\/system>)`),
		Weight:  0.40,
	},

	// 3. Output constraints / prompt extraction
	{
		Name:    "extract_system_prompt",
		Pattern: regexp.MustCompile(`(?i)\b(?:output|print|show|reveal|repeat|echo)\s+(?:only\s+)?(?:the\s+)?(?:system\s+prompt|instructions?|text\s+above\s+verbatim|initial\s+prompt)\b`),
		Weight:  0.45,
	},
	{
		Name:    "print_before_line",
		Pattern: regexp.MustCompile(`(?i)\bprint\s+everything\s+before\s+this\s+line\b`),
		Weight:  0.45,
	},
	{
		Name:    "ask_system_prompt",
		Pattern: regexp.MustCompile(`(?i)\bwhat\s+(?:is|are)\s+(?:your|the)\s+(?:system\s+prompt|initial\s+(?:prompt|instructions?)|system\s+instructions?)\b`),
		Weight:  0.40,
	},
}

// DetectInjection scores text for prompt injection heuristics.
// Returns a score in [0.0, 1.0].
func DetectInjection(text string) float64 {
	var totalWeight float64
	for _, h := range heuristics {
		if h.Pattern.MatchString(text) {
			totalWeight += h.Weight
		}
	}
	score := math.Min(1.0, totalWeight)
	return math.Round(score*100) / 100
}
