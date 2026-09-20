package smartroute

import (
	"context"
	"time"

	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/proof"
	"github.com/proofgate/proofgate/internal/router"
)

type DecisionRecorder interface {
	EmitDecision(d proof.RoutingDecision)
}

type KNNClassifier interface {
	Classify(ctx context.Context, text string) (Classification, float64, error)
}

type Stage struct {
	classifier *RuleClassifier
	knn        KNNClassifier
	recorder   DecisionRecorder
	nowFn      func() time.Time
}

func NewStage(recorder DecisionRecorder, knn KNNClassifier) *Stage {
	return &Stage{
		classifier: NewRuleClassifier(),
		knn:        knn,
		recorder:   recorder,
		nowFn:      time.Now,
	}
}

func (s *Stage) SetNow(fn func() time.Time) {
	s.nowFn = fn
}

func (s *Stage) Name() string { return "smartroute" }

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

func (s *Stage) After(ctx context.Context, c *pipeline.Call) {
	decision, ok := c.Values["routing.decision"].(string)
	if !ok || decision == "" || s.recorder == nil || c.Route == nil {
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

	s.recorder.EmitDecision(d)
}
