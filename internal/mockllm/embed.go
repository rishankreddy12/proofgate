package mockllm

import (
	"hash/fnv"
	"math"
	"strings"
)

// HashEmbedding is a deterministic bag-of-words embedding (feature hashing). Texts that share words have
// high cosine similarity, which is enough to test semantic caching without a real model.
func HashEmbedding(text string, dim int) []float32 {
	v := make([]float32, dim)
	for _, w := range strings.Fields(strings.ToLower(text)) {
		w = strings.Trim(w, ".,?!;:\"'()")
		if w == "" {
			continue
		}
		h := fnv.New32a()
		_, _ = h.Write([]byte(w))
		sum := h.Sum32()
		sign := float32(1)
		if sum&1 == 1 {
			sign = -1
		}
		v[int(sum>>1)%dim] += sign
	}
	var n float64
	for _, x := range v {
		n += float64(x * x)
	}
	if n == 0 {
		return v
	}
	inv := float32(1 / math.Sqrt(n))
	for i := range v {
		v[i] *= inv
	}
	return v
}
