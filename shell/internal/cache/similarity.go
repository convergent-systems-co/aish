package cache

import "math"

// Cosine returns the cosine similarity of a and b, defined as
// dot(a,b) / (|a|*|b|). The result is in [-1, 1] for vectors with
// finite, non-zero magnitudes.
//
// Special cases:
//   - len(a) != len(b)  → returns 0
//   - |a| == 0 or |b| == 0 → returns 0 (a zero vector has no direction;
//     the geometric similarity is undefined, and returning 0 keeps the
//     threshold compare honest — a zero vector matches nothing)
//
// Cosine is the chosen metric for the v0.1-2-followup intent cache per
// .artifacts/plans/v0.1-2-embed.md §"Alternatives Table — similarity
// metric". Isolating the function behind one name lets a future
// behaviour-preserving swap to dot-product (when the gateway guarantees
// L2-normalized output) be a one-line change.
func Cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, magA, magB float64
	for i := range a {
		fa := float64(a[i])
		fb := float64(b[i])
		dot += fa * fb
		magA += fa * fa
		magB += fb * fb
	}
	if magA == 0 || magB == 0 {
		return 0
	}
	return dot / (math.Sqrt(magA) * math.Sqrt(magB))
}
