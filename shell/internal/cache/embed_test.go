package cache

import (
	"bytes"
	"math"
	"testing"
)

// --- Codec round-trip ---------------------------------------------------

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := [][]float32{
		{0},
		{1, -1, 0.5, -0.5},
		{1e-7, -1e7, math.MaxFloat32, math.SmallestNonzeroFloat32},
		make([]float32, 1024),
	}
	for i, in := range cases {
		// seed the all-zero case with something non-trivial
		if i == 3 {
			for j := range in {
				in[j] = float32(j) * 0.001
			}
		}
		blob := encodeEmbedding(in)
		got, err := decodeEmbedding(blob)
		if err != nil {
			t.Errorf("case %d: decode error: %v", i, err)
			continue
		}
		if len(got) != len(in) {
			t.Errorf("case %d: len = %d, want %d", i, len(got), len(in))
			continue
		}
		for j := range in {
			if got[j] != in[j] {
				t.Errorf("case %d index %d: got %v, want %v", i, j, got[j], in[j])
				break
			}
		}
	}
}

func TestEncodeEmptyReturnsNil(t *testing.T) {
	if got := encodeEmbedding(nil); got != nil {
		t.Errorf("encodeEmbedding(nil) = %v, want nil", got)
	}
	if got := encodeEmbedding([]float32{}); got != nil {
		t.Errorf("encodeEmbedding(empty) = %v, want nil", got)
	}
}

func TestDecodeEmptyReturnsNil(t *testing.T) {
	got, err := decodeEmbedding(nil)
	if err != nil || got != nil {
		t.Errorf("decodeEmbedding(nil) = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestDecodeTruncatedBlobReturnsError(t *testing.T) {
	// 7 bytes — not a multiple of 4.
	if _, err := decodeEmbedding([]byte{1, 2, 3, 4, 5, 6, 7}); err == nil {
		t.Error("decodeEmbedding(7-byte blob): expected error, got nil")
	}
}

// TestEncodeIsLittleEndian — pin the byte order so cross-host BLOBs are
// stable. We compare the first 4 bytes of an encoded singleton against
// the known LE byte layout of float32(1.0) (0x3F800000 → 00 00 80 3F).
func TestEncodeIsLittleEndian(t *testing.T) {
	got := encodeEmbedding([]float32{1.0})
	want := []byte{0x00, 0x00, 0x80, 0x3F}
	if !bytes.Equal(got, want) {
		t.Errorf("encodeEmbedding([1.0]) bytes = % x, want % x", got, want)
	}
}

// --- Cosine -------------------------------------------------------------

func TestCosine_SelfSimilarityIsOne(t *testing.T) {
	v := []float32{0.5, -0.5, 0.3, 0.1}
	got := Cosine(v, v)
	if math.Abs(got-1.0) > 1e-9 {
		t.Errorf("Cosine(v, v) = %v, want ~1.0", got)
	}
}

func TestCosine_OrthogonalIsZero(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{0, 1}
	got := Cosine(a, b)
	if math.Abs(got) > 1e-9 {
		t.Errorf("Cosine(orthogonal) = %v, want 0", got)
	}
}

func TestCosine_OppositeIsMinusOne(t *testing.T) {
	a := []float32{1, 2, 3}
	b := []float32{-1, -2, -3}
	got := Cosine(a, b)
	if math.Abs(got+1.0) > 1e-9 {
		t.Errorf("Cosine(opposite) = %v, want -1.0", got)
	}
}

func TestCosine_ZeroVectorReturnsZero(t *testing.T) {
	got := Cosine([]float32{0, 0, 0}, []float32{1, 1, 1})
	if got != 0 {
		t.Errorf("Cosine(zero, _) = %v, want 0", got)
	}
}

func TestCosine_LengthMismatchReturnsZero(t *testing.T) {
	got := Cosine([]float32{1, 2}, []float32{1, 2, 3})
	if got != 0 {
		t.Errorf("Cosine(len-mismatch) = %v, want 0", got)
	}
}

// --- Store: embedding round-trip + LookupNearest ------------------------

func TestStore_Write_EmbeddingRoundTrips(t *testing.T) {
	store := openTemp(t)
	emb := []float32{0.1, -0.2, 0.3, 0.4}
	if err := store.Write("an intent", "darwin", "echo a", 1.0, emb); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// The byte-equal round-trip is asserted by reading the column back
	// directly — exposes the BLOB so we know LE bytes survive.
	var blob []byte
	if err := store.db.QueryRow(`SELECT embedding FROM intents WHERE intent='an intent'`).Scan(&blob); err != nil {
		t.Fatalf("read embedding: %v", err)
	}
	wantBlob := encodeEmbedding(emb)
	if !bytes.Equal(blob, wantBlob) {
		t.Errorf("stored bytes = % x, want % x", blob, wantBlob)
	}
}

func TestStore_LookupNearest_SelfMatchAtUnity(t *testing.T) {
	store := openTemp(t)
	emb := []float32{0.1, -0.2, 0.3, 0.4}
	if err := store.Write("self-match", "darwin", "echo self", 1.0, emb); err != nil {
		t.Fatalf("Write: %v", err)
	}
	intent, invocation, sim, hit, err := store.LookupNearest(emb, 0.99, "darwin")
	if err != nil {
		t.Fatalf("LookupNearest: %v", err)
	}
	if !hit {
		t.Fatal("expected hit on self-similarity")
	}
	if intent != "self-match" || invocation != "echo self" {
		t.Errorf("got intent=%q invocation=%q; want self-match / echo self", intent, invocation)
	}
	if math.Abs(sim-1.0) > 1e-6 {
		t.Errorf("similarity = %v, want ~1.0", sim)
	}
}

func TestStore_LookupNearest_PicksHighestAboveThreshold(t *testing.T) {
	store := openTemp(t)
	// Three rows with embeddings at increasing similarity to the query.
	rows := []struct {
		intent, invocation string
		emb                []float32
	}{
		{"far", "echo far", []float32{1, 0, 0}},             // similarity 0
		{"mid", "echo mid", []float32{0.7071, 0.7071, 0}},   // ~0.7071
		{"near", "echo near", []float32{0.97, 0.0, 0.2425}}, // ~0.97 with query
	}
	for _, r := range rows {
		if err := store.Write(r.intent, "darwin", r.invocation, 1.0, r.emb); err != nil {
			t.Fatalf("Write %s: %v", r.intent, err)
		}
	}
	query := []float32{1, 0, 0}

	// Threshold 0.0 — every row qualifies; the highest-similarity wins.
	intent, _, sim, hit, err := store.LookupNearest(query, 0.0, "darwin")
	if err != nil || !hit {
		t.Fatalf("threshold 0.0: hit=%v err=%v", hit, err)
	}
	if intent != "far" {
		// "far" has emb (1,0,0), identical to query — cosine 1.0.
		t.Errorf("threshold 0.0: best intent = %q, want %q", intent, "far")
	}
	if math.Abs(sim-1.0) > 1e-6 {
		t.Errorf("threshold 0.0: similarity = %v, want ~1.0", sim)
	}

	// Threshold 0.99 — only the "far" (perfectly aligned) row qualifies.
	intent, _, _, hit, err = store.LookupNearest(query, 0.99, "darwin")
	if err != nil || !hit {
		t.Fatalf("threshold 0.99: hit=%v err=%v", hit, err)
	}
	if intent != "far" {
		t.Errorf("threshold 0.99: best intent = %q, want %q", intent, "far")
	}
}

func TestStore_LookupNearest_ThresholdRespected_NoMatchAboveOne(t *testing.T) {
	store := openTemp(t)
	if err := store.Write("x", "darwin", "echo x", 1.0, []float32{1, 0, 0}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// 1.01 is unreachable (cosine maxes at 1.0); MUST return hit=false.
	_, _, _, hit, err := store.LookupNearest([]float32{1, 0, 0}, 1.01, "darwin")
	if err != nil {
		t.Fatalf("LookupNearest: %v", err)
	}
	if hit {
		t.Error("threshold 1.01 returned a hit; want hit=false")
	}
}

func TestStore_LookupNearest_PerOSIsolation(t *testing.T) {
	store := openTemp(t)
	emb := []float32{0.5, 0.5, 0.5, 0.5}
	if err := store.Write("multi-os", "darwin", "echo darwin", 1.0, emb); err != nil {
		t.Fatalf("Write darwin: %v", err)
	}
	if err := store.Write("multi-os", "windows", "Write-Host windows", 1.0, emb); err != nil {
		t.Fatalf("Write windows: %v", err)
	}
	// Query for linux — neither stored row should surface.
	_, _, _, hit, err := store.LookupNearest(emb, 0.0, "linux")
	if err != nil {
		t.Fatalf("LookupNearest linux: %v", err)
	}
	if hit {
		t.Error("linux query unexpectedly hit a darwin/windows row")
	}

	// Query for darwin — only the darwin row should win.
	_, invocation, _, hit, err := store.LookupNearest(emb, 0.0, "darwin")
	if err != nil || !hit {
		t.Fatalf("LookupNearest darwin: hit=%v err=%v", hit, err)
	}
	if invocation != "echo darwin" {
		t.Errorf("darwin query returned invocation = %q, want %q", invocation, "echo darwin")
	}
}

func TestStore_LookupNearest_IgnoresRowsWithoutEmbedding(t *testing.T) {
	store := openTemp(t)
	// One row without embedding (exact-hash-only), one with.
	if err := store.Write("no-emb", "darwin", "echo none", 1.0, nil); err != nil {
		t.Fatalf("Write no-emb: %v", err)
	}
	if err := store.Write("with-emb", "darwin", "echo with", 1.0, []float32{1, 0, 0}); err != nil {
		t.Fatalf("Write with-emb: %v", err)
	}
	intent, _, _, hit, err := store.LookupNearest([]float32{1, 0, 0}, 0.0, "darwin")
	if err != nil || !hit {
		t.Fatalf("LookupNearest: hit=%v err=%v", hit, err)
	}
	if intent != "with-emb" {
		t.Errorf("got intent = %q, want %q (no-emb row should be invisible)", intent, "with-emb")
	}
}

func TestStore_LookupNearest_EmptyDBReturnsMiss(t *testing.T) {
	store := openTemp(t)
	_, _, _, hit, err := store.LookupNearest([]float32{1, 0, 0}, 0.5, "darwin")
	if err != nil {
		t.Fatalf("LookupNearest: %v", err)
	}
	if hit {
		t.Error("empty DB returned a hit; want hit=false")
	}
}

func TestStore_LookupNearest_EmptyQueryReturnsMiss(t *testing.T) {
	store := openTemp(t)
	if err := store.Write("x", "darwin", "echo x", 1.0, []float32{1, 0}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	_, _, _, hit, err := store.LookupNearest(nil, 0.0, "darwin")
	if err != nil {
		t.Fatalf("LookupNearest(nil): %v", err)
	}
	if hit {
		t.Error("nil query returned a hit; want hit=false")
	}
}

func TestStore_LookupNearest_EmptyOSIsError(t *testing.T) {
	store := openTemp(t)
	_, _, _, _, err := store.LookupNearest([]float32{1, 0}, 0.0, "")
	if err == nil {
		t.Error("empty os: expected error, got nil")
	}
}

// TestStore_Write_UpsertOverwritesEmbedding — when a row is re-written
// with a different embedding, the new bytes win.
func TestStore_Write_UpsertOverwritesEmbedding(t *testing.T) {
	store := openTemp(t)
	first := []float32{1, 0, 0}
	second := []float32{0, 1, 0}
	if err := store.Write("k", "darwin", "echo k1", 1.0, first); err != nil {
		t.Fatalf("Write first: %v", err)
	}
	if err := store.Write("k", "darwin", "echo k2", 1.0, second); err != nil {
		t.Fatalf("Write second: %v", err)
	}
	var blob []byte
	if err := store.db.QueryRow(`SELECT embedding FROM intents WHERE intent='k'`).Scan(&blob); err != nil {
		t.Fatalf("read embedding: %v", err)
	}
	want := encodeEmbedding(second)
	if !bytes.Equal(blob, want) {
		t.Errorf("upsert bytes = % x, want % x (post-upsert embedding)", blob, want)
	}
}
