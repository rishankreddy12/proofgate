package proof

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type RoutingShadowStore interface {
	InsertRoutingShadow(ctx context.Context, rs []RoutingShadow) error
}

type RoutingJudge interface {
	EvaluateRouting(ctx context.Context, prompt, cheapAnswer, strongAnswer string) (RoutingEvalResult, error)
}

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

type RoutingShadowWorker struct {
	store  RoutingShadowStore
	judge  RoutingJudge
	leader LeaderElection
	spend  SpendGuard
	nowFn  func() time.Time
}

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

func (w *RoutingShadowWorker) SetNow(fn func() time.Time) {
	w.nowFn = fn
}

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
