package cache

import (
	"encoding/binary"
	"errors"
	"math"
)

// DefaultSimilarityThreshold is the cosine-similarity floor at or above
// which a LookupNearest match is treated as a cache hit. Calibrated
// conservatively for v0.1; tighten or loosen once the v0.2 telemetry
// shows the false-positive vs false-negative trade-off.
//
// Override via Cache.WithSimilarityThreshold.
const DefaultSimilarityThreshold = 0.85

// errEmbeddingBlobTooShort is returned by decodeEmbedding when the BLOB
// length is not a positive multiple of 4 bytes.
var errEmbeddingBlobTooShort = errors.New("cache: embedding blob is not a positive multiple of 4 bytes")

// encodeEmbedding serialises a vector to little-endian float32 bytes.
// The encoding is fixed at LE so a DB file is byte-identical across
// hosts (Go targets are uniformly LE today, but we pin explicitly so a
// future port to a BE host does not silently corrupt the column).
//
// Returns nil for nil/empty input — callers should branch on len() ==
// 0 before persisting (a nil input means "no embedding for this row,"
// not "an empty embedding").
func encodeEmbedding(v []float32) []byte {
	if len(v) == 0 {
		return nil
	}
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// decodeEmbedding reverses encodeEmbedding. Returns an error if the
// blob length is not a positive multiple of 4. nil/empty input returns
// (nil, nil) so a NULL embedding column is not a decoding fault.
func decodeEmbedding(b []byte) ([]float32, error) {
	if len(b) == 0 {
		return nil, nil
	}
	if len(b)%4 != 0 {
		return nil, errEmbeddingBlobTooShort
	}
	out := make([]float32, len(b)/4)
	for i := range out {
		bits := binary.LittleEndian.Uint32(b[i*4:])
		out[i] = math.Float32frombits(bits)
	}
	return out, nil
}
