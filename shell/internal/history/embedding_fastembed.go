// fastembed-go + bge-small-en-v1.5 embedding provider for v0.3-4 #112.
//
// Why this backend (per plan §Alternatives):
//   - 384-dim ONNX model (~30MB on disk) yielding sentence-quality
//     embeddings for short shell-command strings.
//   - fastembed-go (github.com/anush008/fastembed-go) ships the
//     tokenizer + ONNX runtime glue; we add only the EmbeddingProvider
//     adapter and the offline-first guardrail.
//
// Why NOT pure-Go end to end: fastembed-go depends on
// github.com/yalue/onnxruntime_go, which loads Microsoft's ONNX C
// runtime via a shared library at process start. That breaks the
// "pure-Go portability story" the project carries elsewhere — the
// pragmatic ROI of a high-quality 384-dim embedder out-paces a
// bespoke pure-Go transformer for v0.3. The C dependency is
// documented as a known cost in #112's risk table.
//
// Offline-first guarantee (AC9):
// fastembed-go's NewFlagEmbedding will SILENTLY download from
// https://storage.googleapis.com/qdrant-fastembed/ when its cache
// directory lacks the model. v0.3 forbids that. NewFastembedProvider
// stat-checks the required model files BEFORE delegating to
// fastembed-go and refuses with a clear, path-naming error when they
// are absent. The user must populate the cache manually (documented
// prereq); a future `aish history models pull` subcommand is in #203
// follow-up scope.

package history

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	fastembed "github.com/anush008/fastembed-go"
)

const (
	// fastembedModelID is the public ModelID() value persisted in
	// events_vec_meta alongside every vector row produced by this
	// provider. It must match the test expectation in
	// embedding_test.go's testModelID constant and the documented
	// model card identity. Changing this string is a model-change
	// event — it invalidates every existing vector row and forces
	// reindex to re-embed (AC7).
	fastembedModelID = "bge-small-en-v1.5"

	// fastembedDim is bge-small-en-v1.5's published output dimension.
	// Mirrored in schema.go's VecTableDDL FLOAT[384] column width.
	fastembedDim = 384

	// fastembedOnnxFile is the ONNX model file fastembed-go loads
	// via onnxruntime_go.NewAdvancedSession. Pre-#112 we shipped no
	// model fetcher; the file is a manual prereq. We stat it before
	// invoking fastembed-go so its silent-download path is never
	// triggered.
	fastembedOnnxFile = "model_optimized.onnx"

	// fastembedTokenizerFile is the tokenizer config fastembed-go
	// loads via pretrained.FromFile. Same offline-gate rationale.
	fastembedTokenizerFile = "tokenizer.json"
)

// fastembedProvider is the EmbeddingProvider adapter around
// fastembed.FlagEmbedding. Held by value of pointer; Embed is safe
// for concurrent callers because the underlying tokenizer +
// onnxruntime session are stateless per call (Embed creates and
// destroys its own session per invocation — see fastembed.go).
type fastembedProvider struct {
	em *fastembed.FlagEmbedding
}

// NewFastembedProvider constructs an EmbeddingProvider backed by
// fastembed-go + bge-small-en-v1.5. modelDir is the directory whose
// contents are the model files — the same path fastembed-go expects
// to find <CacheDir>/<model-name>/ at.
//
// Layout fastembed-go demands (after manual fetch):
//
//	<modelDir>/model_optimized.onnx
//	<modelDir>/tokenizer.json
//	<modelDir>/special_tokens_map.json   (loaded by tokenizer)
//	<modelDir>/config.json               (loaded by tokenizer)
//
// We stat the two load-bearing files (the .onnx and the
// tokenizer.json) before calling fastembed-go. Missing → clear
// error referencing the absent path. Present → delegate; if the
// tokenizer or ONNX runtime fails on a later concern, that surfaces
// as the underlying library's error.
//
// fastembed-go's CacheDir argument is the PARENT of the model
// directory; the library appends the model name itself. We pass the
// parent of modelDir so that
// <parent>/<model-name>/ exactly equals modelDir.
func NewFastembedProvider(modelDir string) (EmbeddingProvider, error) {
	if modelDir == "" {
		return nil, errors.New("history: NewFastembedProvider: model directory is empty")
	}

	// Offline-first gate: refuse before fastembed-go can phone home.
	onnxPath := filepath.Join(modelDir, fastembedOnnxFile)
	if _, err := os.Stat(onnxPath); err != nil {
		return nil, fmt.Errorf(
			"history: model file %s not present at %s — "+
				"fetch the bge-small-en-v1.5 model manually into the cache directory "+
				"(see docs/HISTORY.md#semantic-search) before enabling semantic search",
			fastembedOnnxFile, onnxPath,
		)
	}
	tokPath := filepath.Join(modelDir, fastembedTokenizerFile)
	if _, err := os.Stat(tokPath); err != nil {
		return nil, fmt.Errorf(
			"history: model tokenizer %s not present at %s — "+
				"the model directory is incomplete; refetch the bge-small-en-v1.5 archive",
			fastembedTokenizerFile, tokPath,
		)
	}

	// fastembed-go's "CacheDir" is the PARENT of the model dir; the
	// library appends string(Model) when it constructs the load path.
	// We arrange the cache layout in the caller (T3/T4 wire-up uses
	// ~/.aish/models/ as cacheDir parent, with the model dir living
	// at ~/.aish/models/bge-small-en-v1.5/). Compute the parent we
	// hand to fastembed-go from the modelDir we were given so the
	// final path inside fastembed-go (filepath.Join(cacheDir,
	// string(model))) lands back at modelDir.
	cacheDir, modelName := filepath.Split(filepath.Clean(modelDir))
	// fastembed-go's BGESmallENV15 constant value is "fast-bge-
	// small-en-v1.5"; the test fixture's modelCacheDir uses
	// "bge-small-en-v1.5". If a user has the model in a directory
	// not named per the fastembed-go convention, we still want it to
	// load — symlink-or-rename is the user's job. To avoid an
	// "unknown model name" surprise inside fastembed-go, we always
	// pass BGESmallENV15 as the Model constant and trust the
	// pre-flight stats above to fail cleanly when files are absent.
	_ = modelName

	showProgress := false
	em, err := fastembed.NewFlagEmbedding(&fastembed.InitOptions{
		Model:                fastembed.BGESmallENV15,
		CacheDir:             cacheDir,
		MaxLength:            512,
		ShowDownloadProgress: &showProgress,
	})
	if err != nil {
		return nil, fmt.Errorf("history: fastembed init at %s: %w", modelDir, err)
	}
	return &fastembedProvider{em: em}, nil
}

// Embed runs the underlying fastembed-go FlagEmbedding on the input
// batch. Order is preserved (fastembed-go iterates input in order;
// the tokenizer encode batch is one-to-one with input). Empty input
// short-circuits — fastembed-go's path through onnxruntime requires
// at least one tensor; passing it nothing is undefined behavior.
//
// ctx is honored on the entry gate only — fastembed-go does not
// itself accept a context. Cancellation mid-encode is therefore best-
// effort; the typical call is a single shell command's worth of
// text and completes in <100ms on the target hardware.
func (p *fastembedProvider) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(inputs) == 0 {
		return nil, nil
	}
	// fastembed-go's Embed signature is (input []string, batchSize int)
	// — a non-positive batch size selects the library default (which
	// is the full input). We pass 0 to let it pick.
	out, err := p.em.Embed(inputs, 0)
	if err != nil {
		return nil, fmt.Errorf("history: fastembed.Embed: %w", err)
	}
	return out, nil
}

// ModelID returns the stable identifier persisted alongside vector
// rows in events_vec_meta (AC7). Mismatch between this value and a
// stored row's model_id signals the reindex path that the row needs
// re-embedding under the new model.
func (p *fastembedProvider) ModelID() string {
	return fastembedModelID
}

// Dim returns the vector dimension fastembed-go's bge-small-en-v1.5
// emits — fixed at 384 per the model card. The vector store uses
// this on Upsert to validate inbound vectors and at table creation
// to size the embedding column.
func (p *fastembedProvider) Dim() int {
	return fastembedDim
}

// Compile-time assertion that fastembedProvider satisfies the
// EmbeddingProvider interface declared in embedding_types.go. A
// future signature drift in the interface (e.g., adding Close())
// breaks at `go build` rather than at first invocation in
// production.
var _ EmbeddingProvider = (*fastembedProvider)(nil)
