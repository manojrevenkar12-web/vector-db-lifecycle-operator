// Package rng gives keyed randomness: the same seed and key always give the same stream, whichever design asks.
package rng

import (
	"fmt"
	"hash/fnv"
	"math/rand/v2"
)

// Keyed returns a generator for (seed, key...).
func Keyed(seed int64, key ...any) *rand.Rand {
	h := fnv.New64a()
	fmt.Fprint(h, seed)
	for _, k := range key {
		fmt.Fprintf(h, "|%v", k)
	}
	s := h.Sum64()
	return rand.New(rand.NewPCG(s, s^0x9e3779b97f4a7c15))
}

// Float returns one uniform draw in [0, 1) for (seed, key...).
func Float(seed int64, key ...any) float64 { return Keyed(seed, key...).Float64() }
