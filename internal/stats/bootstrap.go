package stats

import (
	"math/rand/v2"
	"sort"
)

// BootstrapMeanCI returns the sample mean and a percentile bootstrap (1-alpha) interval.
// For paired comparisons pass the per-item differences (cheap - strong).
func BootstrapMeanCI(x []float64, iters int, alpha float64, seed uint64) (mean, lo, hi float64) {
	n := len(x)
	if n == 0 {
		return 0, 0, 0
	}
	for _, v := range x {
		mean += v
	}
	mean /= float64(n)
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	means := make([]float64, iters)
	for i := range means {
		s := 0.0
		for j := 0; j < n; j++ {
			s += x[r.IntN(n)]
		}
		means[i] = s / float64(n)
	}
	sort.Float64s(means)
	idx := func(q float64) int { return min(iters-1, max(0, int(q*float64(iters)))) }
	return mean, means[idx(alpha/2)], means[idx(1-alpha/2)]
}
