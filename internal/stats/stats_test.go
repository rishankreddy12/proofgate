package stats

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWilson(t *testing.T) {
	// Reference values: 0 of 100 with z=1.96 gives [0, 0.0370]; 10 of 100 gives [0.0552, 0.1744].
	lo, hi := Wilson(0, 100, 1.96)
	require.InDelta(t, 0.0, lo, 1e-4)
	require.InDelta(t, 0.0370, hi, 1e-4)
	lo, hi = Wilson(10, 100, 1.96)
	require.InDelta(t, 0.0552, lo, 1e-4)
	require.InDelta(t, 0.1744, hi, 1e-4)
	lo, hi = Wilson(0, 0, 1.96)
	require.Equal(t, 0.0, lo)
	require.Equal(t, 1.0, hi, "no data: the rate could be anything")
}

func TestBootstrapMeanCI(t *testing.T) {
	x := make([]float64, 400)
	for i := range x {
		x[i] = float64(i%5) - 2 // mean 0, values -2..2
	}
	m, lo, hi := BootstrapMeanCI(x, 2000, 0.05, 7)
	require.InDelta(t, 0.0, m, 1e-9)
	require.Less(t, lo, 0.0)
	require.Greater(t, hi, 0.0)
	require.InDelta(t, -0.14, lo, 0.04) // ~ -1.96 * sd/sqrt(n) = -1.96*1.414/20
	m2, lo2, hi2 := BootstrapMeanCI(x, 2000, 0.05, 7)
	require.Equal(t, [3]float64{m, lo, hi}, [3]float64{m2, lo2, hi2}, "deterministic for a seed")

	shifted := make([]float64, len(x))
	for i := range x {
		shifted[i] = x[i] - 1
	}
	_, _, hi = BootstrapMeanCI(shifted, 2000, 0.05, 7)
	require.Less(t, hi, 0.0, "a clear drop has a CI entirely below zero")
}

func TestCohenKappa(t *testing.T) {
	// Classic example: 50 items, agree yes 20, agree no 15, a-yes/b-no 5, a-no/b-yes 10 -> kappa = 0.4
	var a, b []bool
	add := func(n int, x, y bool) {
		for i := 0; i < n; i++ {
			a, b = append(a, x), append(b, y)
		}
	}
	add(20, true, true)
	add(15, false, false)
	add(5, true, false)
	add(10, false, true)
	k, n, err := CohenKappa(a, b)
	require.NoError(t, err)
	require.Equal(t, 50, n)
	require.InDelta(t, 0.4, k, 1e-9)

	k, _, _ = CohenKappa([]bool{true, false, true}, []bool{true, false, true})
	require.InDelta(t, 1.0, k, 1e-9)

	_, _, err = CohenKappa([]bool{true}, []bool{true, false})
	require.Error(t, err)
}
