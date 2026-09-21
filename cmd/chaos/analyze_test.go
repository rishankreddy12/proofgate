package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFailoverTime(t *testing.T) {
	var obs []Obs
	s := time.Second
	for i := 0; i < 100; i++ { // 10 s before the switch, all on a
		obs = append(obs, Obs{At: time.Duration(i) * 100 * time.Millisecond, Target: "a", Status: 200})
	}
	for i := 0; i < 30; i++ { // 10-13 s: still on a (slow)
		obs = append(obs, Obs{At: 10*s + time.Duration(i)*100*time.Millisecond, Target: "a", Status: 200})
	}
	for i := 0; i < 70; i++ { // from 13 s: on b
		obs = append(obs, Obs{At: 13*s + time.Duration(i)*100*time.Millisecond, Target: "b", Status: 200})
	}
	d, ok := FailoverTime(obs, 10*s, "b", 0.95, s)
	require.True(t, ok)
	require.Equal(t, 3*s, d)

	_, ok = FailoverTime(obs[:130], 10*s, "b", 0.95, s)
	require.False(t, ok, "never converged")
}
