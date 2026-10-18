// SPDX-License-Identifier: AGPL-3.0-only

package lab

import (
	"context"

	"github.com/barzoj-co/llm-test-lab/internal/openaiapi"
)

func (h *openAIHandlers) CreateResponse(ctx context.Context, _ openaiapi.CreateResponseRequestObject) (openaiapi.CreateResponseResponseObject, error) {
	return h.s.forwardSpec(ctx)
}
