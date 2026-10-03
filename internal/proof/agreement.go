// Package proof provides enterprise-grade capabilities, configuration, and structural components for the proof subsystem.
package proof

import (
	"context"
	"fmt"

	"github.com/proofgate/proofgate/internal/stats"
)

// PairedLabelsStore defines the core enterprise configuration and state for PairedLabelsStore.
// It is responsible for managing the lifecycle, validation, and schema of the PairedLabelsStore entity.
type PairedLabelsStore interface {
	HumanAndJudge(ctx context.Context, route string) ([]bool, []bool, error)
}

const (
	// DefaultMinPairs defines a specific variation or structural setting for DefaultMinPairs.
	DefaultMinPairs = 50
	// DefaultMinKappa defines a specific variation or structural setting for DefaultMinKappa.
	DefaultMinKappa = 0.60
)

// AgreementResult defines the core enterprise configuration and state for AgreementResult.
// It is responsible for managing the lifecycle, validation, and schema of the AgreementResult entity.
type AgreementResult struct {
	Kappa   float64
	Pairs   int
	OK      bool
	Message string
}

// CheckAgreement executes the primary logic for the CheckAgreement operation.
// It ensures thread-safe execution, input validation, and proper error handling.
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
