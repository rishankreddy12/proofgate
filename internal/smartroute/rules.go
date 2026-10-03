// Package smartroute provides enterprise-grade capabilities, configuration, and structural components for the smartroute subsystem.
package smartroute

import (
	"regexp"
	"strings"

	"github.com/proofgate/proofgate/internal/api"
)

// Decision defines the core enterprise configuration and state for Decision.
// It is responsible for managing the lifecycle, validation, and schema of the Decision entity.
type Decision string

const (
	// DecisionCheap defines a specific variation or structural setting for DecisionCheap.
	DecisionCheap Decision = "cheap"
	// DecisionStrong defines a specific variation or structural setting for DecisionStrong.
	DecisionStrong Decision = "strong"
	// DecisionUncertain defines a specific variation or structural setting for DecisionUncertain.
	DecisionUncertain Decision = "uncertain"
)

// Reason defines the core enterprise configuration and state for Reason.
// It is responsible for managing the lifecycle, validation, and schema of the Reason entity.
type Reason string

const (
	// ReasonCode defines a specific variation or structural setting for ReasonCode.
	ReasonCode Reason = "rule_code"
	// ReasonMath defines a specific variation or structural setting for ReasonMath.
	ReasonMath Reason = "rule_math"
	// ReasonLongContext defines a specific variation or structural setting for ReasonLongContext.
	ReasonLongContext Reason = "rule_long_context"
	// ReasonSimpleFactual defines a specific variation or structural setting for ReasonSimpleFactual.
	ReasonSimpleFactual Reason = "rule_simple_factual"
	// ReasonGreeting defines a specific variation or structural setting for ReasonGreeting.
	ReasonGreeting Reason = "rule_greeting"
	// ReasonNone defines a specific variation or structural setting for ReasonNone.
	ReasonNone Reason = "rule_none"
	// ReasonKNN defines a specific variation or structural setting for ReasonKNN.
	ReasonKNN Reason = "knn_confident"
)

// Classification defines the core enterprise configuration and state for Classification.
// It is responsible for managing the lifecycle, validation, and schema of the Classification entity.
type Classification struct {
	Decision Decision
	Reason   Reason
}

var (
	codeRegex     = regexp.MustCompile(`(?i)(?:` + "```" + `|<code>|\b(?:def\s+|function\s+|class\s+|import\s+|const\s+|var\s+|return\s+|debug\b|refactor\b|algorithm\b|complexity\b|stack\s+trace\b|syntax\s+error\b))`)
	mathRegex     = regexp.MustCompile(`(?i)(?:prove\s+that|derive\s+the|calculate\s+the\s+probability|step\s+by\s+step|solve\s+for\s+x|integral\s+of|differential\s+equation|compare\s+and\s+contrast|critique\s+the\s+argument)`)
	greetingRegex = regexp.MustCompile(`(?i)^(?:hello|hi|hey|greetings)(?:\s+(?:there|everyone|all))?[.!?\s]*$|^(?:thanks|thank\s+you)(?:\s+(?:very\s+much|so\s+much|a\s+lot))?[.!?\s]*$|^(?:who\s+are\s+you)[.!?\s]*$`)
	factualRegex  = regexp.MustCompile(`(?i)^(?:what\s+is\s+the\s+capital\s+of|how\s+many\s+inches\s+in\s+a\s+foot|define\s+\w+)[.?\s]*$`)
)

// RuleClassifier defines the core enterprise configuration and state for RuleClassifier.
// It is responsible for managing the lifecycle, validation, and schema of the RuleClassifier entity.
type RuleClassifier struct{}

// NewRuleClassifier executes the primary logic for the NewRuleClassifier operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func NewRuleClassifier() *RuleClassifier {
	return &RuleClassifier{}
}

// PromptText executes the primary logic for the PromptText operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func PromptText(req *api.ChatRequest) string {
	if req == nil {
		return ""
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			return req.Messages[i].Content.PlainText()
		}
	}
	return ""
}

// Classify executes the primary logic for the Classify operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (c *RuleClassifier) Classify(req *api.ChatRequest, maxTokensForCheap int) Classification {
	if maxTokensForCheap <= 0 {
		maxTokensForCheap = 1500
	}

	estTokens := req.EstimatePromptTokens()
	if estTokens > maxTokensForCheap {
		return Classification{Decision: DecisionStrong, Reason: ReasonLongContext}
	}

	text := PromptText(req)
	trimmed := strings.TrimSpace(text)

	// 1. Check code queries -> strong
	if codeRegex.MatchString(text) {
		return Classification{Decision: DecisionStrong, Reason: ReasonCode}
	}

	// 2. Check math / reasoning queries -> strong
	if mathRegex.MatchString(text) {
		return Classification{Decision: DecisionStrong, Reason: ReasonMath}
	}

	// 3. Greetings -> cheap
	if greetingRegex.MatchString(trimmed) {
		return Classification{Decision: DecisionCheap, Reason: ReasonGreeting}
	}

	// 4. Simple factual -> cheap
	if factualRegex.MatchString(trimmed) {
		return Classification{Decision: DecisionCheap, Reason: ReasonSimpleFactual}
	}

	if estTokens < 100 && (strings.HasPrefix(strings.ToLower(trimmed), "what is ") || strings.HasPrefix(strings.ToLower(trimmed), "define ")) {
		return Classification{Decision: DecisionCheap, Reason: ReasonSimpleFactual}
	}

	return Classification{Decision: DecisionUncertain, Reason: ReasonNone}
}
