// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"errors"
	"io"
	"strings"
)

const (
	errorSourceUpstream = "upstream"
	errorSourceProxy    = "proxy"

	normalizedStreamTruncated = "stream_truncated"
)

// isIncompleteUpstreamBody reports a read that ended before the HTTP
// framing was complete (no chunked terminator, short Content-Length).
// A clean io.EOF after a complete body is not incomplete.
func isIncompleteUpstreamBody(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "unexpected EOF") ||
		strings.Contains(msg, "incomplete chunked") ||
		strings.Contains(msg, "malformed chunked") ||
		strings.Contains(msg, "invalid byte in chunk length") ||
		strings.Contains(msg, "http chunk length too large") ||
		strings.Contains(msg, "empty hex number for chunk length") ||
		strings.Contains(msg, "stream error:") ||
		strings.Contains(msg, "connection error:") ||
		strings.Contains(msg, "server sent GOAWAY and closed the connection") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "peer closed")
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
	return extra
}
