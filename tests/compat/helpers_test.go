// SPDX-License-Identifier: AGPL-3.0-only

package compat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

type compatProxy struct {
	proxyURL   string
	controlURL string
	upstream   *httptest.Server
}

var sessionSeq atomic.Uint64

func newSession() string {
	return fmt.Sprintf("c%x", sessionSeq.Add(1))
}

func startCompatProxy(t *testing.T, mode, profile, target string) compatProxy {
	t.Helper()
	up := httptest.NewServer(fixtureServer{})
	t.Cleanup(up.Close)
	cfg := proxy.Config{
		ListenAddr:     ":0",
		ControlAddr:    "127.0.0.1:0",
		SpecValidation: mode,
		SpecProfile:    profile,
		SpecTargets:    map[string]string{"default": target},
	}
	if err := cfg.AddUpstream("default=" + up.URL); err != nil {
		t.Fatal(err)
	}
	server, err := proxy.NewServer(cfg, proxy.NewMemoryStore(), proxy.TolerantParser{})
	if err != nil {
		t.Fatal(err)
	}
	proxySrv := httptest.NewServer(server.ProxyHandler())
	t.Cleanup(proxySrv.Close)
	control := httptest.NewServer(server.ControlHandler())
	t.Cleanup(control.Close)
	return compatProxy{proxyURL: proxySrv.URL, controlURL: control.URL, upstream: up}
}

func postCase(t *testing.T, p compatProxy, path, session string, body []byte) (status int, call map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, p.proxyURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(proxy.SessionHeaderName, session)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	wait, err := http.NewRequestWithContext(context.Background(), http.MethodGet, p.controlURL+"/sessions/"+session+"/wait?quiet=20ms&timeout=3s", nil)
	if err != nil {
		t.Fatal(err)
	}
	wres, err := http.DefaultClient.Do(wait)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(wres.Body)
	_ = wres.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var sess map[string]any
	if err := json.Unmarshal(raw, &sess); err != nil {
		t.Fatalf("session json: %v body=%s", err, raw)
	}
	calls, _ := sess["calls"].([]any)
	if len(calls) == 0 {
		t.Fatalf("no calls: %s", raw)
	}
	call, _ = calls[len(calls)-1].(map[string]any)
	return res.StatusCode, call
}

func specClass(call map[string]any) string {
	n, _ := call["normalized"].(map[string]any)
	if n == nil {
		return "off"
	}
	val, _ := n["validation"].(map[string]any)
	if val == nil {
		return "off"
	}
	raw, _ := n["spec_violations"].([]any)
	findings := 0
	for _, item := range raw {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		if fmt.Sprint(m["severity"]) == "info" || fmt.Sprint(m["keyword"]) == "response_shape" {
			continue
		}
		n := 1
		if c, ok := m["count"].(float64); ok && c > 1 {
			n = int(c)
		}
		findings += n
	}
	if findings > 0 {
		return "findings"
	}
	return "clean"
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func verdictDiff(id string, gotStatus int, gotCall map[string]any, want matrixWant) string {
	gotCalled := asBool(gotCall["upstream_called"])
	gotSpec := specClass(gotCall)
	var b strings.Builder
	if gotStatus != want.Status {
		fmt.Fprintf(&b, "  status: got %d want %d\n", gotStatus, want.Status)
	}
	if gotCalled != want.UpstreamCalled {
		fmt.Fprintf(&b, "  upstream_called: got %v want %v\n", gotCalled, want.UpstreamCalled)
	}
	if gotSpec != want.Spec {
		fmt.Fprintf(&b, "  spec: got %s want %s\n", gotSpec, want.Spec)
	}
	if b.Len() == 0 {
		return ""
	}
	return id + "\n" + b.String()
}

func init() {
	http.DefaultClient.Timeout = 10 * time.Second
}
