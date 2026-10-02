package proof

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	metricsOnce   sync.Once
	RollbackTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "proofgate_proof_rollback_total",
			Help: "Total automated rollbacks triggered by the proof monitor.",
		},
		[]string{"route", "reason"},
	)
)

func init() {
	metricsOnce.Do(func() {
		// Register with default registerer if not already registered
		_ = prometheus.Register(RollbackTotal)
	})
}
