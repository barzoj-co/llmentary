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

type orderWriter struct {
	http.ResponseWriter
	order *[]string
}

func (w orderWriter) Write(p []byte) (int, error) {
	*w.order = append(*w.order, "write")
	return w.ResponseWriter.Write(p)
}

func TestCopyAndFlushBeforeWriteHook(t *testing.T) {
	var order []string
	n, err, writeErr := copyAndFlushWithHooks(orderWriter{ResponseWriter: httptest.NewRecorder(), order: &order}, strings.NewReader("ok"), func() {
		order = append(order, "before")
	}, func([]byte) {
		order = append(order, "chunk")
	})
	if n != 2 || err != nil || writeErr {
		t.Fatalf("n=%d err=%v writeErr=%v", n, err, writeErr)
	}
	if strings.Join(order, ",") != "before,write,chunk" {
		t.Fatalf("order=%v", order)
	}
	n, err, writeErr = copyAndFlushWithHooks(httptest.NewRecorder(), strings.NewReader("ok"), nil, nil)
	if n != 2 || err != nil || writeErr {
		t.Fatalf("nil hooks n=%d err=%v writeErr=%v", n, err, writeErr)
	}
}
