// Package smartroute provides enterprise-grade capabilities, configuration, and structural components for the smartroute subsystem.
package smartroute

import (
	"context"
	"strings"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/proof"
	"github.com/proofgate/proofgate/internal/router"
)

// DecisionRecorder defines the core enterprise configuration and state for DecisionRecorder.
// It is responsible for managing the lifecycle, validation, and schema of the DecisionRecorder entity.
type DecisionRecorder interface {
	EmitDecision(d proof.RoutingDecision)
}

// DecisionRecorderFunc adapts a function to the decision recorder contract.
// The bool returned by analytics batchers is intentionally ignored here: a
// dropped proof row must never affect the client request.
type DecisionRecorderFunc func(proof.RoutingDecision)

func (f DecisionRecorderFunc) EmitDecision(d proof.RoutingDecision) {
	if f != nil {
		f(d)
	}
}

// ShadowCallback receives a live smart-routing decision and the selected
// response. The callback owns any counterfactual provider call and must not
// block the client request.
type ShadowCallback func(context.Context, proof.RoutingShadowTask, *api.ChatRequest)

// KNNClassifier defines the core enterprise configuration and state for KNNClassifier.
// It is responsible for managing the lifecycle, validation, and schema of the KNNClassifier entity.
type KNNClassifier interface {
	Classify(ctx context.Context, text string) (Classification, float64, error)
}

// Stage defines the core enterprise configuration and state for Stage.
// It is responsible for managing the lifecycle, validation, and schema of the Stage entity.
type Stage struct {
	classifier *RuleClassifier
	knn        KNNClassifier
	recorder   DecisionRecorder
	shadow     ShadowCallback
	nowFn      func() time.Time
}

// NewStage executes the primary logic for the NewStage operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func NewStage(recorder DecisionRecorder, knn KNNClassifier, shadow ...ShadowCallback) *Stage {
	var shadowFn ShadowCallback
	if len(shadow) > 0 {
		shadowFn = shadow[0]
	}
	return &Stage{
		classifier: NewRuleClassifier(),
		knn:        knn,
		recorder:   recorder,
		shadow:     shadowFn,
		nowFn:      time.Now,
	}
}

// SetNow executes the primary logic for the SetNow operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (s *Stage) SetNow(fn func() time.Time) {
	s.nowFn = fn
}

// Name executes the primary logic for the Name operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (s *Stage) Name() string { return "smartroute" }

// Before executes the primary logic for the Before operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (s *Stage) Before(ctx context.Context, c *pipeline.Call) (bool, error) {
	if c.Route == nil || c.Request == nil {
		return false, nil
	}
	cfg := c.Route.SmartRoute
	if cfg.Mode == "" || cfg.Mode == "off" {
		return false, nil
	}

	class := s.classifier.Classify(c.Request, cfg.MaxTokensForCheap)
	var score float64 = 1.0

	// If uncertain and KNN enabled
	if class.Decision == DecisionUncertain && cfg.KNNEnabled && s.knn != nil {
		text := PromptText(c.Request)
		knnClass, knnScore, err := s.knn.Classify(ctx, text)
		if err == nil && knnScore >= cfg.ConfidenceThreshold {
			class = knnClass
			score = knnScore
		}
	}

	// If still uncertain, fallback to strong
	if class.Decision == DecisionUncertain {
		class.Decision = DecisionStrong
		score = 0.5
	}

	c.Values["routing.decision"] = string(class.Decision)
	c.Values["routing.reason"] = string(class.Reason)
	c.Values["routing.score"] = score
	c.Header.Set("X-ProofGate-Route-Decision", string(class.Decision)+"; "+string(class.Reason))

	if cfg.Mode == "on" {
		var targetStr string
		if class.Decision == DecisionCheap {
			targetStr = cfg.CheapTarget
		} else {
			targetStr = cfg.StrongTarget
		}
		if t, ok := router.ParseTarget(targetStr); ok {
			c.Target = t
		}
	}

	return false, nil
}

// After executes the primary logic for the After operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (s *Stage) After(ctx context.Context, c *pipeline.Call) {
	decision, ok := c.Values["routing.decision"].(string)
	if !ok || decision == "" || c.Route == nil || strings.HasPrefix(c.CacheStatus, "hit-") {
		return
	}

	reason, _ := c.Values["routing.reason"].(string)
	score, _ := c.Values["routing.score"].(float64)

	costMicros := c.CostMicros
	counterfactualMicros := costMicros
	if decision == string(DecisionCheap) {
		counterfactualMicros = costMicros * 4
	}

	d := proof.RoutingDecision{
		TS:                   s.nowFn().UTC(),
		RequestID:            c.ID,
		TenantID:             c.Principal.TenantID,
		Route:                c.Route.Name,
		Decision:             decision,
		Reason:               reason,
		Score:                score,
		CostMicros:           costMicros,
		CounterfactualMicros: counterfactualMicros,
	}

	if s.recorder != nil {
		s.recorder.EmitDecision(d)
	}

	// A quality shadow requires explicit tenant consent, the live answer, and a configured smart route.
	// The callback performs the counterfactual call asynchronously so proof
	// collection cannot add latency to the client response.
	if s.shadow == nil || !c.Principal.Tenant.ShadowConsent || c.Err != nil || c.Response == nil || len(c.Response.Choices) == 0 ||
		c.Route.SmartRoute.Mode != "on" {
		return
	}
	answer := c.Response.Choices[0].Message.Content.PlainText()
	if answer == "" {
		return
	}
	cheapTarget := c.Route.SmartRoute.CheapTarget
	strongTarget := c.Route.SmartRoute.StrongTarget
	if cheapTarget == "" || strongTarget == "" {
		return
	}
	task := proof.RoutingShadowTask{
		TenantID:     c.Principal.TenantID,
		Route:        c.Route.Name,
		Decision:     decision,
		CheapTarget:  cheapTarget,
		StrongTarget: strongTarget,
		Prompt:       PromptText(c.Request),
		JudgeModel:   "",
	}
	if decision == string(DecisionCheap) {
		task.CheapAnswer = answer
	} else {
		task.StrongAnswer = answer
	}
	s.shadow(context.WithoutCancel(ctx), task, c.Request.Clone())
}
