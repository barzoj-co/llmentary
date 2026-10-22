// SPDX-License-Identifier: AGPL-3.0-only

package compat_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEngineHealthOK(t *testing.T) {
	t.Parallel()
	boom := errors.New("dial")
	cases := []struct {
		status int
		err    error
		ok     bool
	}{
		{http.StatusOK, nil, true},
		{http.StatusNoContent, nil, true},
		{299, nil, true},
		{199, nil, false},
		{http.StatusMultipleChoices, nil, false},
		{http.StatusUnauthorized, nil, false},
		{http.StatusNotFound, nil, false},
		{http.StatusInternalServerError, nil, false},
		{http.StatusOK, boom, false},
		{0, boom, false},
	}
	for _, tc := range cases {
		if got := engineHealthOK(tc.status, tc.err); got != tc.ok {
			t.Fatalf("status=%d err=%v got %v want %v", tc.status, tc.err, got, tc.ok)
		}
	}
}

func TestProbeEngineRejectsNon2xx(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError} {
		status := status
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			t.Cleanup(srv.Close)
			err := probeEngine(&http.Client{Timeout: time.Second}, srv.URL)
			if err == nil {
				t.Fatalf("status %d marked healthy", status)
			}
		})
	}
	t.Run("ok", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)
		if err := probeEngine(&http.Client{Timeout: time.Second}, srv.URL); err != nil {
			t.Fatal(err)
		}
	})
}
