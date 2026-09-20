package smartroute

import (
	"regexp"
	"strings"

	"github.com/proofgate/proofgate/internal/api"
)

type Decision string

const (
	DecisionCheap     Decision = "cheap"
	DecisionStrong    Decision = "strong"
	DecisionUncertain Decision = "uncertain"
)

type Reason string

const (
	ReasonCode          Reason = "rule_code"
	ReasonMath          Reason = "rule_math"
	ReasonLongContext   Reason = "rule_long_context"
	ReasonSimpleFactual Reason = "rule_simple_factual"
	ReasonGreeting      Reason = "rule_greeting"
	ReasonNone          Reason = "rule_none"
	ReasonKNN           Reason = "knn_confident"
)

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

type RuleClassifier struct{}

func NewRuleClassifier() *RuleClassifier {
	return &RuleClassifier{}
}

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
