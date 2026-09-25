// Package llm sends one prompt to Claude and returns the text answer.
// Only this package knows the Anthropic SDK.
package llm

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"

	"dashboard/internal/drivers/httpclient"
)

const (
	model     = "claude-opus-5"
	maxTokens = 16000
	timeout   = 2 * time.Minute
)

// ErrRefused means the model declined and no fallback answered.
var ErrRefused = errors.New("llm: refused")

// Complete answers prompt under system. Adaptive thinking at low effort
// suits a short summary; a refusal is re-served by the default fallback.
func Complete(ctx context.Context, apiKey, system, prompt string) (string, error) {
	client := anthropic.NewClient(
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(httpclient.Client(timeout)),
	)
	resp, err := client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:        model,
		MaxTokens:    maxTokens,
		Betas:        []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
		Fallbacks:    anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()},
		Thinking:     anthropic.BetaThinkingConfigParamUnion{OfAdaptive: &anthropic.BetaThinkingConfigAdaptiveParam{}},
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffortLow},
		System:       []anthropic.BetaTextBlockParam{{Text: system}},
		Messages:     []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(prompt))},
	})
	if err != nil {
		return "", err
	}
	if resp.StopReason == anthropic.BetaStopReasonRefusal {
		return "", ErrRefused
	}

	var out strings.Builder
	for _, block := range resp.Content {
		if text, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			out.WriteString(text.Text)
		}
	}
	return strings.TrimSpace(out.String()), nil
}
