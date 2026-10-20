// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"

	"github.com/barzoj-co/llmentary/internal/openaiapi"
)

func (h *openAIHandlers) CreateResponse(ctx context.Context, _ openaiapi.CreateResponseRequestObject) (openaiapi.CreateResponseResponseObject, error) {
	return h.s.forwardSpec(ctx)
}
