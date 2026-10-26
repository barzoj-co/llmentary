// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"errors"
	"io"
	"time"
)

const (
	errorSourceUpstream = "upstream"
	errorSourceProxy    = "proxy"

	normalizedStreamTruncated = "stream_truncated"
)

// isIncompleteUpstreamBody reports a read that ended before the HTTP
// framing was complete. Classification is by provenance: any upstream
// body-read error except a clean EOF or a client cancel. Write failures
// and an already-gone client are filtered in shouldAbortClientResponse.
func isIncompleteUpstreamBody(err error) bool {
	if err == nil || errors.Is(err, io.EOF) {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	return true
}

// shouldAbortClientResponse reports whether the inbound HTTP handler
// must panic(http.ErrAbortHandler) after flushing partial bytes so the
// app sees a broken stream instead of a clean terminator.
func shouldAbortClientResponse(copyErr error, copyWriteErr, idleTimeout, clientGone bool) bool {
	if clientGone || copyWriteErr {
		return false
	}
	if idleTimeout {
		return true
	}
	return isIncompleteUpstreamBody(copyErr)
}

func callOutcomeLogArgs(call Call) []any {
	var extra []any
	if err := call.Normalized.Error; err != nil && err.Normalized != "" {
		extra = append(extra, "error", err.Normalized)
	}
	if call.Normalized.StreamTruncated {
		extra = append(extra, "stream_truncated", true)
	}
	settled := call
	if settled.EndedAt.IsZero() {
		settled.EndedAt = time.Now()
	}
	if outcome := classifyOutcome(settled); outcome != "" {
		extra = append(extra, "outcome", outcome)
	}
	return extra
}
