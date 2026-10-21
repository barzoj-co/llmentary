// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type failWriter struct{ http.ResponseWriter }

func (failWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestCopyAndFlushWriteErr(t *testing.T) {
	_, err, writeErr := copyAndFlush(failWriter{httptest.NewRecorder()}, strings.NewReader("hi"), nil)
	if err == nil || !writeErr {
		t.Fatalf("err=%v writeErr=%v", err, writeErr)
	}
}

func TestCopyAndFlushReadErrNotWrite(t *testing.T) {
	r, w := io.Pipe()
	_ = w.CloseWithError(io.ErrUnexpectedEOF)
	_, err, writeErr := copyAndFlush(httptest.NewRecorder(), r, nil)
	if err == nil || writeErr {
		t.Fatalf("err=%v writeErr=%v", err, writeErr)
	}
}

func TestCopyAndFlushEOFNotWrite(t *testing.T) {
	n, err, writeErr := copyAndFlush(httptest.NewRecorder(), strings.NewReader("ok"), nil)
	if n != 2 || err != nil || writeErr {
		t.Fatalf("n=%d err=%v writeErr=%v", n, err, writeErr)
	}
}
