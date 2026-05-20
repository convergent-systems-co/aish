package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	proto "github.com/convergent-systems-co/aish/libs/proto/inference"
)

// defaultEmbedModel is the model id used when EmbedParams.Model is empty.
// The Convergent Systems gateway routes embed traffic to Voyage AI by
// default; the plugin advertises that model id for cost-bucket telemetry.
const defaultEmbedModel = "voyage-3"

// embedPricePerMTok is the per-1M-token pricing table (USD) for embed
// requests. Distinct from modelPrice (which is infer-only). Unknown
// models yield 0.0 USD rather than failing the request — embeddings
// have no output-tokens dimension so the table is one-sided.
var embedPricePerMTok = map[string]float64{
	"voyage-3":      0.12,
	"voyage-3-lite": 0.02,
}

// embedRequest is the JSON body sent to <base>/embeddings. The wire
// shape is the OpenAI / Voyage / Convergent Systems gateway shape:
// `{"input": "...", "model": "..."}`. `input` MAY be a string or
// an array; we send a single string for simplicity.
type embedRequest struct {
	Input string `json:"input"`
	Model string `json:"model"`
}

// embedResponseDataItem is one element of the `data` array.
type embedResponseDataItem struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

// embedResponseUsage is the optional usage block.
type embedResponseUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// embedResponse is the JSON body returned by <base>/embeddings on a 200.
type embedResponse struct {
	Data  []embedResponseDataItem `json:"data"`
	Model string                  `json:"model"`
	Usage embedResponseUsage      `json:"usage"`
}

// Embed POSTs to <base>/embeddings and returns the resulting vector +
// cost telemetry. Unlike Infer, Embed is non-streaming: the gateway
// returns a single JSON response.
//
// HTTP status mapping mirrors Infer (auth/rate-limit/timeout/internal).
// The returned error MUST NOT contain the API-key value.
func (c *Client) Embed(ctx context.Context, params proto.EmbedParams) (proto.EmbedResult, error) {
	model := params.Model
	if model == "" {
		model = defaultEmbedModel
	}

	body := embedRequest{
		Input: params.Text,
		Model: model,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return proto.EmbedResult{}, &CodedError{
			Code:    proto.CodeInternal,
			Message: "anthropic: failed to marshal embed request body",
		}
	}

	url := c.baseURL + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return proto.EmbedResult{}, &CodedError{
			Code:    proto.CodeInternal,
			Message: "anthropic: failed to build embed request",
		}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return proto.EmbedResult{}, &CodedError{
				Code:    proto.CodeTimeout,
				Message: "anthropic: embed request cancelled or deadline exceeded",
			}
		}
		return proto.EmbedResult{}, &CodedError{
			Code:    proto.CodeInternal,
			Message: "anthropic: embed transport error",
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return proto.EmbedResult{}, statusToCodedError(resp.StatusCode)
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return proto.EmbedResult{}, &CodedError{
			Code:    proto.CodeInternal,
			Message: "anthropic: read embed response body",
		}
	}
	var parsed embedResponse
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return proto.EmbedResult{}, &CodedError{
			Code:    proto.CodeInternal,
			Message: "anthropic: parse embed response body",
		}
	}
	if len(parsed.Data) == 0 || len(parsed.Data[0].Embedding) == 0 {
		return proto.EmbedResult{}, &CodedError{
			Code:    proto.CodeInternal,
			Message: "anthropic: embed response has no vector",
		}
	}
	gotModel := parsed.Model
	if gotModel == "" {
		gotModel = model
	}
	cost := &proto.Cost{
		Model:     gotModel,
		TokensIn:  parsed.Usage.PromptTokens,
		TokensOut: 0,
		USD:       estimateEmbedUSD(gotModel, parsed.Usage.PromptTokens),
	}
	return proto.EmbedResult{
		Vector: parsed.Data[0].Embedding,
		Model:  gotModel,
		Cost:   cost,
	}, nil
}

// estimateEmbedUSD returns the dollar estimate for an embed request. An
// unknown model returns 0.0 (non-fatal; the caller still emits the Cost
// frame so the model id is recorded).
func estimateEmbedUSD(model string, tokensIn int) float64 {
	p, ok := embedPricePerMTok[model]
	if !ok {
		return 0.0
	}
	const perMillion = 1_000_000.0
	return float64(tokensIn) * p / perMillion
}

