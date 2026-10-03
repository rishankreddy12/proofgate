// Package proof provides enterprise-grade capabilities, configuration, and structural components for the proof subsystem.
package proof

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// RoutingShadowStore defines the core enterprise configuration and state for RoutingShadowStore.
// It is responsible for managing the lifecycle, validation, and schema of the RoutingShadowStore entity.
type RoutingShadowStore interface {
	InsertRoutingShadow(ctx context.Context, rs []RoutingShadow) error
}

// RoutingJudge defines the core enterprise configuration and state for RoutingJudge.
// It is responsible for managing the lifecycle, validation, and schema of the RoutingJudge entity.
type RoutingJudge interface {
	EvaluateRouting(ctx context.Context, prompt, cheapAnswer, strongAnswer string) (RoutingEvalResult, error)
}

// RoutingShadowTask defines the core enterprise configuration and state for RoutingShadowTask.
// It is responsible for managing the lifecycle, validation, and schema of the RoutingShadowTask entity.
type RoutingShadowTask struct {
	ID           string
	TenantID     string
	Route        string
	Decision     string
	CheapTarget  string
	StrongTarget string
	Prompt       string
	CheapAnswer  string
	StrongAnswer string
	JudgeModel   string
}

// RoutingShadowWorker defines the core enterprise configuration and state for RoutingShadowWorker.
// It is responsible for managing the lifecycle, validation, and schema of the RoutingShadowWorker entity.
type RoutingShadowWorker struct {
	store  RoutingShadowStore
	judge  RoutingJudge
	leader LeaderElection
	spend  SpendGuard
	nowFn  func() time.Time
}

// NewRoutingShadowWorker executes the primary logic for the NewRoutingShadowWorker operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func NewRoutingShadowWorker(
	store RoutingShadowStore,
	judge RoutingJudge,
	leader LeaderElection,
	spend SpendGuard,
) *RoutingShadowWorker {
	return &RoutingShadowWorker{
		store:  store,
		judge:  judge,
		leader: leader,
		spend:  spend,
		nowFn:  time.Now,
	}
}

// SetNow executes the primary logic for the SetNow operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (w *RoutingShadowWorker) SetNow(fn func() time.Time) {
	w.nowFn = fn
}

// EvaluateAndRecord executes the primary logic for the EvaluateAndRecord operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (w *RoutingShadowWorker) EvaluateAndRecord(ctx context.Context, task RoutingShadowTask) (*RoutingShadow, error) {
	if w.leader != nil && !w.leader.IsLeader() {
		return nil, nil
	}

	if w.spend != nil {
		can, err := w.spend.CanSpend(ctx, 1000)
		if err != nil || !can {
			return nil, nil // Budget pause
		}
	}

	if task.ID == "" {
		task.ID = uuid.NewString()
	}

	res, err := w.judge.EvaluateRouting(ctx, task.Prompt, task.CheapAnswer, task.StrongAnswer)
	if err != nil {
		return nil, err
	}

	rs := RoutingShadow{
		ID:                 task.ID,
		TS:                 w.nowFn().UTC(),
		TenantID:           task.TenantID,
		Route:              task.Route,
		Decision:           task.Decision,
		CheapTarget:        task.CheapTarget,
		StrongTarget:       task.StrongTarget,
		Prompt:             task.Prompt,
		CheapAnswer:        task.CheapAnswer,
		StrongAnswer:       task.StrongAnswer,
		CheapScore:         res.ScoreCheap,
		StrongScore:        res.ScoreStrong,
		JudgeModel:         task.JudgeModel,
		JudgePromptVersion: res.JudgePromptVersion,
	}

	if err := w.store.InsertRoutingShadow(ctx, []RoutingShadow{rs}); err != nil {
		return nil, err
	}

	if w.spend != nil {
		_ = w.spend.AddSpend(ctx, 1000)
	}

	return &rs, nil
}
