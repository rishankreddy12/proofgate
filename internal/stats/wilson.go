// Package stats holds the small, tested statistics that back every ProofGate claim.
package stats

import "math"

// Wilson returns the Wilson score interval for a binomial proportion.
func Wilson(successes, n int, z float64) (lo, hi float64) {
	if n == 0 {
		return 0, 1
	}
	p := float64(successes) / float64(n)
	nf := float64(n)
	z2 := z * z
	den := 1 + z2/nf
	center := (p + z2/(2*nf)) / den
	half := z * math.Sqrt(p*(1-p)/nf+z2/(4*nf*nf)) / den
	return math.Max(0, center-half), math.Min(1, center+half)
}
