package proof

import (
	"context"
	"sync"
	"time"
)

type LabelStore interface {
	UnlabeledShadow(ctx context.Context, route string, lo, hi float64, limit int) ([]ShadowRecord, error)
	InsertLabels(ctx context.Context, ls []CacheLabel) error
}

type CacheJudge interface {
	EvaluateCache(ctx context.Context, r ShadowRecord) (CacheEvalResult, error)
}

type BucketConfig struct {
	Lo, Hi float64
	Limit  int
}

type CacheLabelWorker struct {
	leader     LeaderElection
	spend      SpendGuard
	store      LabelStore
	judge      CacheJudge
	routes     []string
	judgeModel string
	buckets    []BucketConfig
	interval   time.Duration
	nowFn      func() time.Time

	stop chan struct{}
	done chan struct{}
	mu   sync.Mutex
}

func NewCacheLabelWorker(
	leader LeaderElection,
	spend SpendGuard,
	store LabelStore,
	judge CacheJudge,
	routes []string,
	judgeModel string,
	interval time.Duration,
) *CacheLabelWorker {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	return &CacheLabelWorker{
		leader:     leader,
		spend:      spend,
		store:      store,
		judge:      judge,
		routes:     routes,
		judgeModel: judgeModel,
		buckets: []BucketConfig{
			{Lo: 0.70, Hi: 0.80, Limit: 5},
			{Lo: 0.80, Hi: 0.90, Limit: 10},
			{Lo: 0.90, Hi: 0.98, Limit: 10},
		},
		interval: interval,
		nowFn:    time.Now,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

func (w *CacheLabelWorker) SetBuckets(b []BucketConfig) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buckets = b
}

func (w *CacheLabelWorker) SetNow(fn func() time.Time) {
	w.nowFn = fn
}

func (w *CacheLabelWorker) RunOnce(ctx context.Context) (int, error) {
	if w.leader != nil && !w.leader.IsLeader() {
		return 0, nil
	}

	w.mu.Lock()
	buckets := make([]BucketConfig, len(w.buckets))
	copy(buckets, w.buckets)
	w.mu.Unlock()

	totalLabeled := 0
	for _, route := range w.routes {
		for _, b := range buckets {
			if w.spend != nil {
				can, err := w.spend.CanSpend(ctx, 1000)
				if err != nil || !can {
					return totalLabeled, nil // Paused for today
				}
			}

			recs, err := w.store.UnlabeledShadow(ctx, route, b.Lo, b.Hi, b.Limit)
			if err != nil {
				return totalLabeled, err
			}

			for _, rec := range recs {
				if w.spend != nil {
					can, err := w.spend.CanSpend(ctx, 1000)
					if err != nil || !can {
						return totalLabeled, nil
					}
				}

				res, err := w.judge.EvaluateCache(ctx, rec)
				if err != nil {
					continue
				}

				label := CacheLabel{
					ShadowID:           rec.ID,
					TS:                 w.nowFn().UTC(),
					Rater:              "judge",
					RaterID:            w.judgeModel,
					Acceptable:         res.Acceptable,
					Reason:             res.Reason,
					JudgePromptVersion: res.JudgePromptVersion,
				}

				if err := w.store.InsertLabels(ctx, []CacheLabel{label}); err != nil {
					return totalLabeled, err
				}

				if w.spend != nil {
					_ = w.spend.AddSpend(ctx, 1000)
				}
				totalLabeled++
			}
		}
	}

	return totalLabeled, nil
}

func (w *CacheLabelWorker) Start(ctx context.Context) {
	go func() {
		defer close(w.done)
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()

		for {
			select {
			case <-w.stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = w.RunOnce(ctx)
			}
		}
	}()
}

func (w *CacheLabelWorker) Stop() {
	close(w.stop)
	<-w.done
}
