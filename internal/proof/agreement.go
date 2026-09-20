package proof

import (
	"context"
	"fmt"

	"github.com/proofgate/proofgate/internal/stats"
)

type PairedLabelsStore interface {
	HumanAndJudge(ctx context.Context, route string) ([]bool, []bool, error)
}

const (
	DefaultMinPairs = 50
	DefaultMinKappa = 0.60
)

type AgreementResult struct {
	Kappa   float64
	Pairs   int
	OK      bool
	Message string
}

func CheckAgreement(ctx context.Context, store PairedLabelsStore, route string, minPairs int, minKappa float64) (AgreementResult, error) {
	if minPairs <= 0 {
		minPairs = DefaultMinPairs
	}
	if minKappa <= 0 {
		minKappa = DefaultMinKappa
	}

	judge, human, err := store.HumanAndJudge(ctx, route)
	if err != nil {
		return AgreementResult{}, err
	}

	n := len(judge)
	if n < minPairs {
		msg := fmt.Sprintf("insufficient paired labels (need >= %d, have %d)", minPairs, n)
		return AgreementResult{
			Kappa:   0,
			Pairs:   n,
			OK:      false,
			Message: msg,
		}, nil
	}

	kappa, _, err := stats.CohenKappa(judge, human)
	if err != nil {
		return AgreementResult{}, err
	}

	if kappa < minKappa {
		msg := fmt.Sprintf("judge agreement too low (kappa=%.2f < %.2f); threshold auto-apply blocked", kappa, minKappa)
		return AgreementResult{
			Kappa:   kappa,
			Pairs:   n,
			OK:      false,
			Message: msg,
		}, nil
	}

	return AgreementResult{
		Kappa:   kappa,
		Pairs:   n,
		OK:      true,
		Message: "",
	}, nil
}
