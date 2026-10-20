// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"

	"github.com/barzoj-co/llmentary/internal/openaiapi"
)

func (h *openAIHandlers) CreateChatCompletion(ctx context.Context, _ openaiapi.CreateChatCompletionRequestObject) (openaiapi.CreateChatCompletionResponseObject, error) {
	return h.s.forwardSpec(ctx)
}
