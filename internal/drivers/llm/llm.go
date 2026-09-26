// Package llm sends one prompt to Claude and returns the text answer.
// Only this package knows the Anthropic SDK.
package llm

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"

	"andon/internal/drivers/httpclient"
)

const (
	model     = "claude-opus-5"
	maxTokens = 16000
	timeout   = 2 * time.Minute
)

// ErrRefused means the model declined and no fallback answered.
var ErrRefused = errors.New("llm: refused")

// Media types Claude reads as documents or images.
const (
	mediaPDF  = "application/pdf"
	mediaJPEG = "image/jpeg"
	mediaPNG  = "image/png"
	mediaGIF  = "image/gif"
	mediaWebP = "image/webp"
)

// File is one attachment sent along with a prompt.
type File struct {
	Media   string
	Content []byte
}

// Readable reports whether Claude can read a media type.
func Readable(media string) bool {
	switch media {
	case mediaPDF, mediaJPEG, mediaPNG, mediaGIF, mediaWebP:
		return true
	}
	return false
}

// Complete answers prompt under system. Adaptive thinking at low effort
// suits a short summary; a refusal is re-served by the default fallback.
func Complete(ctx context.Context, apiKey, system, prompt string) (string, error) {
	return CompleteFiles(ctx, apiKey, system, prompt, nil)
}

// fileBlock turns an attachment into a document or image block.
func fileBlock(f File) (anthropic.BetaContentBlockParamUnion, bool) {
	data := base64.StdEncoding.EncodeToString(f.Content)
	switch f.Media {
	case mediaPDF:
		return anthropic.NewBetaDocumentBlock(anthropic.BetaBase64PDFSourceParam{Data: data}), true
	case mediaJPEG, mediaPNG, mediaGIF, mediaWebP:
		return anthropic.NewBetaImageBlock(anthropic.BetaBase64ImageSourceParam{Data: data, MediaType: anthropic.BetaBase64ImageSourceMediaType(f.Media)}), true
	}
	return anthropic.BetaContentBlockParamUnion{}, false
}

// CompleteFiles is Complete with readable attachments (PDF, images) put
// before the prompt; other files are skipped.
func CompleteFiles(ctx context.Context, apiKey, system, prompt string, files []File) (string, error) {
	var blocks []anthropic.BetaContentBlockParamUnion
	for _, f := range files {
		if block, ok := fileBlock(f); ok {
			blocks = append(blocks, block)
		}
	}
	blocks = append(blocks, anthropic.NewBetaTextBlock(prompt))

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
		Messages:     []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(blocks...)},
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
