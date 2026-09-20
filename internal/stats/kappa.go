package stats

import "errors"

// CohenKappa measures agreement between two binary raters beyond chance.
func CohenKappa(a, b []bool) (float64, int, error) {
	if len(a) != len(b) || len(a) == 0 {
		return 0, 0, errors.New("kappa needs two non-empty label lists of equal length")
	}
	n := float64(len(a))
	var agree, aYes, bYes float64
	for i := range a {
		if a[i] == b[i] {
			agree++
		}
		if a[i] {
			aYes++
		}
		if b[i] {
			bYes++
		}
	}
	po := agree / n
	pe := (aYes/n)*(bYes/n) + (1-aYes/n)*(1-bYes/n)
	if pe == 1 {
		return 1, len(a), nil // both raters constant and identical
	}
	return (po - pe) / (1 - pe), len(a), nil
}
