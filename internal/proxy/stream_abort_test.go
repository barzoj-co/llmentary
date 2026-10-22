// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
)

func TestIsIncompleteUpstreamBody(t *testing.T) {
	t.Parallel()
	if isIncompleteUpstreamBody(nil) {
		t.Fatal("nil")
	}
	if isIncompleteUpstreamBody(io.EOF) {
		t.Fatal("clean EOF is a complete body")
	}
	if !isIncompleteUpstreamBody(io.ErrUnexpectedEOF) {
		t.Fatal("unexpected EOF")
	}
	if !isIncompleteUpstreamBody(fmt.Errorf("read: %w", io.ErrUnexpectedEOF)) {
		t.Fatal("wrapped unexpected EOF")
	}
	if !isIncompleteUpstreamBody(errors.New("http: incomplete chunked read")) {
		t.Fatal("incomplete chunked")
	}
	if !isIncompleteUpstreamBody(errors.New("peer closed connection without sending complete message body")) {
		t.Fatal("peer closed")
	}
	if !isIncompleteUpstreamBody(errors.New("http: malformed chunked data")) {
		t.Fatal("malformed chunked")
	}
	if !isIncompleteUpstreamBody(errors.New("invalid byte in chunk length")) {
		t.Fatal("invalid chunk length")
	}
	if !isIncompleteUpstreamBody(errors.New("http chunk length too large")) {
		t.Fatal("chunk length too large")
	}
	if !isIncompleteUpstreamBody(errors.New("empty hex number for chunk length")) {
		t.Fatal("empty chunk length")
	}
	if !isIncompleteUpstreamBody(errors.New("read tcp: connection reset by peer")) {
		t.Fatal("connection reset")
	}
	if isIncompleteUpstreamBody(errors.New("context canceled")) {
		t.Fatal("canceled is not an incomplete body")
	}
}

func TestShouldAbortClientResponse(t *testing.T) {
	t.Parallel()
	if shouldAbortClientResponse(io.ErrUnexpectedEOF, false, false, false) != true {
		t.Fatal("drop")
	}
	if shouldAbortClientResponse(nil, false, true, false) != true {
		t.Fatal("idle")
	}
	if shouldAbortClientResponse(io.ErrUnexpectedEOF, true, false, false) {
		t.Fatal("write error is a client abort, not an upstream drop")
	}
	if shouldAbortClientResponse(io.ErrUnexpectedEOF, false, false, true) {
		t.Fatal("client already gone")
	}
	if shouldAbortClientResponse(nil, false, false, false) {
		t.Fatal("clean complete body")
	}
	if shouldAbortClientResponse(io.EOF, false, false, false) {
		t.Fatal("clean EOF")
	}
	if shouldAbortClientResponse(&net.OpError{Op: "read", Err: io.ErrUnexpectedEOF}, false, false, false) != true {
		t.Fatal("net unexpected EOF")
	}
}

func TestCallOutcomeLogArgs(t *testing.T) {
	t.Parallel()
	if args := callOutcomeLogArgs(Call{}); len(args) != 0 {
		t.Fatalf("clean=%v", args)
	}
	call := Call{}
	call.Normalized.Error = &CallError{Normalized: "stream_truncated"}
	call.Normalized.StreamTruncated = true
	args := callOutcomeLogArgs(call)
	if len(args) != 4 || args[0] != "error" || args[1] != "stream_truncated" || args[2] != "stream_truncated" || args[3] != true {
		t.Fatalf("args=%v", args)
	}
}
