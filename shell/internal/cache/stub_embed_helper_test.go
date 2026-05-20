package cache

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
)

// stubEmbedHelper mirrors StubEmbed in testdata/stub-plugin/main.go.
// The duplication is intentional — the stub-plugin builds as its own
// binary and cannot be imported into this package. Both functions MUST
// produce identical output; if you change one, change the other. The
// codec test below pins this lock-step at test time.
//
// Construction: sha256(text) → 8 chunks of 4 bytes → 8 float32 values
// in [-1, 1] → L2-normalized.
func stubEmbedHelper(text string) []float32 {
	hash := sha256.Sum256([]byte(text))
	v := make([]float32, 8)
	for i := 0; i < 8; i++ {
		u := binary.LittleEndian.Uint32(hash[i*4:])
		v[i] = float32(u)/float32(math.MaxUint32)*2 - 1
	}
	var sum float64
	for _, f := range v {
		sum += float64(f) * float64(f)
	}
	if sum == 0 {
		return v
	}
	norm := float32(math.Sqrt(sum))
	for i := range v {
		v[i] /= norm
	}
	return v
}
